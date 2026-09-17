package coderefs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/REPPL/Testimony/internal/analyze"
	"github.com/REPPL/Testimony/internal/session"
)

// repoPath is the fixture repository the sample session's references point
// into. It is relative to the package directory, which is where go test runs.
const repoPath = "testdata/repo"

// fixture reads one testdata file. The fixture session mirrors the bundled
// example: F-001 confirmed with a selector and route (eligible), F-002
// unverified with an anchor, F-003 rejected, F-004 confirmed but mode B, F-005 a
// duplicate of F-001, F-006 confirmed with no ui at all (anchorless), and F-007
// confirmed with a route only (eligible).
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// writeSession lays a session directory from the testdata fixtures. Any
// artefact can be replaced by passing its name and contents; empty contents
// omits the artefact, so the missing-file hints can be exercised.
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
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func loadFixtureFindings(t *testing.T) ([]analyze.Finding, []analyze.Verdict) {
	t.Helper()
	_, findings, verdicts, err := analyze.Load(writeSession(t))
	if err != nil {
		t.Fatalf("analyze.Load: %v", err)
	}
	return findings, verdicts
}

func ref(id string) Ref {
	return Ref{
		ID: id, Finding: "F-001", Session: "fixture-session",
		Path: "src/settings/ProfileForm.tsx", Line: 46, Role: "owner", Status: "proposed",
	}
}

// --- eligibility -----------------------------------------------------------

// TestEligibilityRequiresConfirmedAndAnchored is AC1's load-side half: a finding
// is mappable iff its effective status is confirmed, its mode is not B, and its
// ui carries a selector or route. F-006 is confirmed and still excluded because
// there is nothing to hand over.
func TestEligibilityRequiresConfirmedAndAnchored(t *testing.T) {
	findings, verdicts := loadFixtureFindings(t)
	got := eligible(findings, verdicts)
	var ids []string
	for _, f := range got {
		ids = append(ids, f.ID)
	}
	if want := "F-001 F-007"; strings.Join(ids, " ") != want {
		t.Fatalf("eligible = %v, want %s", ids, want)
	}
}

// TestEligibilityHonoursLastVerdict: effective status comes from
// analyze.EffectiveStatus, so a later verdict overriding an earlier one is
// honoured for free.
func TestEligibilityHonoursLastVerdict(t *testing.T) {
	findings, verdicts := loadFixtureFindings(t)
	verdicts = append(verdicts,
		analyze.Verdict{Kind: "verdict", Finding: "F-001", Verdict: "rejected", At: "2026-09-13"},
		analyze.Verdict{Kind: "verdict", Finding: "F-002", Verdict: "confirmed", At: "2026-09-13"},
	)
	got := eligible(findings, verdicts)
	var ids []string
	for _, f := range got {
		ids = append(ids, f.ID)
	}
	if want := "F-002 F-007"; strings.Join(ids, " ") != want {
		t.Fatalf("eligible after later verdicts = %v, want %s", ids, want)
	}
}

// TestHasAnchorDecidesOnRenderedForm: a selector that is non-empty raw but
// strips to nothing under SafeText is not an anchor the host would ever see.
func TestHasAnchorDecidesOnRenderedForm(t *testing.T) {
	f := analyze.Finding{UI: &analyze.UI{Selector: "\u200b\u200b"}}
	if hasAnchor(f) {
		t.Fatal("an invisible-only selector counts as an anchor")
	}
	f.UI.Route = "#general"
	if !hasAnchor(f) {
		t.Fatal("a route alone is an anchor")
	}
	if hasAnchor(analyze.Finding{}) {
		t.Fatal("a finding with no ui has an anchor")
	}
}

// TestFindingCountsNamesAnchoredCount pins the refusal tally's shape, including
// the anchored count that tells "no confirmed finding" from "confirmed findings
// with no anchor".
func TestFindingCountsNamesAnchoredCount(t *testing.T) {
	findings, verdicts := loadFixtureFindings(t)
	got := findingCounts(findings, verdicts)
	want := "7 findings: 4 confirmed (2 with a selector or route), 1 unverified, 1 duplicate, 1 rejected"
	if got != want {
		t.Fatalf("findingCounts = %q, want %q", got, want)
	}
}

// --- records ---------------------------------------------------------------

func TestParseRecordsSplitsRefsAndDecisions(t *testing.T) {
	in := `{"id":"R-001","finding":"F-001","session":"s","path":"a.ts","line":1,"role":"owner","status":"proposed"}` + "\n" +
		"\n" +
		`{"kind":"decision","ref":"R-001","decision":"accepted","at":"2026-09-16"}` + "\n"
	refs, decisions, err := ParseRecords(strings.NewReader(in), "refs.jsonl")
	if err != nil {
		t.Fatalf("ParseRecords: %v", err)
	}
	if len(refs) != 1 || len(decisions) != 1 {
		t.Fatalf("got %d refs and %d decisions, want 1 and 1", len(refs), len(decisions))
	}
	if refs[0].Line != 1 || refs[0].Role != "owner" {
		t.Fatalf("ref = %+v", refs[0])
	}
}

func TestParseRecordsIgnoresOutOfEnumDecision(t *testing.T) {
	in := `{"id":"R-001","finding":"F-001","session":"s","path":"a.ts","role":"owner","status":"proposed"}` + "\n" +
		`{"kind":"decision","ref":"R-001","decision":"edited","at":"2026-09-16"}` + "\n"
	refs, decisions, err := ParseRecords(strings.NewReader(in), "refs.jsonl")
	if err != nil {
		t.Fatalf("ParseRecords: %v", err)
	}
	if len(decisions) != 0 {
		t.Fatalf("an out-of-enum decision was applied: %+v", decisions)
	}
	if st := EffectiveStatus(refs, decisions)["R-001"].Value; st != "proposed" {
		t.Fatalf("status = %q, want proposed", st)
	}
}

func TestParseRecordsRefusesMalformedLines(t *testing.T) {
	for name, in := range map[string]string{
		"null line":    "null\n",
		"empty object": "{}\n",
		"no id":        `{"path":"a.ts","role":"owner"}` + "\n",
		"duplicate id": `{"id":"R-001","path":"a.ts"}` + "\n" + `{"id":"R-001","path":"b.ts"}` + "\n",
		"not json":     "nope\n",
	} {
		if _, _, err := ParseRecords(strings.NewReader(in), "refs.jsonl"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestEffectiveStatusLastDecisionWins(t *testing.T) {
	refs := []Ref{ref("R-001"), ref("R-002")}
	decisions := []Decision{
		{Kind: "decision", Ref: "R-001", Decision: "rejected", At: "2026-09-15"},
		{Kind: "decision", Ref: "R-001", Decision: "accepted", At: "2026-09-16"},
		{Kind: "decision", Ref: "R-999", Decision: "accepted", At: "2026-09-16"},
	}
	eff := EffectiveStatus(refs, decisions)
	if eff["R-001"].Value != "accepted" || eff["R-001"].At != "2026-09-16" {
		t.Fatalf("R-001 = %+v", eff["R-001"])
	}
	if eff["R-002"].Value != "proposed" {
		t.Fatalf("R-002 = %+v", eff["R-002"])
	}
	if _, ok := eff["R-999"]; ok {
		t.Fatal("a decision on an unknown reference created a status entry")
	}
}

func TestSameIdentityIgnoresStatusOnly(t *testing.T) {
	a, b := ref("R-001"), ref("R-001")
	b.Status = "accepted"
	if !SameIdentity(a, b) {
		t.Fatal("status alone breaks identity")
	}
	b.Line = 47
	if SameIdentity(a, b) {
		t.Fatal("a different line keeps identity")
	}
}

func TestIDAndDecisionFlags(t *testing.T) {
	if !IsRefID("R-001") || IsRefID("R-1") || IsRefID("T-001") {
		t.Fatal("IsRefID")
	}
	if _, err := ParseDecisionFlag("edited"); err == nil {
		t.Fatal("edited is not a reference decision")
	}
	if d, err := ParseDecisionFlag("rejected"); err != nil || d != "rejected" {
		t.Fatalf("rejected: %q, %v", d, err)
	}
}

func TestClockRendersNegativeAndBounded(t *testing.T) {
	for sec, want := range map[float64]string{22: "00:22", -61: "-01:01", -0.2: "00:00", 1e12: "--:--"} {
		if got := clock(sec); got != want {
			t.Errorf("clock(%v) = %q, want %q", sec, got, want)
		}
	}
}
