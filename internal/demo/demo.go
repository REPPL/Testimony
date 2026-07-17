// Package demo serves a small instrumented web app so a think-aloud session
// can be captured end-to-end before any real application is wired up. It
// persists two streams into a fresh session directory: raw rrweb events
// (archival) and normalised interactions (what merge consumes).
package demo

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
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

// Run starts the demo capture server on addr, creating a new session
// directory under outRoot. It blocks until the process is interrupted.
func Run(addr, outRoot string) error {
	now := time.Now()
	dir := filepath.Join(outRoot, now.Format("2006-01-02_150405"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	man := session.Manifest{
		Session:     filepath.Base(dir),
		App:         "testimony demo",
		Participant: "P1",
		T0EpochMS:   now.UnixMilli(),
		Tasks:       []string{"Explore the settings prototype and think aloud"},
	}
	if err := session.SaveManifest(dir, man); err != nil {
		return err
	}

	open := func(name string) (*os.File, error) {
		return os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	}
	inter, err := open(session.InteractionsFile)
	if err != nil {
		return err
	}
	raw, err := open(session.RawEventsFile)
	if err != nil {
		return err
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

	return http.ListenAndServe(addr, mux)
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
			lines = append(lines, m)
		}
	} else {
		if !json.Valid(body) {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		lines = append(lines, body)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range lines {
		f.Write(l)
		f.Write([]byte("\n"))
	}
	w.WriteHeader(http.StatusNoContent)
}
