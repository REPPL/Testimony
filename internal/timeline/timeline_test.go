package timeline

import (
	"errors"
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

// TestMergeRejectsNegativeT0WithInteractions is the negative-anchor regression:
// Merge used to guard the anchor with an inline `man.T0EpochMS == 0` test, which
// is narrower than the `<= 0` rule session.Manifest.T0 owns. A NEGATIVE
// t0_epoch_ms therefore slipped through, and BuildEntries shifted every
// interaction by +|t0| — placing each event decades into the session — while
// Merge wrote that silently corrupt timeline and exited 0. Routing through
// man.T0 now refuses it with the ErrNoT0-based error, and no timeline is written.
func TestMergeRejectsNegativeT0WithInteractions(t *testing.T) {
	dir := t.TempDir()
	// A negative anchor: it would place the session before the epoch, which no
	// capture can produce.
	if err := session.SaveManifest(dir, session.Manifest{Session: "s", T0EpochMS: -1}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	ints := []Interaction{{T: t0 + 9_500, Kind: "click", Selector: "[data-testid=save-btn]"}}
	if err := session.WriteJSONL(filepath.Join(dir, session.InteractionsFile), ints); err != nil {
		t.Fatalf("write interactions: %v", err)
	}
	_, _, err := Merge(dir)
	if err == nil || !errors.Is(err, session.ErrNoT0) {
		t.Fatalf("expected an ErrNoT0-based error for a negative anchor, got %v", err)
	}
	// The corrupt, +|t0|-shifted timeline must not have been written.
	if _, statErr := os.Stat(filepath.Join(dir, session.TimelineFile)); statErr == nil {
		t.Fatalf("timeline.jsonl was written despite the negative t0")
	}
}

// TestMergeTranscriptOnlyWithoutT0 pins the exemption the anchor guard must
// preserve: a transcript-only session (no interactions.jsonl) carries no
// epoch-ms times, is already session-relative, and legitimately needs no anchor,
// so it must still merge even when the manifest omits t0_epoch_ms. Routing the
// guard through man.T0 could have over-tightened Merge into demanding an anchor
// no transcript-only session has any use for; keeping the guard conditional on
// interactions being present is what this test defends. It differs from
// TestMergeAudioOnlySession, which supplies a valid t0: here t0 is absent.
func TestMergeTranscriptOnlyWithoutT0(t *testing.T) {
	dir := t.TempDir()
	// Manifest without t0_epoch_ms (left at the zero value); no interactions file.
	if err := session.SaveManifest(dir, session.Manifest{Session: "s"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	utts := []Utterance{{ID: "utt-001", T0: 1, T1: 2, Speaker: "P1", Text: "hello"}}
	if err := session.WriteJSONL(filepath.Join(dir, session.TranscriptFile), utts); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	speech, events, err := Merge(dir)
	if err != nil {
		t.Fatalf("Merge on a transcript-only session without t0: %v", err)
	}
	if speech != 1 || events != 0 {
		t.Fatalf("want speech=1 events=0, got speech=%d events=%d", speech, events)
	}
	if _, err := os.Stat(filepath.Join(dir, session.TimelineFile)); err != nil {
		t.Fatalf("timeline.jsonl not written: %v", err)
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

// TestMergeRejectsUtteranceMissingT0 is the phantom-utterance regression, the
// speech-side twin of TestMergeRejectsInteractionMissingT. A transcript.jsonl
// line with no "t0" used to decode leniently to the zero value 0, so pre-fix
// Merge counted it and BuildEntries placed it at the session's very start: the
// report printed words at 00:00 that the transcript had never timed, above
// everything that genuinely happened there. Merge must refuse the session and
// name the offending line.
func TestMergeRejectsUtteranceMissingT0(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "s", T0EpochMS: t0}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	// The second record is the malformed one: an utterance with no "t0".
	lines := "" +
		`{"id":"utt-001","t0":8.0,"t1":12.5,"speaker":"P1","text":"I'll change my display name."}` + "\n" +
		`{"id":"utt-002","t1":28.0,"speaker":"P1","text":"I clicked save and nothing happened."}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, session.TranscriptFile), []byte(lines), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	_, _, err := Merge(dir)
	if err == nil {
		t.Fatalf("expected a missing-t0 error, got nil")
	}
	if !strings.Contains(err.Error(), "utterance 2") || !strings.Contains(err.Error(), "missing t0") {
		t.Fatalf("error should name the offending line and field, got %v", err)
	}
	// The timeline carrying the phantom utterance must not have been written.
	if _, statErr := os.Stat(filepath.Join(dir, session.TimelineFile)); statErr == nil {
		t.Fatalf("timeline.jsonl was written despite the malformed utterance")
	}
}

// TestMergeRejectsUtteranceHugeT0 is the magnitude twin of the interaction-side
// range check. Utterance t0/t1 are session-relative seconds (negative is
// legitimate), so the guard bounds magnitude, not sign. A hand-edited transcript
// carrying t0=1e300 is not a session time: it overflows report's float64→int
// clock into a garbage duration and makes the utterance's EventsNear window span
// the whole session, silently stealing every event from the utterances they were
// spoken over. Merge must refuse it and name the line. Pre-fix it merged with
// exit 0.
func TestMergeRejectsUtteranceHugeT0(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "s", T0EpochMS: t0}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	lines := "" +
		`{"id":"utt-001","t0":8.0,"t1":12.5,"speaker":"P1","text":"ordinary"}` + "\n" +
		`{"id":"utt-002","t0":1e300,"t1":1e300,"speaker":"P1","text":"planted"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, session.TranscriptFile), []byte(lines), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	_, _, err := Merge(dir)
	if err == nil {
		t.Fatalf("expected an out-of-range t0 error, got nil")
	}
	if !strings.Contains(err.Error(), "utterance 2") || !strings.Contains(err.Error(), "t0") {
		t.Fatalf("error should name the offending line and field, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, session.TimelineFile)); statErr == nil {
		t.Fatalf("timeline.jsonl was written despite the out-of-range utterance")
	}
}

// TestMergeRejectsUtteranceHugeT1 covers the t1 half: a sane t0 but an
// astronomical t1 (t1 >= t0, so it passes the ordering check) must also be
// refused, since the join window's upper bound is built from t1.
func TestMergeRejectsUtteranceHugeT1(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "s", T0EpochMS: t0}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	line := `{"id":"utt-001","t0":8.0,"t1":1e300,"speaker":"P1","text":"planted end"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, session.TranscriptFile), []byte(line), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	_, _, err := Merge(dir)
	if err == nil || !strings.Contains(err.Error(), "t1") {
		t.Fatalf("expected an out-of-range t1 error, got %v", err)
	}
}

// TestMergeAcceptsUtteranceAtT0 guards the other half of the required-field
// check, mirroring TestMergeAcceptsInteractionAtT0: an utterance beginning the
// moment capture starts carries t0 0, which a value-typed decode cannot
// distinguish from an absent "t0". Alice speaking as recording begins is
// legitimate evidence and must still merge, at t 0.
func TestMergeAcceptsUtteranceAtT0(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "s", T0EpochMS: t0}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	line := `{"id":"utt-001","t0":0,"t1":3.5,"speaker":"P1","text":"Right, I'm starting now."}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, session.TranscriptFile), []byte(line), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	speech, events, err := Merge(dir)
	if err != nil {
		t.Fatalf("Merge with an utterance at t0: %v", err)
	}
	if speech != 1 || events != 0 {
		t.Fatalf("want speech=1 events=0, got speech=%d events=%d", speech, events)
	}
	entries, err := session.ReadJSONL[Entry](filepath.Join(dir, session.TimelineFile))
	if err != nil {
		t.Fatalf("read timeline: %v", err)
	}
	if len(entries) != 1 || entries[0].T != 0 || entries[0].Src != "speech" {
		t.Fatalf("want a single speech entry at t=0, got %+v", entries)
	}
}

// TestMergeDefaultsUtteranceMissingT1ToT0 pins the deliberate asymmetry between
// the two transcript times: a missing end cannot move an utterance to a moment
// it did not occur, so it is defaulted to t0 rather than refused, matching the
// fallback SpeechEnd already documents. Pre-fix a missing "t1" decoded to 0, so
// an utterance at t0 22 got the join window [22-w, 0+w] — inverted, silently
// matching no event at all. The defaulted entry must instead carry t1 equal to
// t0, giving an honest zero-length span.
func TestMergeDefaultsUtteranceMissingT1ToT0(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "s", T0EpochMS: t0}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	line := `{"id":"utt-002","t0":22.0,"speaker":"P1","text":"I clicked save and nothing happened."}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, session.TranscriptFile), []byte(line), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	if _, _, err := Merge(dir); err != nil {
		t.Fatalf("Merge with an utterance missing t1: %v", err)
	}
	entries, err := session.ReadJSONL[Entry](filepath.Join(dir, session.TimelineFile))
	if err != nil {
		t.Fatalf("read timeline: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want a single speech entry, got %+v", entries)
	}
	if got := SpeechEnd(entries[0]); got != 22.0 {
		t.Fatalf("want the end defaulted to t0 (22.0), got %v", got)
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

// TestMergeRejectsInteractionNonPositiveT is the range-check twin of
// TestMergeRejectsInteractionMissingT. t is epoch milliseconds, so a value at or
// below zero anchors the event at or before 1 January 1970 — no capture produces
// that, and at the extreme (a t near math.MinInt64) the (t - t0) subtraction in
// BuildEntries wraps on signed overflow and plants the event millions of years
// after the session start, inflating the report span while Merge still exits 0.
// checkedInteractions now refuses a non-positive t, naming the line, and writes
// no timeline. Pre-fix this line merged silently.
func TestMergeRejectsInteractionNonPositiveT(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "s", T0EpochMS: t0}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	lines := "" +
		`{"t":` + strconv.FormatInt(t0+9_500, 10) + `,"kind":"click","selector":"[data-testid=save-btn]"}` + "\n" +
		`{"t":-9223372036854775808,"kind":"click","selector":"[data-testid=tab-appearance]"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, session.InteractionsFile), []byte(lines), 0o644); err != nil {
		t.Fatalf("write interactions: %v", err)
	}
	_, _, err := Merge(dir)
	if err == nil || !strings.Contains(err.Error(), "interaction 2") {
		t.Fatalf("expected a non-positive-t error naming interaction 2, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, session.TimelineFile)); statErr == nil {
		t.Fatalf("timeline.jsonl was written despite the out-of-range interaction time")
	}
}

// TestMergeClampsUtteranceInvertedSpan covers an explicit t1 < t0 in a
// transcript.jsonl. The nil-t1 default already guards a missing end, but an
// explicit backwards end recreates the same inverted join window
// [t0-window, t1+window] (hi < lo) that silently matches no event. Pre-fix the
// value passed through unvalidated; now it falls back to t0, so the utterance's
// own interactions still attach. Alice speaks over an event that must stay joined.
func TestMergeClampsUtteranceInvertedSpan(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "s", T0EpochMS: t0}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	utt := `{"id":"utt-001","t0":22.0,"t1":0.0,"text":"I clicked save and nothing happened."}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, session.TranscriptFile), []byte(utt), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	inter := `{"t":` + strconv.FormatInt(t0+23_000, 10) + `,"kind":"click","selector":"[data-testid=save-btn]"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, session.InteractionsFile), []byte(inter), 0o644); err != nil {
		t.Fatalf("write interactions: %v", err)
	}
	if _, _, err := Merge(dir); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	entries, err := session.ReadJSONL[Entry](filepath.Join(dir, session.TimelineFile))
	if err != nil {
		t.Fatalf("read timeline: %v", err)
	}
	var speech Entry
	for _, e := range entries {
		if e.Src == "speech" {
			speech = e
		}
	}
	if got := SpeechEnd(speech); got != 22.0 {
		t.Fatalf("inverted t1 should clamp to t0 (22.0), SpeechEnd = %v", got)
	}
	// The event at 23.0s falls in [22-2.5, 22+2.5] once t1 is clamped up to t0,
	// so it attaches; with the pre-fix inverted window [19.5, 2.5] it did not.
	near := EventsNear(entries, speech, 2.5)
	if len(near) != 1 || near[0] != "ev-001" {
		t.Fatalf("want the event to attach to the utterance, got %v", near)
	}
}
