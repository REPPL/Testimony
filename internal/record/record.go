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

// startupWindow bounds how soon after a recorder starts an exit is still
// treated as a start-up failure (e.g. a TCC denial, which fails within a
// second or two). A recorder that ran longer than this before exiting cannot
// be a start-up denial, so it is reported as an unexpected mid-session stop
// rather than mislabelled as a permissions problem.
var startupWindow = 5 * time.Second

// Test seams: overridden in tests to drive the lifecycle without installing a
// real signal handler or spawning ffmpeg. In production they are the real
// implementations.
var (
	notifyContext    = defaultNotifyContext
	startRecordersFn = startRecorders
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

	dir, err := session.Create(opts.Out, time.Now(), session.Manifest{
		App:         app,
		Commit:      opts.Commit,
		Participant: participant,
		Tasks:       tasks,
	})
	if err != nil {
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

	children, err := startRecordersFn(dir, recorders)
	if err != nil {
		return err
	}
	audioCaptured := contains(recorders, streamMicrophone)

	var srv *http.Server
	if opts.Demo {
		srv, err = demo.Serve(opts.Addr, dir)
		if err != nil {
			stopAll(children)
			return fmt.Errorf("start demo server: %w", err)
		}
	}

	printStatus(opts.Log, recorders, opts.Demo, opts.Addr)

	// Nothing is running to wait on (degraded platform, no demo): the session
	// dir and manifest are written; print next steps and exit cleanly.
	if len(children) == 0 && srv == nil {
		fmt.Fprintf(opts.Log, "\n%s\n", nextCommands(dir, audioCaptured))
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
		stopAll(children)
		if srv != nil {
			_ = srv.Shutdown(context.Background())
		}
		return errors.New(classifyRecorderExit(dead.stream, dead.err, dead.stderr.tail(), atStartup))
	}

	stopAll(children)
	if srv != nil {
		_ = srv.Shutdown(context.Background())
	}

	// Finalisation validates that each recorder actually left a usable artefact.
	// A recorder blocked on its TCC prompt for the whole session finalises no
	// container on SIGINT — audio.wav (or screen.mp4) is absent or empty — and
	// this is the only place that catches it, since it never exited on its own.
	audioReady, problems := finaliseOutputs(dir, children)
	for _, p := range problems {
		fmt.Fprintf(opts.Log, "\n%s\n", p)
	}
	fmt.Fprintf(opts.Log, "\n%s\n", nextCommands(dir, audioReady))
	if len(problems) > 0 {
		return errors.New("capture incomplete — see the messages above")
	}
	return nil
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
		if fi, err := os.Stat(out); err == nil && fi.Size() > 0 {
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
func startRecorders(dir string, streams []string) ([]*liveChild, error) {
	if len(streams) == 0 {
		return nil, nil
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found on PATH (needed to capture audio/screen): brew install ffmpeg")
	}
	micIndex, screenIndex, err := probeDevices(ffmpeg, contains(streams, streamScreen))
	if err != nil {
		return nil, err
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
			args = micArgs(micIndex, out)
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

// stopAll finalises every recorder container in turn.
func stopAll(children []*liveChild) {
	for _, c := range children {
		stopChild(c, stopGrace)
	}
}

// printStatus reports what is recording and how to stop.
func printStatus(log io.Writer, recorders []string, demoOn bool, addr string) {
	if len(recorders) > 0 {
		fmt.Fprintf(log, "  recording   : %s\n", strings.Join(recorders, ", "))
	}
	if demoOn {
		fmt.Fprintf(log, "  demo url    : %s\n", demo.DisplayURL(addr))
	}
	fmt.Fprint(log, "\n  Say “session start” aloud, then think aloud while you work.\n")
	fmt.Fprint(log, "  Press Ctrl+C to stop.\n")
}

// nextCommands is the pure downstream-command block, carrying the real session
// dir. With audio captured in place it offers `transcribe -session DIR` with no
// -audio flag, because transcribe reuses the session's audio.wav directly.
//
// With no audio.wav — a recorder blocked on its permission, or a platform
// without capture — the bare transcribe command is withheld (there is nothing
// for it to reuse) and replaced by guidance: merge and report still stand
// because interactions may have been captured, and transcribe is reachable
// either by re-running record once the permission is granted or by supplying an
// external recording via -audio.
func nextCommands(dir string, audioCaptured bool) string {
	lines := []string{"Next:"}
	if audioCaptured {
		lines = append(lines, "  testimony transcribe -session "+dir)
	}
	lines = append(lines,
		"  testimony merge      -session "+dir,
		"  testimony report     -session "+dir,
	)
	if !audioCaptured {
		lines = append(lines,
			"",
			"  transcribe needs audio, and this session has none — re-run record after granting",
			"  the microphone permission, or transcribe an external recording:",
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
