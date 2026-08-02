package cli

import (
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
		{"analyze", "-session", dir, "junk", "-out", "x", "-ingest", "-"},
		{"review", "-session", dir, "junk", "-finding", "F-001", "-verdict", "confirmed"},
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
		{[]string{"review", "-session", dir, "-finding", "F-001", "-verdict", "duplicate-of-F-001"}, `review: -finding cannot be a duplicate of itself`},
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
	for _, want := range []string{"-commit HASH", "testimony help"} {
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
	for _, cmd := range []string{"merge", "report", "transcribe", "analyze", "review"} {
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
