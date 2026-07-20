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
		got, err := listenAddr(in)
		if err != nil {
			t.Errorf("listenAddr(%q) returned an error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("listenAddr(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestListenAddrRejectsUnparseableAddr is the bind-everywhere regression: an
// addr that does not parse into host and port must be refused, never handed on
// to net.Listen. Pre-fix listenAddr returned such an addr unchanged, so an empty
// -addr reached net.Listen("tcp", "") and bound the unauthenticated capture
// write endpoints on every interface — the exact outcome the loopback default
// exists to prevent.
func TestListenAddrRejectsUnparseableAddr(t *testing.T) {
	for _, in := range []string{"", "8737", "localhost", "127.0.0.1"} {
		got, err := listenAddr(in)
		if err == nil {
			t.Errorf("listenAddr(%q) = %q with no error; want a refusal", in, got)
		}
		if got != "" {
			t.Errorf("listenAddr(%q) returned %q alongside its error; want no address", in, got)
		}
	}
}

// TestServeRefusesUnparseableAddr proves the refusal reaches the capture server:
// Serve must not bind at all for an empty addr, and must not leave stream files
// behind in the session directory for a session it never served.
func TestServeRefusesUnparseableAddr(t *testing.T) {
	dir := t.TempDir()
	srv, err := Serve("", dir)
	if err == nil {
		Shutdown(srv)
		t.Fatal("Serve accepted an empty addr; want a refusal rather than a bind on every interface")
	}
	if _, statErr := os.Stat(filepath.Join(dir, session.InteractionsFile)); !os.IsNotExist(statErr) {
		t.Fatalf("Serve left a stream file behind for a refused addr: %v", statErr)
	}
}

// TestServeBoundsRequestTimeouts is the Ctrl+C-hang regression: the capture
// server must give every request phase a finite budget. Pre-fix it was built
// with none, so a single client that opened a connection and then stalled kept
// it alive for ever and the shutdown waited on it, leaving 'testimony record'
// hanging after Ctrl+C instead of finalising the session.
func TestServeBoundsRequestTimeouts(t *testing.T) {
	srv, err := Serve(":0", t.TempDir())
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	defer Shutdown(srv)

	if srv.ReadHeaderTimeout <= 0 {
		t.Errorf("ReadHeaderTimeout = %v, want a bounded budget", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout <= 0 {
		t.Errorf("ReadTimeout = %v, want a bounded budget", srv.ReadTimeout)
	}
	if srv.IdleTimeout <= 0 {
		t.Errorf("IdleTimeout = %v, want a bounded budget", srv.IdleTimeout)
	}
	// A request must still be allowed to carry the largest batch the endpoint
	// accepts, so the budget cannot be so tight that it refuses honest capture.
	if srv.ReadTimeout < srv.ReadHeaderTimeout {
		t.Errorf("ReadTimeout %v is shorter than ReadHeaderTimeout %v", srv.ReadTimeout, srv.ReadHeaderTimeout)
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

// jsonRecordOfSize builds a single valid, whitespace-free interaction JSON
// object of exactly n bytes, padding its text field. Because it carries no
// insignificant whitespace, json.Compact leaves it byte-for-byte, so its stored
// line length is exactly n.
func jsonRecordOfSize(t *testing.T, n int) string {
	t.Helper()
	const prefix = `{"t":1,"kind":"click","text":"`
	const suffix = `"}`
	if n < len(prefix)+len(suffix) {
		t.Fatalf("cannot build a %d-byte record: the envelope alone is %d bytes", n, len(prefix)+len(suffix))
	}
	return prefix + strings.Repeat("a", n-len(prefix)-len(suffix)) + suffix
}

// TestOversizedInteractionIsRefusedNotPersisted is the unreadable-record
// regression: the write side must honour the read side's session.MaxJSONLLine
// invariant. Pre-fix the endpoint accepted a body up to 8 MiB and wrote it as
// one JSONL line, so a record between 4 and 8 MiB was durably persisted and then
// permanently unreadable — merge, report and analyze all failed for that session
// with no way to recover it. Such a record must be refused with 413 and nothing
// must reach the stream file.
func TestOversizedInteractionIsRefusedNotPersisted(t *testing.T) {
	cases := map[string]string{
		// Between the old 8 MiB body cap and the readers' 4 MiB line limit: the
		// size that used to be accepted and corrupt the session.
		"over the body cap": jsonRecordOfSize(t, 6<<20),
		// Exactly the line limit: the terminating newline pushes the physical line
		// one byte past what the readers can scan back, so it too must be refused.
		"line limit plus newline": jsonRecordOfSize(t, session.MaxJSONLLine),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			s, dir := newTestServer(t)
			w := httptest.NewRecorder()
			s.handleInteraction(w, jsonPost("/api/interactions", body, nil))

			if w.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want 413", w.Code)
			}
			if lines := fileLines(t, filepath.Join(dir, session.InteractionsFile)); len(lines) != 0 {
				t.Fatalf("refused record still wrote %d lines of %d bytes", len(lines), len(lines[0]))
			}
			if _, err := session.ReadJSONL[map[string]any](filepath.Join(dir, session.InteractionsFile)); err != nil {
				t.Fatalf("ReadJSONL on the stream file failed after a refusal: %v", err)
			}
		})
	}
}

// TestAcceptedInteractionStaysReadable pins the other side of the limit: a
// record just inside it is still accepted and can be read straight back by the
// same reader merge uses, so the refusal above is not simply refusing
// everything large.
func TestAcceptedInteractionStaysReadable(t *testing.T) {
	s, dir := newTestServer(t)
	w := httptest.NewRecorder()
	s.handleInteraction(w, jsonPost("/api/interactions", jsonRecordOfSize(t, session.MaxJSONLLine-1), nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	got, err := session.ReadJSONL[map[string]any](filepath.Join(dir, session.InteractionsFile))
	if err != nil {
		t.Fatalf("ReadJSONL on an accepted record failed: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("read back %d records, want 1", len(got))
	}
}

// TestOversizedBatchRecordIsRefusedWhole is the same invariant on the batch
// path, where a batch may legitimately be larger than one record: a batch whose
// records are individually fine except for one over-long element must be refused
// entirely. Persisting the good records first and then the unreadable one would
// still leave the reader unable to scan past it.
func TestOversizedBatchRecordIsRefusedWhole(t *testing.T) {
	s, dir := newTestServer(t)
	body := "[" + `{"a":1}` + "," + jsonRecordOfSize(t, session.MaxJSONLLine) + "]"

	w := httptest.NewRecorder()
	s.handleRawEvents(w, jsonPost("/api/events", body, nil))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
	if lines := fileLines(t, filepath.Join(dir, session.RawEventsFile)); len(lines) != 0 {
		t.Fatalf("refused batch persisted %d lines, want none", len(lines))
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
