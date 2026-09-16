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
	"github.com/REPPL/Testimony/internal/coderefs"
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
  testimony record      [-out ~/Testimony/sessions] [-app NAME] [-participant P1] [-task ...]   managed capture: session dir + manifest, start recorders, run until Ctrl+C
                        [-commit HASH] [-video|-no-video] [-demo [-addr :8737]]
  testimony demo        [-addr :8737] [-out ~/Testimony/sessions]   serve the instrumented demo app, capture a session
  testimony transcribe  [-session DIR] [-audio FILE]    transcribe a voice recording into transcript.jsonl (reuses the session's audio.wav when -audio is omitted)
                        [-engine auto|whisperx|whispercpp] [-model large-v3-turbo] [-language en] [-offset SECONDS]
                        [-device auto|cpu|cuda] [-compute_type auto|int8|float16|…] [-vad auto|silero|pyannote]   (whisperx only)
  testimony import      [-session DIR] [-cast FILE]     import an asciinema recording's output into interactions.jsonl (reuses the session's terminal.cast when -cast is omitted)
                        [-offset SECONDS]
  testimony merge       [-session DIR]                  merge transcript + interactions into timeline.jsonl
  testimony report      [-session DIR] [-window 2.5]    render timeline.jsonl as a Markdown report
  testimony analyze     [-session DIR] [-out FILE]      emit the analysis request (rubric + timeline) on stdout or to FILE
  testimony analyze     [-session DIR] -ingest FILE     validate answer JSON (FILE or "-") → findings.jsonl (all findings unverified)
                        [-backend local|cloud] [-model NAME]   record what answered the request, beside the findings
  testimony draft-tests [-session DIR] [-window 10] [-out FILE]   emit the regression-test drafting request (rubric + confirmed findings + event windows)
  testimony draft-tests [-session DIR] -ingest FILE     validate answer JSON (FILE or "-") → tests.jsonl (all drafts proposed)
  testimony draft-tests [-session DIR] -render [-out FILE]        render the accepted drafts as Markdown test cases
  testimony map         [-session DIR] -repo DIR [-window 10] [-out FILE]   emit the code-mapping request (rubric + confirmed anchored findings + event windows + repository path)
  testimony map         [-session DIR] -repo DIR -ingest FILE     validate answer JSON (FILE or "-") → refs.jsonl (all references proposed; paths and lines checked against -repo)
  testimony map         [-session DIR] -render [-out FILE]        render an issue draft per mapped finding as Markdown
  testimony review      [-session DIR] [-kind findings|tests|refs]   interactively record verdicts on unverified findings, decisions on proposed test drafts, or decisions on proposed code references (stdin must be a character device)
  testimony review      [-session DIR] -finding F-NNN -verdict confirmed|rejected|duplicate-of-F-NNN
  testimony review      [-session DIR] -kind tests -test T-NNN -decision accepted|rejected
  testimony review      [-session DIR] -kind tests -test T-NNN -decision edited -edit FILE
  testimony review      [-session DIR] -kind refs [-repo DIR]   interactively decide proposed code references, showing the source around each when -repo is given
  testimony review      [-session DIR] -kind refs -ref R-NNN -decision accepted|rejected
  testimony version
  testimony help

A session directory is described in docs/reference/session-directory.md.
Omitting -session on transcribe, import, merge, report, analyze, draft-tests,
map, or review uses the current directory when it holds a Testimony session
manifest.json (one with a session field), and names the inferred session on
stderr.
record and demo create a new session under ~/Testimony/sessions unless -out
names another root, so a session lands in the same place whatever directory the
command was run from.
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
		root, rootErr := defaultSessionRoot()
		out := fs.String("out", root, "root directory for new session folders")
		fs.Parse(rest)
		if err := rejectArgs(fs); err != nil {
			return usageErr(err)
		}
		outSet := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "out" {
				outSet = true
			}
		})
		// The default root is the only thing a home directory is needed for, so
		// an unresolvable one is refused here and nowhere else: an explicit -out
		// runs exactly as it always has.
		if !outSet && rootErr != nil {
			return usageErr(fmt.Errorf("demo: %w", unresolvedRootErr(rootErr)))
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
		root, rootErr := defaultSessionRoot()
		out := fs.String("out", root, "root directory for new session folders")
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
		outSet := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "out" {
				outSet = true
			}
		})
		// See demo's identical check above.
		if !outSet && rootErr != nil {
			return usageErr(fmt.Errorf("record: %w", unresolvedRootErr(rootErr)))
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
		backend := fs.String("backend", "", "ingest mode: record which backend answered the request: local | cloud")
		model := fs.String("model", "", "ingest mode: record the model that answered the request (free text)")
		fs.Parse(rest)
		if err := rejectArgs(fs); err != nil {
			return usageErr(err)
		}
		outSet, ingestSet := false, false
		backendSet, modelSet := false, false
		fs.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "out":
				outSet = true
			case "ingest":
				ingestSet = true
			case "backend":
				backendSet = true
			case "model":
				modelSet = true
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
		// An explicitly-empty -backend or -model is a wrong invocation for the same
		// reason -ingest/-out are, and the guards must come before NewProvenance
		// below: an empty -backend would otherwise fall through its "flag not given"
		// branch and silently record "unrecorded" for an operator who believed they
		// had named a backend — the provenance record then states the opposite of
		// what they meant it to.
		if backendSet && *backend == "" {
			return usageErr(fmt.Errorf("analyze: -backend must not be empty"))
		}
		if modelSet && *model == "" {
			return usageErr(fmt.Errorf("analyze: -model must not be empty"))
		}
		// Both flags record what answered the request, which only ingest has an
		// answer to record against; emit mutates nothing in the session directory.
		// Silently ignored, they would let an operator who meant to record their
		// backend believe they had — the draft-tests -window precedent, which
		// refuses rather than ignores a flag that does nothing in the mode you are
		// in.
		if *ingest == "" && (backendSet || modelSet) {
			return usageErr(fmt.Errorf("analyze: -backend and -model apply to the ingest mode only"))
		}
		// The declaration's rules live in internal/analyze, which owns the record —
		// the review.ParseVerdictFlag precedent — so a bad declaration surfaces here
		// as a wrong invocation (exit 2) rather than as a runtime failure.
		prov, err := analyze.NewProvenance(*backend, *model, time.Now().Format("2006-01-02"))
		if err != nil {
			return usageErr(fmt.Errorf("analyze: %w", err))
		}
		// Resolved last of the invocation checks (see report above): a run refused
		// for another flag must not first announce an inferred session.
		sess, err := resolveSession(fs, *dir)
		if err != nil {
			return usageErr(err)
		}
		if *ingest != "" {
			// An implicit choice must at least be visible in the output of the run
			// that made it — resolveSession's inferred-session notice, applied to the
			// other implicit choice this command makes. On stderr, so a caller piping
			// analyze's output is unaffected.
			//
			// The tense is deliberate. This prints before the answer is validated, so
			// it also prints on runs that go on to fail and write nothing; "will
			// record" states an intention that a later refusal simply overtakes,
			// where "recording" would claim something the run never did.
			if !backendSet {
				fmt.Fprintln(os.Stderr, `analyze: no -backend given; the provenance will record "backend not recorded"`)
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
			findings, err := analyze.Ingest(sess, in, prov)
			if err != nil {
				return fail(err)
			}
			fmt.Printf("validated %d findings → %s (all unverified; %s)\n",
				len(findings), filepath.Join(sess, session.FindingsFile), describeProvenance(prov))
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

	case "map":
		fs := flag.NewFlagSet("map", flag.ExitOnError)
		dir := fs.String("session", "", "session directory")
		repo := fs.String("repo", "", "emit/ingest mode: the application's repository root (read only)")
		window := fs.Float64("window", coderefs.DefaultWindow, "emit mode: event-window half-width around a finding's evidence, seconds")
		out := fs.String("out", "", "emit/render mode: write to FILE instead of stdout")
		ingest := fs.String("ingest", "", "validate answer JSON at FILE (or \"-\" for stdin) into refs.jsonl")
		render := fs.Bool("render", false, "render an issue draft per mapped finding as Markdown")
		fs.Parse(rest)
		if err := rejectArgs(fs); err != nil {
			return usageErr(err)
		}
		outSet, ingestSet, windowSet, repoSet := false, false, false, false
		fs.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "out":
				outSet = true
			case "ingest":
				ingestSet = true
			case "window":
				windowSet = true
			case "repo":
				repoSet = true
			}
		})
		// An explicitly-empty path flag is a wrong invocation (an unset shell
		// variable spliced into the flag, say), not a valid path — the draft-tests
		// guard, applied to -repo as well.
		if ingestSet && *ingest == "" {
			return usageErr(fmt.Errorf("map: -ingest must not be empty"))
		}
		if outSet && *out == "" {
			return usageErr(fmt.Errorf("map: -out must not be empty"))
		}
		if repoSet && *repo == "" {
			return usageErr(fmt.Errorf("map: -repo must not be empty"))
		}
		// map runs in exactly one mode, following draft-tests' rule: emit (neither
		// -ingest nor -render), ingest (-ingest), or render (-render).
		if *ingest != "" {
			if *out != "" {
				return usageErr(fmt.Errorf("map: -out and -ingest cannot be combined"))
			}
			if *render {
				return usageErr(fmt.Errorf("map: -render and -ingest cannot be combined"))
			}
		}
		if windowSet && (*ingest != "" || *render) {
			return usageErr(fmt.Errorf("map: -window applies to the emit mode only"))
		}
		if math.IsNaN(*window) || math.IsInf(*window, 0) {
			return usageErr(fmt.Errorf("map: -window must be a finite number of seconds, got %v", *window))
		}
		// -repo is what emit hands the host and what ingest checks paths against;
		// render reads only what is on disk in the session and never opens the
		// repository, so a -repo alongside it is a flag from another mode, refused
		// rather than silently ignored. Required in the other two modes: there is
		// no default repository, and a request without one cannot be answered.
		if *render {
			if repoSet {
				return usageErr(fmt.Errorf("map: -repo applies to the emit and ingest modes only"))
			}
		} else {
			if *repo == "" {
				return usageErr(fmt.Errorf("map: -repo is required (the application's repository root)"))
			}
			// Refused from the flags alone, at the usage status: a -repo that is not
			// a directory is a wrong invocation, not a session that cannot be read.
			if fi, err := os.Stat(*repo); err != nil || !fi.IsDir() {
				if err == nil {
					err = fmt.Errorf("%s is not a directory", *repo)
				}
				return usageErr(fmt.Errorf("map: -repo: %v", err))
			}
		}
		sess, err := resolveSession(fs, *dir)
		if err != nil {
			return usageErr(err)
		}
		// The emitted request carries the repository's absolute path, the one
		// place an absolute local path appears in any artefact this tool writes,
		// and a session directory is an exchange unit. So in emit mode -out may
		// not land inside the session: an operator who inferred the session from
		// the current directory and wrote `-out request.md` would otherwise ship
		// their machine's layout with the session. Refused from the paths alone,
		// before any file is read.
		if *ingest == "" && !*render && *out != "" {
			if inside, err := insideDir(sess, *out); err != nil {
				return usageErr(fmt.Errorf("map: -out: %v", err))
			} else if inside {
				return usageErr(fmt.Errorf("map: -out must not be inside the session directory (the request names the repository's absolute path, and a session directory is an exchange unit)"))
			}
		}
		if *ingest != "" {
			in := os.Stdin
			if *ingest != "-" {
				f, err := session.OpenFileNoFollowRead(*ingest)
				if err != nil {
					return fail(err)
				}
				defer f.Close()
				in = f
			}
			refs, err := coderefs.Ingest(sess, *repo, in)
			if err != nil {
				return fail(err)
			}
			fmt.Printf("validated %d references → %s (all proposed)\n",
				len(refs), filepath.Join(sess, session.RefsFile))
			return 0
		}
		var doc string
		if *render {
			doc, err = coderefs.Render(sess)
		} else {
			doc, err = coderefs.EmitRequest(sess, *repo, *window)
		}
		if err != nil {
			return fail(err)
		}
		if *out != "" {
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
		kind := fs.String("kind", review.KindFindings, "which record family to review: findings | tests | refs")
		finding := fs.String("finding", "", "non-interactive: the finding to judge (F-NNN)")
		verdict := fs.String("verdict", "", "non-interactive: confirmed | rejected | duplicate-of-F-NNN")
		test := fs.String("test", "", "non-interactive (-kind tests): the test draft to decide (T-NNN)")
		decision := fs.String("decision", "", "non-interactive (-kind tests or refs): accepted | edited | rejected (edited is -kind tests only)")
		edit := fs.String("edit", "", "with -decision edited: the replacement fields as a JSON object at FILE (or \"-\" for stdin)")
		ref := fs.String("ref", "", "non-interactive (-kind refs): the reference to decide (R-NNN)")
		repo := fs.String("repo", "", "-kind refs: the application's repository root, read only, to show the source around each reference")
		fs.Parse(rest)
		if err := rejectArgs(fs); err != nil {
			return usageErr(err)
		}
		f, v := strings.TrimSpace(*finding), strings.TrimSpace(*verdict)
		tst, dec := strings.TrimSpace(*test), strings.TrimSpace(*decision)
		rf := strings.TrimSpace(*ref)
		findingSet, verdictSet := false, false
		kindSet, testSet, decisionSet, editSet := false, false, false, false
		refSet, repoSet := false, false
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
			case "ref":
				refSet = true
			case "repo":
				repoSet = true
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
		if refSet && rf == "" {
			return usageErr(fmt.Errorf("review: -ref must not be empty"))
		}
		if repoSet && *repo == "" {
			return usageErr(fmt.Errorf("review: -repo must not be empty"))
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
		switch recordKind {
		case review.KindTests:
			if f != "" || v != "" {
				return usageErr(fmt.Errorf("review: -finding and -verdict apply to -kind findings, not -kind tests"))
			}
			if rf != "" || *repo != "" {
				return usageErr(fmt.Errorf("review: -ref and -repo apply to -kind refs, not -kind tests"))
			}
		case review.KindRefs:
			if f != "" || v != "" {
				return usageErr(fmt.Errorf("review: -finding and -verdict apply to -kind findings, not -kind refs"))
			}
			if tst != "" || *edit != "" {
				return usageErr(fmt.Errorf("review: -test and -edit apply to -kind tests, not -kind refs"))
			}
		default:
			// The refs family is checked first so that -ref alongside -decision is
			// named for the flag that identifies the family, not for the one the
			// two families share.
			if rf != "" || *repo != "" {
				return usageErr(fmt.Errorf("review: -ref and -repo apply to -kind refs, not -kind findings"))
			}
			if tst != "" || dec != "" || *edit != "" {
				return usageErr(fmt.Errorf("review: -test, -decision and -edit apply to -kind tests, not -kind findings"))
			}
		}
		// The -ref/-decision pairing, the reference id's syntax, and the decision
		// enum (no "edited": a wrong path is rejected and a corrected one is
		// ingested, never patched) are invocation facts, refused at the usage
		// status. A -repo that is not a directory is refused the same way, as map
		// refuses it; the walk opens it read-only for the source snippet.
		if recordKind == review.KindRefs {
			if rf != "" && dec == "" {
				return usageErr(fmt.Errorf("review: -decision is required with -ref"))
			}
			if dec != "" && rf == "" {
				return usageErr(fmt.Errorf("review: -ref is required with -decision"))
			}
			if rf != "" && !coderefs.IsRefID(rf) {
				return usageErr(fmt.Errorf("review: invalid -ref %q (want R-NNN)", rf))
			}
			if dec != "" {
				if _, err := coderefs.ParseDecisionFlag(dec); err != nil {
					return usageErr(fmt.Errorf("review: %w", err))
				}
			}
			if *repo != "" {
				// The snippet is shown only by the interactive walk, so -repo beside
				// a single decision would be silently ignored; refused instead, as
				// map refuses -window outside emit.
				if rf != "" {
					return usageErr(fmt.Errorf("review: -repo applies to the interactive walk, not to -ref/-decision"))
				}
				if fi, err := os.Stat(*repo); err != nil || !fi.IsDir() {
					if err == nil {
						err = fmt.Errorf("%s is not a directory", *repo)
					}
					return usageErr(fmt.Errorf("review: -repo: %v", err))
				}
			}
		}
		// The -test/-decision pairing, the draft id's syntax, the decision enum, and
		// -edit's pairing are all invocation facts, so they are refused here at the
		// usage status rather than from inside the package after the drafts load.
		if recordKind == review.KindTests {
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
			Ref:      rf,
			Repo:     *repo,
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

// describeProvenance renders the declaration for the ingest success line, so the
// operator sees what was recorded in the output of the run that recorded it
// rather than having to open findings.jsonl to find out.
//
// The backend phrase comes from a switch on the closed enum, never from the
// stored string; the model is operator-supplied text reaching a terminal, so it
// goes through session.SafeText — the same treatment the emitted request gives
// manifest fields — and falls back to the placeholder when it renders as
// nothing.
func describeProvenance(p analyze.Provenance) string {
	var backend string
	switch p.Backend {
	case analyze.BackendLocal:
		backend = "local backend"
	case analyze.BackendCloud:
		backend = "cloud backend"
	default:
		backend = "backend not recorded"
	}
	// Every branch falls through to the model clause rather than returning early:
	// the two halves of the declaration are independent, so a record that carries
	// a model must say so whatever its backend reads, and the line keeps one shape
	// the operator can scan for in all three cases.
	if session.CodeRendersEmpty(p.Model) {
		return backend + ", model not recorded"
	}
	return backend + ", model " + session.SafeText(p.Model)
}

// insideDir reports whether path lies inside dir (or is dir itself), comparing
// absolute, symlink-resolved forms so a session reached through a symlinked
// temp root and an -out written through the real one still compare equal. The
// output file need not exist yet, so its parent is what is resolved.
func insideDir(dir, path string) (bool, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return false, err
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	target = resolveExisting(target)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false, nil
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))), nil
}

// resolveExisting resolves the symlinks in the deepest existing ancestor of an
// absolute path and rejoins the rest, so a path whose file or parent does not
// exist yet still compares against a resolved root on equal terms.
func resolveExisting(abs string) string {
	rest := ""
	dir := abs
	for {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return abs
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
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

// defaultRootDisplay is the default session root as an operator writes it —
// the form the usage block, the documentation, and every message about it use,
// rather than the expanded path of whoever happens to be running the command.
const defaultRootDisplay = "~/Testimony/sessions"

// defaultSessionRoot returns the root under which record and demo create a new
// session when -out is not given. It is the single definition of that default:
// both commands register the value it returns as their -out default, so `-h`
// shows the real directory a session will land in and the two cannot drift.
//
// The root is fixed rather than relative to the working directory because a
// session is evidence an operator comes back to, and a relative "sessions"
// scattered one session per directory the command happened to be run from —
// findable later only by remembering where you stood. ~/Testimony/sessions is
// preferred to an XDG-style ~/.local/share/testimony/sessions for the same
// reason: captured sessions are documents to open, browse, and hand on, not
// application state, and a hidden directory hides them.
//
// The home directory is resolved at invocation time, never cached, and a home
// that cannot be resolved is reported rather than papered over: silently
// falling back to a relative root would put the session in whatever directory
// the operator was standing in, which is the outcome the fixed default exists
// to end.
func defaultSessionRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Testimony", "sessions"), nil
}

// unresolvedRootErr phrases the refusal both capture commands give when -out is
// omitted and the default root cannot be resolved. It names the root, carries
// the reason, and tells the operator the one thing that gets them moving —
// exactly one flag — rather than leaving them to discover it in the usage block.
func unresolvedRootErr(err error) error {
	return fmt.Errorf("-out is required (the default root %s cannot be resolved: %w); pass -out DIR",
		defaultRootDisplay, err)
}

// resolveSession returns the session directory a pipeline command operates on:
// the explicit -session flag when it is given, otherwise the current directory
// when that directory itself holds a manifest.json. It is the single resolution
// point for transcribe, import, merge, report, analyze, draft-tests, map, and
// review, so the eight commands cannot drift in what they accept.
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
