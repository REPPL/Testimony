// Package demo serves a small instrumented web app so a think-aloud session
// can be captured end-to-end before any real application is wired up. It
// persists two streams into a fresh session directory: raw rrweb events
// (archival) and normalised interactions (what merge consumes).
package demo

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/REPPL/Testimony/internal/session"
)

//go:embed assets/index.html
var assets embed.FS

type server struct {
	mu           sync.Mutex
	interactions *os.File
	rawEvents    *os.File
}

// DefaultApp is the app-under-test name a demo session records.
const DefaultApp = "testimony demo"

// DefaultTask is the seeded task for a demo session.
const DefaultTask = "Explore the settings prototype and think aloud"

// The capture server serves one operator over loopback, so every phase of a
// request has a generous but finite budget. Without these an http.Server waits
// for ever: a single connection that opens and then stalls before sending its
// request headers — a browser tab suspended by the OS, a half-closed socket a
// sleeping laptop left behind — keeps a connection alive indefinitely, and
// Shutdown waits for it, so Ctrl+C hangs instead of finalising the session.
// readTimeout covers the whole request including a maxBatchBody rrweb batch,
// which crosses loopback in milliseconds; idleTimeout reaps keep-alive
// connections the page will not reuse.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 60 * time.Second
	idleTimeout       = 120 * time.Second
)

// shutdownTimeout bounds how long a caller waits for in-flight capture writes
// to finish before the server is closed out from under them. Finalising the
// session promptly matters more than the last few bytes of one stalled
// connection, and an operator who has pressed Ctrl+C is waiting.
const shutdownTimeout = 5 * time.Second

// maxBatchBody caps a raw-event batch body. A batch carries many records, so it
// may legitimately exceed the limit that applies to any one of them; each line
// it produces is still checked individually against session.MaxJSONLLine.
const maxBatchBody = 8 << 20

// Run starts the demo capture server on addr, creating a new session
// directory under outRoot. It blocks until the process is interrupted.
func Run(addr, outRoot string) error {
	dir, err := session.Create(outRoot, time.Now(), session.Manifest{
		App:         DefaultApp,
		Participant: "P1",
		Tasks:       []string{DefaultTask},
	})
	if err != nil {
		return err
	}

	srv, err := Serve(addr, dir)
	if err != nil {
		return err
	}

	fmt.Printf(`testimony demo — capture session started

  session dir : %s
  url         : %s

  1. Start your voice/screen recorder NOW (QuickTime → File → New Audio Recording).
  2. Say “session start” aloud, open the URL, and think aloud while you explore.
  3. When done: stop the recorder, press Ctrl+C here, then:

       testimony transcribe -session %s -audio <your-recording.m4a>
       testimony merge      -session %s
       testimony report     -session %s

`, dir, DisplayURL(addr), dir, dir, dir)

	// Block until interrupted, then shut the server down cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	return Shutdown(srv)
}

// Shutdown stops a capture server returned by Serve, under a deadline. It is
// how every caller should stop the server: srv.Shutdown with a context that
// never expires blocks for as long as any connection stays open, so one stalled
// client left Ctrl+C hanging for ever instead of finalising the session. When
// the graceful drain misses the deadline the remaining connections are closed
// outright — the two stream files use direct O_APPEND writes, so records already
// accepted are durable either way.
func Shutdown(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return srv.Close()
	}
	return nil
}

// Serve starts the demo capture server on addr, appending its two interaction
// streams into the existing session directory dir. It binds synchronously (so
// a bind failure is returned) and then serves in the background, returning the
// running *http.Server for the caller to Shutdown. record reuses this to run
// the demo app into the same directory as the recorders.
func Serve(addr, dir string) (*http.Server, error) {
	// Resolve the bind address before touching the session directory, so an addr
	// that will be refused never leaves empty stream files behind.
	bind, err := listenAddr(addr)
	if err != nil {
		return nil, err
	}
	open := func(name string) (*os.File, error) {
		return session.OpenFileNoFollow(filepath.Join(dir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	}
	inter, err := open(session.InteractionsFile)
	if err != nil {
		return nil, err
	}
	raw, err := open(session.RawEventsFile)
	if err != nil {
		inter.Close()
		return nil, err
	}
	s := &server{interactions: inter, rawEvents: raw}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		b, _ := assets.ReadFile("assets/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
	mux.HandleFunc("/api/interactions", s.handleInteraction)
	mux.HandleFunc("/api/events", s.handleRawEvents)

	ln, err := net.Listen("tcp", bind)
	if err != nil {
		inter.Close()
		raw.Close()
		return nil, err
	}
	// The two stream files use direct O_APPEND writes (no buffering), so their
	// data is durable without an explicit Close; the OS reclaims them on exit,
	// as before. Not closing them on Shutdown avoids racing an in-flight write.
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		IdleTimeout:       idleTimeout,
	}
	go srv.Serve(ln)
	return srv, nil
}

// handleInteraction appends one normalised interaction (a single JSON object)
// as one line of interactions.jsonl.
func (s *server) handleInteraction(w http.ResponseWriter, r *http.Request) {
	s.appendLines(w, r, s.interactions, false)
}

// handleRawEvents appends a batch (JSON array) of raw rrweb events, one per
// line, to events.rrweb.jsonl.
func (s *server) handleRawEvents(w http.ResponseWriter, r *http.Request) {
	s.appendLines(w, r, s.rawEvents, true)
}

func (s *server) appendLines(w http.ResponseWriter, r *http.Request, f *os.File, batch bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !allowWrite(w, r) {
		return
	}
	// The write side must respect the read side's invariant: every JSONL reader
	// stops at session.MaxJSONLLine, so a longer line accepted here would be
	// durably persisted and permanently unreadable, breaking merge, report and
	// analyze for the whole session. A single interaction can therefore never be
	// larger than one readable line; a batch may be, because it becomes many. Read
	// one byte past the cap so an over-long body is refused as too large rather
	// than silently truncated and then rejected as invalid JSON, which would tell
	// the operator the page sent nonsense when it sent too much.
	maxBody := int64(session.MaxJSONLLine)
	if batch {
		maxBody = maxBatchBody
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if int64(len(body)) > maxBody {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	var lines [][]byte
	if batch {
		var msgs []json.RawMessage
		if err := json.Unmarshal(body, &msgs); err != nil {
			http.Error(w, "expected JSON array", http.StatusBadRequest)
			return
		}
		for _, m := range msgs {
			line, err := compactLine(m)
			if err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			if tooLongForJSONL(line) {
				http.Error(w, "record exceeds the readable JSONL line limit", http.StatusRequestEntityTooLarge)
				return
			}
			lines = append(lines, line)
		}
	} else {
		line, err := compactLine(body)
		if err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if tooLongForJSONL(line) {
			http.Error(w, "record exceeds the readable JSONL line limit", http.StatusRequestEntityTooLarge)
			return
		}
		lines = append(lines, line)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := appendRecords(f, lines); err != nil {
		// The capture was not persisted. Tell the client so it does not treat a
		// dropped event as recorded (it answers the 500 rather than a 204), and log
		// to the operator's terminal: the client uses sendBeacon, which cannot report
		// a server status back to the page, so this line is the only signal the person
		// running the session gets that their evidence stream has started dropping.
		fmt.Fprintf(os.Stderr, "testimony demo: capture write failed, event(s) dropped: %v\n", err)
		http.Error(w, "capture write failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// appendFile is the subset of *os.File that appendRecords needs; a fake
// satisfies it in tests to exercise the partial-write rollback.
type appendFile interface {
	io.Writer
	Seek(offset int64, whence int) (int64, error)
	Truncate(size int64) error
}

// appendRecords writes each line and its terminating newline to f. os.File.Write
// gives no atomicity guarantee: a full disk fills the remaining space and returns
// a short count, so a bare Write can persist a truncated, newline-less prefix
// (e.g. `{"t":123,"kind":"cl`) before ENOSPC surfaces. That partial line would
// join the next successful write into one malformed physical record and break
// the JSONL reader for the whole file. So on any write error appendRecords
// truncates f back to the length it had before the failing write, so no partial
// line survives — the caller only reports the drop, the file stays clean.
func appendRecords(f appendFile, lines [][]byte) error {
	for _, l := range lines {
		before, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(l, '\n')); err != nil {
			// Best-effort roll back any partial bytes; surface the original error.
			f.Truncate(before)
			return err
		}
	}
	return nil
}

// compactLine canonicalises one accepted JSON value into a single newline-free
// physical line. json.Compact strips insignificant whitespace — including the
// raw newlines JSON permits between tokens — so one accepted value maps to
// exactly one JSONL line and cannot be split across lines, which would corrupt
// interactions.jsonl / events.rrweb.jsonl and break merge's line-by-line
// reader. It also rejects invalid JSON (replacing the previous json.Valid gate).
func compactLine(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// tooLongForJSONL reports whether line, plus the newline appendRecords adds,
// would exceed what session.ReadJSONL and analyze.Load can scan back. It is
// checked before anything is written, so a batch carrying one over-long record
// is refused whole rather than leaving the records before it on disk followed by
// a line no reader can reach past.
func tooLongForJSONL(line []byte) bool {
	return len(line)+1 > session.MaxJSONLLine
}

// allowWrite guards the capture write endpoints against cross-origin forgery
// (CSRF) and DNS-rebinding of the loopback server. It requires a loopback Host
// (a rebinding page still sends the attacker hostname), a same-origin Origin
// when present, and a JSON Content-Type — a non-CORS-safelisted type that forces
// a preflight the server never answers permissively, so a cross-origin no-cors
// "simple request" POST cannot reach the write. It writes the error response and
// returns false when the request must be refused.
func allowWrite(w http.ResponseWriter, r *http.Request) bool {
	if !loopbackHost(r.Host) {
		http.Error(w, "unexpected Host", http.StatusForbidden)
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || !loopbackHost(u.Host) {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return false
		}
	}
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return false
	}
	return true
}

// loopbackHost reports whether hostport names the local machine: the literal
// "localhost" or any loopback IP. Used to pin the Host/Origin to loopback.
func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isJSONContentType reports whether ct is an application/json media type,
// tolerating a charset or other parameter.
func isJSONContentType(ct string) bool {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.EqualFold(strings.TrimSpace(ct), "application/json")
}

// DisplayURL renders the human-facing URL an operator opens for a capture
// server bound to addr. It shows "localhost" only for the host-less default
// (":8737" -> http://localhost:8737); when an operator passes an explicit host
// for a wider bind (e.g. "0.0.0.0:8737") it shows that host, so the printed URL
// is never the broken "http://localhost0.0.0.0:8737" that raw concatenation of
// addr after the "localhost" literal produced. Shared by demo.Run and
// record.printStatus.
func DisplayURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://localhost" + addr // malformed addr: preserve the old form
	}
	if host == "" {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// listenAddr binds the capture server to loopback by default: a bare ":8737"
// (empty host) becomes "127.0.0.1:8737", so the unauthenticated write endpoints
// are not published to the LAN even though the banner prints "localhost". An
// operator who deliberately wants a wider bind can still pass an explicit host
// (e.g. "0.0.0.0:8737").
//
// An addr that does not parse into host and port is refused outright rather
// than passed through to net.Listen. Passing it through defeated the very
// defaulting above: net.Listen("tcp", "") binds every interface, so an empty
// -addr published the unauthenticated capture write endpoints to the whole LAN
// on an arbitrary port, silently and with the banner still saying "localhost".
// Refusing names the expected form instead, and leaves the deliberate host-less
// ":8737" -> loopback behaviour untouched.
func listenAddr(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("invalid capture address %q: want host:port or :port, e.g. \":8737\"", addr)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), nil
}
