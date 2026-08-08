package demo

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/REPPL/Testimony/internal/session"
	"github.com/REPPL/Testimony/internal/timeline"
)

// captureStderr runs fn with os.Stderr redirected to a pipe and returns
// everything it wrote there, so a test can assert on the operator-facing
// signal the capture server emits (its clients post via sendBeacon, so stderr
// is the only place a refusal ever surfaces).
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
	// t0 anchors the interaction shape check; 1 keeps the toy `"t":1` records
	// these tests post at a session-relative time of 0.
	return &server{interactions: inter, rawEvents: raw, t0: 1}, dir
}

// manifestDir builds a temp session directory holding a manifest with a usable
// t0 anchor, which Serve now loads for the interaction shape check.
func manifestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "s", App: "a", Participant: "P1", T0EpochMS: 1}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	return dir
}

// jsonPost builds a POST that passes the loopback/JSON guard by default; hdr
// overrides individual headers (and, for "Host", the request host; for
// "RemoteAddr", the request's remote address). httptest.NewRequest defaults
// RemoteAddr to the non-loopback "192.0.2.1:1234", so it is pinned to
// loopback here to keep every other case exercising the Host/Origin/
// Content-Type guards it actually targets, not the separate RemoteAddr one.
func jsonPost(path, body string, hdr map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Host = "localhost:8737"
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		if k == "Host" {
			r.Host = v
			continue
		}
		if k == "RemoteAddr" {
			r.RemoteAddr = v
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

// TestListenAddrRejectsOutOfRangePort is the invalid-port regression: a numeric
// port outside 0-65535 must be refused by listenAddr itself, not handed on to
// net.Listen. Pre-fix, ":99999" reached net.Listen and was refused there during
// address resolution ("invalid port"), but at exit 1, indistinguishable from a
// genuine bind failure such as a taken port, instead of the exit-2 usage
// refusal every other invalid -addr gets. An all-digit port too large to fit an
// int (strconv.Atoi's ErrRange) is refused the same way. A non-numeric port (a
// service name such as "http") is left untouched: only net.Listen's own
// /etc/services lookup can tell whether it resolves.
func TestListenAddrRejectsOutOfRangePort(t *testing.T) {
	for _, in := range []string{":99999", ":65536", ":-1", "127.0.0.1:99999", ":99999999999999999999"} {
		got, err := listenAddr(in)
		if err == nil {
			t.Errorf("listenAddr(%q) = %q with no error; want a refusal", in, got)
		} else if got != "" {
			t.Errorf("listenAddr(%q) returned %q alongside its error; want no address", in, got)
		}
	}
	for _, in := range []string{":8737", ":0", ":65535", ":http"} {
		if _, err := listenAddr(in); err != nil {
			t.Errorf("listenAddr(%q) returned an error: %v", in, err)
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
	srv, err := Serve(":0", manifestDir(t))
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

// TestBatchRejectsNonArray is the batch-path sibling of
// TestInteractionRefusedWhenMergeWouldRefuseIt's null case: json.Unmarshal
// into a []json.RawMessage succeeds for a JSON null, decoding it as a nil
// slice, so a batch body of `null` slipped past the "body is not a JSON
// array" refusal and wrote zero records with 204 instead of the 400
// docs/how-to/instrument-your-own-app.md promises for anything that is not a
// JSON array.
func TestBatchRejectsNonArray(t *testing.T) {
	cases := map[string]string{
		"null":   `null`,
		"object": `{"a":1}`,
		"number": `5`,
		"string": `"hello"`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			s, dir := newTestServer(t)
			w := httptest.NewRecorder()
			s.handleRawEvents(w, jsonPost("/api/events", body, nil))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			if lines := fileLines(t, filepath.Join(dir, session.RawEventsFile)); len(lines) != 0 {
				t.Fatalf("refused batch still wrote %d lines: %q", len(lines), lines)
			}
		})
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

// TestWriteEndpointGuard covers the CSRF / DNS-rebinding / forged-Host-on-a-
// wide-bind surface: a request must originate from a loopback remote address,
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
		{"forged loopback host from a LAN peer", map[string]string{"RemoteAddr": "192.0.2.7:54321"}, false},
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

// TestRunBindFailureCreatesNoSessionDir pins Run's ordering: the bind comes
// before the session directory is created. A well-formed -addr can still fail
// to bind (most plainly, the port is already taken — a second `testimony demo`
// beside a first), and creating the directory first left a stray session
// behind — manifest plus two empty stream files — for a server that never
// served, the same class the malformed-addr refusal already forecloses. Run
// only blocks after a successful bind, so with the port held it returns.
func TestRunBindFailureCreatesNoSessionDir(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold a port: %v", err)
	}
	defer ln.Close()
	out := t.TempDir()
	if err := Run(ln.Addr().String(), out); err == nil {
		t.Fatal("Run on a taken port: want a bind error, got nil")
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatalf("read out root: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("a refused bind created a session directory anyway: %v", entries)
	}
}

// TestWiderBindWarnsCaptureStaysLoopback pins the operator signal for the
// advertised wider bind: an explicit non-loopback host serves the page to
// other devices, but allowWrite still pins capture posts to loopback clients,
// so every remote post is refused and both streams stay empty. Pre-fix nothing
// said so anywhere — the operator learned only when merge counted 0 events.
func TestWiderBindWarnsCaptureStaysLoopback(t *testing.T) {
	dir := manifestDir(t)
	var srv *http.Server
	var err error
	stderr := captureStderr(t, func() { srv, err = Serve("0.0.0.0:0", dir) })
	if err != nil {
		t.Skipf("cannot bind 0.0.0.0 here: %v", err)
	}
	defer Shutdown(srv)
	if want := "capture posts are accepted from loopback clients only"; !strings.Contains(stderr, want) {
		t.Errorf("wider bind printed no warning; want %q on stderr, got %q", want, stderr)
	}

	// A loopback bind stays quiet — the warning must not cry wolf.
	quiet := captureStderr(t, func() {
		s2, err := Serve(":0", manifestDir(t))
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
		Shutdown(s2)
	})
	if strings.Contains(quiet, "loopback clients only") {
		t.Errorf("loopback bind printed the wider-bind warning: %q", quiet)
	}
}

// TestRefusedCaptureWriteIsLogged pins the refusal signal: EVERY refused
// capture post answers an error status the sendBeacon client cannot surface,
// so each refusal — the forgery guard's, a mis-shaped record's, an over-long
// body's — must reach the operator's terminal, as a failed persist already
// does. The 400 shape refusals went silent when they were first added.
func TestRefusedCaptureWriteIsLogged(t *testing.T) {
	cases := []struct {
		name string
		body string
		hdr  map[string]string
		code int
	}{
		{"rebound host", `{"t":1,"kind":"click"}`, map[string]string{"Host": "evil.example:8737"}, http.StatusForbidden},
		{"shape refusal", `{"t":1}`, nil, http.StatusBadRequest},
		{"non-object body", `[1,2,3]`, nil, http.StatusBadRequest},
		{"over-long record", jsonRecordOfSize(t, session.MaxJSONLLine), nil, http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := newTestServer(t)
			stderr := captureStderr(t, func() {
				w := httptest.NewRecorder()
				s.handleInteraction(w, jsonPost("/api/interactions", c.body, c.hdr))
				if w.Code != c.code {
					t.Fatalf("status = %d, want %d", w.Code, c.code)
				}
			})
			if want := "capture write refused"; !strings.Contains(stderr, want) {
				t.Errorf("refused write logged nothing; want %q on stderr, got %q", want, stderr)
			}
		})
	}
}

// TestInteractionRefusedWhenMergeWouldRefuseIt is the write/read shape
// regression: the endpoint accepted any JSON value — an array, a bare string,
// null, a number, an object missing the required t or kind — with 204, and the
// record was durably persisted for merge to refuse later, breaking the whole
// session (docs/reference/cli.md promises one JSON object per request and 400
// on malformed bodies). The write side must refuse exactly what the reader
// cannot take back.
func TestInteractionRefusedWhenMergeWouldRefuseIt(t *testing.T) {
	cases := map[string]string{
		"array":          `[1,2,3]`,
		"string":         `"hello"`,
		"null":           `null`,
		"number":         `42`,
		"missing t":      `{"kind":"click"}`,
		"missing kind":   `{"t":1}`,
		"non-positive t": `{"t":0,"kind":"click"}`,
		"absurd t":       `{"t":9000000000000000000,"kind":"click"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			s, dir := newTestServer(t)
			w := httptest.NewRecorder()
			s.handleInteraction(w, jsonPost("/api/interactions", body, nil))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			if lines := fileLines(t, filepath.Join(dir, session.InteractionsFile)); len(lines) != 0 {
				t.Fatalf("refused record still wrote %d lines: %q", len(lines), lines)
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
		// Just inside the line limit as received, so this record alone would pass
		// tooLongForJSONL — but merge's src/id/payload envelope pushes its timeline
		// entry back over the limit, and that entry is what session.WriteJSONL
		// checks when merge writes timeline.jsonl. Pre-fix this was accepted (204)
		// and durably persisted, then permanently unreadable from the first merge
		// onward with no CLI-level repair.
		"record fits alone but not once wrapped": jsonRecordOfSize(t, session.MaxJSONLLine-1),
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
// large record is still accepted and can be read straight back by the same
// reader merge uses, so the refusal above is not simply refusing everything
// large. It stays well clear of the line limit itself — a record that close
// also has to leave room for its timeline entry's src/id/payload envelope,
// which "record fits alone but not once wrapped" above exercises instead.
func TestAcceptedInteractionStaysReadable(t *testing.T) {
	s, dir := newTestServer(t)
	w := httptest.NewRecorder()
	s.handleInteraction(w, jsonPost("/api/interactions", jsonRecordOfSize(t, session.MaxJSONLLine-4096), nil))
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

// newTestServerWithSeededInteractions is newTestServer, except
// interactions.jsonl starts out pre-populated with seed rather than empty —
// for exercising appendLines' total-size check, which only bites once the
// file already carries most of its way to session.MaxJSONLBytes.
func newTestServerWithSeededInteractions(t *testing.T, seed []byte) (*server, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, session.InteractionsFile)
	if err := os.WriteFile(path, seed, 0o644); err != nil {
		t.Fatalf("seed %s: %v", session.InteractionsFile, err)
	}
	inter, err := session.OpenFileNoFollow(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", session.InteractionsFile, err)
	}
	raw, err := session.OpenFileNoFollow(filepath.Join(dir, session.RawEventsFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", session.RawEventsFile, err)
	}
	t.Cleanup(func() { inter.Close(); raw.Close() })
	return &server{interactions: inter, rawEvents: raw, t0: 1}, dir
}

// TestInteractionRefusedOverTotalCap is the write-side twin of
// session.ReadJSONL's total-size cap: merge reads interactions.jsonl through
// ReadJSONL, which refuses a file over session.MaxJSONLBytes outright, but
// appendLines' checks (maxBody, tooLongForJSONL, tooLongOnceWrapped) were all
// per-record, so a sequence of individually-valid POSTs — each answered 204 —
// could accumulate past the cap and leave the session durably unmergeable,
// discovered only after capture ends. A single small record posted against a
// file already near the cap must now be refused with 413 instead.
func TestInteractionRefusedOverTotalCap(t *testing.T) {
	seed := []byte(strings.Repeat("x", session.MaxJSONLBytes-10) + "\n")
	s, dir := newTestServerWithSeededInteractions(t, seed)

	w := httptest.NewRecorder()
	s.handleInteraction(w, jsonPost("/api/interactions", `{"t":1,"kind":"click"}`, nil))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}

	got, err := os.ReadFile(filepath.Join(dir, session.InteractionsFile))
	if err != nil {
		t.Fatalf("read %s: %v", session.InteractionsFile, err)
	}
	if !bytes.Equal(got, seed) {
		t.Fatal("interactions.jsonl was modified despite the refusal")
	}
}

// TestInteractionAcceptedUnderTotalCap is the positive control for
// TestInteractionRefusedOverTotalCap: a file with enough headroom under
// session.MaxJSONLBytes still accepts a record, and merge's own reader can
// scan the result back.
func TestInteractionAcceptedUnderTotalCap(t *testing.T) {
	seed := []byte(strings.Repeat("x", session.MaxJSONLBytes-10000) + "\n")
	s, dir := newTestServerWithSeededInteractions(t, seed)

	w := httptest.NewRecorder()
	s.handleInteraction(w, jsonPost("/api/interactions", `{"t":1,"kind":"click"}`, nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}

	got, err := os.ReadFile(filepath.Join(dir, session.InteractionsFile))
	if err != nil {
		t.Fatalf("read %s: %v", session.InteractionsFile, err)
	}
	if !bytes.HasPrefix(got, seed) {
		t.Fatal("accepted write lost the file's existing content")
	}
	if !bytes.Contains(got, []byte(`"kind":"click"`)) {
		t.Fatal("accepted write did not append the new record")
	}
}

// TestInteractionRefusedWhenWrappedTotalExceedsCap is the wrapped-total twin of
// TestInteractionRefusedOverTotalCap: session.WriteJSONL's cap on timeline.jsonl
// binds on each interaction's *wrapped* timeline-entry size, not its raw
// interactions.jsonl bytes, and the wrapped form runs larger (the src/id/payload
// envelope and rebased t). Tracking only the raw running total let a sequence of
// individually-valid, 204-accepted records cross the wrapped cap long before the
// raw total did, durably bricking merge without ever refusing a single request
// (empirically: tens of thousands of accepted records past the point merge could
// still write timeline.jsonl). A record posted once entryBytes — the running
// wrapped total — is close to the cap must be refused, even though
// interactions.jsonl's own raw bytes are nowhere near it.
func TestInteractionRefusedWhenWrappedTotalExceedsCap(t *testing.T) {
	s, dir := newTestServer(t)
	// As if prior records already wrapped to just under the cap; the raw file
	// itself is still empty, so the pre-existing raw-size check alone would
	// let this record through.
	s.entryBytes = session.MaxJSONLBytes - 10

	w := httptest.NewRecorder()
	s.handleInteraction(w, jsonPost("/api/interactions", `{"t":1,"kind":"click"}`, nil))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}

	got, err := os.ReadFile(filepath.Join(dir, session.InteractionsFile))
	if err != nil {
		t.Fatalf("read %s: %v", session.InteractionsFile, err)
	}
	if len(got) != 0 {
		t.Fatal("refused write was persisted")
	}
}

// TestInteractionAcceptedWhenWrappedTotalUnderCap is the positive control for
// TestInteractionRefusedWhenWrappedTotalExceedsCap: a record posted while
// entryBytes has enough headroom under session.MaxJSONLBytes is still accepted,
// and entryBytes grows to reflect it.
func TestInteractionAcceptedWhenWrappedTotalUnderCap(t *testing.T) {
	s, dir := newTestServer(t)
	s.entryBytes = session.MaxJSONLBytes - 10000

	w := httptest.NewRecorder()
	s.handleInteraction(w, jsonPost("/api/interactions", `{"t":1,"kind":"click"}`, nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if s.entryBytes <= session.MaxJSONLBytes-10000 {
		t.Fatal("accepted write did not grow entryBytes")
	}
	if lines := fileLines(t, filepath.Join(dir, session.InteractionsFile)); len(lines) != 1 {
		t.Fatalf("wrote %d lines, want 1", len(lines))
	}
}

// TestIDGrowth pins idGrowth's boundaries against the real "ev-%03d" width it
// mirrors: zero for every ordinal the "%03d" padding already covers, then one
// extra byte per extra decimal digit.
func TestIDGrowth(t *testing.T) {
	cases := map[int64]int64{
		1:         0,
		999:       0,
		1000:      1,
		9999:      1,
		10000:     2,
		99999:     2,
		100000:    3,
		999999999: 6,
	}
	for nth, want := range cases {
		if got := idGrowth(nth); got != want {
			t.Errorf("idGrowth(%d) = %d, want %d", nth, got, want)
		}
	}
}

// entryLenFor computes the wrapped timeline-entry size handleInteraction
// itself would compute for body, the same way production code does, so a
// test can pin entryBytes to an exact boundary relative to a real record
// rather than an arbitrary one.
func entryLenFor(t *testing.T, body string, t0 int64) int {
	t.Helper()
	var rec timeline.Interaction
	if err := json.Unmarshal([]byte(body), &rec); err != nil {
		t.Fatalf("unmarshal %q: %v", body, err)
	}
	n, err := session.EncodedLen(timeline.EventEntry(rec, t0))
	if err != nil {
		t.Fatalf("EncodedLen: %v", err)
	}
	return n
}

// TestInteractionRefusedWhenIDGrowthPushesOverCap pins entryBytes' own blind
// spot: it assumes every entry keeps EventEntry's "ev-001" placeholder width,
// but merge assigns the real, position-based id — the same gap
// eventIDGrowthMargin closes for the per-record tooLongOnceWrapped check. A
// record posted once the wrapped total sits exactly at the cap using the
// placeholder width, but whose real ordinal (tracked by entryCount) needs one
// extra digit, must still be refused.
func TestInteractionRefusedWhenIDGrowthPushesOverCap(t *testing.T) {
	body := `{"t":1,"kind":"click"}`
	s, dir := newTestServer(t)
	s.entryBytes = session.MaxJSONLBytes - int64(entryLenFor(t, body, s.t0))
	s.entryCount = 999 // next ordinal is 1000: "ev-1000" runs one byte past "ev-001"

	w := httptest.NewRecorder()
	s.handleInteraction(w, jsonPost("/api/interactions", body, nil))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (id growth ignored)", w.Code)
	}
	if lines := fileLines(t, filepath.Join(dir, session.InteractionsFile)); len(lines) != 0 {
		t.Fatalf("refused write persisted %d lines, want none", len(lines))
	}
}

// TestInteractionAcceptedWhenIDGrowthStaysZero is the positive control for
// TestInteractionRefusedWhenIDGrowthPushesOverCap: the identical entryBytes
// boundary is accepted when entryCount stays low enough that the next
// ordinal's id does not outgrow the placeholder width.
func TestInteractionAcceptedWhenIDGrowthStaysZero(t *testing.T) {
	body := `{"t":1,"kind":"click"}`
	s, dir := newTestServer(t)
	s.entryBytes = session.MaxJSONLBytes - int64(entryLenFor(t, body, s.t0))
	s.entryCount = 0 // next ordinal is 1: "ev-001" matches the placeholder exactly

	w := httptest.NewRecorder()
	s.handleInteraction(w, jsonPost("/api/interactions", body, nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if lines := fileLines(t, filepath.Join(dir, session.InteractionsFile)); len(lines) != 1 {
		t.Fatalf("wrote %d lines, want 1", len(lines))
	}
}

// TestBatchEventsIgnoreTotalCap pins the deliberate asymmetry: events.rrweb.jsonl
// is archival (session.MaxJSONLBytes' own doc comment exempts it — nothing reads
// it back through a total-capped path), so a batch upload against a file already
// past where interactions.jsonl would be refused must still succeed.
func TestBatchEventsIgnoreTotalCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, session.RawEventsFile)
	seed := []byte(strings.Repeat("x", session.MaxJSONLBytes+10000) + "\n")
	if err := os.WriteFile(path, seed, 0o644); err != nil {
		t.Fatalf("seed %s: %v", session.RawEventsFile, err)
	}
	raw, err := session.OpenFileNoFollow(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", session.RawEventsFile, err)
	}
	inter, err := session.OpenFileNoFollow(filepath.Join(dir, session.InteractionsFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", session.InteractionsFile, err)
	}
	t.Cleanup(func() { inter.Close(); raw.Close() })
	s := &server{interactions: inter, rawEvents: raw, t0: 1}

	w := httptest.NewRecorder()
	s.handleRawEvents(w, jsonPost("/api/events", `[{"a":1}]`, nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (events.rrweb.jsonl has no total cap)", w.Code)
	}
}

// TestServeRefusesSymlinkStream ensures the capture server will not open its
// stream files through a pre-planted symlink (arbitrary-file append).
func TestServeRefusesSymlinkStream(t *testing.T) {
	dir := manifestDir(t)
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

// TestServeOrCleanupRemovesSessionDirOnFailure covers the failure Run's
// bind-first ordering does not reach: the port bound fine and
// session.Create already wrote the manifest, but serveOn itself then fails
// (here, a directory sits where a stream file needs to be created — the
// same shape of failure a full disk or a permissions problem produces).
// Pre-fix, Run returned the error and left the session directory (manifest
// plus a stray stream-file collision) behind for a server that never
// served.
func TestServeOrCleanupRemovesSessionDirOnFailure(t *testing.T) {
	dir := manifestDir(t)
	if err := os.Mkdir(filepath.Join(dir, session.InteractionsFile), 0o755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if _, err := serveOrCleanup(ln, ":0", dir); err == nil {
		t.Fatal("serveOrCleanup with a poisoned stream path: want an error, got nil")
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("session directory must be removed after a serveOn failure, stat err = %v", statErr)
	}
}

// TestServeSurfacesBoundAddr pins the ephemeral-port fix: with ":0" the
// requested address names no real port, so Serve records the bound one on the
// returned server for callers to print — previously demo -addr :0 printed the
// unopenable http://localhost:0 while the real port was knowable only outside
// the tool. An explicit port must round-trip unchanged.
func TestServeSurfacesBoundAddr(t *testing.T) {
	srv, err := Serve(":0", manifestDir(t))
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	defer Shutdown(srv)
	host, port, err := net.SplitHostPort(srv.Addr)
	if err != nil {
		t.Fatalf("srv.Addr %q: %v", srv.Addr, err)
	}
	if host != "" {
		t.Fatalf("requested host must be kept (empty for \":0\"), got %q", host)
	}
	if port == "0" || port == "" {
		t.Fatalf("bound port not surfaced: srv.Addr = %q", srv.Addr)
	}
	if got := DisplayURL(displayAddr(srv, ":0")); strings.Contains(got, ":0") || !strings.HasPrefix(got, "http://localhost:") {
		t.Fatalf("display URL still unopenable: %q", got)
	}
}
