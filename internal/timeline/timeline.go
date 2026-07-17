// Package timeline merges a session's speech transcript and interaction
// stream into a single, session-relative timeline (timeline.jsonl) — the one
// artefact the analysis layer consumes.
package timeline

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/REPPL/Testimony/internal/session"
)

// Utterance is one line of transcript.jsonl. Times are session-relative
// seconds (WhisperX audio time, assuming audio capture starts at t0).
type Utterance struct {
	ID      string  `json:"id"`
	T0      float64 `json:"t0"`
	T1      float64 `json:"t1"`
	Speaker string  `json:"speaker,omitempty"`
	Text    string  `json:"text"`
	Words   []Word  `json:"words,omitempty"`
}

// Word is one aligned word inside an utterance: its text and start time in
// session-relative seconds (docs/architecture.md §5).
type Word struct {
	W string  `json:"w"`
	T float64 `json:"t"`
}

// Interaction is one line of interactions.jsonl as captured in the browser
// (or another instrumented surface). T is epoch milliseconds.
type Interaction struct {
	T        int64  `json:"t"`
	Kind     string `json:"kind"`
	Selector string `json:"selector,omitempty"`
	Text     string `json:"text,omitempty"`
	Value    string `json:"value,omitempty"`
	Route    string `json:"route,omitempty"`
}

// Entry is one line of timeline.jsonl: a speech or event item on the shared
// session-relative clock.
type Entry struct {
	T       float64        `json:"t"`
	Src     string         `json:"src"` // "speech" | "event"
	ID      string         `json:"id"`
	Payload map[string]any `json:"payload"`
}

// BuildEntries converts utterances and interactions to a single slice of
// timeline entries, sorted by time. t0EpochMS anchors interaction times.
func BuildEntries(t0EpochMS int64, utts []Utterance, ints []Interaction) []Entry {
	entries := make([]Entry, 0, len(utts)+len(ints))

	for _, u := range utts {
		p := map[string]any{
			"t1":      u.T1,
			"speaker": u.Speaker,
			"text":    u.Text,
		}
		if len(u.Words) > 0 {
			p["words"] = u.Words
		}
		entries = append(entries, Entry{
			T:       u.T0,
			Src:     "speech",
			ID:      u.ID,
			Payload: p,
		})
	}
	for i, ev := range ints {
		rel := float64(ev.T-t0EpochMS) / 1000.0
		p := map[string]any{"kind": ev.Kind}
		if ev.Selector != "" {
			p["selector"] = ev.Selector
		}
		if ev.Text != "" {
			p["text"] = ev.Text
		}
		if ev.Value != "" {
			p["value"] = ev.Value
		}
		if ev.Route != "" {
			p["route"] = ev.Route
		}
		entries = append(entries, Entry{
			T:       rel,
			Src:     "event",
			ID:      fmt.Sprintf("ev-%03d", i+1),
			Payload: p,
		})
	}

	sort.SliceStable(entries, func(i, j int) bool { return entries[i].T < entries[j].T })
	return entries
}

// SpeechEnd returns the end time of a speech entry (its t1), falling back to
// its start time.
func SpeechEnd(e Entry) float64 {
	if t1, ok := e.Payload["t1"].(float64); ok {
		return t1
	}
	return e.T
}

// EventsNear returns the IDs of event entries that fall inside the utterance
// span [u.T0-window, u.T1+window]. Used by the report's join step.
func EventsNear(entries []Entry, u Entry, window float64) []string {
	lo := u.T - window
	hi := SpeechEnd(u) + window
	var ids []string
	for _, e := range entries {
		if e.Src != "event" {
			continue
		}
		if e.T >= lo && e.T <= hi {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

// Merge reads manifest, transcript and interactions from dir, writes
// timeline.jsonl, and returns the number of speech and event entries.
func Merge(dir string) (speech, events int, err error) {
	man, err := session.LoadManifest(dir)
	if err != nil {
		return 0, 0, err
	}
	utts, err := session.ReadJSONL[Utterance](filepath.Join(dir, session.TranscriptFile))
	if err != nil {
		return 0, 0, fmt.Errorf("read transcript: %w", err)
	}
	ints, err := session.ReadJSONL[Interaction](filepath.Join(dir, session.InteractionsFile))
	if err != nil {
		return 0, 0, fmt.Errorf("read interactions: %w", err)
	}

	entries := BuildEntries(man.T0EpochMS, utts, ints)
	if err := session.WriteJSONL(filepath.Join(dir, session.TimelineFile), entries); err != nil {
		return 0, 0, fmt.Errorf("write timeline: %w", err)
	}
	return len(utts), len(ints), nil
}
