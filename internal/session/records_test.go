package session

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shortWriteFile is an appendFile whose Write persists a prefix and then errors,
// standing in for a full disk (write(2) fills the remaining space, returns a
// short count, and the next write returns ENOSPC — os.File.Write persists the
// truncated prefix before returning the error). Seek always reports the current
// length, so it also stands in for the O_APPEND descriptor writeRecord holds.
type shortWriteFile struct {
	buf  []byte
	fail bool // when true, Write keeps only a prefix then returns an error
}

func (f *shortWriteFile) Seek(offset int64, whence int) (int64, error) {
	return int64(len(f.buf)), nil
}

func (f *shortWriteFile) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(f.buf)) {
		return 0, io.EOF
	}
	return copy(p, f.buf[off:]), nil
}

func (f *shortWriteFile) Truncate(size int64) error {
	f.buf = f.buf[:size]
	return nil
}

func (f *shortWriteFile) Write(p []byte) (int, error) {
	if f.fail {
		half := len(p) / 2
		f.buf = append(f.buf, p[:half]...)
		return half, errors.New("no space left on device")
	}
	f.buf = append(f.buf, p...)
	return len(p), nil
}

// failAfterWriter is a commitFile whose Write fails, recording whether the caller
// truncated back to 0 *after* the failed write — the rollback writeRecords must
// perform so a partial write never bricks a session JSONL file against its own
// recovery. The post-write ordering matters: writeRecords also truncates to 0
// before writing, so only a truncate that follows the write attempt proves the
// rollback ran.
type failAfterWriter struct {
	wrote      bool
	rolledBack bool
}

func (w *failAfterWriter) Write(p []byte) (int, error) {
	w.wrote = true
	return 0, errors.New("no space left on device")
}

func (w *failAfterWriter) Truncate(size int64) error {
	if w.wrote && size == 0 {
		w.rolledBack = true
	}
	return nil
}

func (w *failAfterWriter) Seek(offset int64, whence int) (int64, error) { return 0, nil }

// TestWriteRecordRollsBackPartialWrite is the ENOSPC regression on the append
// path (moved here from internal/review with the primitive it exercises): a short
// write that persists a newline-less prefix must be truncated away, so the file
// never retains a partial line that would fuse with the next record into one
// malformed physical record and make the whole file — the human-decision record
// the append path exists to protect — unparseable to every reader.
func TestWriteRecordRollsBackPartialWrite(t *testing.T) {
	f := &shortWriteFile{}
	first := []byte(`{"kind":"verdict","finding":"F-001","verdict":"confirmed","at":"2026-07-17"}`)
	if err := writeRecord(f, append(first, '\n')); err != nil {
		t.Fatalf("first record: %v", err)
	}
	good := string(f.buf)

	f.fail = true
	second := []byte(`{"kind":"verdict","finding":"F-002","verdict":"rejected","at":"2026-07-17"}`)
	if err := writeRecord(f, append(second, '\n')); err == nil {
		t.Fatalf("expected a write error on a full disk")
	}
	if string(f.buf) != good {
		t.Fatalf("partial line survived: file is %q, want the clean prefix %q", f.buf, good)
	}
	if !strings.HasSuffix(string(f.buf), "\n") {
		t.Fatalf("file does not end on a newline: %q", f.buf)
	}

	// The rolled-back file still parses one record per line, so a later record
	// lands cleanly rather than fusing onto a fragment.
	f.fail = false
	if err := writeRecord(f, append(second, '\n')); err != nil {
		t.Fatalf("record after rollback: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(f.buf), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), f.buf)
	}
}

// TestWriteRecordsRollsBackOnWriteError is the corrupt-on-failure regression for
// the commit path (moved here from internal/analyze with the primitive it
// exercises). writeRecords runs f.Truncate(0) before writing, so a short write
// (ENOSPC) would otherwise leave a truncated JSON fragment that not only breaks
// every reader but blocks the recovery path — the next ingest's guard errors on
// the fragment before it can conclude the file is free to rewrite. It must roll
// the file back to empty (parseable, re-ingestable) on any write error.
func TestWriteRecordsRollsBackOnWriteError(t *testing.T) {
	w := &failAfterWriter{}
	err := writeRecords(w, [][]byte{[]byte(`{"id":"F-001"}`)})
	if err == nil {
		t.Fatal("writeRecords returned nil on a failing write; want the write error")
	}
	if !w.rolledBack {
		t.Fatal("writeRecords did not truncate back to empty after the failed write; a partial line would survive and brick re-ingest")
	}
}

func appendFixture(t *testing.T, contents string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, FindingsFile)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	return dir, path
}

// TestAppendRecordFramesUnterminatedFile is the fused-line regression: a session
// JSONL file that does not end in a newline (hand edited, produced by another
// tool, or left short by a crash) must not have the new record appended onto its
// final line, which would make one physical line hold two JSON objects and the
// whole file unparseable.
func TestAppendRecordFramesUnterminatedFile(t *testing.T) {
	_, path := appendFixture(t, `{"id":"F-001"}`) // no trailing newline
	rec := []byte(`{"kind":"verdict","finding":"F-001"}`)
	if err := AppendRecord(Append{Path: path, Record: rec, Label: "verdict for F-001", Kind: "verdict"}); err != nil {
		t.Fatalf("AppendRecord: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := `{"id":"F-001"}` + "\n" + string(rec) + "\n"
	if string(got) != want {
		t.Fatalf("framing wrong:\n got %q\nwant %q", got, want)
	}
}

// TestAppendRecordRefusesOversizedLine holds the appended record to
// MaxJSONLLine, the read-side invariant every reader scans to. The refusal is
// pre-write and, since an unwritable record is a fact about the record alone,
// pre-open: the file need not exist for the caller to hear which limit was
// crossed.
func TestAppendRecordRefusesOversizedLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FindingsFile)
	rec := make([]byte, MaxJSONLLine)
	err := AppendRecord(Append{Path: path, Record: rec, Label: "verdict for F-001", Kind: "verdict"})
	if err == nil || !strings.Contains(err.Error(), "line limit") {
		t.Fatalf("expected an over-limit refusal, got %v", err)
	}
	if !strings.Contains(err.Error(), "verdict for F-001") {
		t.Fatalf("refusal does not carry the caller's label: %v", err)
	}
	if !strings.Contains(err.Error(), FindingsFile) {
		t.Fatalf("refusal does not name the file: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("the refusal created %s; want no write at all", path)
	}
}

// TestAppendRecordRefusesOversizedTotal is the write-side twin of ReadJSONL's
// total-size cap: a file written by an ingest step can legally sit right at
// MaxJSONLBytes, and appending even one small record would push it past the cap,
// durably bricking every record already in it.
func TestAppendRecordRefusesOversizedTotal(t *testing.T) {
	pad := strings.Repeat("x", MaxJSONLBytes-20) + "\n"
	_, path := appendFixture(t, pad)
	rec := []byte(`{"kind":"verdict","finding":"F-001"}`)
	err := AppendRecord(Append{Path: path, Record: rec, Label: "verdict for F-001", Kind: "verdict"})
	if err == nil || !strings.Contains(err.Error(), "JSONL file limit") {
		t.Fatalf("expected an over-total refusal naming the JSONL file limit, got %v", err)
	}
	if !strings.Contains(err.Error(), "appending this verdict") {
		t.Fatalf("refusal does not carry the caller's record kind: %v", err)
	}
	got, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("read: %v", rerr)
	}
	if string(got) != pad {
		t.Fatalf("the file was modified despite the refusal")
	}
}

// TestAppendRecordVerifyRefusalWritesNothing covers the Verify seam: the closure
// runs under the append lock, over the file's current contents, and an error from
// it refuses the append outright. That is how a caller confirms the record still
// targets what the operator decided on after a concurrent rewrite.
func TestAppendRecordVerifyRefusalWritesNothing(t *testing.T) {
	const seed = `{"id":"F-001"}` + "\n"
	_, path := appendFixture(t, seed)

	var saw []byte
	refusal := errors.New("the target changed since review started")
	err := AppendRecord(Append{
		Path:   path,
		Record: []byte(`{"kind":"verdict","finding":"F-001"}`),
		Label:  "verdict for F-001",
		Kind:   "verdict",
		Verify: func(current io.Reader) error {
			b, rerr := io.ReadAll(current)
			if rerr != nil {
				return rerr
			}
			saw = b
			return refusal
		},
	})
	if !errors.Is(err, refusal) {
		t.Fatalf("AppendRecord: got %v, want the Verify refusal", err)
	}
	if string(saw) != seed {
		t.Fatalf("Verify saw %q, want the file's current contents %q", saw, seed)
	}
	got, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("read: %v", rerr)
	}
	if string(got) != seed {
		t.Fatalf("a refused append still wrote: %q", got)
	}
}

// TestCommitRecordsReplacesContents is the happy path: the file is truncated and
// rewritten as one record per line, whatever it held before.
func TestCommitRecordsReplacesContents(t *testing.T) {
	_, path := appendFixture(t, "stale\nlines\nthat must go\n")
	err := CommitRecords(Commit{
		Path:    path,
		Records: [][]byte{[]byte(`{"id":"F-001"}`), []byte(`{"id":"F-002"}`)},
	})
	if err != nil {
		t.Fatalf("CommitRecords: %v", err)
	}
	got, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("read: %v", rerr)
	}
	want := `{"id":"F-001"}` + "\n" + `{"id":"F-002"}` + "\n"
	if string(got) != want {
		t.Fatalf("commit wrote %q, want %q", got, want)
	}
}

// TestCommitRecordsGuardRefusalLeavesFileIntact is the protected-record
// regression: the guard reads the file's current contents before anything is
// truncated, and returning an error from it must leave every prior byte in place.
func TestCommitRecordsGuardRefusalLeavesFileIntact(t *testing.T) {
	const seed = `{"id":"F-001"}` + "\n" + `{"kind":"verdict","finding":"F-001"}` + "\n"
	_, path := appendFixture(t, seed)

	err := CommitRecords(Commit{
		Path:    path,
		Records: [][]byte{[]byte(`{"id":"F-009"}`)},
		Guard: func(current io.Reader) error {
			b, rerr := io.ReadAll(current)
			if rerr != nil {
				return rerr
			}
			if bytes.Contains(b, []byte(`"kind":"verdict"`)) {
				return fmt.Errorf("refusing to overwrite %s: it already holds verdict records", FindingsFile)
			}
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("expected the guard refusal, got %v", err)
	}
	got, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("read: %v", rerr)
	}
	if string(got) != seed {
		t.Fatalf("a refused commit still rewrote the file: %q", got)
	}
}

// TestCommitRecordsRefusesSymlink is the arbitrary-file-truncation regression: a
// session artefact planted as a symlink must not be followed out of the session
// directory.
func TestCommitRecordsRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("original\n"), 0o600); err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	path := filepath.Join(dir, FindingsFile)
	if err := os.Symlink(outside, path); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := CommitRecords(Commit{Path: path, Records: [][]byte{[]byte(`{"id":"F-001"}`)}}); err == nil {
		t.Fatal("CommitRecords followed a symlink; want refusal")
	}
	if b, _ := os.ReadFile(outside); string(b) != "original\n" {
		t.Fatalf("victim file rewritten through symlink: %q", b)
	}
}
