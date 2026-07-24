package cli

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/REPPL/Testimony/internal/session"
)

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
