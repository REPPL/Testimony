// Package cli implements the testimony command-line interface.
package cli

import (
	"flag"
	"fmt"
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
                        [-video|-no-video] [-demo [-addr :8737]]
  testimony demo        [-addr :8737] [-out sessions]   serve the instrumented demo app, capture a session
  testimony transcribe   -session DIR [-audio FILE]     transcribe a voice recording into transcript.jsonl (reuses the session's audio.wav when -audio is omitted)
                        [-engine auto|whisperx|whispercpp] [-model large-v3-turbo] [-language en] [-offset SECONDS]
                        [-device auto|cpu|cuda] [-compute_type auto|int8|float16] [-vad auto|silero|pyannote]   (whisperx only)
  testimony merge        -session DIR                   merge transcript + interactions into timeline.jsonl
  testimony report       -session DIR [-window 2.5]     render timeline.jsonl as a Markdown report
  testimony analyze      -session DIR [-out FILE]        emit the analysis request (rubric + timeline) on stdout or to FILE
  testimony analyze      -session DIR -ingest FILE       validate answer JSON (FILE or "-") → findings.jsonl (all findings unverified)
  testimony review       -session DIR                    interactively record verdicts on unverified findings (TTY-gated)
  testimony review       -session DIR -finding F-NNN -verdict confirmed|rejected|duplicate-of-F-NNN
  testimony version

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
		if err := demo.Run(*addr, *out); err != nil {
			return fail(err)
		}
		return 0

	case "merge":
		fs := flag.NewFlagSet("merge", flag.ExitOnError)
		dir := fs.String("session", "", "session directory")
		fs.Parse(rest)
		if *dir == "" {
			return fail(fmt.Errorf("merge: -session is required"))
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
		if *dir == "" {
			return fail(fmt.Errorf("report: -session is required"))
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
		if *dir == "" {
			return fail(fmt.Errorf("transcribe: -session is required"))
		}
		offsetSet := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "offset" {
				offsetSet = true
			}
		})
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
		if *dir == "" {
			return fail(fmt.Errorf("analyze: -session is required"))
		}
		if *ingest != "" {
			if *out != "" {
				return fail(fmt.Errorf("analyze: -out and -ingest cannot be combined"))
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
		if *dir == "" {
			return fail(fmt.Errorf("review: -session is required"))
		}
		if err := review.Run(review.Options{
			Dir:     *dir,
			Finding: strings.TrimSpace(*finding),
			Verdict: strings.TrimSpace(*verdict),
			In:      os.Stdin,
			Out:     os.Stdout,
			IsTTY:   isCharDevice(os.Stdin),
			Today:   time.Now().Format("2006-01-02"),
		}); err != nil {
			return fail(err)
		}
		return 0

	case "version":
		fmt.Println("testimony", Version)
		return 0

	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0

	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "testimony:", err)
	return 1
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
