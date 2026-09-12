package cast

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/REPPL/Testimony/internal/session"
	"github.com/REPPL/Testimony/internal/timeline"
)

func TestEncodeRecordMatchesWriteJSONL(t *testing.T) {
	rec := timeline.Interaction{T: testT0, Kind: OutputKind, Text: "a <b> & c\r\n"}
	line, err := encodeRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	// HTML escaping is off, matching session.WriteJSONL, so a literal <, >, or &
	// stays one byte rather than inflating to a six-byte escape.
	if !strings.Contains(string(line), "a <b> & c") {
		t.Errorf("encodeRecord escaped HTML: %s", line)
	}
	if strings.HasSuffix(string(line), "\n") {
		t.Errorf("encodeRecord kept the terminating newline: %q", line)
	}
	n, err := session.EncodedLen(rec)
	if err != nil {
		t.Fatal(err)
	}
	if len(line)+1 != n {
		t.Errorf("encodeRecord wrote %d bytes, session.EncodedLen measures %d", len(line)+1, n)
	}
}

// TestRewriteTerminatesAnUnterminatedFinalLine is the one byte this rewrite
// changes: without the newline the first imported record would be appended onto
// a malformed final line and neither would survive a read back.
func TestRewriteTerminatesAnUnterminatedFinalLine(t *testing.T) {
	dir := newSession(t, testT0)
	path := filepath.Join(dir, session.InteractionsFile)
	foreign := `{"t":1784300419200,"kind":"click"}`
	if err := os.WriteFile(path, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := timeline.Interaction{T: testT0, Kind: OutputKind, Text: "out\r\n"}
	if _, err := rewriteInteractions(dir, "session.cast", testT0, []timeline.Interaction{rec}); err != nil {
		t.Fatalf("rewriteInteractions: %v", err)
	}
	lines := readLines(t, path)
	if len(lines) != 2 || lines[0] != foreign {
		t.Fatalf("lines = %q, want the foreign line kept and the record appended", lines)
	}
}

func TestRewriteRefusesOverlongExistingLine(t *testing.T) {
	dir := newSession(t, testT0)
	path := filepath.Join(dir, session.InteractionsFile)
	long := `{"t":1784300419200,"kind":"click","text":"` + strings.Repeat("x", session.MaxJSONLLine) + `"}`
	if err := os.WriteFile(path, []byte(long+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := timeline.Interaction{T: testT0, Kind: OutputKind, Text: "out\r\n"}
	_, err := rewriteInteractions(dir, "session.cast", testT0, []timeline.Interaction{rec})
	if err == nil || !strings.Contains(err.Error(), "line exceeds") {
		t.Fatalf("error = %v, want an over-long-line refusal", err)
	}
	// The refusal is before the write, so the file is as it was.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != long+"\n" {
		t.Error("the refused rewrite changed interactions.jsonl")
	}
}

func TestCommitCastRenamesIntoPlace(t *testing.T) {
	dir := newSession(t, testT0)
	src := filepath.Join(t.TempDir(), "session.cast")
	if err := os.WriteFile(src, []byte("{\"version\":2}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tmpPath, err := stageCast(dir, src)
	if err != nil {
		t.Fatalf("stageCast: %v", err)
	}
	if filepath.Dir(tmpPath) != dir {
		t.Errorf("staged outside the session directory: %s", tmpPath)
	}
	if _, err := os.Stat(filepath.Join(dir, session.TerminalCastFile)); !os.IsNotExist(err) {
		t.Error("stageCast committed the archive itself")
	}
	if err := commitCast(tmpPath); err != nil {
		t.Fatalf("commitCast: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, session.TerminalCastFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "{\"version\":2}\n" {
		t.Errorf("terminal.cast = %q", b)
	}
}
