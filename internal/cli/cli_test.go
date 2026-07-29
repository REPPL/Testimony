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
		if code == 0 {
			t.Fatalf("-window %s returned success; want a refusal", w)
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
