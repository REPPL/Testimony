// Package timeline merges a session's speech transcript and interaction
// stream into a single, session-relative timeline (timeline.jsonl) — the one
// artefact the analysis layer consumes.
package timeline

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
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

// rawUtterance is how a transcript.jsonl record is decoded before it is
// trusted. Its T0 and T1 are pointers so that an absent "t0" stays
// distinguishable from a genuine 0, exactly as rawInteraction's T is: an
// utterance that begins the moment capture starts legitimately carries t0 equal
// to 0, so a value-typed field cannot tell that record apart from one whose
// "t0" the transcript never named, and a zero test would either reject honest
// testimony or admit malformed lines. Everything else mirrors Utterance.
type rawUtterance struct {
	ID      string   `json:"id"`
	T0      *float64 `json:"t0"`
	T1      *float64 `json:"t1"`
	Speaker string   `json:"speaker,omitempty"`
	Text    string   `json:"text"`
	Words   []Word   `json:"words,omitempty"`
}

// Word is one aligned word inside an utterance: its text and start time in
// session-relative seconds (docs/reference/session-directory.md).
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

// rawInteraction is how an interactions.jsonl record is decoded before it is
// trusted. Its T is a pointer so that an absent "t" stays distinguishable from a
// genuine 0: an interaction captured at exactly t0 legitimately carries t equal
// to the anchor, so a value-typed field cannot tell the two apart and a
// required-field check on it would either reject honest records or admit
// malformed ones. Everything else mirrors Interaction.
type rawInteraction struct {
	T        *int64 `json:"t"`
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

// readOptionalJSONL reads a JSONL input that a valid session may legitimately
// lack. A default audio-only `testimony record` run captures no interactions,
// and a session may equally reach merge before transcription: an absent file is
// therefore zero records, not a fatal error, so an audio-only or interaction-only
// session still merges to a partial timeline instead of aborting the documented
// pipeline. Any other read error (a malformed line, a permission failure) is
// still returned unchanged.
func readOptionalJSONL[T any](path string) ([]T, error) {
	out, err := session.ReadJSONL[T](path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return out, err
}

// maxUtteranceSeconds bounds the magnitude of an utterance's session-relative
// t0/t1. A real usability session is minutes to hours; 1e9 seconds (~31 years)
// is far past any genuine value while staying small enough that (t - t0) and the
// report's clock conversion never approach an integer or float hazard. It
// mirrors report.maxClockSeconds so the input boundary and the display sink
// refuse the same set of absurd times.
const maxUtteranceSeconds = 1e9

// checkedInteractions enforces the two fields that
// docs/reference/session-directory.md marks required on an interaction — t and
// kind — and converts the accepted records for BuildEntries. Without the check a
// record missing "t" decodes leniently to the zero value 0, which BuildEntries
// turns into a relative time of (0 - t0)/1000: for a real anchor that is roughly
// minus fifty-six years. Merge would still count the event and the report would
// render it at the top of the timeline, so one malformed capture line would
// silently plant a phantom event at the very start of the evidence record — the
// least visible place for a fabrication to sit. A record with no kind is refused
// for the same reason: it would join the timeline naming no observed action.
// Refusing the whole merge names the offending line so the operator repairs the
// capture instead of reading a corrupted account of the session.
func checkedInteractions(path string, raw []rawInteraction) ([]Interaction, error) {
	out := make([]Interaction, 0, len(raw))
	for i, r := range raw {
		if r.T == nil {
			return nil, fmt.Errorf("%s: interaction %d is missing t; cannot place it on the session clock", path, i+1)
		}
		// t is epoch milliseconds, so a value at or below zero anchors the event at
		// or before 1 January 1970 — no capture produces that, exactly the reasoning
		// session.Manifest.T0 refuses a non-positive anchor on. Refusing it here also
		// forecloses the extreme: a t near math.MinInt64 makes (t - t0EpochMS) wrap on
		// signed overflow in BuildEntries and plant the event millions of years after
		// session start, inflating the report's span while Merge still exits 0.
		if *r.T <= 0 {
			return nil, fmt.Errorf("%s: interaction %d has t %d; an epoch-millisecond time must be positive", path, i+1, *r.T)
		}
		if r.Kind == "" {
			return nil, fmt.Errorf("%s: interaction %d is missing kind; an event must name what happened", path, i+1)
		}
		out = append(out, Interaction{
			T:        *r.T,
			Kind:     r.Kind,
			Selector: r.Selector,
			Text:     r.Text,
			Value:    r.Value,
			Route:    r.Route,
		})
	}
	return out, nil
}

// checkedUtterances enforces the one transcript field whose absence cannot be
// caught later — t0 — and converts the accepted records for BuildEntries. It is
// the speech-side counterpart of checkedInteractions, and it exists because
// transcript.jsonl is as exchangeable and as hand-editable as interactions.jsonl
// while only the latter was ever guarded. Without the check a line missing "t0"
// decodes leniently to 0, BuildEntries places it at the session's very start,
// and the report prints it at 00:00 above everything that genuinely happened
// there: a malformed transcript would silently plant words at the opening of the
// evidence record, with nothing to say the transcript never timed them. Refusing
// the whole merge names the offending line so the operator repairs the
// transcript instead of reading a fabricated account of when Alice spoke.
//
// A nil t1 is defaulted to t0 rather than refused. A missing end time cannot
// move an utterance to a moment it did not occur — the fabrication hazard t0
// carries — it can only shrink the span the report joins events against, and
// SpeechEnd already commits the package to a documented answer for a speech
// entry with no end: fall back to its start. Defaulting here reproduces that
// answer at the boundary instead of contradicting it, and it avoids the far
// worse alternative of a zero-valued t1 on an utterance at, say, t0 22, whose
// join window [t0-window, 0+window] is inverted and quietly matches no event at
// all. Discarding a whole session's recoverable testimony over a shrinkable
// window would be the disproportionate answer.
func checkedUtterances(path string, raw []rawUtterance) ([]Utterance, error) {
	out := make([]Utterance, 0, len(raw))
	for i, r := range raw {
		if r.T0 == nil {
			return nil, fmt.Errorf("%s: utterance %d is missing t0; cannot place it on the session clock", path, i+1)
		}
		// A present t1 is accepted only when it does not precede t0. An explicit
		// t1 < t0 is the same inverted-window hazard the nil case is defaulted away
		// from: EventsNear would join over [t0-window, t1+window] with hi < lo and
		// silently match no event, detaching from the utterance the very
		// interactions spoken over it. Falling back to t0 reproduces the documented
		// SpeechEnd/nil-t1 answer rather than contradicting it — a missing or
		// backwards end can only shrink the join window, never move the utterance.
		// Bound the magnitude of t0 (and t1 below). Utterance times are
		// session-relative seconds where a small negative value is legitimate
		// (speech captured just before t0), so unlike the interaction side's
		// positive-epoch-ms rule this checks magnitude, not sign. An astronomically
		// large |t0| — a hand-edited transcript's 1e300 — is not a session time: it
		// overflows report's float64→int clock into a nonsensical duration and makes
		// the utterance's EventsNear join window span the whole session, so it
		// silently captures every event away from the utterances they were spoken
		// over. This is the speech-side twin of checkedInteractions' range refusal;
		// the report sink clock() defends the same class for hand-authored
		// timeline.jsonl that reaches it without passing through here.
		if math.Abs(*r.T0) > maxUtteranceSeconds {
			return nil, fmt.Errorf("%s: utterance %d has t0 %g; a session-relative time in seconds cannot exceed %g in magnitude", path, i+1, *r.T0, maxUtteranceSeconds)
		}
		t1 := *r.T0
		if r.T1 != nil && *r.T1 >= *r.T0 {
			if math.Abs(*r.T1) > maxUtteranceSeconds {
				return nil, fmt.Errorf("%s: utterance %d has t1 %g; a session-relative time in seconds cannot exceed %g in magnitude", path, i+1, *r.T1, maxUtteranceSeconds)
			}
			t1 = *r.T1
		}
		out = append(out, Utterance{
			ID:      r.ID,
			T0:      *r.T0,
			T1:      t1,
			Speaker: r.Speaker,
			Text:    r.Text,
			Words:   r.Words,
		})
	}
	return out, nil
}

// Merge reads manifest, transcript and interactions from dir, writes
// timeline.jsonl, and returns the number of speech and event entries.
func Merge(dir string) (speech, events int, err error) {
	man, err := session.LoadManifest(dir)
	if err != nil {
		return 0, 0, err
	}
	uttsPath := filepath.Join(dir, session.TranscriptFile)
	rawUtts, err := readOptionalJSONL[rawUtterance](uttsPath)
	if err != nil {
		return 0, 0, fmt.Errorf("read transcript: %w", err)
	}
	utts, err := checkedUtterances(uttsPath, rawUtts)
	if err != nil {
		return 0, 0, err
	}
	intsPath := filepath.Join(dir, session.InteractionsFile)
	raw, err := readOptionalJSONL[rawInteraction](intsPath)
	if err != nil {
		return 0, 0, fmt.Errorf("read interactions: %w", err)
	}

	// Interaction times are epoch milliseconds; without a usable t0 they cannot be
	// placed on the session clock. The anchor is obtained through session.Manifest.T0
	// rather than read from the field directly, so this call site honours the single
	// rule that accessor owns: it treats a zero anchor as absent and, crucially,
	// also refuses a NEGATIVE t0_epoch_ms — a case an inline `== 0` test missed, so a
	// negative anchor slipped through and BuildEntries shifted every interaction by
	// +|t0|, writing a silently corrupt timeline while Merge exited 0. The guard
	// stays conditional on interactions being present: a transcript-only session
	// carries no epoch-ms times, is already session-relative, and legitimately needs
	// no anchor, so it must still merge without one.
	t0 := man.T0EpochMS
	if len(raw) > 0 {
		t0, err = man.T0()
		if err != nil {
			return 0, 0, err
		}
	}

	ints, err := checkedInteractions(intsPath, raw)
	if err != nil {
		return 0, 0, err
	}

	entries := BuildEntries(t0, utts, ints)
	if err := session.WriteJSONL(filepath.Join(dir, session.TimelineFile), entries); err != nil {
		return 0, 0, fmt.Errorf("write timeline: %w", err)
	}
	return len(utts), len(ints), nil
}
