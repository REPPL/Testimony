// Package cli implements the testimony command-line interface.
package cli

import (
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/REPPL/Testimony/internal/analyze"
	"github.com/REPPL/Testimony/internal/demo"
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
  testimony transcribe   -session DIR [-audio FILE]     transcribe a voice recording into transcript.jsonl (reuses the session's audio.wav when -audio is omitted)
                        [-engine auto|whisperx|whispercpp] [-model large-v3-turbo] [-language en] [-offset SECONDS]
                        [-device auto|cpu|cuda] [-compute_type auto|int8|float16] [-vad auto|silero|pyannote]   (whisperx only)
  testimony merge        -session DIR                   merge transcript + interactions into timeline.jsonl
  testimony report       -session DIR [-window 2.5]     render timeline.jsonl as a Markdown report
  testimony analyze      -session DIR [-out FILE]        emit the analysis request (rubric + timeline) on stdout or to FILE
  testimony analyze      -session DIR -ingest FILE       validate answer JSON (FILE or "-") → findings.jsonl (all findings unverified)
  testimony review       -session DIR                    interactively record verdicts on unverified findings (stdin must be a character device)
  testimony review       -session DIR -finding F-NNN -verdict confirmed|rejected|duplicate-of-F-NNN
  testimony version
  testimony help

A session directory is described in docs/reference/session-directory.md.
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
		if *dir == "" {
			return usageErr(fmt.Errorf("merge: -session is required"))
		}
		speech, events, err := timeline.Merge(*dir)
		if err != nil {
			return fail(err)
		}
		fmt.Printf("merged %d utterances + %d events → %s\n",
			speech, events, filepath.Join(*dir, "timeline.jsonl"))
		return 0

	case "report":
		fs := flag.NewFlagSet("report", flag.ExitOnError)
		dir := fs.String("session", "", "session directory")
		window := fs.Float64("window", 2.5, "utterance↔event join window, seconds")
		fs.Parse(rest)
		if err := rejectArgs(fs); err != nil {
			return usageErr(err)
		}
		if *dir == "" {
			return usageErr(fmt.Errorf("report: -session is required"))
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
		md, err := report.Render(*dir, *window)
		if err != nil {
			return fail(err)
		}
		out := filepath.Join(*dir, "report.md")
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
		if *dir == "" {
			return usageErr(fmt.Errorf("transcribe: -session is required"))
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
		n, err := transcribe.Run(transcribe.Options{
			SessionDir: *dir,
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
		fmt.Printf("transcribed %d utterances → %s\n", n, filepath.Join(*dir, session.TranscriptFile))
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
		if *dir == "" {
			return usageErr(fmt.Errorf("analyze: -session is required"))
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
		if *ingest != "" {
			if *out != "" {
				return usageErr(fmt.Errorf("analyze: -out and -ingest cannot be combined"))
			}
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
			findings, err := analyze.Ingest(*dir, in)
			if err != nil {
				return fail(err)
			}
			fmt.Printf("validated %d findings → %s (all unverified)\n",
				len(findings), filepath.Join(*dir, session.FindingsFile))
			return 0
		}
		prompt, err := analyze.EmitRequest(*dir)
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

	case "review":
		fs := flag.NewFlagSet("review", flag.ExitOnError)
		dir := fs.String("session", "", "session directory")
		finding := fs.String("finding", "", "non-interactive: the finding to judge (F-NNN)")
		verdict := fs.String("verdict", "", "non-interactive: confirmed | rejected | duplicate-of-F-NNN")
		fs.Parse(rest)
		if err := rejectArgs(fs); err != nil {
			return usageErr(err)
		}
		if *dir == "" {
			return usageErr(fmt.Errorf("review: -session is required"))
		}
		f, v := strings.TrimSpace(*finding), strings.TrimSpace(*verdict)
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
		if err := review.Run(review.Options{
			Dir:     *dir,
			Finding: f,
			Verdict: v,
			In:      os.Stdin,
			Out:     os.Stdout,
			IsTTY:   isCharDevice(os.Stdin),
			Today:   time.Now().Format("2006-01-02"),
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
