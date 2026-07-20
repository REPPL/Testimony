package timeline

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// TestMergeRejectsMissingT0WithInteractions is the missing-anchor regression: a
// session with interactions.jsonl but a manifest lacking t0_epoch_ms cannot place
// those epoch-ms interaction times on the session clock. Pre-fix Merge used the
// zero-value t0 (0), turning each epoch-ms timestamp into a ~55-year offset and
// writing a silently corrupt timeline while exiting 0. Merge must reject it.
func TestMergeRejectsMissingT0WithInteractions(t *testing.T) {
	dir := t.TempDir()
	// Manifest without t0_epoch_ms (left at the zero value).
	if err := session.SaveManifest(dir, session.Manifest{Session: "s"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	ints := []Interaction{{T: t0 + 9_500, Kind: "click", Selector: "[data-testid=save-btn]"}}
	if err := session.WriteJSONL(filepath.Join(dir, session.InteractionsFile), ints); err != nil {
		t.Fatalf("write interactions: %v", err)
	}
	if _, _, err := Merge(dir); err == nil || !strings.Contains(err.Error(), "t0_epoch_ms") {
		t.Fatalf("expected a missing-t0 error, got %v", err)
	}
	// The corrupt timeline must not have been written.
	if _, statErr := os.Stat(filepath.Join(dir, session.TimelineFile)); statErr == nil {
		t.Fatalf("timeline.jsonl was written despite the missing t0")
	}
}

// TestMergeRejectsInteractionMissingT is the phantom-event regression: an
// interactions.jsonl line with no "t" used to decode leniently to the zero value
// 0, which BuildEntries turned into a relative time of about minus fifty-six
// years. Pre-fix Merge counted that record and the report rendered it at 00:00,
// so a malformed capture line silently became an event at the very start of the
// evidence record. Merge must refuse the session and name the offending line.
func TestMergeRejectsInteractionMissingT(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "s", T0EpochMS: t0}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	// The second record is the malformed one: a click with no "t".
	lines := "" +
		`{"t":` + strconv.FormatInt(t0+9_500, 10) + `,"kind":"click","selector":"[data-testid=save-btn]"}` + "\n" +
		`{"kind":"click","selector":"[data-testid=tab-appearance]"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, session.InteractionsFile), []byte(lines), 0o644); err != nil {
		t.Fatalf("write interactions: %v", err)
	}

	_, _, err := Merge(dir)
	if err == nil {
		t.Fatalf("expected a missing-t error, got nil")
	}
	if !strings.Contains(err.Error(), "interaction 2") || !strings.Contains(err.Error(), "missing t") {
		t.Fatalf("error should name the offending line and field, got %v", err)
	}
	// The timeline carrying the phantom event must not have been written.
	if _, statErr := os.Stat(filepath.Join(dir, session.TimelineFile)); statErr == nil {
		t.Fatalf("timeline.jsonl was written despite the malformed interaction")
	}
}

// TestMergeRejectsInteractionMissingKind covers the other field
// docs/reference/session-directory.md marks required: an interaction with no
// kind would join the timeline naming no observed action, so Merge refuses it.
func TestMergeRejectsInteractionMissingKind(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "s", T0EpochMS: t0}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	line := `{"t":` + strconv.FormatInt(t0+9_500, 10) + `,"selector":"[data-testid=save-btn]"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, session.InteractionsFile), []byte(line), 0o644); err != nil {
		t.Fatalf("write interactions: %v", err)
	}
	if _, _, err := Merge(dir); err == nil || !strings.Contains(err.Error(), "missing kind") {
		t.Fatalf("expected a missing-kind error, got %v", err)
	}
}

// TestMergeAcceptsInteractionAtT0 guards the other half of the required-field
// check: an interaction captured at exactly t0 has a relative time of 0, which a
// value-typed decode cannot distinguish from an absent "t". Alice clicking the
// moment capture starts is legitimate evidence and must still merge.
func TestMergeAcceptsInteractionAtT0(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "s", T0EpochMS: t0}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	line := `{"t":` + strconv.FormatInt(t0, 10) + `,"kind":"click","selector":"[data-testid=start]"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, session.InteractionsFile), []byte(line), 0o644); err != nil {
		t.Fatalf("write interactions: %v", err)
	}

	speech, events, err := Merge(dir)
	if err != nil {
		t.Fatalf("Merge with an interaction at t0: %v", err)
	}
	if speech != 0 || events != 1 {
		t.Fatalf("want speech=0 events=1, got speech=%d events=%d", speech, events)
	}
	entries, err := session.ReadJSONL[Entry](filepath.Join(dir, session.TimelineFile))
	if err != nil {
		t.Fatalf("read timeline: %v", err)
	}
	if len(entries) != 1 || entries[0].T != 0 {
		t.Fatalf("want a single event at t=0, got %+v", entries)
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
