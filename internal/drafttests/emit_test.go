package drafttests

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/REPPL/Testimony/internal/session"
)

// TestEmitCarriesConfirmedFindingsAndWindows is AC1's request half: the emitted
// text is self-contained — rubric version, stance, instructions, rubric body,
// session context, the eligible finding's own record, and its event window — so
// an agent given only this text can answer.
func TestEmitCarriesConfirmedFindingsAndWindows(t *testing.T) {
	dir := writeSession(t)
	got, err := EmitRequest(dir, 10)
	if err != nil {
		t.Fatalf("EmitRequest: %v", err)
	}
	for _, want := range []string{
		"Testimony regression-test drafting rubric: testimony-testdraft/v1",
		"## Stance",
		"born `proposed`",
		"## Instructions",
		"## Rubric",
		"`rationale_quote` must **equal** the source finding's `quote`",
		"## Session",
		"- Session: fixture-session",
		"- App: testimony demo",
		"- Participant: P1",
		"1. Change your display name and save it",
		"2. Try the appearance settings",
		"## Confirmed findings",
		"Finding F-001 — bug, severity 3, at [00:22]:",
		"Event window:",
		"## Answer",
		`{"rubric":"testimony-testdraft/v1","tests":[ … ]}`,
		outputExample,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("request is missing %q:\n%s", want, got)
		}
	}
	// The finding's own JSON line, so the quote bytes the model must copy are
	// unambiguous.
	if !strings.Contains(got, `"quote":"I clicked save and nothing happened"`) {
		t.Fatalf("request does not carry F-001's own record:\n%s", got)
	}
	// Its event window, in time order: the lead-up (the display-name click and
	// the typed value) and the aftermath, not merely the anchored moment.
	for _, id := range []string{"ev-001", "ev-002", "utt-003", "ev-003", "utt-004", "ev-004", "utt-005", "ev-005", "utt-006"} {
		if !strings.Contains(got, `"id":"`+id+`"`) {
			t.Fatalf("event window is missing %s:\n%s", id, got)
		}
	}
	// Entries outside the window stay out.
	for _, id := range []string{"utt-001", "utt-002", "ev-006", "utt-007"} {
		if strings.Contains(got, `"id":"`+id+`"`) {
			t.Fatalf("entry %s is outside the window but appears in the request:\n%s", id, got)
		}
	}
}

// TestEmitOmitsUnverifiedRejectedAndDuplicateFindings is AC2's emit half: a
// finding that is unverified, rejected, a duplicate, or mode B never reaches the
// model at all.
func TestEmitOmitsUnverifiedRejectedAndDuplicateFindings(t *testing.T) {
	dir := writeSession(t)
	got, err := EmitRequest(dir, 10)
	if err != nil {
		t.Fatalf("EmitRequest: %v", err)
	}
	for _, id := range []string{"F-002", "F-003", "F-004", "F-005"} {
		if strings.Contains(got, id) {
			t.Fatalf("non-eligible finding %s appears in the request:\n%s", id, got)
		}
	}
	if strings.Count(got, "Finding F-001") != 1 {
		t.Fatalf("expected exactly one per-finding block, got:\n%s", got)
	}
}

// TestEmitWindowFlagWidensAndNarrows pins the -window flag's effect on the
// request itself: at 2.5 seconds utt-003 — the one utterance stating the expected
// behaviour — falls outside the window, which is the worked justification for the
// 10-second default.
func TestEmitWindowFlagWidensAndNarrows(t *testing.T) {
	dir := writeSession(t)
	narrow, err := EmitRequest(dir, 2.5)
	if err != nil {
		t.Fatalf("EmitRequest: %v", err)
	}
	if strings.Contains(narrow, `"id":"utt-003"`) {
		t.Fatalf("utt-003 is outside the 2.5 s window but appears:\n%s", narrow)
	}
	wide, err := EmitRequest(dir, 10)
	if err != nil {
		t.Fatalf("EmitRequest: %v", err)
	}
	if !strings.Contains(wide, `"id":"utt-003"`) {
		t.Fatal("utt-003 is inside the 10 s window but does not appear")
	}
}

func TestEmitRequestIsDeterministic(t *testing.T) {
	dir := writeSession(t)
	first, err := EmitRequest(dir, 10)
	if err != nil {
		t.Fatalf("EmitRequest: %v", err)
	}
	for i := 0; i < 3; i++ {
		again, err := EmitRequest(dir, 10)
		if err != nil {
			t.Fatalf("EmitRequest: %v", err)
		}
		if again != first {
			t.Fatal("EmitRequest is not deterministic across runs")
		}
	}
}

// TestEmitRefusesWithNoConfirmedFindings is the loud-staging refusal: a session
// whose findings are all unverified, rejected, or duplicates names the counts by
// status, points at `review`, and emits nothing.
func TestEmitRefusesWithNoConfirmedFindings(t *testing.T) {
	findings := `{"id":"F-001","t":22,"type":"bug","severity":3,"quote":"q","evidence":["utt-004"],"status":"unverified"}
{"id":"F-002","t":22,"type":"bug","severity":3,"quote":"q","evidence":["utt-004"],"status":"unverified"}
{"id":"F-003","t":22,"type":"bug","severity":3,"quote":"q","evidence":["utt-004"],"status":"unverified"}
{"id":"F-004","t":22,"type":"bug","severity":3,"quote":"q","evidence":["utt-004"],"status":"unverified"}
{"id":"F-005","t":22,"type":"bug","severity":3,"quote":"q","evidence":["utt-004"],"status":"unverified"}
{"kind":"verdict","finding":"F-002","verdict":"rejected","at":"2026-09-12"}
{"kind":"verdict","finding":"F-003","verdict":"rejected","at":"2026-09-12"}
{"kind":"verdict","finding":"F-004","verdict":"duplicate","of":"F-001","at":"2026-09-12"}
`
	dir := writeSession(t, session.FindingsFile, findings)
	_, err := EmitRequest(dir, 10)
	if err == nil {
		t.Fatal("EmitRequest on a session with no confirmed finding: want a refusal")
	}
	if !errors.Is(err, ErrNoConfirmedFindings) {
		t.Fatalf("refusal does not wrap ErrNoConfirmedFindings: %v", err)
	}
	want := "no confirmed findings to draft tests from (5 findings: 0 confirmed, 2 unverified, 1 duplicate, 2 rejected); confirm one with `testimony review -session " + dir + "` first"
	if err.Error() != want {
		t.Fatalf("refusal message:\n got %q\nwant %q", err.Error(), want)
	}
}

// TestEmitRefusalPrecedesTheTimelineRead: eligibility is the whole point of the
// step, so a session with nothing to draft from hears why it is empty rather than
// being sent to run merge for a request it could not fill either way.
func TestEmitRefusalPrecedesTheTimelineRead(t *testing.T) {
	findings := `{"id":"F-001","t":22,"type":"bug","severity":3,"quote":"q","evidence":["utt-004"],"status":"unverified"}` + "\n"
	dir := writeSession(t, session.FindingsFile, findings, session.TimelineFile, "")
	_, err := EmitRequest(dir, 10)
	if !errors.Is(err, ErrNoConfirmedFindings) {
		t.Fatalf("want the eligibility refusal ahead of the merge hint, got %v", err)
	}
}

func TestEmitHintsMergeWhenTimelineMissing(t *testing.T) {
	dir := writeSession(t, session.TimelineFile, "")
	_, err := EmitRequest(dir, 10)
	if err == nil || !strings.Contains(err.Error(), "testimony merge") {
		t.Fatalf("want a merge hint, got %v", err)
	}
}

func TestEmitHintsIngestWhenFindingsMissing(t *testing.T) {
	dir := writeSession(t, session.FindingsFile, "")
	_, err := EmitRequest(dir, 10)
	if err == nil || !strings.Contains(err.Error(), "testimony analyze -ingest") {
		t.Fatalf("want an analyze -ingest hint, got %v", err)
	}
}

// TestEmitMutatesNothing: emit is a read; the session directory is untouched.
func TestEmitMutatesNothing(t *testing.T) {
	dir := writeSession(t)
	before := dirDigest(t, dir)
	if _, err := EmitRequest(dir, 10); err != nil {
		t.Fatalf("EmitRequest: %v", err)
	}
	if after := dirDigest(t, dir); after != before {
		t.Fatalf("emit mutated the session directory:\nbefore %s\nafter  %s", before, after)
	}
}

func dirDigest(t *testing.T, dir string) string {
	t.Helper()
	names, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var parts []string
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, n.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", n.Name(), err)
		}
		parts = append(parts, n.Name()+":"+string(b))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

// TestEmitEscapesInlineMarkdownInManifestFields is the beacon regression that the
// analysis request already carries: the manifest is attacker-authorable and these
// fields render as list items outside any code fence, so an unescaped
// `[x](http://…)` or image form would survive into a saved request.md as an active
// link or a tracking beacon the moment it is previewed.
func TestEmitEscapesInlineMarkdownInManifestFields(t *testing.T) {
	man := `{"session":"fixture-session","app":"[x](http://example.test/beacon.png)","participant":"P1","t0_epoch_ms":1784300400000,"tasks":["![t](http://example.test/t.png)"]}`
	dir := writeSession(t, session.ManifestFile, man)
	got, err := EmitRequest(dir, 10)
	if err != nil {
		t.Fatalf("EmitRequest: %v", err)
	}
	if strings.Contains(got, "- App: [x](http://example.test/beacon.png)") {
		t.Fatalf("manifest app survived as an active link:\n%s", got)
	}
	if !strings.Contains(got, `\[x\]\(http://example.test/beacon.png\)`) {
		t.Fatalf("manifest app is not backslash-escaped:\n%s", got)
	}
	if !strings.Contains(got, `\!\[t\]\(http://example.test/t.png\)`) {
		t.Fatalf("manifest task is not backslash-escaped:\n%s", got)
	}
}

// TestEmitSanitisesTimelineBidi is the Trojan-Source regression for the window
// block. json.Marshal escapes the C0 controls and ESC but passes the Unicode
// Bidi_Control set through as raw bytes, so a right-to-left override in an
// exchanged session's transcript would reorder the displayed window — the exact
// spoofing SafeText strips on every other path.
func TestEmitSanitisesTimelineBidi(t *testing.T) {
	tl := "{\"t\":22,\"src\":\"speech\",\"id\":\"utt-004\",\"payload\":{\"speaker\":\"P1\",\"t1\":28,\"text\":\"benign \u202egnihtemos evil\u202c end\"}}\n" +
		"{\"t\":19.2,\"src\":\"event\",\"id\":\"ev-003\",\"payload\":{\"kind\":\"click\"}}\n" +
		"{\"t\":24.1,\"src\":\"event\",\"id\":\"ev-004\",\"payload\":{\"kind\":\"click\"}}\n"
	dir := writeSession(t, session.TimelineFile, tl)
	got, err := EmitRequest(dir, 10)
	if err != nil {
		t.Fatalf("EmitRequest: %v", err)
	}
	if strings.ContainsRune(got, 0x202e) || strings.ContainsRune(got, 0x202c) {
		t.Fatalf("request carries raw Bidi_Control bytes from the timeline:\n%q", got)
	}
	if !strings.Contains(got, "benign gnihtemos evil end") {
		t.Fatalf("sanitised window lost the utterance text:\n%s", got)
	}
}

// TestEmitPlaceholdersInvisibleOnlyManifestFields: a manifest field that is
// non-empty raw but strips to nothing under SafeText must print the "(none)"
// placeholder rather than a blank field, and a blank task must not be numbered.
func TestEmitPlaceholdersInvisibleOnlyManifestFields(t *testing.T) {
	man := "{\"session\":\"fixture-session\",\"app\":\"\u200b\",\"participant\":\"  \",\"t0_epoch_ms\":1784300400000,\"tasks\":[\"\u200b\",\"Real task\"]}"
	dir := writeSession(t, session.ManifestFile, man)
	got, err := EmitRequest(dir, 10)
	if err != nil {
		t.Fatalf("EmitRequest: %v", err)
	}
	if !strings.Contains(got, "- App: (none)") || !strings.Contains(got, "- Participant: (none)") {
		t.Fatalf("invisible-only manifest fields did not fall back to the placeholder:\n%s", got)
	}
	if !strings.Contains(got, "  1. Real task\n") || strings.Contains(got, "  2. ") {
		t.Fatalf("a blank task was numbered:\n%s", got)
	}
}
