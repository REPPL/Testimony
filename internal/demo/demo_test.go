package demo

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/REPPL/Testimony/internal/session"
)

// newTestServer builds a server writing into a fresh temp session directory,
// mirroring Serve's stream files.
func newTestServer(t *testing.T) (*server, string) {
	t.Helper()
	dir := t.TempDir()
	open := func(name string) *os.File {
		f, err := session.OpenFileNoFollow(filepath.Join(dir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		return f
	}
	inter := open(session.InteractionsFile)
	raw := open(session.RawEventsFile)
	t.Cleanup(func() { inter.Close(); raw.Close() })
	return &server{interactions: inter, rawEvents: raw}, dir
}

// jsonPost builds a POST that passes the loopback/JSON guard by default; hdr
// overrides individual headers (and, for "Host", the request host).
func jsonPost(path, body string, hdr map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Host = "localhost:8737"
	r.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		if k == "Host" {
			r.Host = v
			continue
		}
		r.Header.Set(k, v)
	}
	return r
}

func fileLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	s := strings.TrimRight(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// TestListenAddrDefaultsToLoopback pins the capture surface to loopback: a bare
// ":port" must bind 127.0.0.1, not 0.0.0.0, while an explicit host is honoured.
func TestListenAddrDefaultsToLoopback(t *testing.T) {
	cases := map[string]string{
		":8737":            "127.0.0.1:8737",
		"127.0.0.1:8737":   "127.0.0.1:8737",
		"0.0.0.0:8737":     "0.0.0.0:8737",
		"[::1]:8737":       "[::1]:8737",
		"192.168.1.5:8737": "192.168.1.5:8737",
	}
	for in, want := range cases {
		if got := listenAddr(in); got != want {
			t.Errorf("listenAddr(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestInteractionCompactsEmbeddedNewline is the JSONL-injection regression: a
// body that is valid JSON but carries a raw newline between tokens must be
// stored as exactly one physical line so merge's line-by-line reader still
// parses it. Pre-fix the raw body was written verbatim and split into two lines,
// the first of which fails to parse.
func TestInteractionCompactsEmbeddedNewline(t *testing.T) {
	s, dir := newTestServer(t)
	body := "{\"t\":1,\n\"kind\":\"click\"}"

	w := httptest.NewRecorder()
	s.handleInteraction(w, jsonPost("/api/interactions", body, nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}

	lines := fileLines(t, filepath.Join(dir, session.InteractionsFile))
	if len(lines) != 1 {
		t.Fatalf("interactions.jsonl has %d physical lines, want 1: %q", len(lines), lines)
	}
	if strings.ContainsAny(lines[0], "\n\r") {
		t.Fatalf("stored line still contains a newline: %q", lines[0])
	}
	// The stored line must be readable by the same reader merge uses.
	if _, err := session.ReadJSONL[map[string]any](filepath.Join(dir, session.InteractionsFile)); err != nil {
		t.Fatalf("ReadJSONL on stored interactions failed: %v", err)
	}
}

// TestBatchCompactsEmbeddedNewline is the same regression for the /api/events
// batch path, whose json.RawMessage elements were also written verbatim.
func TestBatchCompactsEmbeddedNewline(t *testing.T) {
	s, dir := newTestServer(t)
	body := "[{\"a\":1},\n{\"b\":\n2}]"

	w := httptest.NewRecorder()
	s.handleRawEvents(w, jsonPost("/api/events", body, nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	lines := fileLines(t, filepath.Join(dir, session.RawEventsFile))
	if len(lines) != 2 {
		t.Fatalf("events.rrweb.jsonl has %d physical lines, want 2: %q", len(lines), lines)
	}
	for _, l := range lines {
		if strings.ContainsAny(l, "\n\r") {
			t.Fatalf("stored event line contains a newline: %q", l)
		}
	}
}

// TestAppendLinesReportsWriteError is the dropped-write regression: when the
// append to a stream file fails, the handler must not answer 204 (which tells the
// browser the capture was persisted and stops it re-sending). Here the stream
// file is closed underneath the server so its Write fails; the handler must
// surface an error status instead of a silent 204.
func TestAppendLinesReportsWriteError(t *testing.T) {
	s, _ := newTestServer(t)
	if err := s.interactions.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	w := httptest.NewRecorder()
	s.handleInteraction(w, jsonPost("/api/interactions", "{\"t\":1,\"kind\":\"click\"}", nil))
	if w.Code == http.StatusNoContent {
		t.Fatalf("a failed capture write returned 204; want an error status")
	}
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

// shortWriteFile is an appendFile whose Write persists a prefix and then errors,
// standing in for a full disk (write(2) fills the remaining space, returns a
// short count, and the next write returns ENOSPC — os.File.Write persists the
// truncated prefix before returning the error).
type shortWriteFile struct {
	buf  []byte
	fail bool // when true, Write keeps only a prefix then returns an error
}

func (f *shortWriteFile) Seek(offset int64, whence int) (int64, error) {
	return int64(len(f.buf)), nil
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

// TestAppendRecordsRollsBackPartialWrite is the ENOSPC regression: a short write
// that persists a newline-less prefix must be truncated away, so the stream file
// never retains a partial line that would corrupt one physical JSONL record and
// break merge's reader for the whole file. Pre-fix appendLines wrote directly
// with no rollback, so the prefix survived.
func TestAppendRecordsRollsBackPartialWrite(t *testing.T) {
	f := &shortWriteFile{}
	if err := appendRecords(f, [][]byte{[]byte(`{"t":1,"kind":"click"}`)}); err != nil {
		t.Fatalf("first append: %v", err)
	}
	good := string(f.buf)

	f.fail = true
	if err := appendRecords(f, [][]byte{[]byte(`{"t":2,"kind":"click"}`)}); err == nil {
		t.Fatalf("expected a write error on a full disk")
	}
	if string(f.buf) != good {
		t.Fatalf("partial line survived: file is %q, want the clean prefix %q", f.buf, good)
	}
	if !strings.HasSuffix(string(f.buf), "\n") {
		t.Fatalf("file does not end on a newline: %q", f.buf)
	}
}

// TestDisplayURL pins the human-facing URL: "localhost" only for the host-less
// default, and the real host otherwise, never the broken "localhost0.0.0.0:8737"
// that concatenating a full -addr after the "localhost" literal produced.
func TestDisplayURL(t *testing.T) {
	cases := map[string]string{
		":8737":          "http://localhost:8737",
		"0.0.0.0:8737":   "http://0.0.0.0:8737",
		"127.0.0.1:8737": "http://127.0.0.1:8737",
		"[::1]:8737":     "http://[::1]:8737",
	}
	for in, want := range cases {
		if got := DisplayURL(in); got != want {
			t.Errorf("DisplayURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestWriteEndpointGuard covers the CSRF / DNS-rebinding surface: a request must
// carry a JSON Content-Type, a loopback Host, and (when present) a same-origin
// Origin. A rejected request must write nothing.
func TestWriteEndpointGuard(t *testing.T) {
	good := "{\"t\":1,\"kind\":\"click\"}"
	cases := []struct {
		name   string
		hdr    map[string]string
		accept bool
	}{
		{"legit loopback json", nil, true},
		{"legit json origin", map[string]string{"Origin": "http://localhost:8737"}, true},
		{"text/plain simple-request CSRF", map[string]string{"Content-Type": "text/plain"}, false},
		{"missing content-type", map[string]string{"Content-Type": ""}, false},
		{"cross-origin", map[string]string{"Origin": "http://evil.example"}, false},
		{"rebound foreign host", map[string]string{"Host": "evil.example:8737"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, dir := newTestServer(t)
			w := httptest.NewRecorder()
			s.handleInteraction(w, jsonPost("/api/interactions", good, c.hdr))
			lines := fileLines(t, filepath.Join(dir, session.InteractionsFile))
			if c.accept {
				if w.Code != http.StatusNoContent {
					t.Fatalf("status = %d, want 204", w.Code)
				}
				if len(lines) != 1 {
					t.Fatalf("accepted request wrote %d lines, want 1", len(lines))
				}
			} else {
				if w.Code == http.StatusNoContent {
					t.Fatalf("status = 204, want a rejection")
				}
				if len(lines) != 0 {
					t.Fatalf("rejected request still wrote %d lines: %q", len(lines), lines)
				}
			}
		})
	}
}

// TestServeRefusesSymlinkStream ensures the capture server will not open its
// stream files through a pre-planted symlink (arbitrary-file append).
func TestServeRefusesSymlinkStream(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, session.InteractionsFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := Serve(":0", dir); err == nil {
		t.Fatal("Serve accepted a symlinked interactions.jsonl; want refusal")
	}
	if b, _ := os.ReadFile(outside); string(b) != "keep\n" {
		t.Fatalf("victim file was modified through the symlink: %q", b)
	}
}
