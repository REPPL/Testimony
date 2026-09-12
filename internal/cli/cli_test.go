package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/REPPL/Testimony/internal/session"
)

// captureStderr runs fn with os.Stderr redirected to a pipe and returns
// everything it wrote there, so a test can assert on the operator-facing
// message and not merely on the exit code.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stderr
	os.Stderr = w
	read := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		read <- string(b)
	}()
	fn()
	os.Stderr = old
	w.Close()
	got := <-read
	r.Close()
	return got
}

// miniSession writes a minimal but valid session (manifest + one timeline entry)
// so `analyze` reaches its -out write / -ingest read without failing earlier.
func miniSession(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "s", App: "app", Participant: "P1"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	tl := `{"t":0,"src":"speech","id":"utt-001","payload":{"speaker":"P1","t1":1,"text":"hi"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(tl), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}
	return dir
}

// TestAnalyzeOutRefusesSymlink is the F6 write-side regression: `analyze -out` used
// plain os.WriteFile, which follows a symlink planted at the output name in an
// exchanged session and truncates an arbitrary file outside it. Routed through
// session.WriteFileNoFollow, the write is refused and the outside target is untouched.
func TestAnalyzeOutRefusesSymlink(t *testing.T) {
	dir := miniSession(t)
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("ORIGINAL"), 0o600); err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	out := filepath.Join(dir, "request.md")
	if err := os.Symlink(outside, out); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if code := Run([]string{"analyze", "-session", dir, "-out", out}); code == 0 {
		t.Fatal("analyze -out followed a symlink; want a non-zero exit")
	}
	if b, _ := os.ReadFile(outside); string(b) != "ORIGINAL" {
		t.Fatalf("out-of-session file overwritten through symlink: %q", b)
	}
}

// TestAnalyzeIngestRefusesFIFO is the F6 read-side regression: `analyze -ingest FILE`
// used plain os.Open, which blocks in open(2) for ever on a FIFO planted at the
// answer name in an exchanged session — Ingest's byte cap never helps because the
// open never returns. Routed through session.OpenFileNoFollowRead, the FIFO is
// refused at once. The test runs Run in a goroutine and fails on timeout.
func TestAnalyzeIngestRefusesFIFO(t *testing.T) {
	dir := miniSession(t)
	fifo := filepath.Join(dir, "answer.json")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("FIFOs unavailable: %v", err)
	}
	done := make(chan int, 1)
	go func() { done <- Run([]string{"analyze", "-session", dir, "-ingest", fifo}) }()
	select {
	case code := <-done:
		if code == 0 {
			t.Fatal("analyze -ingest of a FIFO returned success; want refusal")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("analyze -ingest blocked on a FIFO instead of refusing it")
	}
}

// TestReportRejectsNonFiniteWindow is the fabricated-join regression: -window
// took any float64 strconv would parse, so NaN and ±Inf reached report.Render.
// Every comparison against NaN is false, so a NaN window detached every event
// from the speech it accompanied; +Inf made every event fall inside the first
// utterance's window and be filed under it. Both wrote a report.md that misstates
// what the participant was doing while they spoke, and both exited 0. A negative
// window is legitimate (it narrows the join) and must stay accepted.
func TestReportRejectsNonFiniteWindow(t *testing.T) {
	for _, w := range []string{"NaN", "Inf", "-Inf"} {
		dir := miniSession(t)
		var code int
		stderr := captureStderr(t, func() {
			code = Run([]string{"report", "-session", dir, "-window", w})
		})
		if code != 2 {
			t.Fatalf("-window %s: exit %d, want 2 (usage error, like every other bad invocation)", w, code)
		}
		if !strings.Contains(stderr, "testimony: report: -window must be a finite number") {
			t.Fatalf("-window %s: want the finite-window refusal on stderr, got %q", w, stderr)
		}
		if _, err := os.Stat(filepath.Join(dir, session.ReportFile)); !os.IsNotExist(err) {
			t.Fatalf("-window %s rendered a report anyway (err=%v)", w, err)
		}
	}

	for _, w := range []string{"2.5", "-1"} {
		dir := miniSession(t)
		if code := Run([]string{"report", "-session", dir, "-window", w}); code != 0 {
			t.Fatalf("-window %s must still render, got exit %d", w, code)
		}
		if _, err := os.Stat(filepath.Join(dir, session.ReportFile)); err != nil {
			t.Fatalf("-window %s wrote no report: %v", w, err)
		}
	}
}

// TestTranscribeRejectsUnusableOffset pins -offset to the same invocation
// contract as -window: a non-finite or over-magnitude value is a wrong
// invocation (exit 2), refused before any conversion or engine work. Unchecked,
// `-offset NaN` failed only after that work was already spent, with a bare
// JSON encoding error at exit 1, and `-offset 1e300` wrote a transcript at
// exit 0 that merge refuses one command later, naming transcript.jsonl
// rather than the flag. The refusal must precede engine detection, so this test
// needs no ASR engine on PATH — on the pre-fix path these invocations instead
// failed with the engine-missing (or JSON encoding) runtime error at exit 1.
func TestTranscribeRejectsUnusableOffset(t *testing.T) {
	dir := miniSession(t)
	for _, v := range []string{"NaN", "Inf", "-Inf", "1e300"} {
		var code int
		stderr := captureStderr(t, func() {
			code = Run([]string{"transcribe", "-session", dir, "-offset", v})
		})
		if code != 2 {
			t.Errorf("-offset %s: exit %d, want 2 (usage error, like -window)", v, code)
		}
		if !strings.Contains(stderr, "testimony: transcribe: -offset") {
			t.Errorf("-offset %s: want the -offset refusal on stderr, got %q", v, stderr)
		}
	}
}

// TestStrayPositionalIsAUsageError pins the other half of the invocation
// contract: no command takes positional arguments (docs/reference/cli.md), and
// flag parsing stops at the first non-flag argument, so a stray positional
// silently discarded every flag after it — `report -session S junk -window X`
// rendered with the default window at exit 0, and `transcribe ... recording.m4a
// -offset 99` dropped the operator's offset. A leftover argument must refuse
// the run as a usage error before any work starts. `demo` is exercised through
// the same shared guard but not run here: on the pre-fix path it blocks
// serving until interrupted.
func TestStrayPositionalIsAUsageError(t *testing.T) {
	dir := miniSession(t)
	cases := [][]string{
		{"merge", "-session", dir, "junk"},
		{"report", "-session", dir, "junk", "-window", "NaN"},
		{"transcribe", "-session", dir, "junk", "-offset", "99"},
		{"import", "-session", dir, "junk", "-offset", "99"},
		{"analyze", "-session", dir, "junk", "-out", "x", "-ingest", "-"},
		{"review", "-session", dir, "junk", "-finding", "F-001", "-verdict", "confirmed"},
		{"draft-tests", "-session", dir, "junk", "-render"},
		{"record", "-out", t.TempDir(), "junk", "-participant", "P9"},
		{"version", "junk"},
		{"help", "junk"},
	}
	for _, args := range cases {
		var code int
		stderr := captureStderr(t, func() { code = Run(args) })
		if code != 2 {
			t.Errorf("%v: exit %d, want 2 (usage error)", args, code)
		}
		if want := `unexpected argument "junk"`; !strings.Contains(stderr, want) {
			t.Errorf("%v: want %q on stderr, got %q", args, want, stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, session.ReportFile)); !os.IsNotExist(err) {
		t.Errorf("report with a stray positional rendered a report anyway (err=%v)", err)
	}
}

// TestInvalidFlagValuesExitTwo pins exit 2 for the usage errors that were still
// reported at the runtime status: the -finding/-verdict pairing, an invalid
// -verdict value, an unknown -engine, and a malformed capture -addr. Reported
// from inside the packages they took exit 1, so a script could not tell a
// mistyped flag from a session that genuinely could not be read. Validation
// must also precede any work: `demo -addr bogus` used to create a session
// directory before refusing the address.
//
// An explicitly-empty -ingest/-out on analyze is the same "unset shell
// variable spliced into the flag" class as demo/record's -out guard, but at
// the one site where it silently changes which mode the command runs in: an
// empty -ingest fell through analyze's `*ingest != ""` mode check to emit
// mode at exit 0, and an empty -out fell through to stdout at exit 0 instead
// of writing a file — both a script trusts to have written the wrong thing.
// A finding claimed as a duplicate of itself is knowable from the flags
// alone (IsFindingID makes plain string equality decide it), but was
// previously refused only in review.checkTargets after review.Run had
// already stat'd the session directory and loaded findings.jsonl — at exit
// 1, and on a session with no findings.jsonl yet, masked entirely behind
// "run analyze -ingest first".
//
// An explicitly-empty -audio on transcribe is the same class as analyze's
// -ingest/-out: unchecked, it silently selected the in-place branch instead
// of refusing the caller's mistake — transcribing the session's own
// audio.wav at exit 0 when one exists, or failing at exit 1 with a message
// claiming -audio was never given when one doesn't.
//
// An explicitly-empty -engine/-device/-vad closes the last gap in that same
// class: each is a closed enum (docs/reference/cli.md) whose own package-level
// Check* function treated "" the same as the documented "auto", so a typo'd or
// unset shell variable silently discarded the caller's actual choice at
// exit 0 instead of refusing it like every other closed-enum flag on this
// command.
func TestInvalidFlagValuesExitTwo(t *testing.T) {
	dir := miniSession(t)
	demoOut := t.TempDir()
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"review", "-session", dir, "-finding", "F-001"}, "review: -verdict is required with -finding"},
		{[]string{"review", "-session", dir, "-verdict", "confirmed"}, "review: -finding is required with -verdict"},
		{[]string{"review", "-session", dir, "-finding", "F-001", "-verdict", "bogus"}, `review: invalid verdict "bogus"`},
		{[]string{"review", "-session", dir, "-finding", "F-01", "-verdict", "confirmed"}, `review: invalid -finding "F-01"`},
		{[]string{"transcribe", "-session", dir, "-audio", "rec.txt"}, `transcribe: unsupported audio format ".txt"`},
		{[]string{"transcribe", "-session", dir, "-engine", "bogus"}, `transcribe: unknown engine "bogus"`},
		{[]string{"transcribe", "-session", dir, "-device", "cudda"}, `transcribe: unknown -device "cudda"`},
		{[]string{"transcribe", "-session", dir, "-vad", "silreo"}, `transcribe: unknown -vad "silreo"`},
		{[]string{"demo", "-addr", "bogus", "-out", demoOut}, `demo: invalid capture address "bogus"`},
		{[]string{"record", "-demo", "-addr", "bogus", "-out", t.TempDir()}, `record: invalid capture address "bogus"`},
		{[]string{"demo", "-out", ""}, `demo: -out must not be empty`},
		{[]string{"record", "-out", ""}, `record: -out must not be empty`},
		{[]string{"analyze", "-session", dir, "-ingest", ""}, `analyze: -ingest must not be empty`},
		{[]string{"analyze", "-session", dir, "-out", ""}, `analyze: -out must not be empty`},
		{[]string{"transcribe", "-session", dir, "-audio", ""}, `transcribe: -audio must not be empty`},
		{[]string{"transcribe", "-session", dir, "-engine", ""}, `transcribe: -engine must not be empty`},
		{[]string{"transcribe", "-session", dir, "-device", ""}, `transcribe: -device must not be empty`},
		{[]string{"transcribe", "-session", dir, "-vad", ""}, `transcribe: -vad must not be empty`},
		{[]string{"import", "-session", dir, "-cast", ""}, `import: -cast must not be empty`},
		{[]string{"import", "-session", dir, "-offset", "NaN"}, `import: -offset must be a finite number of seconds, got NaN`},
		{[]string{"import", "-session", dir, "-offset", "1e10"}, `import: -offset 1e+10 exceeds 1e+09 seconds in magnitude`},
		{[]string{"review", "-session", dir, "-finding", "", "-verdict", ""}, `review: -finding must not be empty`},
		{[]string{"review", "-session", dir, "-finding", "F-001", "-verdict", ""}, `review: -verdict must not be empty`},
		{[]string{"review", "-session", dir, "-finding", "F-001", "-verdict", "duplicate-of-F-001"}, `review: -finding cannot be a duplicate of itself`},
		{[]string{"draft-tests", "-session", dir, "-ingest", ""}, `draft-tests: -ingest must not be empty`},
		{[]string{"draft-tests", "-session", dir, "-out", ""}, `draft-tests: -out must not be empty`},
		{[]string{"draft-tests", "-session", dir, "-out", "f.md", "-ingest", "-"}, `draft-tests: -out and -ingest cannot be combined`},
		{[]string{"draft-tests", "-session", dir, "-render", "-ingest", "-"}, `draft-tests: -render and -ingest cannot be combined`},
		{[]string{"draft-tests", "-session", dir, "-window", "NaN"}, `draft-tests: -window must be a finite number of seconds`},
		{[]string{"draft-tests", "-session", dir, "-window", "+Inf"}, `draft-tests: -window must be a finite number of seconds`},
		{[]string{"draft-tests", "-session", dir, "-window", "20", "-ingest", "-"}, `draft-tests: -window applies to the emit mode only`},
		{[]string{"draft-tests", "-session", dir, "-window", "20", "-render"}, `draft-tests: -window applies to the emit mode only`},
		{[]string{"review", "-session", dir, "-kind", ""}, `review: -kind must not be empty`},
		{[]string{"review", "-session", dir, "-kind", "tests", "-test", ""}, `review: -test must not be empty`},
		{[]string{"review", "-session", dir, "-kind", "tests", "-decision", ""}, `review: -decision must not be empty`},
		{[]string{"review", "-session", dir, "-kind", "tests", "-edit", ""}, `review: -edit must not be empty`},
		{[]string{"review", "-session", dir, "-kind", "verdicts"}, `review: invalid kind "verdicts"`},
		{[]string{"review", "-session", dir, "-kind", "tests", "-test", "T-01", "-decision", "accepted"}, `review: invalid -test "T-01"`},
		{[]string{"review", "-session", dir, "-kind", "tests", "-test", "T-001", "-decision", "maybe"}, `review: invalid decision "maybe"`},
		{[]string{"review", "-session", dir, "-kind", "tests", "-test", "T-001"}, `review: -decision is required with -test`},
		{[]string{"review", "-session", dir, "-kind", "tests", "-decision", "accepted"}, `review: -test is required with -decision`},
		{[]string{"review", "-session", dir, "-kind", "tests", "-test", "T-001", "-decision", "edited"}, `review: -edit is required with -decision edited`},
		{[]string{"review", "-session", dir, "-kind", "tests", "-test", "T-001", "-decision", "accepted", "-edit", "e.json"}, `review: -edit applies only to -decision edited`},
		{[]string{"review", "-session", dir, "-kind", "tests", "-finding", "F-001", "-verdict", "confirmed"}, `review: -finding and -verdict apply to -kind findings, not -kind tests`},
		{[]string{"review", "-session", dir, "-test", "T-001", "-decision", "accepted"}, `review: -test, -decision and -edit apply to -kind tests, not -kind findings`},
		{[]string{"review", "-session", dir, "-edit", "e.json"}, `review: -test, -decision and -edit apply to -kind tests, not -kind findings`},
	}
	for _, c := range cases {
		var code int
		stderr := captureStderr(t, func() { code = Run(c.args) })
		if code != 2 {
			t.Errorf("%v: exit %d, want 2 (usage error)", c.args, code)
		}
		if !strings.Contains(stderr, c.want) {
			t.Errorf("%v: want %q on stderr, got %q", c.args, c.want, stderr)
		}
	}
	if entries, err := os.ReadDir(demoOut); err != nil || len(entries) != 0 {
		t.Errorf("demo -addr bogus created a session directory before refusing (entries=%d, err=%v)", len(entries), err)
	}
}

// TestUsageListsEveryFlagAndCommand pins the top-level usage text against the
// documented invocation surface: record's -commit flag and the help command
// are part of docs/reference/cli.md but were absent from `testimony help`.
func TestUsageListsEveryFlagAndCommand(t *testing.T) {
	for _, want := range []string{"-commit HASH", "testimony help",
		"testimony draft-tests", "-window 10", "-kind findings|tests", "-decision edited -edit FILE",
		"transcribe, import, merge, report, analyze, draft-tests, or"} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage text does not mention %q", want)
		}
	}
}

// TestMissingSessionIsAUsageError pins the exit-status contract of
// docs/reference/cli.md: a wrong invocation exits 2 and a runtime failure of a
// well-formed command exits 1. A missing required -session was reported as a
// runtime error (1), so it was indistinguishable to a caller from a session that
// genuinely could not be read, while the sibling usage errors — no command, an
// unknown command, a flag-parse failure — all exited 2.
func TestMissingSessionIsAUsageError(t *testing.T) {
	// Run from a directory that is certainly not a session: without this the
	// refusal depends on the package directory happening to hold no
	// manifest.json, which is no longer merely incidental now that its
	// absence is what sends these invocations down the refusal path.
	chdir(t, t.TempDir())
	for _, cmd := range []string{"merge", "report", "transcribe", "import", "analyze", "draft-tests", "review"} {
		var code int
		stderr := captureStderr(t, func() { code = Run([]string{cmd}) })
		if code != 2 {
			t.Errorf("%s without -session: exit %d, want 2 (usage error)", cmd, code)
		}
		if want := "testimony: " + cmd + ": -session is required"; !strings.Contains(stderr, want) {
			t.Errorf("%s without -session: want %q on stderr, got %q", cmd, want, stderr)
		}
	}

	// Mutually exclusive flags are a wrong invocation too.
	{
		var code int
		stderr := captureStderr(t, func() {
			code = Run([]string{"analyze", "-session", t.TempDir(), "-out", "f.md", "-ingest", "-"})
		})
		if code != 2 {
			t.Errorf("analyze -out with -ingest: exit %d, want 2 (usage error)", code)
		}
		if want := "testimony: analyze: -out and -ingest cannot be combined"; !strings.Contains(stderr, want) {
			t.Errorf("analyze -out with -ingest: want %q on stderr, got %q", want, stderr)
		}
	}

	// A well-formed command that fails at runtime keeps exit 1.
	var code int
	captureStderr(t, func() {
		code = Run([]string{"merge", "-session", filepath.Join(t.TempDir(), "absent")})
	})
	if code != 1 {
		t.Errorf("merge on an unreadable session: exit %d, want 1 (runtime error)", code)
	}
}

// chdir makes dir the process working directory for the duration of the test
// and restores the old one afterwards, so the session-inference tests can run
// "from inside a session" the way an operator does. (testing.T.Chdir would say
// this in one line, but it postdates the language version in go.mod.)
func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
}

// manifestOnlySession writes a session holding nothing but manifest.json — the
// marker inference keys on, and enough for merge to write an empty timeline.
func manifestOnlySession(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "s", App: "app", Participant: "P1"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	return dir
}

// TestSessionInferredFromCurrentDirectory is the itd-12 acceptance case: with a
// manifest.json in the current directory and no -session flag, a pipeline
// command operates on that directory exactly as `-session .` does, and says on
// stderr which session it inferred — the implicit choice has to be visible, or
// an operator who mistook which directory they were in has nothing in the
// output of the run that misfired to tell them so.
func TestSessionInferredFromCurrentDirectory(t *testing.T) {
	t.Run("merge", func(t *testing.T) {
		dir := manifestOnlySession(t)
		chdir(t, dir)
		var code int
		stderr := captureStderr(t, func() { code = Run([]string{"merge"}) })
		if code != 0 {
			t.Fatalf("merge from inside a session: exit %d, want 0 (stderr %q)", code, stderr)
		}
		if want := "merge: using session . (inferred from the current directory)"; !strings.Contains(stderr, want) {
			t.Errorf("want %q on stderr, got %q", want, stderr)
		}
		if _, err := os.Stat(filepath.Join(dir, session.TimelineFile)); err != nil {
			t.Errorf("merge wrote no timeline into the inferred session: %v", err)
		}
	})

	t.Run("report", func(t *testing.T) {
		dir := miniSession(t)
		chdir(t, dir)
		var code int
		stderr := captureStderr(t, func() { code = Run([]string{"report"}) })
		if code != 0 {
			t.Fatalf("report from inside a session: exit %d, want 0 (stderr %q)", code, stderr)
		}
		if want := "report: using session . (inferred from the current directory)"; !strings.Contains(stderr, want) {
			t.Errorf("want %q on stderr, got %q", want, stderr)
		}
		if _, err := os.Stat(filepath.Join(dir, session.ReportFile)); err != nil {
			t.Errorf("report wrote no report.md into the inferred session: %v", err)
		}
	})
}

// TestExplicitSessionWinsOverInference pins the second acceptance case: an
// explicit -session is used verbatim from any directory and the current
// directory is not consulted at all — even when it is itself a session, which
// is the case that would otherwise be ambiguous. The explicit invocation also
// stays silent: the inference line reports an implicit choice, and there is
// none to report here.
func TestExplicitSessionWinsOverInference(t *testing.T) {
	decoy := miniSession(t)  // the current directory: a session that must stay untouched
	target := miniSession(t) // the session actually named
	chdir(t, decoy)
	var code int
	stderr := captureStderr(t, func() { code = Run([]string{"report", "-session", target}) })
	if code != 0 {
		t.Fatalf("report -session from inside another session: exit %d, want 0 (stderr %q)", code, stderr)
	}
	if strings.Contains(stderr, "inferred") {
		t.Errorf("explicit -session printed an inference line: %q", stderr)
	}
	if _, err := os.Stat(filepath.Join(target, session.ReportFile)); err != nil {
		t.Errorf("report did not render into the named session: %v", err)
	}
	if _, err := os.Stat(filepath.Join(decoy, session.ReportFile)); !os.IsNotExist(err) {
		t.Errorf("report rendered into the current directory instead of the named session (err=%v)", err)
	}
}

// TestNoSessionAndNoManifestIsAUsageError pins the third acceptance case: with
// neither an explicit flag nor a manifest.json in the current directory, the
// command refuses at the usage status (2, as a missing required flag always
// has) with a message naming both things that were checked — "-session is
// required" alone left an operator standing one directory above a session with
// no hint that the current directory had been consulted at all.
func TestNoSessionAndNoManifestIsAUsageError(t *testing.T) {
	chdir(t, t.TempDir())
	for _, cmd := range []string{"merge", "report", "transcribe", "analyze", "review"} {
		var code int
		stderr := captureStderr(t, func() { code = Run([]string{cmd}) })
		if code != 2 {
			t.Errorf("%s with no -session and no manifest: exit %d, want 2 (usage error)", cmd, code)
		}
		want := "testimony: " + cmd + ": -session is required (no -session flag, and the current directory holds no regular manifest.json file)"
		if !strings.Contains(stderr, want) {
			t.Errorf("%s with no -session and no manifest: want %q on stderr, got %q", cmd, want, stderr)
		}
	}
}

// TestEmptySessionIsAUsageErrorNotInference keeps an explicitly-empty -session
// in the wrong-invocation class it shares with analyze's -ingest/-out and
// transcribe's -audio (an unset shell variable spliced into the flag), rather
// than letting it fall through to inference: `merge -session "$SESSION"` with
// SESSION unset must refuse, not silently run against whatever directory the
// caller happened to be standing in — which is exactly the wrong-session
// hazard the inference line exists to surface.
func TestEmptySessionIsAUsageErrorNotInference(t *testing.T) {
	dir := miniSession(t)
	chdir(t, dir)
	for _, cmd := range []string{"merge", "report", "transcribe", "import", "analyze", "review"} {
		var code int
		stderr := captureStderr(t, func() { code = Run([]string{cmd, "-session", ""}) })
		if code != 2 {
			t.Errorf("%s -session \"\": exit %d, want 2 (usage error)", cmd, code)
		}
		if want := "testimony: " + cmd + ": -session must not be empty"; !strings.Contains(stderr, want) {
			t.Errorf("%s -session \"\": want %q on stderr, got %q", cmd, want, stderr)
		}
		if strings.Contains(stderr, "inferred") {
			t.Errorf("%s -session \"\" fell through to inference: %q", cmd, stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, session.ReportFile)); !os.IsNotExist(err) {
		t.Errorf("report -session \"\" rendered into the current directory anyway (err=%v)", err)
	}
}

// captureStdout is captureStderr's sibling for the one stream that is a
// contract: `analyze` in emit mode writes the request to stdout for a pipe to
// carry, so anything the command has to say about its own invocation has to
// stay off it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	read := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		read <- string(b)
	}()
	fn()
	os.Stdout = old
	w.Close()
	got := <-read
	r.Close()
	return got
}

// TestForeignManifestIsNotASession keeps inference off directories that merely
// hold a file of that name. manifest.json is one of the most common file names
// in software — a web app manifest, a browser-extension manifest, a package
// manifest — so keying on the name alone made a bare `merge` in an unrelated
// project's root write timeline.jsonl into it, and a bare `report` overwrite a
// hand-written report.md there, both at exit 0. The marker is a Testimony
// session manifest: a regular file carrying the `session` field session.Create
// always writes.
func TestForeignManifestIsNotASession(t *testing.T) {
	dir := t.TempDir()
	// A browser-extension manifest: valid JSON, every field foreign.
	foreign := `{"manifest_version":3,"name":"Some Extension","version":"1.0.0"}`
	if err := os.WriteFile(filepath.Join(dir, session.ManifestFile), []byte(foreign), 0o644); err != nil {
		t.Fatalf("write foreign manifest: %v", err)
	}
	hand := filepath.Join(dir, session.ReportFile)
	if err := os.WriteFile(hand, []byte("# hand-written\n"), 0o644); err != nil {
		t.Fatalf("seed report.md: %v", err)
	}
	chdir(t, dir)
	for _, cmd := range []string{"merge", "report"} {
		var code int
		stderr := captureStderr(t, func() { code = Run([]string{cmd}) })
		if code != 2 {
			t.Errorf("%s in a foreign-manifest directory: exit %d, want 2 (usage error)", cmd, code)
		}
		want := "testimony: " + cmd + `: -session is required (the current directory holds a manifest.json, but it is not a session manifest: no "session" field)`
		if !strings.Contains(stderr, want) {
			t.Errorf("%s in a foreign-manifest directory: want %q on stderr, got %q", cmd, want, stderr)
		}
	}
	if b, _ := os.ReadFile(hand); string(b) != "# hand-written\n" {
		t.Errorf("report overwrote a hand-written report.md in a foreign-manifest directory: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, session.TimelineFile)); !os.IsNotExist(err) {
		t.Errorf("merge wrote a timeline into a foreign-manifest directory (err=%v)", err)
	}
}

// TestNonRegularManifestIsNotASession pins the marker to a regular file, the
// way every other manifest access goes through the no-follow guard: a
// directory named manifest.json, or a dangling symlink at that name, satisfies
// os.Stat's error-free path but is not a session manifest and cannot be read
// as one.
func TestNonRegularManifestIsNotASession(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, session.ManifestFile), 0o755); err != nil {
			t.Fatalf("mkdir manifest.json: %v", err)
		}
		chdir(t, dir)
		var code int
		stderr := captureStderr(t, func() { code = Run([]string{"merge"}) })
		if code != 2 {
			t.Errorf("merge with a directory named manifest.json: exit %d, want 2", code)
		}
		if want := "holds no regular manifest.json file"; !strings.Contains(stderr, want) {
			t.Errorf("want %q on stderr, got %q", want, stderr)
		}
	})

	t.Run("dangling symlink", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Symlink(filepath.Join(dir, "gone.json"), filepath.Join(dir, session.ManifestFile)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		chdir(t, dir)
		var code int
		stderr := captureStderr(t, func() { code = Run([]string{"merge"}) })
		if code != 2 {
			t.Errorf("merge with a dangling symlink named manifest.json: exit %d, want 2", code)
		}
		if want := "holds no regular manifest.json file"; !strings.Contains(stderr, want) {
			t.Errorf("want %q on stderr, got %q", want, stderr)
		}
	})
}

// TestCorruptSessionManifestStillInfers keeps the foreign-manifest guard from
// swallowing a real session's real problem: a manifest that cannot be parsed
// is not evidence that this is somebody else's directory, so the command
// infers and fails at the runtime status with the parse error itself, rather
// than claiming at exit 2 that there is no manifest here.
func TestCorruptSessionManifestStillInfers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, session.ManifestFile), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt manifest: %v", err)
	}
	chdir(t, dir)
	var code int
	stderr := captureStderr(t, func() { code = Run([]string{"merge"}) })
	if code != 1 {
		t.Errorf("merge on a corrupt session manifest: exit %d, want 1 (runtime error)", code)
	}
	if want := "merge: using session . (inferred from the current directory)"; !strings.Contains(stderr, want) {
		t.Errorf("want the inference line on stderr, got %q", stderr)
	}
	if want := "parse manifest"; !strings.Contains(stderr, want) {
		t.Errorf("want the real parse error on stderr, got %q", stderr)
	}
}

// TestInferenceLineStaysOffStdout pins the stream the inference line uses on
// the one command where stdout is a contract: `analyze` in emit mode writes
// the analysis request to stdout for a pipe to carry into an assistant, so a
// line about how the session was resolved must not be spliced into it.
func TestInferenceLineStaysOffStdout(t *testing.T) {
	dir := miniSession(t)
	chdir(t, dir)
	var code int
	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() { code = Run([]string{"analyze"}) })
	})
	if code != 0 {
		t.Fatalf("analyze from inside a session: exit %d, want 0 (stderr %q)", code, stderr)
	}
	if want := "analyze: using session . (inferred from the current directory)"; !strings.Contains(stderr, want) {
		t.Errorf("want %q on stderr, got %q", want, stderr)
	}
	if strings.Contains(stdout, "inferred") {
		t.Errorf("the inference line reached stdout, which carries the analysis request: %q", stdout)
	}
	if !strings.HasPrefix(stdout, "Testimony analysis rubric: testimony-analysis/") {
		t.Errorf("stdout is not the request it was before inference existed: %.80q", stdout)
	}
}

// TestReviewInfersSessionNonInteractively covers the inferred success path on
// the one command that never loads the manifest for its own work: review
// resolves the session the same way as the rest, and a verdict recorded from
// inside the session lands in that session's findings.jsonl.
func TestReviewInfersSessionNonInteractively(t *testing.T) {
	dir := miniSession(t)
	finding := `{"id":"F-001","t":0,"type":"bug","severity":3,"mode":"A","quote":"hi","evidence":["utt-001"],"status":"unverified"}` + "\n"
	path := filepath.Join(dir, session.FindingsFile)
	if err := os.WriteFile(path, []byte(finding), 0o644); err != nil {
		t.Fatalf("write findings: %v", err)
	}
	chdir(t, dir)
	var code int
	stderr := captureStderr(t, func() {
		code = Run([]string{"review", "-finding", "F-001", "-verdict", "confirmed"})
	})
	if code != 0 {
		t.Fatalf("review from inside a session: exit %d, want 0 (stderr %q)", code, stderr)
	}
	if want := "review: using session . (inferred from the current directory)"; !strings.Contains(stderr, want) {
		t.Errorf("want %q on stderr, got %q", want, stderr)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read findings: %v", err)
	}
	if !strings.Contains(string(b), `"kind":"verdict"`) {
		t.Errorf("review appended no verdict to the inferred session's findings.jsonl: %q", b)
	}
}

// TestRefusedInvocationAnnouncesNoSession keeps the inference line to runs that
// actually use the inferred session: resolution is the last invocation check,
// so a command refused for some other flag says only what is wrong with the
// flag — it never first announces a session it went on to not use.
func TestRefusedInvocationAnnouncesNoSession(t *testing.T) {
	dir := miniSession(t)
	chdir(t, dir)
	cases := [][]string{
		{"report", "-window", "NaN"},
		{"transcribe", "-engine", "bogus"},
		{"transcribe", "-offset", "NaN"},
		{"import", "-offset", "NaN"},
		{"analyze", "-out", "req.md", "-ingest", "-"},
		{"review", "-finding", "F-001"},
	}
	for _, args := range cases {
		var code int
		stderr := captureStderr(t, func() { code = Run(args) })
		if code != 2 {
			t.Errorf("%v: exit %d, want 2 (usage error)", args, code)
		}
		if strings.Contains(stderr, "inferred") {
			t.Errorf("%v announced an inferred session before refusing: %q", args, stderr)
		}
	}
}

// --- draft-tests ------------------------------------------------------------

// draftableSession writes a session the drafting layer can work on: a manifest,
// a two-entry timeline, and a findings.jsonl whose F-001 carries a confirmed
// verdict — the one state `draft-tests` is allowed to draft from.
func draftableSession(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "s", App: "app", Participant: "P1"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	tl := `{"t":0,"src":"speech","id":"utt-001","payload":{"speaker":"P1","t1":5,"text":"I clicked save and nothing happened"}}` + "\n" +
		`{"t":1,"src":"event","id":"ev-001","payload":{"kind":"click","selector":"[data-testid=save-btn]","route":"#general"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(tl), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}
	fnd := `{"id":"F-001","t":0,"type":"bug","severity":3,"mode":"A","quote":"I clicked save and nothing happened","evidence":["utt-001","ev-001"],"status":"unverified"}` + "\n" +
		`{"kind":"verdict","finding":"F-001","verdict":"confirmed","at":"2026-09-12"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, session.FindingsFile), []byte(fnd), 0o644); err != nil {
		t.Fatalf("write findings: %v", err)
	}
	return dir
}

// goodCLIAnswer is a schema-clean answer for draftableSession's F-001.
const goodCLIAnswer = `{"rubric":"testimony-testdraft/v1","tests":[` +
	`{"id":"T-001","finding":"F-001","session":"s","title":"Saving gives no confirmation",` +
	`"steps":["Open #general.","Click Save."],"expected":"The save is confirmed.",` +
	`"observed":"Nothing visibly changes.","rationale_quote":"I clicked save and nothing happened",` +
	`"severity":3,"status":"accepted"}]}`

// TestDraftTestsLoudStagingExitsOne pins both loud-staging refusals at exit 1 —
// a well-formed invocation whose work cannot be done — with the counts by status
// on stderr, and nothing written. A caller cannot tell these from a mistyped flag
// if they share exit 2, and cannot tell them from success if they exit 0.
func TestDraftTestsLoudStagingExitsOne(t *testing.T) {
	// No confirmed finding: emit and ingest both refuse, naming the counts.
	dir := draftableSession(t)
	fnd := `{"id":"F-001","t":0,"type":"bug","severity":3,"quote":"I clicked save and nothing happened","evidence":["utt-001"],"status":"unverified"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, session.FindingsFile), []byte(fnd), 0o644); err != nil {
		t.Fatalf("write findings: %v", err)
	}
	// The ingest case passes "-" deliberately: the refusal comes before a byte of
	// the answer is read, so it fires with nothing on stdin at all.
	for _, args := range [][]string{
		{"draft-tests", "-session", dir},
		{"draft-tests", "-session", dir, "-ingest", "-"},
	} {
		var code int
		stderr := captureStderr(t, func() { code = Run(args) })
		if code != 1 {
			t.Errorf("%v: exit %d, want 1 (runtime refusal)", args, code)
		}
		want := "testimony: no confirmed findings to draft tests from (1 findings: 0 confirmed, 1 unverified, 0 duplicate, 0 rejected); confirm one with `testimony review -session " + dir + "` first"
		if !strings.Contains(stderr, want) {
			t.Errorf("%v: want %q on stderr, got %q", args, want, stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, session.TestsFile)); !os.IsNotExist(err) {
		t.Errorf("a refused draft-tests wrote %s (err=%v)", session.TestsFile, err)
	}

	// No accepted draft: render refuses, and -out writes nothing, so an existing
	// test plan cannot be truncated into an empty document.
	dir2 := draftableSession(t)
	seed := filepath.Join(t.TempDir(), "answer.json")
	if err := os.WriteFile(seed, []byte(goodCLIAnswer), 0o644); err != nil {
		t.Fatalf("write answer: %v", err)
	}
	if code := Run([]string{"draft-tests", "-session", dir2, "-ingest", seed}); code != 0 {
		t.Fatalf("seed ingest: exit %d", code)
	}
	out := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(out, []byte("PRIOR PLAN\n"), 0o644); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	var code int
	stderr := captureStderr(t, func() { code = Run([]string{"draft-tests", "-session", dir2, "-render", "-out", out}) })
	if code != 1 {
		t.Errorf("render with no accepted draft: exit %d, want 1", code)
	}
	want := "testimony: no accepted test drafts to render (1 drafts: 0 accepted, 0 edited, 1 proposed, 0 rejected); accept one with `testimony review -session " + dir2 + " -kind tests` first"
	if !strings.Contains(stderr, want) {
		t.Errorf("render refusal: want %q on stderr, got %q", want, stderr)
	}
	if b, err := os.ReadFile(out); err != nil || string(b) != "PRIOR PLAN\n" {
		t.Errorf("a refused render truncated the prior plan: %q (err=%v)", b, err)
	}
}

// TestDraftTestsHintsMissingArtefacts: each mode names the command that produces
// what it is missing, rather than surfacing a bare filesystem error.
func TestDraftTestsHintsMissingArtefacts(t *testing.T) {
	cases := []struct {
		name string
		prep func(t *testing.T, dir string)
		args func(dir string) []string
		want string
	}{
		{
			"no tests.jsonl to render",
			func(t *testing.T, dir string) {},
			func(dir string) []string { return []string{"draft-tests", "-session", dir, "-render"} },
			"no tests.jsonl (run `testimony draft-tests -ingest` first)",
		},
		{
			"no tests.jsonl to review",
			func(t *testing.T, dir string) {},
			func(dir string) []string {
				return []string{"review", "-session", dir, "-kind", "tests", "-test", "T-001", "-decision", "accepted"}
			},
			"no tests.jsonl (run `testimony draft-tests -ingest` first)",
		},
		{
			"no findings.jsonl",
			func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, session.FindingsFile)); err != nil {
					t.Fatalf("remove findings: %v", err)
				}
			},
			func(dir string) []string { return []string{"draft-tests", "-session", dir} },
			"no findings.jsonl (run `testimony analyze -ingest` first)",
		},
		{
			"no timeline.jsonl",
			func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, session.TimelineFile)); err != nil {
					t.Fatalf("remove timeline: %v", err)
				}
			},
			func(dir string) []string { return []string{"draft-tests", "-session", dir} },
			"run `testimony merge` first",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := draftableSession(t)
			c.prep(t, dir)
			var code int
			stderr := captureStderr(t, func() { code = Run(c.args(dir)) })
			if code != 1 {
				t.Errorf("exit %d, want 1", code)
			}
			if !strings.Contains(stderr, c.want) {
				t.Errorf("want %q on stderr, got %q", c.want, stderr)
			}
		})
	}
}

// TestDraftTestsRoundTripThroughTheCLI drives the whole drafting layer the way an
// operator does: emit the request, ingest a known-good answer, record all three
// decisions, and render the plan — asserting the printed lines, that the draft
// line survives every decision byte-for-byte, and that findings.jsonl is never
// written to.
func TestDraftTestsRoundTripThroughTheCLI(t *testing.T) {
	dir := draftableSession(t)
	findingsBefore, err := os.ReadFile(filepath.Join(dir, session.FindingsFile))
	if err != nil {
		t.Fatalf("read findings: %v", err)
	}

	// Emit: to stdout, and the inference notice never contaminates it.
	req := captureStdout(t, func() {
		if code := Run([]string{"draft-tests", "-session", dir}); code != 0 {
			t.Errorf("emit: exit %d, want 0", code)
		}
	})
	for _, want := range []string{
		"Testimony regression-test drafting rubric: testimony-testdraft/v1",
		"Finding F-001 — bug, severity 3, at [00:00], confirmed by human verdict on 2026-09-12:",
		"Event window:",
	} {
		if !strings.Contains(req, want) {
			t.Fatalf("emitted request is missing %q:\n%s", want, req)
		}
	}

	// Emit to a file.
	reqPath := filepath.Join(t.TempDir(), "request.md")
	stdout := captureStdout(t, func() {
		if code := Run([]string{"draft-tests", "-session", dir, "-out", reqPath}); code != 0 {
			t.Errorf("emit -out: exit %d, want 0", code)
		}
	})
	if want := "wrote " + reqPath; !strings.Contains(stdout, want) {
		t.Errorf("emit -out: want %q on stdout, got %q", want, stdout)
	}
	if b, err := os.ReadFile(reqPath); err != nil || !strings.Contains(string(b), "testimony-testdraft/v1") {
		t.Errorf("emit -out wrote no request (err=%v)", err)
	}

	// Ingest a known-good answer from a file.
	answerPath := filepath.Join(t.TempDir(), "answer.json")
	if err := os.WriteFile(answerPath, []byte(goodCLIAnswer), 0o644); err != nil {
		t.Fatalf("write answer: %v", err)
	}
	stdout = captureStdout(t, func() {
		if code := Run([]string{"draft-tests", "-session", dir, "-ingest", answerPath}); code != 0 {
			t.Errorf("ingest: exit %d, want 0", code)
		}
	})
	if want := "validated 1 test drafts → " + filepath.Join(dir, session.TestsFile) + " (all proposed)"; !strings.Contains(stdout, want) {
		t.Errorf("ingest: want %q on stdout, got %q", want, stdout)
	}
	draftLine := testsDraftLine(t, dir)
	// The answer claimed "accepted"; ingest launders it.
	if !strings.Contains(draftLine, `"status":"proposed"`) {
		t.Errorf("ingest did not force the draft to proposed: %q", draftLine)
	}

	// Three decisions, each appended.
	editPath := filepath.Join(t.TempDir(), "edit.json")
	if err := os.WriteFile(editPath, []byte(`{"title":"Saving a display name gives no confirmation"}`), 0o644); err != nil {
		t.Fatalf("write edit: %v", err)
	}
	today := time.Now().Format("2006-01-02")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"review", "-session", dir, "-kind", "tests", "-test", "T-001", "-decision", "accepted"}, "recorded: T-001 accepted (" + today + ")"},
		{[]string{"review", "-session", dir, "-kind", "tests", "-test", "T-001", "-decision", "edited", "-edit", editPath}, "recorded: T-001 edited (" + today + ")"},
	} {
		stdout = captureStdout(t, func() {
			if code := Run(c.args); code != 0 {
				t.Errorf("%v: exit %d, want 0", c.args, code)
			}
		})
		if !strings.Contains(stdout, c.want) {
			t.Errorf("%v: want %q on stdout, got %q", c.args, c.want, stdout)
		}
	}

	// Render: the latest edit is applied, and the draft line is untouched.
	plan := captureStdout(t, func() {
		if code := Run([]string{"draft-tests", "-session", dir, "-render"}); code != 0 {
			t.Errorf("render: exit %d, want 0", code)
		}
	})
	for _, want := range []string{
		"# Regression tests — s",
		"## T-001 — Saving a display name gives no confirmation",
		"- **Source:** finding `F-001` (bug, severity 3) in session `s`, at [00:00]",
		"- **Decision:** edited (" + today + ")",
		"1. Open #general.",
		"**Expected:** The save is confirmed.",
		"“I clicked save and nothing happened”",
		"1 of 1 drafts accepted.",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("rendered plan is missing %q:\n%s", want, plan)
		}
	}
	if got := testsDraftLine(t, dir); got != draftLine {
		t.Errorf("the draft line changed across the decisions:\n got %q\nwant %q", got, draftLine)
	}

	// A rejection removes it from the plan again.
	stdout = captureStdout(t, func() {
		if code := Run([]string{"review", "-session", dir, "-kind", "tests", "-test", "T-001", "-decision", "rejected"}); code != 0 {
			t.Errorf("reject: exit %d, want 0", code)
		}
	})
	if want := "recorded: T-001 rejected (" + today + ")"; !strings.Contains(stdout, want) {
		t.Errorf("reject: want %q on stdout, got %q", want, stdout)
	}
	if code := Run([]string{"draft-tests", "-session", dir, "-render"}); code != 1 {
		t.Errorf("render after the rejection: exit %d, want 1", code)
	}

	// findings.jsonl is a different record family and is never written to.
	after, err := os.ReadFile(filepath.Join(dir, session.FindingsFile))
	if err != nil {
		t.Fatalf("read findings: %v", err)
	}
	if string(after) != string(findingsBefore) {
		t.Error("the drafting pipeline modified findings.jsonl")
	}
}

// testsDraftLine returns the single draft (non-decision) line of tests.jsonl, so
// the append-only property can be asserted byte-for-byte across decisions.
func testsDraftLine(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, session.TestsFile))
	if err != nil {
		t.Fatalf("read tests: %v", err)
	}
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if !strings.Contains(l, `"kind":"decision"`) {
			return l
		}
	}
	t.Fatalf("tests.jsonl holds no draft line: %q", b)
	return ""
}

// TestDraftTestsInfersSession: draft-tests joins the commands that take their
// session from the current directory, announcing the inference on stderr and
// keeping it off stdout, where the emitted request has to stay a clean pipe.
func TestDraftTestsInfersSession(t *testing.T) {
	dir := draftableSession(t)
	chdir(t, dir)
	var code int
	var req string
	stderr := captureStderr(t, func() {
		req = captureStdout(t, func() { code = Run([]string{"draft-tests"}) })
	})
	if code != 0 {
		t.Fatalf("draft-tests with an inferred session: exit %d, want 0", code)
	}
	if want := "draft-tests: using session . (inferred from the current directory)"; !strings.Contains(stderr, want) {
		t.Errorf("want %q on stderr, got %q", want, stderr)
	}
	if !strings.Contains(req, "testimony-testdraft/v1") {
		t.Errorf("the inferred run emitted no request: %q", req)
	}
	if strings.Contains(req, "inferred") {
		t.Errorf("the inference notice reached stdout: %q", req)
	}
}

// castTestT0 anchors the import tests' session. It matches the t0 the
// session-directory reference's own examples carry, and the fixture cast below
// declares a header timestamp two seconds earlier, so the imported records land
// on a session-relative clock that starts negative and crosses zero.
const (
	castTestT0     = 1784300400000
	castHeaderUnix = 1784300398
)

// castSession writes a session with a usable t0 (which import requires on every
// path, since the records it writes are epoch-millisecond-timed) and a small
// asciicast v2 file beside it, and returns both paths. The cast carries a
// keystroke-echoed command, an input event import must drop, and a resize event
// it must count rather than normalise.
func castSession(t *testing.T) (dir, castPath string) {
	t.Helper()
	dir = t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{
		Session: "s", App: "a shell", Participant: "P1", T0EpochMS: castTestT0,
	}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	lines := []string{
		fmt.Sprintf(`{"version":2,"width":80,"height":24,"timestamp":%d}`, castHeaderUnix),
		`[0,"o","alice@example.test:~/project$ "]`,
		`[0.52,"i","l"]`,
		`[0.53,"o","l"]`,
		`[0.62,"o","s"]`,
		`[0.75,"o","\r\n"]`,
		`[0.8,"r","120x40"]`,
		`[0.81,"o","docs  internal  README.md\r\n"]`,
	}
	castPath = filepath.Join(t.TempDir(), "session.cast")
	if err := os.WriteFile(castPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write cast: %v", err)
	}
	return dir, castPath
}

// TestImportWritesTerminalRecords is the well-formed run: exit 0, the summary
// line on stdout, the records in interactions.jsonl, and the cast archived in
// the session so a later bare re-import has something to read.
func TestImportWritesTerminalRecords(t *testing.T) {
	dir, castPath := castSession(t)
	var code int
	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			code = Run([]string{"import", "-session", dir, "-cast", castPath})
		})
	})
	if code != 0 {
		t.Fatalf("import: exit %d, want 0 (stderr %q)", code, stderr)
	}
	want := "imported 3 records → " + filepath.Join(dir, session.InteractionsFile)
	if !strings.Contains(stdout, want) {
		t.Errorf("want %q on stdout, got %q", want, stdout)
	}
	b, err := os.ReadFile(filepath.Join(dir, session.InteractionsFile))
	if err != nil {
		t.Fatalf("read interactions: %v", err)
	}
	for _, want := range []string{`"kind":"terminal_output"`, `"text":"ls\r\n"`, `"t":1784300398000`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("interactions.jsonl does not contain %q: %s", want, b)
		}
	}
	// A keystroke must never reach the derived text, whatever the recorder did.
	if strings.Contains(string(b), `"text":"l"`) {
		t.Errorf("an input event reached interactions.jsonl: %s", b)
	}
	archived, err := os.ReadFile(filepath.Join(dir, session.TerminalCastFile))
	if err != nil {
		t.Fatalf("read archived cast: %v", err)
	}
	original, err := os.ReadFile(castPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(archived) != string(original) {
		t.Errorf("terminal.cast is not byte-identical to the imported file")
	}
}

// TestImportDiagnosticsStayOffStdout pins the stream split: the offset
// provenance line and the dropped-event counts are diagnostics that belong
// beside the session-inference line on stderr, so stdout carries only the one
// summary line a script reads.
func TestImportDiagnosticsStayOffStdout(t *testing.T) {
	dir, castPath := castSession(t)
	var code int
	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			code = Run([]string{"import", "-session", dir, "-cast", castPath})
		})
	})
	if code != 0 {
		t.Fatalf("import: exit %d, want 0 (stderr %q)", code, stderr)
	}
	for _, want := range []string{
		"offset: -2.00s (derived: cast header timestamp − manifest t0 (whole seconds, ±1s))",
		"dropped 1 input (i) event(s): keystrokes are never imported",
		"dropped 1 other event(s): 1 resize (r)",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("want %q on stderr, got %q", want, stderr)
		}
	}
	if strings.Contains(stdout, "offset:") || strings.Contains(stdout, "dropped") {
		t.Errorf("import's diagnostics reached stdout: %q", stdout)
	}
	if lines := strings.Count(strings.TrimSpace(stdout), "\n"); lines != 0 {
		t.Errorf("stdout carries %d extra line(s) beyond the summary: %q", lines, stdout)
	}
}

// TestImportInfersSessionAndReImportsInPlace is the pair the how-to's
// offset-correction recipe rests on: import resolves -session by the same
// shared rule as every other pipeline command, and with -cast omitted it
// re-reads the session's own terminal.cast, so correcting a wrong offset is one
// command with no file to find again.
func TestImportInfersSessionAndReImportsInPlace(t *testing.T) {
	dir, castPath := castSession(t)
	captureStdout(t, func() {
		captureStderr(t, func() {
			if code := Run([]string{"import", "-session", dir, "-cast", castPath}); code != 0 {
				t.Fatalf("first import: exit %d, want 0", code)
			}
		})
	})
	chdir(t, dir)

	var code int
	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() { code = Run([]string{"import", "-offset", "-12.4"}) })
	})
	if code != 0 {
		t.Fatalf("bare import from inside a session: exit %d, want 0 (stderr %q)", code, stderr)
	}
	for _, want := range []string{
		"import: using session . (inferred from the current directory)",
		"offset: -12.40s (from -offset flag)",
		"replaced 3 terminal_output record(s) from an earlier import",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("want %q on stderr, got %q", want, stderr)
		}
	}
	if want := "imported 3 records → " + session.InteractionsFile; !strings.Contains(stdout, want) {
		t.Errorf("want %q on stdout, got %q", want, stdout)
	}
	// The explicit offset replaced the derived one rather than being applied on
	// top of it: the first record sits 12.4 s before t0, not 14.4 s.
	b, err := os.ReadFile(session.InteractionsFile)
	if err != nil {
		t.Fatalf("read interactions: %v", err)
	}
	if want := `"t":1784300387600`; !strings.Contains(string(b), want) {
		t.Errorf("interactions.jsonl does not carry the corrected time %s: %s", want, b)
	}
	if strings.Count(string(b), `"kind":"terminal_output"`) != 3 {
		t.Errorf("the re-import did not replace the first import's records: %s", b)
	}
}

// TestImportRefusesUnreadableCastAtRuntime keeps the exit-status contract:
// a well-formed invocation whose cast cannot be read is a runtime failure (1),
// not a usage error, so a script can tell a mistyped flag from a missing file.
func TestImportRefusesUnreadableCastAtRuntime(t *testing.T) {
	dir, _ := castSession(t)
	var code int
	stderr := captureStderr(t, func() {
		code = Run([]string{"import", "-session", dir, "-cast", filepath.Join(t.TempDir(), "absent.cast")})
	})
	if code != 1 {
		t.Errorf("import with an absent -cast: exit %d, want 1 (runtime error)", code)
	}
	if want := "testimony: cast file:"; !strings.Contains(stderr, want) {
		t.Errorf("want %q on stderr, got %q", want, stderr)
	}
}

// TestUsageListsImport pins the surface change the intent's last criterion
// asks for: the new verb is visible in the usage text, and record's own flag
// set is untouched by it.
func TestUsageListsImport(t *testing.T) {
	for _, want := range []string{
		"testimony import      [-session DIR] [-cast FILE]",
		"[-offset SECONDS]",
		"import an asciinema recording's output into interactions.jsonl",
		"Omitting -session on transcribe, import, merge, report, analyze, draft-tests, or",
	} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage text does not mention %q", want)
		}
	}
	if strings.Contains(usage, "-terminal") {
		t.Error("usage text offers a -terminal flag; terminal capture is a hand-off, not a record mode")
	}
}
