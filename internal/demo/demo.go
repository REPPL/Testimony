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
  url         : http://localhost%s

  1. Start your voice/screen recorder NOW (QuickTime → File → New Audio Recording).
  2. Say “session start” aloud, open the URL, and think aloud while you explore.
  3. When done: stop the recorder, press Ctrl+C here, then:

       testimony transcribe -session %s -audio <your-recording.m4a>
       testimony merge      -session %s
       testimony report     -session %s

`, dir, addr, dir, dir, dir)

	// Block until interrupted, then shut the server down cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	return srv.Shutdown(context.Background())
}

// Serve starts the demo capture server on addr, appending its two interaction
// streams into the existing session directory dir. It binds synchronously (so
// a bind failure is returned) and then serves in the background, returning the
// running *http.Server for the caller to Shutdown. record reuses this to run
// the demo app into the same directory as the recorders.
func Serve(addr, dir string) (*http.Server, error) {
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

	ln, err := net.Listen("tcp", listenAddr(addr))
	if err != nil {
		inter.Close()
		raw.Close()
		return nil, err
	}
	// The two stream files use direct O_APPEND writes (no buffering), so their
	// data is durable without an explicit Close; the OS reclaims them on exit,
	// as before. Not closing them on Shutdown avoids racing an in-flight write.
	srv := &http.Server{Handler: mux}
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
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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
			lines = append(lines, line)
		}
	} else {
		line, err := compactLine(body)
		if err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		lines = append(lines, line)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range lines {
		f.Write(l)
		f.Write([]byte("\n"))
	}
	w.WriteHeader(http.StatusNoContent)
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

// listenAddr binds the capture server to loopback by default: a bare ":8737"
// (empty host) becomes "127.0.0.1:8737", so the unauthenticated write endpoints
// are not published to the LAN even though the banner prints "localhost". An
// operator who deliberately wants a wider bind can still pass an explicit host
// (e.g. "0.0.0.0:8737").
func listenAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr // let net.Listen surface a malformed address
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
