package drafttests

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/REPPL/Testimony/internal/analyze"
	"github.com/REPPL/Testimony/internal/session"
	"github.com/REPPL/Testimony/internal/timeline"
)

// fixture reads one testdata file. The fixtures are a session mirroring the
// bundled example: F-001 confirmed (the one eligible finding), F-002 unverified,
// F-003 rejected, F-004 confirmed but mode B, F-005 a duplicate of F-001.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// writeSession lays a session directory from the testdata fixtures. Any of the
// three artefacts can be replaced by passing its name and contents; empty
// contents omits the artefact, so the missing-file hints can be exercised.
func writeSession(t *testing.T, overrides ...string) string {
	t.Helper()
	if len(overrides)%2 != 0 {
		t.Fatalf("writeSession: overrides must be name/contents pairs")
	}
	files := map[string][]byte{
		session.ManifestFile: fixture(t, "manifest.json"),
		session.FindingsFile: fixture(t, "findings.jsonl"),
		session.TimelineFile: fixture(t, "timeline.jsonl"),
	}
	for i := 0; i < len(overrides); i += 2 {
		if overrides[i+1] == "" {
			delete(files, overrides[i])
			continue
		}
		files[overrides[i]] = []byte(overrides[i+1])
	}
	dir := t.TempDir()
	for name, body := range files {
		if body == nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func readEntries(t *testing.T) []timeline.Entry {
	t.Helper()
	dir := writeSession(t)
	entries, err := analyze.LoadTimeline(dir)
	if err != nil {
		t.Fatalf("LoadTimeline: %v", err)
	}
	return entries
}

func loadFixtureFindings(t *testing.T) ([]analyze.Finding, []analyze.Verdict) {
	t.Helper()
	_, findings, verdicts, err := analyze.Load(writeSession(t))
	if err != nil {
		t.Fatalf("analyze.Load: %v", err)
	}
	return findings, verdicts
}

func findingID(t *testing.T, findings []analyze.Finding, id string) analyze.Finding {
	t.Helper()
	for _, f := range findings {
		if f.ID == id {
			return f
		}
	}
	t.Fatalf("fixture has no finding %s", id)
	return analyze.Finding{}
}

func entryIDs(entries []timeline.Entry) []string {
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	return ids
}

func draft(id string) Draft {
	return Draft{
		ID: id, Finding: "F-001", Session: "fixture-session",
		Title:          "Saving gives no confirmation",
		Steps:          []string{"Open #general.", "Click Save."},
		Expected:       "The save is confirmed.",
		Observed:       "Nothing happens.",
		RationaleQuote: "I clicked save and nothing happened",
		Severity:       3, Status: "proposed",
	}
}

func ptr(s string) *string { return &s }

// --- eligibility -----------------------------------------------------------

// TestEligibilityHonoursLastVerdict is AC2's load-side half. Effective status
// comes from analyze.EffectiveStatus, so a later verdict overriding an earlier
// one is honoured for free: confirmed-then-rejected is not eligible, and
// rejected-then-confirmed is. A duplicate of a confirmed finding is never
// eligible — the canonical finding carries the evidence — and a mode B
// (reference-capture) finding is excluded as a guard.
func TestEligibilityHonoursLastVerdict(t *testing.T) {
	findings, verdicts := loadFixtureFindings(t)
	got := map[string]bool{}
	for _, f := range eligible(findings, verdicts) {
		got[f.ID] = true
	}
	if !got["F-001"] {
		t.Fatalf("F-001 (confirmed, mode A) is not eligible: %v", got)
	}
	for _, id := range []string{"F-002", "F-003", "F-005"} {
		if got[id] {
			t.Fatalf("%s is eligible but is not confirmed", id)
		}
	}
	if got["F-004"] {
		t.Fatal("F-004 is eligible but is a mode B (reference-capture) finding")
	}

	// confirmed-then-rejected drops out; rejected-then-confirmed comes back in.
	later := append(verdicts,
		analyze.Verdict{Kind: "verdict", Finding: "F-001", Verdict: "rejected", At: "2026-09-13"},
		analyze.Verdict{Kind: "verdict", Finding: "F-003", Verdict: "confirmed", At: "2026-09-13"},
	)
	got = map[string]bool{}
	for _, f := range eligible(findings, later) {
		got[f.ID] = true
	}
	if got["F-001"] {
		t.Fatal("F-001 stayed eligible after a later rejected verdict")
	}
	if !got["F-003"] {
		t.Fatal("F-003 did not become eligible after a later confirmed verdict")
	}
}

func TestFindingCountsNamesEveryStatus(t *testing.T) {
	findings, verdicts := loadFixtureFindings(t)
	want := "5 findings: 2 confirmed, 1 unverified, 1 duplicate, 1 rejected"
	if got := findingCounts(findings, verdicts); got != want {
		t.Fatalf("findingCounts = %q, want %q", got, want)
	}
}

// --- the event window ------------------------------------------------------

// TestWindowSpansEvidenceWidenedByWindow is the worked example from the spec:
// F-001 cites utt-004 (22–28 s), ev-003 (19.2 s) and ev-004 (24.1 s), so the
// 10-second window is [9.2, 38.0] and holds the whole repro — click the
// display-name field, type Alice, click save, click save again — plus the
// utterances that frame it. The upper bound comes from the utterance's end
// (SpeechEnd), not its start.
func TestWindowSpansEvidenceWidenedByWindow(t *testing.T) {
	entries := readEntries(t)
	findings, _ := loadFixtureFindings(t)
	f := findingID(t, findings, "F-001")

	got := entryIDs(Window(entries, f, 10))
	want := []string{"ev-001", "ev-002", "utt-003", "ev-003", "utt-004", "ev-004", "utt-005", "ev-005", "utt-006"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Window(10) = %v, want %v", got, want)
	}
	// utt-006 sits exactly on the upper bound (38.0): the bounds are inclusive.
	if got[len(got)-1] != "utt-006" {
		t.Fatalf("the entry on the upper bound was dropped: %v", got)
	}
	// utt-002 starts at 8, before the lower bound of 9.2.
	for _, id := range got {
		if id == "utt-002" {
			t.Fatalf("an entry before the lower bound was included: %v", got)
		}
	}
}

// TestWindowNarrowsAndWidens shows why the default is 10 seconds rather than
// report's 2.5: at 2.5 the window is [16.7, 30.5] and excludes utt-003 — the one
// utterance in the session that states the expected behaviour — so a repro drafted
// from it would have nothing to ground "expected" in. A negative window is
// legitimate and narrows further, to nothing.
func TestWindowNarrowsAndWidens(t *testing.T) {
	entries := readEntries(t)
	findings, _ := loadFixtureFindings(t)
	f := findingID(t, findings, "F-001")

	narrow := entryIDs(Window(entries, f, 2.5))
	want := []string{"ev-003", "utt-004", "ev-004"}
	if strings.Join(narrow, ",") != strings.Join(want, ",") {
		t.Fatalf("Window(2.5) = %v, want %v", narrow, want)
	}
	wide := Window(entries, f, 60)
	if len(wide) != len(entries) {
		t.Fatalf("Window(60) returned %d of %d entries; a wide window holds the whole timeline", len(wide), len(entries))
	}
	if got := Window(entries, f, -10); len(got) != 0 {
		t.Fatalf("Window(-10) = %v, want an empty (over-narrowed) window", entryIDs(got))
	}
}

// TestWindowUsesEventOnlyEvidence covers a finding whose evidence resolves only
// to events: the span is still taken from the cited entries, not from f.T.
func TestWindowUsesEventOnlyEvidence(t *testing.T) {
	entries := readEntries(t)
	f := analyze.Finding{ID: "F-900", T: 0, Evidence: []string{"ev-003"}}
	got := entryIDs(Window(entries, f, 1))
	want := []string{"ev-003"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Window over event-only evidence = %v, want %v", got, want)
	}
}

// TestWindowFallsBackToFindingTime covers a finding whose evidence resolves to no
// entry at all — impossible after analyze -ingest, reachable via a hand-edited
// findings.jsonl. The window falls back to f.T ± window so the finding still
// arrives with context rather than with nothing.
func TestWindowFallsBackToFindingTime(t *testing.T) {
	entries := readEntries(t)
	f := analyze.Finding{ID: "F-900", T: 22, Evidence: []string{"utt-999"}}
	got := entryIDs(Window(entries, f, 2))
	want := []string{"utt-004"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("fallback window = %v, want %v", got, want)
	}
}

// TestWindowOrdersByTime pins the ordering promise: the window is presented to
// the model as the sequence to reconstruct steps from, so an out-of-order
// timeline.jsonl (which reaches these readers directly when a session is
// hand-edited or exchanged) must not hand it a repro in the wrong order.
func TestWindowOrdersByTime(t *testing.T) {
	entries := []timeline.Entry{
		{T: 24.1, Src: "event", ID: "ev-004", Payload: map[string]any{"kind": "click"}},
		{T: 19.2, Src: "event", ID: "ev-003", Payload: map[string]any{"kind": "click"}},
		{T: 22, Src: "speech", ID: "utt-004", Payload: map[string]any{"t1": 28.0, "text": "x"}},
	}
	f := analyze.Finding{ID: "F-001", T: 22, Evidence: []string{"utt-004"}}
	got := entryIDs(Window(entries, f, 10))
	want := []string{"ev-003", "utt-004", "ev-004"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Window over an out-of-order timeline = %v, want %v", got, want)
	}
}

// TestWindowMatchesEvidenceInSafeTextForm covers the form the answering agent is
// actually shown: EmitRequest routes each marshalled entry through SafeText, so
// an evidence id carrying a stripped byte must resolve here exactly as it does in
// analyze's own validation.
func TestWindowMatchesEvidenceInSafeTextForm(t *testing.T) {
	entries := readEntries(t)
	f := analyze.Finding{ID: "F-900", T: 0, Evidence: []string{"ev-​003"}} // zero-width space
	if got := entryIDs(Window(entries, f, 0)); strings.Join(got, ",") != "ev-003" {
		t.Fatalf("Window did not match the evidence id in SafeText form: %v", got)
	}
}

// --- records and effective status ------------------------------------------

func TestParseRecordsSplitsDraftsAndDecisions(t *testing.T) {
	in := `{"id":"T-001","finding":"F-001","session":"s","title":"t","steps":["a"],"expected":"e","observed":"o","rationale_quote":"q","severity":3,"status":"proposed"}

{"kind":"decision","test":"T-001","decision":"accepted","at":"2026-09-12"}
`
	drafts, decisions, err := ParseRecords(strings.NewReader(in), session.TestsFile)
	if err != nil {
		t.Fatalf("ParseRecords: %v", err)
	}
	if len(drafts) != 1 || len(decisions) != 1 {
		t.Fatalf("got %d drafts and %d decisions, want 1 and 1", len(drafts), len(decisions))
	}
}

// TestParseRecordsIgnoresOutOfEnumDecision mirrors analyze.ParseRecords: a
// decision value outside the closed enum is not representable, so it is ignored
// rather than applied — the draft keeps "proposed" and stays in the review queue
// instead of vanishing into a status group nothing renders.
func TestParseRecordsIgnoresOutOfEnumDecision(t *testing.T) {
	in := `{"kind":"decision","test":"T-001","decision":"maybe","at":"2026-09-12"}` + "\n"
	_, decisions, err := ParseRecords(strings.NewReader(in), session.TestsFile)
	if err != nil {
		t.Fatalf("ParseRecords: %v", err)
	}
	if len(decisions) != 0 {
		t.Fatalf("an out-of-enum decision was applied: %+v", decisions)
	}
}

func TestParseRecordsRefusesMalformedDraftLines(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"null line", "null\n", "not a test draft or decision record"},
		{"no severity", `{"id":"T-001"}` + "\n", "not a test draft or decision record"},
		{"no id", `{"severity":3}` + "\n", "has no id"},
		{"whitespace id", `{"id":"  ","severity":3}` + "\n", "has no id"},
		{"duplicate id", `{"id":"T-001","severity":3}` + "\n" + `{"id":"T-001","severity":3}` + "\n", "duplicate test draft id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ParseRecords(strings.NewReader(tc.in), session.TestsFile)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestParseRecordsRefusesDuplicateIDBySafeText: two ids distinct only by stripped
// bytes render identically on every surface, so they count as the collision they
// display as.
func TestParseRecordsRefusesDuplicateIDBySafeText(t *testing.T) {
	in := `{"id":"T-001","severity":3}` + "\n" + `{"id":"T-​001","severity":3}` + "\n"
	_, _, err := ParseRecords(strings.NewReader(in), session.TestsFile)
	if err == nil || !strings.Contains(err.Error(), "duplicate test draft id") {
		t.Fatalf("got %v, want a duplicate-id refusal", err)
	}
}

// TestEffectiveStatusLastDecisionWins is AC3's retention half: decisions apply in
// file order, the last one for a draft wins, and one naming an unknown draft is
// ignored for display.
func TestEffectiveStatusLastDecisionWins(t *testing.T) {
	drafts := []Draft{draft("T-001"), draft("T-002")}
	decisions := []Decision{
		{Kind: "decision", Test: "T-001", Decision: "accepted", At: "2026-09-12"},
		{Kind: "decision", Test: "T-001", Decision: "rejected", At: "2026-09-13"},
		{Kind: "decision", Test: "T-404", Decision: "accepted", At: "2026-09-13"},
	}
	eff := EffectiveStatus(drafts, decisions)
	if eff["T-001"].Value != "rejected" || eff["T-001"].At != "2026-09-13" {
		t.Fatalf("T-001 status %+v, want rejected on 2026-09-13", eff["T-001"])
	}
	if eff["T-002"].Value != "proposed" {
		t.Fatalf("T-002 status %+v, want proposed", eff["T-002"])
	}
	if _, ok := eff["T-404"]; ok {
		t.Fatal("a decision naming an unknown draft created a status entry")
	}
}

func TestEffectiveStatusOnlyCarriesEditForEdited(t *testing.T) {
	drafts := []Draft{draft("T-001")}
	edit := &Edit{Title: ptr("new title")}
	eff := EffectiveStatus(drafts, []Decision{
		{Kind: "decision", Test: "T-001", Decision: "edited", At: "2026-09-12", Edit: edit},
		{Kind: "decision", Test: "T-001", Decision: "accepted", At: "2026-09-13", Edit: edit},
	})
	if eff["T-001"].Edit != nil {
		t.Fatalf("an accepted decision carried an edit into the status: %+v", eff["T-001"])
	}
}

func TestSameIdentityIgnoresStatusOnly(t *testing.T) {
	a, b := draft("T-001"), draft("T-001")
	b.Status = "accepted"
	if !SameIdentity(a, b) {
		t.Fatal("SameIdentity treated a status change as a different draft")
	}
	b.Finding = "F-002"
	if SameIdentity(a, b) {
		t.Fatal("SameIdentity treated a re-pointed finding as the same draft")
	}
}

// TestClockRendersNegativeSessionRelativeTimes is the clamped-clock regression,
// the sibling of the ones fixed in internal/report and internal/review. A
// recording whose creation_time predates the manifest t0 yields a negative
// offset, and analyze.indexTimeline admits findings anchored there, so the sign
// must be rendered rather than clamped away — and a non-finite or absurd value
// must render a visibly-broken placeholder rather than a precise-looking wrong
// stamp.
func TestClockRendersNegativeSessionRelativeTimes(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{22, "00:22"},
		{-90, "-01:30"},
		{-0.2, "00:00"},
		{1e12, "--:--"},
	} {
		if got := clock(tc.in); got != tc.want {
			t.Fatalf("clock(%g) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestDraftTestsIgnoresProvenanceRecord pins the itd-8 non-goal: the analysis
// provenance is not carried into the drafting request and not written into
// tests.jsonl. Which backend coded a finding changes no instruction in a request
// that asks for reproduction steps, so every mode must produce byte-identical
// output over a findings.jsonl that carries the record and one that does not.
func TestDraftTestsIgnoresProvenanceRecord(t *testing.T) {
	const provLine = `{"kind":"provenance","rubric":"testimony-analysis/v1","backend":"local","model":"llama3.1:70b","at":"2026-09-15"}`

	plain := writeSession(t)
	withProv := writeSession(t, session.FindingsFile, provLine+"\n"+string(fixture(t, "findings.jsonl")))

	reqA, err := EmitRequest(plain, 10)
	if err != nil {
		t.Fatalf("EmitRequest (plain): %v", err)
	}
	reqB, err := EmitRequest(withProv, 10)
	if err != nil {
		t.Fatalf("EmitRequest (with provenance): %v", err)
	}
	if reqA != reqB {
		t.Fatalf("the drafting request differs with a provenance record present")
	}
	if strings.Contains(reqB, "llama3.1:70b") || strings.Contains(reqB, "provenance") {
		t.Fatalf("the drafting request leaked the analysis provenance:\n%s", reqB)
	}

	for _, dir := range []string{plain, withProv} {
		if _, err := Ingest(dir, strings.NewReader(string(fixture(t, "answer.json")))); err != nil {
			t.Fatalf("Ingest: %v", err)
		}
	}
	a, err := os.ReadFile(filepath.Join(plain, session.TestsFile))
	if err != nil {
		t.Fatalf("read tests (plain): %v", err)
	}
	b, err := os.ReadFile(filepath.Join(withProv, session.TestsFile))
	if err != nil {
		t.Fatalf("read tests (with provenance): %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("tests.jsonl differs with a provenance record present:\n%s\n%s", a, b)
	}
	if bytes.Contains(b, []byte("provenance")) {
		t.Fatalf("tests.jsonl gained a provenance record: %s", b)
	}
}
