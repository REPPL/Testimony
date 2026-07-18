package timeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/REPPL/Testimony/internal/session"
)

const t0 = int64(1_784_300_400_000) // arbitrary session anchor, epoch ms

func sample() []Entry {
	utts := []Utterance{
		{ID: "utt-001", T0: 8.0, T1: 12.5, Speaker: "P1", Text: "I'll change my display name."},
		{ID: "utt-002", T0: 22.0, T1: 28.0, Speaker: "P1", Text: "I clicked save and nothing happened."},
	}
	ints := []Interaction{
		{T: t0 + 9_500, Kind: "click", Selector: "[data-testid=display-name]"},    // 9.5s → in utt-001 span
		{T: t0 + 19_200, Kind: "click", Selector: "[data-testid=save-btn]"},       // 19.2s → in neither (window 2.5)
		{T: t0 + 24_100, Kind: "click", Selector: "[data-testid=save-btn]"},       // 24.1s → in utt-002 span
		{T: t0 + 29_900, Kind: "click", Selector: "[data-testid=tab-appearance]"}, // 29.9s → within utt-002 end+2.5
	}
	return BuildEntries(t0, utts, ints)
}

func TestBuildEntriesSortsByTime(t *testing.T) {
	entries := sample()
	if len(entries) != 6 {
		t.Fatalf("want 6 entries, got %d", len(entries))
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].T < entries[i-1].T {
			t.Fatalf("entries not sorted at %d: %.2f < %.2f", i, entries[i].T, entries[i-1].T)
		}
	}
	// Epoch→relative conversion.
	var first *Entry
	for i := range entries {
		if entries[i].Src == "event" {
			first = &entries[i]
			break
		}
	}
	if first == nil || first.T != 9.5 {
		t.Fatalf("first event should be at 9.5s, got %+v", first)
	}
}

// TestMergeAudioOnlySession is the audio-only-record regression: a default
// `testimony record` session has manifest + transcript but no interactions.jsonl
// (only the demo server writes that file), and record itself prints `merge` as
// the next step. Merge must treat the absent interactions file as zero events and
// still write a speech-only timeline, not abort with a "no such file" error.
func TestMergeAudioOnlySession(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "s", T0EpochMS: t0}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	utts := []Utterance{{ID: "utt-001", T0: 1, T1: 2, Speaker: "P1", Text: "hello"}}
	if err := session.WriteJSONL(filepath.Join(dir, session.TranscriptFile), utts); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	speech, events, err := Merge(dir)
	if err != nil {
		t.Fatalf("Merge on audio-only session: %v", err)
	}
	if speech != 1 || events != 0 {
		t.Fatalf("want speech=1 events=0, got speech=%d events=%d", speech, events)
	}
	if _, err := os.Stat(filepath.Join(dir, session.TimelineFile)); err != nil {
		t.Fatalf("timeline.jsonl not written: %v", err)
	}
}

func TestEventsNearWindow(t *testing.T) {
	entries := sample()
	var speech []Entry
	for _, e := range entries {
		if e.Src == "speech" {
			speech = append(speech, e)
		}
	}

	near1 := EventsNear(entries, speech[0], 2.5) // span 5.5–15.0
	if len(near1) != 1 || near1[0] != "ev-001" {
		t.Fatalf("utt-001: want [ev-001], got %v", near1)
	}

	near2 := EventsNear(entries, speech[1], 2.5) // span 19.5–30.5
	if len(near2) != 2 || near2[0] != "ev-003" || near2[1] != "ev-004" {
		t.Fatalf("utt-002: want [ev-003 ev-004], got %v", near2)
	}

	// ev-002 (19.2s) sits between spans with the default window — by design
	// it must surface as a standalone event, not silently vanish.
	all := map[string]bool{}
	for _, u := range speech {
		for _, id := range EventsNear(entries, u, 2.5) {
			all[id] = true
		}
	}
	if all["ev-002"] {
		t.Fatalf("ev-002 should be outside both windows")
	}
}
