// Package cli implements the testimony command-line interface.
package cli

import (
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/REPPL/Testimony/internal/analyze"
	"github.com/REPPL/Testimony/internal/cast"
	"github.com/REPPL/Testimony/internal/demo"
	"github.com/REPPL/Testimony/internal/drafttests"
	"github.com/REPPL/Testimony/internal/record"
	"github.com/REPPL/Testimony/internal/report"
	"github.com/REPPL/Testimony/internal/review"
	"github.com/REPPL/Testimony/internal/session"
	"github.com/REPPL/Testimony/internal/timeline"
	"github.com/REPPL/Testimony/internal/transcribe"
)

// Version is stamped by the release process; "dev" otherwise.
var Version = "dev"

const usage = `testimony — usability evidence, on the record

Usage:
  testimony record      [-out sessions] [-app NAME] [-participant P1] [-task ...]   managed capture: session dir + manifest, start recorders, run until Ctrl+C
                        [-commit HASH] [-video|-no-video] [-demo [-addr :8737]]
  testimony demo        [-addr :8737] [-out sessions]   serve the instrumented demo app, capture a session
  testimony transcribe  [-session DIR] [-audio FILE]    transcribe a voice recording into transcript.jsonl (reuses the session's audio.wav when -audio is omitted)
                        [-engine auto|whisperx|whispercpp] [-model large-v3-turbo] [-language en] [-offset SECONDS]
                        [-device auto|cpu|cuda] [-compute_type auto|int8|float16|…] [-vad auto|silero|pyannote]   (whisperx only)
  testimony import      [-session DIR] [-cast FILE]     import an asciinema recording's output into interactions.jsonl (reuses the session's terminal.cast when -cast is omitted)
                        [-offset SECONDS]
  testimony merge       [-session DIR]                  merge transcript + interactions into timeline.jsonl
  testimony report      [-session DIR] [-window 2.5]    render timeline.jsonl as a Markdown report
  testimony analyze     [-session DIR] [-out FILE]      emit the analysis request (rubric + timeline) on stdout or to FILE
  testimony analyze     [-session DIR] -ingest FILE     validate answer JSON (FILE or "-") → findings.jsonl (all findings unverified)
  testimony draft-tests [-session DIR] [-window 10] [-out FILE]   emit the regression-test drafting request (rubric + confirmed findings + event windows)
  testimony draft-tests [-session DIR] -ingest FILE     validate answer JSON (FILE or "-") → tests.jsonl (all drafts proposed)
  testimony draft-tests [-session DIR] -render [-out FILE]        render the accepted drafts as Markdown test cases
  testimony review      [-session DIR] [-kind findings|tests]     interactively record verdicts on unverified findings, or decisions on proposed test drafts (stdin must be a character device)
  testimony review      [-session DIR] -finding F-NNN -verdict confirmed|rejected|duplicate-of-F-NNN
  testimony review      [-session DIR] -kind tests -test T-NNN -decision accepted|rejected
  testimony review      [-session DIR] -kind tests -test T-NNN -decision edited -edit FILE
  testimony version
  testimony help

A session directory is described in docs/reference/session-directory.md.
Omitting -session on transcribe, import, merge, report, analyze, draft-tests, or
review uses the current directory when it holds a Testimony session
manifest.json (one with a session field), and names the inferred session on
stderr.
`

// Run executes the CLI and returns a process exit code.
func Run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]

	switch cmd {
	case "demo":
		fs := flag.NewFlagSet("demo", flag.ExitOnError)
		addr := fs.String("addr", ":8737", "listen address")
		out := fs.String("out", "sessions", "root directory for new session folders")
		fs.Parse(rest)
		if err := rejectArgs(fs); err != nil {
			return usageErr(err)
		}
		// An empty -out is a wrong invocation (an unset shell variable spliced
		// into the flag, say), not a valid root: every other validated flag on
		// this path exits 2 naming itself, but an empty -out previously reached
		// os.MkdirAll unvalidated and surfaced as a bare "mkdir : no such file
		// or directory" at exit 1, naming no flag at all.
		if *out == "" {
			return usageErr(fmt.Errorf("demo: -out must not be empty"))
		}
		// Refuse a malformed address here, where wrong invocations exit 2 —
		// reported from Serve it took the runtime status, after Run had already
		// created a session directory for a server that could never bind.
		if err := demo.CheckAddr(*addr); err != nil {
			return usageErr(fmt.Errorf("demo: %w", err))
		}
		if err := demo.Run(*addr, *out); err != nil {
			return fail(err)
		}
		return 0

	case "merge":
		fs := flag.NewFlagSet("merge", flag.ExitOnError)
		dir := fs.String("session", "", "session directory")
		fs.Parse(rest)
		if err := rejectArgs(fs); err != nil {
			return usageErr(err)
		}
		sess, err := resolveSession(fs, *dir)
		if err != nil {
			return usageErr(err)
		}
		speech, events, err := timeline.Merge(sess)
		if err != nil {
			return fail(err)
		}
		fmt.Printf("merged %d utterances + %d events → %s\n",
			speech, events, filepath.Join(sess, session.TimelineFile))
		return 0

	case "report":
		fs := flag.NewFlagSet("report", flag.ExitOnError)
		dir := fs.String("session", "", "session directory")
		window := fs.Float64("window", 2.5, "utterance↔event join window, seconds")
		fs.Parse(rest)
		if err := rejectArgs(fs); err != nil {
			return usageErr(err)
		}
		// A non-finite window is not a join window at all, and Render has no way to
		// notice: every comparison against NaN is false, so a NaN window silently
		// detaches every event from the speech it accompanied, while +Inf puts every
		// event inside the first utterance's window and files them all under it.
		// Either way report.md — the human evidence artefact — misstates what the
		// participant was doing while they spoke, and the command exits 0. A negative
		// window is legitimate (it narrows the join), so only finiteness is required.
		if math.IsNaN(*window) || math.IsInf(*window, 0) {
			return usageErr(fmt.Errorf("report: -window must be a finite number of seconds, got %v", *window))
		}
		// Resolved last of the invocation checks: an inferred session is announced
		// on stderr, and a run that is about to be refused for some other flag must
		// not first announce a session it never used.
		sess, err := resolveSession(fs, *dir)
		if err != nil {
			return usageErr(err)
		}
		md, err := report.Render(sess, *window)
		if err != nil {
			return fail(err)
		}
		out := filepath.Join(sess, session.ReportFile)
		if err := session.WriteFileNoFollow(out, []byte(md), 0o644); err != nil {
			return fail(err)
		}
		fmt.Printf("wrote %s\n", out)
		return 0

	case "record":
		fs := flag.NewFlagSet("record", flag.ExitOnError)
		out := fs.String("out", "sessions", "root directory for new session folders")
		app := fs.String("app", "", "application under test")
		participant := fs.String("participant", "P1", "participant pseudonym")
		commit := fs.String("commit", "", "build/commit hash under test")
		var tasks record.StringSlice
		fs.Var(&tasks, "task", "a task the participant will attempt (repeatable)")
		video := fs.Bool("video", false, "also capture the screen to screen.mp4 (needs Screen Recording permission)")
		noVideo := fs.Bool("no-video", false, "explicitly disable screen capture (the default)")
		demoFlag := fs.Bool("demo", false, "also serve the instrumented demo app into the session")
		addr := fs.String("addr", ":8737", "demo server listen address (with -demo)")
		fs.Parse(rest)
		if err := rejectArgs(fs); err != nil {
			return usageErr(err)
		}
		// See demo's identical check above: an empty -out is a wrong invocation,
		// not a valid root, and must exit 2 naming the flag rather than surface
		// os.MkdirAll's bare "mkdir : no such file or directory" at exit 1.
		if *out == "" {
			return usageErr(fmt.Errorf("record: -out must not be empty"))
		}
		if *demoFlag {
			if err := demo.CheckAddr(*addr); err != nil {
				return usageErr(fmt.Errorf("record: %w", err))
			}
		}
		if err := record.Run(record.Options{
			Out:         *out,
			App:         *app,
			Participant: *participant,
			Tasks:       tasks,
			Commit:      *commit,
			Video:       record.ResolveVideo(*video, *noVideo),
			Demo:        *demoFlag,
			Addr:        *addr,
			Log:         os.Stdout,
		}); err != nil {
			return fail(err)
		}
		return 0

	case "transcribe":
		fs := flag.NewFlagSet("transcribe", flag.ExitOnError)
		dir := fs.String("session", "", "session directory")
		audio := fs.String("audio", "", "voice recording (.m4a, .mov, or .wav); omit to reuse the session's audio.wav")
		engine := fs.String("engine", "auto", "ASR engine: auto, whisperx, or whispercpp")
		model := fs.String("model", "large-v3-turbo", "Whisper model name, or (whispercpp) a ggml model file path")
		language := fs.String("language", "en", "spoken language code")
		device := fs.String("device", "auto", "(whisperx) inference device: auto, cpu, or cuda")
		compute := fs.String("compute_type", "auto", "(whisperx) compute type: auto, int8, float16, ...")
		vad := fs.String("vad", "auto", "(whisperx) VAD method: auto, silero, or pyannote (auto picks silero; pyannote trips newer torch's weights_only load)")
		offset := fs.Float64("offset", 0, "audio→session clock offset in seconds (default: derived from the recording's creation time)")
		fs.Parse(rest)
		if err := rejectArgs(fs); err != nil {
			return usageErr(err)
		}
		audioSet := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "audio" {
				audioSet = true
			}
		})
		// An explicitly-empty -audio is a wrong invocation (an unset shell
		// variable spliced into the flag, say), not "omit -audio" — the
		// analyze -ingest/-out precedent above. Left unchecked, it silently
		// selects the in-place branch: with a session audio.wav present, that
		// transcribes the session's own recording instead of the external one
		// the caller named, at exit 0; without one, it fails at exit 1 with a
		// message claiming -audio was never given.
		if audioSet && *audio == "" {
			return usageErr(fmt.Errorf("transcribe: -audio must not be empty"))
		}
		// -audio's extension is a closed set (docs/reference/cli.md), the same
		// class as -engine/-device/-vad below — refuse it at exit 2 before
		// detectEngine and offset resolution spend any work on a path
		// whisperx/whisper.cpp were never going to accept. Unchecked, a bad
		// extension surfaced only after detectEngine ran, either as "no ASR
		// engine found" on a machine with none installed (masking the real
		// mistake) or as the same unsupported-format error at exit 1.
		if *audio != "" {
			if err := transcribe.CheckAudioExt(*audio); err != nil {
				return usageErr(fmt.Errorf("transcribe: %w", err))
			}
		}
		// -engine, -device, and -vad are closed enums (docs/reference/cli.md).
		// Explicitly empty is the same wrong-invocation class as -audio above
		// (an unset shell variable spliced into the flag), not "omit the
		// flag" — left unchecked, each Check* function's own zero-value case
		// treats "" the same as the documented "auto" and silently keeps the
		// run going at exit 0, discarding the caller's actual choice instead
		// of the exit-2 refusal every other closed-enum flag on this command
		// gets. -compute_type stays unchecked: its documented set is
		// open-ended (auto, int8, float16, ...), so there is no closed set
		// for an empty value to fall outside of.
		engineSet, deviceSet, vadSet := false, false, false
		fs.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "engine":
				engineSet = true
			case "device":
				deviceSet = true
			case "vad":
				vadSet = true
			}
		})
		if engineSet && *engine == "" {
			return usageErr(fmt.Errorf("transcribe: -engine must not be empty"))
		}
		if deviceSet && *device == "" {
			return usageErr(fmt.Errorf("transcribe: -device must not be empty"))
		}
		if vadSet && *vad == "" {
			return usageErr(fmt.Errorf("transcribe: -vad must not be empty"))
		}
		// An unknown engine name is a wrong invocation (exit 2) — reported from
		// detectEngine it took the runtime status a script could not tell from a
		// genuinely missing engine binary.
		if err := transcribe.CheckEngine(*engine); err != nil {
			return usageErr(fmt.Errorf("transcribe: %w", err))
		}
		// -device and -vad are the same class of wrong invocation as -engine: both
		// are closed enums (docs/reference/cli.md), and unchecked, a typo spent the
		// offset resolution and (on the -audio path) the audio conversion before
		// whisperx itself rejected the literal argument at exit 1.
		if err := transcribe.CheckDevice(*device); err != nil {
			return usageErr(fmt.Errorf("transcribe: %w", err))
		}
		if err := transcribe.CheckVAD(*vad); err != nil {
			return usageErr(fmt.Errorf("transcribe: %w", err))
		}
		offsetSet := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "offset" {
				offsetSet = true
			}
		})
		// An unusable explicit offset is a wrong invocation (exit 2), refused
		// before any conversion or engine work starts — the -window precedent
		// above. Unchecked, a non-finite -offset failed only after that work
		// was already spent, with a bare JSON encoding error at exit 1, and a
		// finite but absurd one wrote a transcript at exit 0 that merge
		// refuses one command later, naming transcript.jsonl rather than the flag.
		if offsetSet {
			if err := transcribe.CheckOffset(*offset); err != nil {
				return usageErr(fmt.Errorf("transcribe: %w", err))
			}
		}
		// Resolved last of the invocation checks (see report above): a run refused
		// for another flag must not first announce an inferred session.
		sess, err := resolveSession(fs, *dir)
		if err != nil {
			return usageErr(err)
		}
		n, err := transcribe.Run(transcribe.Options{
			SessionDir: sess,
			Audio:      *audio,
			Engine:     *engine,
			Model:      *model,
			Language:   *language,
			Device:     *device,
			Compute:    *compute,
			VAD:        *vad,
			Offset:     *offset,
			OffsetSet:  offsetSet,
			Log:        os.Stdout,
		})
		if err != nil {
			return fail(err)
		}
		fmt.Printf("transcribed %d utterances → %s\n", n, filepath.Join(sess, session.TranscriptFile))
		return 0

	case "import":
		fs := flag.NewFlagSet("import", flag.ExitOnError)
		dir := fs.String("session", "", "session directory")
		castFile := fs.String("cast", "", "asciicast file (v2 or v3); omit to reuse the session's terminal.cast")
		offset := fs.Float64("offset", 0, "cast→session clock offset in seconds (default: derived from the cast header's timestamp)")
		fs.Parse(rest)
		if err := rejectArgs(fs); err != nil {
			return usageErr(err)
		}
		castSet, offsetSet := false, false
		fs.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "cast":
				castSet = true
			case "offset":
				offsetSet = true
			}
		})
		// An explicitly-empty -cast is a wrong invocation (an unset shell variable
		// spliced into the flag, say), not "omit -cast" — transcribe's -audio
		// precedent, and for the same reason: left unchecked it silently selects the
		// in-place branch, re-importing the session's own terminal.cast instead of
		// the file the caller named, at exit 0.
		//
		// -cast carries no extension check, unlike -audio's closed .m4a/.mov/.wav
		// set. That set exists because ffmpeg accepts only those containers; here the
		// cast header's version field is the authority on whether a file is
		// importable, and a name rule would refuse a legitimately-named cast
		// (`asciinema rec` writes whatever name the operator gives it, and a
		// redirected recording may carry no extension at all).
		if castSet && *castFile == "" {
			return usageErr(fmt.Errorf("import: -cast must not be empty"))
		}
		// The same bound transcribe's -offset obeys, from the same function, refused
		// at exit 2 before anything reads the cast: a non-finite offset has no
		// millisecond value to round to, and a finite but absurd one would write
		// records merge refuses one command later, naming interactions.jsonl rather
		// than the flag.
		if offsetSet {
			if err := transcribe.CheckOffset(*offset); err != nil {
				return usageErr(fmt.Errorf("import: %w", err))
			}
		}
		// Resolved last of the invocation checks, as on every other pipeline
		// command: import is one of them, so -session obeys the one shared rule
		// rather than a required-flag refusal of its own, and a run refused for
		// another flag announces no session it never used.
		sess, err := resolveSession(fs, *dir)
		if err != nil {
			return usageErr(err)
		}
		// The offset provenance line, the dropped-event counts, and the replaced-record
		// count go to stderr, beside resolveSession's own inference line, so stdout
		// carries just the one summary line a script reads.
		n, err := cast.Run(cast.Options{
			SessionDir: sess,
			Cast:       *castFile,
			Offset:     *offset,
			OffsetSet:  offsetSet,
			Log:        os.Stderr,
		})
		if err != nil {
			return fail(err)
		}
		fmt.Printf("imported %d records → %s\n", n, filepath.Join(sess, session.InteractionsFile))
		return 0

	case "analyze":
		fs := flag.NewFlagSet("analyze", flag.ExitOnError)
		dir := fs.String("session", "", "session directory")
		out := fs.String("out", "", "write the emitted request to FILE instead of stdout")
		ingest := fs.String("ingest", "", "validate answer JSON at FILE (or \"-\" for stdin) into findings.jsonl")
		fs.Parse(rest)
		if err := rejectArgs(fs); err != nil {
			return usageErr(err)
		}
		outSet, ingestSet := false, false
		fs.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "out":
				outSet = true
			case "ingest":
				ingestSet = true
			}
		})
		// An explicitly-empty -ingest or -out is a wrong invocation (an unset
		// shell variable spliced into the flag, say), not a valid path — the
		// demo/record -out precedent above. Left unchecked here, an empty
		// -ingest silently falls through the `*ingest != ""` mode check below
		// to emit mode at exit 0 (the answer is never validated), and an empty
		// -out in emit mode silently falls through to stdout at exit 0 instead
		// of writing a file — both leave a caller trusting the wrong thing
		// happened.
		if ingestSet && *ingest == "" {
			return usageErr(fmt.Errorf("analyze: -ingest must not be empty"))
		}
		if outSet && *out == "" {
			return usageErr(fmt.Errorf("analyze: -out must not be empty"))
		}
		if *ingest != "" && *out != "" {
			return usageErr(fmt.Errorf("analyze: -out and -ingest cannot be combined"))
		}
		// Resolved last of the invocation checks (see report above): a run refused
		// for another flag must not first announce an inferred session.
		sess, err := resolveSession(fs, *dir)
		if err != nil {
			return usageErr(err)
		}
		if *ingest != "" {
			in := os.Stdin
			if *ingest != "-" {
				// Read the answer file through the no-follow guard, like every other
				// session-surface read: the operator naturally saves the model's answer
				// beside the session (e.g. sessions/x/answer.json), and a session is an
				// exchange unit — a received one can ship a FIFO at that name (plain
				// os.Open blocks in open(2) for ever) or a symlink out of the directory.
				f, err := session.OpenFileNoFollowRead(*ingest)
				if err != nil {
					return fail(err)
				}
				defer f.Close()
				in = f
			}
			findings, err := analyze.Ingest(sess, in)
			if err != nil {
				return fail(err)
			}
			fmt.Printf("validated %d findings → %s (all unverified)\n",
				len(findings), filepath.Join(sess, session.FindingsFile))
			return 0
		}
		prompt, err := analyze.EmitRequest(sess)
		if err != nil {
			return fail(err)
		}
		if *out != "" {
			// Write through the no-follow guard, matching the report.md write above and
			// every other session-surface write: the operator naturally directs -out at
			// a path beside the session (e.g. sessions/x/request.md), and a received
			// session can ship a symlink there that plain os.WriteFile would follow,
			// truncating an arbitrary operator-writable file outside the session.
			if err := session.WriteFileNoFollow(*out, []byte(prompt), 0o644); err != nil {
				return fail(err)
			}
			fmt.Printf("wrote %s\n", *out)
			return 0
		}
		fmt.Print(prompt)
		return 0

	case "draft-tests":
		fs := flag.NewFlagSet("draft-tests", flag.ExitOnError)
		dir := fs.String("session", "", "session directory")
		window := fs.Float64("window", 10, "emit mode: event-window half-width around a finding's evidence, seconds")
		out := fs.String("out", "", "emit/render mode: write to FILE instead of stdout")
		ingest := fs.String("ingest", "", "validate answer JSON at FILE (or \"-\" for stdin) into tests.jsonl")
		render := fs.Bool("render", false, "render the accepted drafts as Markdown test cases")
		fs.Parse(rest)
		if err := rejectArgs(fs); err != nil {
			return usageErr(err)
		}
		outSet, ingestSet, windowSet := false, false, false
		fs.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "out":
				outSet = true
			case "ingest":
				ingestSet = true
			case "window":
				windowSet = true
			}
		})
		// An explicitly-empty -ingest or -out is a wrong invocation (an unset shell
		// variable spliced into the flag, say), not a valid path — analyze's
		// identical guard above. Left unchecked, an empty -ingest falls through the
		// mode check below into emit at exit 0 (the answer is never validated), and
		// an empty -out falls through to stdout at exit 0 instead of writing a file.
		if ingestSet && *ingest == "" {
			return usageErr(fmt.Errorf("draft-tests: -ingest must not be empty"))
		}
		if outSet && *out == "" {
			return usageErr(fmt.Errorf("draft-tests: -out must not be empty"))
		}
		// draft-tests runs in exactly one mode, extending analyze's emit-or-ingest
		// rule by one: emit (neither), ingest (-ingest), or render (-render).
		// -ingest writes tests.jsonl and produces no document, so neither -out nor
		// -render has any meaning alongside it; a caller who combined them meant one
		// of the two modes and must be told which they cannot have.
		if *ingest != "" {
			if *out != "" {
				return usageErr(fmt.Errorf("draft-tests: -out and -ingest cannot be combined"))
			}
			if *render {
				return usageErr(fmt.Errorf("draft-tests: -render and -ingest cannot be combined"))
			}
		}
		// -window sizes the event window emit puts in the request; ingest validates
		// against the findings and render reads only what is already on disk, so in
		// neither mode does it do anything. Silently ignored, it lets a caller who
		// meant to widen the window believe they had — refusing names the mode they
		// are actually in, the same class as the -out/-ingest combination above.
		if windowSet && (*ingest != "" || *render) {
			return usageErr(fmt.Errorf("draft-tests: -window applies to the emit mode only"))
		}
		// A non-finite window is not a window at all, and Window has no way to
		// notice: every comparison against NaN is false, so a NaN window emits an
		// empty event window for every finding — a request whose steps cannot be
		// grounded in anything — while +Inf emits the whole timeline as every
		// finding's window. Either way the drafting request misstates its own
		// evidence and the command exits 0. A negative window is legitimate (it
		// narrows the window), so only finiteness is required — the report -window
		// precedent.
		if math.IsNaN(*window) || math.IsInf(*window, 0) {
			return usageErr(fmt.Errorf("draft-tests: -window must be a finite number of seconds, got %v", *window))
		}
		// Resolved last of the invocation checks (see report above): a run refused
		// for another flag must not first announce an inferred session.
		sess, err := resolveSession(fs, *dir)
		if err != nil {
			return usageErr(err)
		}
		if *ingest != "" {
			in := os.Stdin
			if *ingest != "-" {
				// Read the answer through the no-follow guard, like analyze -ingest: the
				// operator naturally saves the model's answer beside the session, and a
				// received session can ship a FIFO at that name (plain os.Open blocks in
				// open(2) for ever) or a symlink out of the directory.
				f, err := session.OpenFileNoFollowRead(*ingest)
				if err != nil {
					return fail(err)
				}
				defer f.Close()
				in = f
			}
			// Both loud-staging refusals (no confirmed finding to draft from, no
			// accepted draft to render) are well-formed invocations whose work cannot
			// be done, so they take the runtime status here rather than the usage one.
			drafts, err := drafttests.Ingest(sess, in)
			if err != nil {
				return fail(err)
			}
			fmt.Printf("validated %d test drafts → %s (all proposed)\n",
				len(drafts), filepath.Join(sess, session.TestsFile))
			return 0
		}
		var doc string
		if *render {
			doc, err = drafttests.Render(sess)
		} else {
			doc, err = drafttests.EmitRequest(sess, *window)
		}
		if err != nil {
			return fail(err)
		}
		if *out != "" {
			// Write through the no-follow guard, matching analyze -out: the operator
			// naturally directs -out at a path beside the session, and a received
			// session can ship a symlink there that plain os.WriteFile would follow,
			// truncating an arbitrary operator-writable file outside the session.
			if err := session.WriteFileNoFollow(*out, []byte(doc), 0o644); err != nil {
				return fail(err)
			}
			fmt.Printf("wrote %s\n", *out)
			return 0
		}
		fmt.Print(doc)
		return 0

	case "review":
		fs := flag.NewFlagSet("review", flag.ExitOnError)
		dir := fs.String("session", "", "session directory")
		kind := fs.String("kind", review.KindFindings, "which record family to review: findings | tests")
		finding := fs.String("finding", "", "non-interactive: the finding to judge (F-NNN)")
		verdict := fs.String("verdict", "", "non-interactive: confirmed | rejected | duplicate-of-F-NNN")
		test := fs.String("test", "", "non-interactive (-kind tests): the test draft to decide (T-NNN)")
		decision := fs.String("decision", "", "non-interactive (-kind tests): accepted | edited | rejected")
		edit := fs.String("edit", "", "with -decision edited: the replacement fields as a JSON object at FILE (or \"-\" for stdin)")
		fs.Parse(rest)
		if err := rejectArgs(fs); err != nil {
			return usageErr(err)
		}
		f, v := strings.TrimSpace(*finding), strings.TrimSpace(*verdict)
		tst, dec := strings.TrimSpace(*test), strings.TrimSpace(*decision)
		findingSet, verdictSet := false, false
		kindSet, testSet, decisionSet, editSet := false, false, false, false
		fs.Visit(func(fl *flag.Flag) {
			switch fl.Name {
			case "finding":
				findingSet = true
			case "verdict":
				verdictSet = true
			case "kind":
				kindSet = true
			case "test":
				testSet = true
			case "decision":
				decisionSet = true
			case "edit":
				editSet = true
			}
		})
		// An explicitly-empty -finding or -verdict is a wrong invocation (an
		// unset shell variable spliced into the flag, say), not omission —
		// the analyze -ingest/-out precedent above. Left unchecked here, a
		// pair that is set-but-empty on both sides is indistinguishable from
		// both being omitted, and silently falls through to the interactive
		// walk at exit 0 instead of refusing the caller's mistake.
		if findingSet && f == "" {
			return usageErr(fmt.Errorf("review: -finding must not be empty"))
		}
		if verdictSet && v == "" {
			return usageErr(fmt.Errorf("review: -verdict must not be empty"))
		}
		// The tests side gets the identical unset-shell-variable guard on each of its
		// own flags: set-but-empty is indistinguishable from omitted, so it would
		// otherwise fall through to the interactive walk at exit 0 rather than refuse
		// the caller's mistake. -edit is a path, so it is not trimmed (matching
		// -ingest/-out); -kind, -test and -decision are identifiers and are.
		if kindSet && strings.TrimSpace(*kind) == "" {
			return usageErr(fmt.Errorf("review: -kind must not be empty"))
		}
		if testSet && tst == "" {
			return usageErr(fmt.Errorf("review: -test must not be empty"))
		}
		if decisionSet && dec == "" {
			return usageErr(fmt.Errorf("review: -decision must not be empty"))
		}
		if editSet && *edit == "" {
			return usageErr(fmt.Errorf("review: -edit must not be empty"))
		}
		// The record family is a closed set, so an unknown one is a wrong invocation
		// rather than a silently-ignored value that would run the findings walk under
		// a name the caller did not mean.
		recordKind, err := review.ParseKindFlag(strings.TrimSpace(*kind))
		if err != nil {
			return usageErr(fmt.Errorf("review: %w", err))
		}
		// A flag belonging to the other record family is a wrong invocation, not a
		// silently ignored one: a caller who typed -verdict against -kind tests meant
		// a decision this walk cannot record, and recording nothing while exiting 0
		// would let a script believe it landed. review.Run refuses the same pairings,
		// so the rule holds for any caller; refusing here gives it the usage status
		// and does so before the session is resolved or any file is read.
		if recordKind == review.KindTests {
			if f != "" || v != "" {
				return usageErr(fmt.Errorf("review: -finding and -verdict apply to -kind findings, not -kind tests"))
			}
		} else if tst != "" || dec != "" || *edit != "" {
			return usageErr(fmt.Errorf("review: -test, -decision and -edit apply to -kind tests, not -kind findings"))
		}
		// The -test/-decision pairing, the draft id's syntax, the decision enum, and
		// -edit's pairing are all invocation facts, so they are refused here at the
		// usage status rather than from inside the package after the drafts load.
		if tst != "" && dec == "" {
			return usageErr(fmt.Errorf("review: -decision is required with -test"))
		}
		if dec != "" && tst == "" {
			return usageErr(fmt.Errorf("review: -test is required with -decision"))
		}
		if tst != "" && !drafttests.IsDraftID(tst) {
			return usageErr(fmt.Errorf("review: invalid -test %q (want T-NNN)", tst))
		}
		if dec != "" {
			if _, err := drafttests.ParseDecisionFlag(dec); err != nil {
				return usageErr(fmt.Errorf("review: %w", err))
			}
		}
		// An "edited" decision with no replacement fields is not representable, and
		// an -edit alongside any other decision would be silently discarded, so each
		// half of the pairing is refused from the flags alone.
		if dec == "edited" && *edit == "" {
			return usageErr(fmt.Errorf("review: -edit is required with -decision edited"))
		}
		if *edit != "" && dec != "edited" {
			return usageErr(fmt.Errorf("review: -edit applies only to -decision edited"))
		}
		// The -finding/-verdict pairing and the verdict's syntax are invocation
		// facts, so they are refused here at the usage status — reported from
		// review.Run they exited 1, and only after the findings load, so a wrong
		// flag on a session with no findings.jsonl was misreported as that.
		if f != "" && v == "" {
			return usageErr(fmt.Errorf("review: -verdict is required with -finding"))
		}
		if v != "" && f == "" {
			return usageErr(fmt.Errorf("review: -finding is required with -verdict"))
		}
		if f != "" && !analyze.IsFindingID(f) {
			return usageErr(fmt.Errorf("review: invalid -finding %q (want F-NNN)", f))
		}
		if v != "" {
			verdict, of, err := review.ParseVerdictFlag(v)
			if err != nil {
				return usageErr(fmt.Errorf("review: %w", err))
			}
			// A finding claimed as a duplicate of itself is a contradiction
			// knowable from the flags alone (IsFindingID is a strict F-NNN
			// match, so plain string equality decides it) — refused here, at
			// exit 2, alongside the other pairing/syntax checks. Left to
			// review.checkTargets, it surfaced only after review.Run had
			// stat'd the session directory and loaded findings.jsonl: at exit
			// 1, and on a session with no findings.jsonl yet, masked entirely
			// behind "run analyze -ingest first".
			if verdict == "duplicate" && of == f {
				return usageErr(fmt.Errorf("review: -finding cannot be a duplicate of itself"))
			}
		}
		// Resolved last of the invocation checks (see report above): a run refused
		// for another flag must not first announce an inferred session.
		sess, err := resolveSession(fs, *dir)
		if err != nil {
			return usageErr(err)
		}
		// Read the replacement fields through the no-follow guard, like every other
		// operator-named path on a session surface: the edit is naturally saved beside
		// the session, and a received session can ship a FIFO or a symlink at that name.
		var editIn io.Reader
		if *edit != "" {
			if *edit == "-" {
				editIn = os.Stdin
			} else {
				ef, err := session.OpenFileNoFollowRead(*edit)
				if err != nil {
					return fail(err)
				}
				defer ef.Close()
				editIn = ef
			}
		}
		if err := review.Run(review.Options{
			Dir:      sess,
			Kind:     recordKind,
			Finding:  f,
			Verdict:  v,
			Test:     tst,
			Decision: dec,
			EditIn:   editIn,
			In:       os.Stdin,
			Out:      os.Stdout,
			IsTTY:    isCharDevice(os.Stdin),
			Today:    time.Now().Format("2006-01-02"),
		}); err != nil {
			return fail(err)
		}
		return 0

	case "version":
		// version and help parse no flags, so the shared rejectArgs guard never
		// sees their leftovers; the same no-positional contract applies.
		if len(rest) > 0 {
			return usageErr(fmt.Errorf("version: unexpected argument %q (the command takes no positional arguments)", rest[0]))
		}
		fmt.Println("testimony", Version)
		return 0

	case "help", "-h", "--help":
		if len(rest) > 0 {
			return usageErr(fmt.Errorf("help: unexpected argument %q (the command takes no positional arguments)", rest[0]))
		}
		fmt.Print(usage)
		return 0

	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}

// rejectArgs refuses leftover positional arguments after flag parsing. Flag
// parsing stops at the first non-flag argument, so a stray positional silently
// discarded every flag that followed it and the command ran with defaults at
// exit 0 — an invocation the operator never gave. No command takes positional
// arguments (docs/reference/cli.md), so a leftover is a usage error.
func rejectArgs(fs *flag.FlagSet) error {
	if fs.NArg() > 0 {
		return fmt.Errorf("%s: unexpected argument %q (the command takes no positional arguments)", fs.Name(), fs.Arg(0))
	}
	return nil
}

// resolveSession returns the session directory a pipeline command operates on:
// the explicit -session flag when it is given, otherwise the current directory
// when that directory itself holds a manifest.json. It is the single resolution
// point for transcribe, merge, report, analyze, draft-tests, and review, so the
// six commands cannot drift in what they accept.
//
// Inference covers the exact current directory only — never a parent, the way
// git searches upward for .git — because a command that operated on an ancestor
// session from somewhere inside it would write evidence into a session the
// operator never named. It reports the inferred directory on stderr before any
// work starts: the choice is implicit, so it must at least be visible, and an
// operator who mistook which directory they were in can see it in the output of
// the run that misfired. An explicit -session prints nothing extra.
//
// An explicitly-empty -session is a wrong invocation (an unset shell variable
// spliced into the flag, say), not omission — the analyze -ingest/-out
// precedent. Left to fall through to inference it would silently run against
// whatever directory the caller happened to be standing in, which is exactly
// the wrong-session hazard inference has to avoid.
//
// The marker is a Testimony session manifest, not merely the file name:
// manifest.json is one of the most common file names in software (a web app
// manifest, a browser-extension manifest, a package manifest), so keying on
// the name alone would make `merge` write timeline.jsonl into an unrelated
// project's root and `report` overwrite a hand-written report.md there, both
// at exit 0. The manifest must be a regular file (Lstat, not Stat: every other
// manifest access goes through the no-follow guard, and a directory or a
// dangling symlink at that name is not the marker) and, when it parses, must
// carry the `session` field that session.Create always writes and
// docs/reference/session-directory.md requires. A manifest that fails to parse
// still infers, so a genuinely corrupt Testimony session reports its real
// parse error instead of a misleading "holds no manifest.json".
func resolveSession(fs *flag.FlagSet, dir string) (string, error) {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "session" {
			set = true
		}
	})
	if set {
		if dir == "" {
			return "", fmt.Errorf("%s: -session must not be empty", fs.Name())
		}
		return dir, nil
	}
	fi, err := os.Lstat(session.ManifestFile)
	if err != nil || !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s: -session is required (no -session flag, and the current directory holds no regular %s file)",
			fs.Name(), session.ManifestFile)
	}
	if m, err := session.LoadManifest("."); err == nil && m.Session == "" {
		return "", fmt.Errorf("%s: -session is required (the current directory holds a %s, but it is not a session manifest: no %q field)",
			fs.Name(), session.ManifestFile, "session")
	}
	fmt.Fprintf(os.Stderr, "%s: using session . (inferred from the current directory)\n", fs.Name())
	return ".", nil
}

// printErr writes an operator-facing error in the one shape every command uses.
func printErr(err error) {
	fmt.Fprintln(os.Stderr, "testimony:", err)
}

// fail reports a runtime failure of a well-formed command — the invocation was
// right and the work could not be done (exit 1).
func fail(err error) int {
	printErr(err)
	return 1
}

// usageErr reports a wrong invocation (exit 2), the status the no-command,
// unknown-command, and flag-parse paths already use. A missing required flag
// belongs with them: reported as a runtime error it was indistinguishable to a
// caller — a script, CI — from a session that genuinely could not be read.
// docs/reference/cli.md states the contract.
func usageErr(err error) int {
	printErr(err)
	return 2
}

// isCharDevice reports whether f is an interactive terminal, gating review's
// interactive walk so CI (where stdin is a pipe) never blocks.
func isCharDevice(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
