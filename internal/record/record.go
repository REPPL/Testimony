// Package record implements `testimony record`: a managed capture launcher.
// One command creates the session directory, writes manifest.json with the
// shared t0_epoch_ms anchor (via session.Create, the same code path demo uses),
// starts the microphone recorder — and, with -video, the screen recorder — as
// ffmpeg subprocesses, prints status, and runs until Ctrl+C. On SIGINT/SIGTERM/SIGHUP
// it stops each recorder cleanly (SIGINT so ffmpeg finalises its container,
// SIGKILL only on timeout), shuts any demo server down, and prints the exact
// downstream commands with the real session path. Audio-only is the default;
// screen video is opt-in retained evidence, not yet consumed downstream.
//
// Everything device-facing is isolated behind pure builders (micArgs,
// screenArgs, parseAVDevices, plan, classifyRecorderExit) and a small proc
// interface, so the argv, flag, manifest, lifecycle, TCC-classifier, and
// platform-plan logic is unit-tested without ffmpeg or a TTY; only the actual
// spawning and the live demo run need the real tools and are skipped in CI.
package record

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/REPPL/Testimony/internal/demo"
	"github.com/REPPL/Testimony/internal/session"
)

// stopGrace is how long each recorder is given to finalise its container after
// SIGINT before it is escalated to SIGKILL.
//
// stopGrace and startupWindow are vars rather than consts only so the lifecycle
// tests can shrink them: exercising the interaction between the stop path and
// the start-up classification otherwise costs five seconds of wall clock per
// assertion. Production never reassigns them.
var stopGrace = 5 * time.Second

// stopReapGrace bounds how long stopChild waits for the reaper after the
// escalation SIGKILL, mirroring recorders.go's probeKillGrace. A wedged capture
// driver can defer even SIGKILL delivery indefinitely (the child is pinned in an
// uninterruptible kernel wait), so an unbounded reap here would hang the whole
// sequential shutdown on one bad recorder. A var only so tests can shrink it.
var stopReapGrace = 2 * time.Second

// startupWindow bounds how soon after a recorder starts an exit is still
// treated as a start-up failure (e.g. a TCC denial, which fails within a
// second or two). A recorder that ran longer than this before exiting cannot
// be a start-up denial, so it is reported as an unexpected mid-session stop
// rather than mislabelled as a permissions problem.
var startupWindow = 5 * time.Second

// Test seams: overridden in tests to drive the lifecycle without installing a
// real signal handler, spawning ffmpeg, or binding a port for the demo server.
// In production they are the real implementations.
var (
	notifyContext    = defaultNotifyContext
	startRecordersFn = startRecorders
	bindDemoFn       = demo.Bind
	serveDemoFn      = demo.ServeListener
	shutdownDemoFn   = demo.Shutdown
)

// defaultNotifyContext returns a context cancelled on SIGINT/SIGTERM/SIGHUP —
// SIGHUP because closing the terminal window mid-session is an observed way
// real sessions end, and it must finalise exactly like Ctrl+C. It is the
// production notifyContext.
func defaultNotifyContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
}

// Options configures one record run. Flag parsing lives in the CLI; Run takes
// the resolved values.
type Options struct {
	Out         string    // root directory for new session folders
	App         string    // application under test
	Participant string    // participant pseudonym
	Tasks       []string  // tasks the participant will attempt
	Commit      string    // build/commit hash under test (optional)
	Video       bool      // also capture the screen to screen.mp4
	Demo        bool      // also serve the instrumented demo app into the session
	Addr        string    // demo server listen address (with -demo)
	GOOS        string    // runtime.GOOS override for tests; empty means real
	Log         io.Writer // status sink; defaults to os.Stderr
}

// Run performs one managed capture session. It blocks until interrupted.
func Run(opts Options) error {
	if opts.Log == nil {
		opts.Log = os.Stderr
	}
	goos := opts.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}

	// -demo seeds the demo app-under-test name and a task when none are given,
	// matching a standalone `testimony demo` session.
	app := opts.App
	tasks := opts.Tasks
	if opts.Demo {
		if app == "" {
			app = demo.DefaultApp
		}
		if len(tasks) == 0 {
			tasks = []string{demo.DefaultTask}
		}
	}
	participant := opts.Participant
	if participant == "" {
		participant = "P1"
	}

	// Bind the demo port, if requested, before the session directory exists or
	// any recorder is spawned. demo.CheckAddr (called by the CLI) only parses
	// -addr; a well-formed address can still fail to bind (the port is taken,
	// or the host cannot be listened on), and binding is the only way to learn
	// that. Binding first, like demo.Run itself does, means a refused bind never
	// leaves a stray session directory (or, on a platform with capture, a
	// recorder already spawned and then stopped) behind.
	var ln net.Listener
	var err error
	if opts.Demo {
		ln, err = bindDemoFn(opts.Addr)
		if err != nil {
			return fmt.Errorf("start demo server: %w", err)
		}
	}

	dir, err := session.Create(opts.Out, time.Now(), session.Manifest{
		App:         app,
		Commit:      opts.Commit,
		Participant: participant,
		Tasks:       tasks,
	})
	if err != nil {
		if ln != nil {
			ln.Close()
		}
		return err
	}
	fmt.Fprintf(opts.Log, "testimony record — session started\n\n  session dir : %s\n", dir)

	recorders, skips := plan(goos, opts.Video)
	for _, s := range skips {
		fmt.Fprintf(opts.Log, "  skipped     : %s\n", s)
	}

	// Install the interrupt handler BEFORE spawning any recorder or the demo
	// server. Each recorder runs in its own process group (Setpgid), so the
	// terminal's Ctrl+C never reaches ffmpeg directly — stopAll is the only path
	// that signals the children, so it must be reachable from the very first
	// spawn. Installing it after startRecorders left a startup window in which a
	// Ctrl+C killed record under Go's default handler while the already-spawned
	// ffmpeg children were orphaned and kept recording.
	ctx, stop := notifyContext()
	defer stop()

	children, err := startRecordersFn(dir, recorders, opts.Log)
	if err != nil {
		if ln != nil {
			ln.Close()
		}
		// A refused start (ffmpeg missing, no usable device, a later stream
		// failing before the first ever wrote output) must not leave a
		// stray, empty session directory behind — the same guarantee
		// session.Create and demo.serveOrCleanup already give on their own
		// failure paths. Only remove it when nothing but the manifest was
		// written: startRecordersFn stops (and finalises) any earlier
		// stream in this run before returning an error, so a real partial
		// capture can already be on disk and must survive.
		if onlyManifest(dir) {
			os.RemoveAll(dir)
		}
		return err
	}
	audioCaptured := contains(recorders, streamMicrophone)
	// capturePossible distinguishes "no audio was captured this run" from "this
	// platform cannot capture audio at all": plan() (platform.go) plans
	// microphone capture only on darwin, and nextCommands must not send an
	// operator on any other platform chasing a microphone permission prompt
	// that does not exist there. Reusing audioCaptured (rather than, say,
	// len(recorders) > 0) keeps this tied to the microphone specifically, so
	// it stays correct even if plan() ever grows a screen-only platform.
	capturePossible := audioCaptured

	var srv *http.Server
	if opts.Demo {
		srv, err = serveDemoFn(ln, opts.Addr, dir)
		if err != nil {
			stopAll(children)
			// Same guarantee as the startRecordersFn failure path above: don't
			// leave a stray, manifest-only session directory behind. Only
			// reachable when serveOn's own stream-file opens fail (disk full,
			// read-only filesystem) right after session.Create succeeded and no
			// recorder has written anything yet.
			if onlyManifest(dir) {
				os.RemoveAll(dir)
			}
			return fmt.Errorf("start demo server: %w", err)
		}
	}

	// Print the address the demo server actually bound (Serve records it on
	// the returned server), not the requested one: with -addr :0 the requested
	// form renders the unopenable http://localhost:0.
	statusAddr := opts.Addr
	if srv != nil && srv.Addr != "" {
		statusAddr = srv.Addr
	}
	running := len(children) > 0 || srv != nil
	printStatus(opts.Log, recorders, opts.Demo, statusAddr, running)

	// Nothing is running to wait on (degraded platform, no demo): the session
	// dir and manifest are written; print next steps and exit cleanly.
	if !running {
		fmt.Fprintf(opts.Log, "\n%s\n", nextCommands(dir, audioCaptured, capturePossible))
		return nil
	}

	select {
	case <-ctx.Done():
		fmt.Fprintln(opts.Log, "\nstopping — finalising capture files…")
	case dead := <-anyExit(children):
		// A recorder exited before we asked it to stop. Within the startup
		// window this is most often a TCC denial; a later exit is an unexpected
		// mid-session stop (e.g. a device disconnect). Stop the rest and report
		// actionably, letting the classifier decide the phrasing.
		//
		// The classification is sampled HERE, at the moment the exit is observed,
		// and only used after the stopping work. Measuring it after stopAll
		// charged the shutdown against the recorder's lifetime: stopAll blocks up
		// to stopGrace per remaining child, which alone equals startupWindow, so a
		// genuine TCC denial that failed in the first second was reported as an
		// unexpected mid-session stop and the operator was sent looking for a
		// device fault instead of the permission they had never granted.
		atStartup := time.Since(dead.started) < startupWindow
		// Other children may have exited on their own at the same moment as
		// dead: anyExit's channel is buffered to len(children), so a second
		// self-exit sent before this select fired is already sitting there,
		// unread, the instant this case runs. Both the exit itself and its
		// start-up-window classification are sampled now, before stopAll's
		// SIGINT reaches them: after that, a live recorder's own clean
		// shutdown becomes indistinguishable from a dead one's self-exit, and
		// stopAll's own wait — up to stopGrace per remaining child, which
		// alone equals startupWindow — would charge the shutdown against a
		// second early exit's classification exactly as it would have
		// against dead's, the mistake atStartup above exists to avoid.
		early := map[*liveChild]bool{dead: true}
		atStartupOf := map[*liveChild]bool{dead: atStartup}
		for _, c := range children {
			if c == dead {
				continue
			}
			select {
			case <-c.done:
				early[c] = true
				atStartupOf[c] = time.Since(c.started) < startupWindow
			default:
			}
		}
		stopAll(children)
		stopDemo(srv)
		// An early exit is reported the same way as a recorder that produced
		// nothing (docs/reference/cli.md) — which includes validating what the
		// OTHER recorders left behind and printing the next-command block. A
		// session whose recorder died mid-way still holds whatever was captured
		// up to that point (a partial audio.wav is transcribable), and without
		// this the operator got the classification but no word on whether the
		// artefacts are usable or what to run next.
		//
		// Every early-exited child is excluded from the artefact sweep: each
		// one's story is a classifyRecorderExit call below, and
		// classifyMissingOutput's stayed-blocked-on-the-prompt narrative is
		// disproved by the very exit that brought it here — running both
		// printed mutually exclusive diagnoses (and the stderr tail twice) for
		// the same recorder. Excluding only the single child anyExit's select
		// happened to pick left a second, equally self-exited child routed
		// through classifyMissingOutput regardless — misdiagnosed as still
		// blocked on a permission prompt it had already failed past, with its
		// own exit status never surfaced at all. Their artefacts still count
		// towards the Next block.
		others := make([]*liveChild, 0, len(children))
		for _, c := range children {
			if !early[c] {
				others = append(others, c)
			}
		}
		audioReady, problems := finaliseOutputs(dir, others)
		for c := range early {
			if c.stream == streamMicrophone {
				if fi, err := os.Stat(expectedOutput(dir, c.stream)); err == nil && fi.Size() > 0 {
					audioReady = true
				}
			}
		}
		for _, p := range problems {
			fmt.Fprintf(opts.Log, "\n%s\n", p)
		}
		// dead's own diagnosis is the error Run returns below; every other
		// early-exited child gets the same honest diagnosis here instead,
		// since only one classification can be the command's single exit
		// error. atStartupOf[c] carries each child's pre-stopAll sampling, so
		// this diagnosis is exactly as accurate as dead's own.
		for _, c := range children {
			if c == dead || !early[c] {
				continue
			}
			fmt.Fprintf(opts.Log, "\n%s\n", classifyRecorderExit(c.stream, c.err, c.stderr.tail(), atStartupOf[c]))
		}
		fmt.Fprintf(opts.Log, "\n%s\n", nextCommands(dir, audioReady, capturePossible))
		return errors.New(classifyRecorderExit(dead.stream, dead.err, dead.stderr.tail(), atStartup))
	}

	stopAll(children)
	stopDemo(srv)

	// Finalisation validates that each recorder actually left a usable artefact.
	// A recorder blocked on its TCC prompt for the whole session finalises no
	// container on SIGINT — audio.wav (or screen.mp4) is absent or empty — and
	// this is the only place that catches it, since it never exited on its own.
	audioReady, problems := finaliseOutputs(dir, children)
	for _, p := range problems {
		fmt.Fprintf(opts.Log, "\n%s\n", p)
	}
	fmt.Fprintf(opts.Log, "\n%s\n", nextCommands(dir, audioReady, capturePossible))
	if len(problems) > 0 {
		return errors.New("capture incomplete — see the messages above")
	}
	return nil
}

// stopDemo stops the demo capture server, when one is running, through demo's
// bounded shutdown helper. Both of Run's exit paths — the Ctrl+C branch and the
// recorder-exited branch — go through this one function so neither can drift
// back to stopping the server itself: srv.Shutdown with a context that never
// expires blocks for as long as any connection stays open, so a single stalled
// browser tab left `testimony record -demo` hanging on Ctrl+C instead of
// finalising the session. That is the hang demo.Shutdown exists to prevent, and
// it is only prevented where the helper is actually used.
func stopDemo(srv *http.Server) {
	if srv == nil {
		return
	}
	// The shutdown error is deliberately dropped: the two capture streams use
	// direct O_APPEND writes, so records already accepted are durable whether the
	// drain completed or the deadline forced a Close, and the session must still
	// be finalised either way.
	_ = shutdownDemoFn(srv)
}

// finaliseOutputs validates each stopped recorder's expected artefact and turns
// any that produced nothing into an actionable explanation. It reports whether a
// usable audio.wav is present, so the Next block can decide whether to offer
// transcribe. An empty events.rrweb.jsonl is deliberately not checked here — the
// browser may legitimately not batch any rrweb events, and interactions.jsonl
// carries the evidence regardless.
func finaliseOutputs(dir string, children []*liveChild) (audioReady bool, problems []string) {
	for _, c := range children {
		out := expectedOutput(dir, c.stream)
		fi, err := os.Stat(out)
		hasData := err == nil && fi.Size() > 0
		if c.killed {
			// The recorder was force-terminated because it did not finalise within the
			// grace period, so even a non-empty file may be truncated/unplayable (an MP4
			// missing its moov atom especially). Surface it rather than let the size
			// check bless a broken artefact. A PCM WAV survives a kill largely intact
			// (only the RIFF sizes go stale), so a present audio.wav is still offered for
			// transcription; a killed screen.mp4 is not consumed downstream regardless.
			problems = append(problems, classifyKilledOutput(c.stream, filepath.Base(out), hasData, c.stderr.tail()))
			if c.stream == streamMicrophone && hasData {
				audioReady = true
			}
			continue
		}
		if hasData {
			if c.stream == streamMicrophone {
				audioReady = true
			}
			continue
		}
		problems = append(problems, classifyMissingOutput(c.stream, filepath.Base(out), c.stderr.tail()))
	}
	return audioReady, problems
}

// expectedOutput is the artefact path a recorder for the given stream writes
// into the session directory. Pure: it is the single source of truth for both
// the ffmpeg output argv and the finalise-time validation.
func expectedOutput(dir, stream string) string {
	if stream == streamScreen {
		return filepath.Join(dir, session.ScreenFile)
	}
	return filepath.Join(dir, session.AudioFile)
}

// checkPlainOutput refuses an ffmpeg output path that already exists as anything
// other than a regular file. ffmpeg is handed the path as a string and told to
// overwrite it with -y, so it is the one write in this codebase that cannot go
// through session.OpenFileNoFollow — and it follows a symlink at the final
// component exactly as OpenFileNoFollow's doc comment warns. A session directory
// is an exchange unit: a symlink pre-planted at sessions/<ts>/audio.wav would
// silently redirect the whole recording outside the session, overwriting an
// arbitrary file the operator never named. os.Lstat does not resolve the link,
// so a symlink is reported with ModeSymlink set even when its target is missing.
// An absent path is fine — that is the ordinary case, and ffmpeg creates it.
func checkPlainOutput(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write %s: it is a symlink", path)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("refusing to write %s: it is not a regular file", path)
	}
	return nil
}

// startRecorders resolves the ffmpeg binary and device indices, then starts one
// ffmpeg subprocess per requested stream, each in its own process group with
// captured stderr. On darwin the streams are non-empty; elsewhere they are, so
// this is a no-op returning no children.
func startRecorders(dir string, streams []string, log io.Writer) ([]*liveChild, error) {
	if len(streams) == 0 {
		return nil, nil
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found on PATH (needed to capture audio/screen): brew install ffmpeg")
	}
	screenIndex, mics, err := probeDevices(ffmpeg, contains(streams, streamScreen))
	if err != nil {
		return nil, err
	}
	// The microphone is captured via avfoundation ":default" (micArgs), so ffmpeg
	// resolves the system default at capture time. Log the detected input roster
	// so a virtual audio driver shadowing the real mic is visible before a session
	// is recorded to silence.
	if contains(streams, streamMicrophone) && len(mics) > 0 {
		fmt.Fprintf(log, "  audio inputs: %s\n  microphone  : system default (avfoundation :default)\n", strings.Join(mics, ", "))
	}

	var children []*liveChild
	for _, stream := range streams {
		out := expectedOutput(dir, stream)
		if err := checkPlainOutput(out); err != nil {
			stopAll(children)
			return nil, err
		}
		var args []string
		switch stream {
		case streamMicrophone:
			args = micArgs(out)
		case streamScreen:
			args = screenArgs(screenIndex, out)
		}
		cmd := exec.Command(ffmpeg, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		buf := &lockedBuffer{}
		cmd.Stderr = buf
		p := &execProc{cmd: cmd}
		if err := p.Start(); err != nil {
			stopAll(children)
			return nil, fmt.Errorf("start %s recorder: %w", stream, err)
		}
		children = append(children, newLiveChild(stream, p, buf))
	}
	return children, nil
}

// anyExit reports the first child to exit, via a channel buffered to the child
// count so late exits (after we begin stopping) never leak a goroutine.
func anyExit(children []*liveChild) <-chan *liveChild {
	ch := make(chan *liveChild, len(children))
	for _, c := range children {
		go func(c *liveChild) {
			<-c.done
			ch <- c
		}(c)
	}
	return ch
}

// onlyManifest reports whether dir contains nothing but manifest.json — i.e.
// no recorder ever wrote output to it. It fails safe: any error reading the
// directory (permissions, a concurrent removal) is treated as "not empty" so
// the caller never removes a session it cannot fully account for.
func onlyManifest(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() != session.ManifestFile {
			return false
		}
	}
	return true
}

// stopAll finalises every recorder container in turn.
func stopAll(children []*liveChild) {
	for _, c := range children {
		stopChild(c, stopGrace)
	}
}

// printStatus reports what is recording and how to stop. The "say session
// start… Press Ctrl+C" banner only applies while something is actually
// running (running is the caller's own len(children) == 0 && srv == nil
// test, inverted): printing it unconditionally told an operator on a
// platform with no capture support and no -demo to speak into a session that
// had already finished, immediately above the Next block for a command that
// had already exited.
func printStatus(log io.Writer, recorders []string, demoOn bool, addr string, running bool) {
	if len(recorders) > 0 {
		fmt.Fprintf(log, "  recording   : %s\n", strings.Join(recorders, ", "))
	}
	if demoOn {
		fmt.Fprintf(log, "  demo url    : %s\n", demo.DisplayURL(addr))
	}
	if running {
		fmt.Fprint(log, "\n  Say “session start” aloud, then think aloud while you work.\n")
		fmt.Fprint(log, "  Press Ctrl+C to stop.\n")
	}
}

// nextCommands is the pure downstream-command block, carrying the real session
// dir. With audio captured in place it offers `transcribe -session DIR` with no
// -audio flag, because transcribe reuses the session's audio.wav directly.
//
// With no audio.wav, the bare transcribe command is withheld (there is
// nothing for it to reuse) and replaced by guidance, which branches on
// capturePossible: a recorder blocked on its permission (capturePossible
// true — this platform has microphone capture, this run just did not get it)
// is told to re-run record after granting the permission; a platform with no
// capture support at all (capturePossible false — plan returned no
// recorders) is not, since there is no permission prompt to grant there and
// the operator would go looking for one that does not exist. Both cases can
// still transcribe an external recording via -audio.
func nextCommands(dir string, audioCaptured, capturePossible bool) string {
	lines := []string{"Next:"}
	if audioCaptured {
		lines = append(lines, "  testimony transcribe -session "+dir)
	}
	lines = append(lines,
		"  testimony merge      -session "+dir,
		"  testimony report     -session "+dir,
	)
	if !audioCaptured && capturePossible {
		lines = append(lines,
			"",
			"  transcribe needs audio, and this session has none — re-run record after granting",
			"  the microphone permission, or transcribe an external recording:",
			"  testimony transcribe -session "+dir+" -audio <your-recording.m4a>",
		)
	} else if !audioCaptured {
		lines = append(lines,
			"",
			"  transcribe needs audio, and this session has none — this platform has no",
			"  microphone capture; transcribe an external recording instead:",
			"  testimony transcribe -session "+dir+" -audio <your-recording.m4a>",
		)
	}
	return strings.Join(lines, "\n")
}

// contains reports whether s is in xs.
func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
