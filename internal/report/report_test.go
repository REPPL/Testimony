package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/REPPL/Testimony/internal/analyze"
	"github.com/REPPL/Testimony/internal/review"
	"github.com/REPPL/Testimony/internal/session"
)

const timelineFixture = `{"t":22,"src":"speech","id":"utt-004","payload":{"speaker":"P1","t1":28,"text":"Hm. I clicked save and nothing happened. No message."}}
{"t":19.2,"src":"event","id":"ev-003","payload":{"kind":"click","route":"#general","selector":"[data-testid=save-btn]","text":"Save"}}
{"t":38,"src":"speech","id":"utt-006","payload":{"speaker":"P1","t1":45,"text":"Oh, I like this dark mode toggle. This is how the save button should feel."}}
`

const answerFixture = `{"rubric":"testimony-analysis/v1","findings":[
 {"id":"F-001","t":22,"type":"bug","severity":3,"quote":"I clicked save and nothing happened","evidence":["utt-004","ev-003"],"ui":{"selector":"[data-testid=save-btn]","route":"#general"}},
 {"id":"F-002","t":38,"type":"preference","severity":2,"quote":"I like this dark mode toggle","evidence":["utt-006"]}
]}`

func setupSession(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "fixture", App: "settings prototype", Participant: "P1"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(timelineFixture), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}
	return dir
}

// TestRoundTrip exercises the golden path: ingest → review (two verdicts) →
// report, asserting the append-only property and every status group.
func TestRoundTrip(t *testing.T) {
	dir := setupSession(t)

	if _, err := analyze.Ingest(dir, strings.NewReader(answerFixture)); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	findingsBefore := findingLines(t, dir)

	// Two verdicts non-interactively: confirm F-001, mark F-002 a duplicate.
	if err := review.Run(review.Options{Dir: dir, Finding: "F-001", Verdict: "confirmed", Out: &discard{}, Today: "2026-07-17"}); err != nil {
		t.Fatalf("review confirm: %v", err)
	}
	if err := review.Run(review.Options{Dir: dir, Finding: "F-002", Verdict: "duplicate-of-F-001", Out: &discard{}, Today: "2026-07-17"}); err != nil {
		t.Fatalf("review duplicate: %v", err)
	}

	// Append-only: the original finding lines are byte-unchanged.
	if before, after := strings.Join(findingsBefore, "\n"), strings.Join(findingLines(t, dir), "\n"); before != after {
		t.Fatalf("finding lines changed after review:\nbefore %q\nafter %q", before, after)
	}

	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"## Findings",
		"### Confirmed (1)",
		"### Unverified (0)",
		"### Duplicate (1)",
		"### Rejected (0)",
		"**F-001** bug",
		"confirmed (2026-07-17)",
		"duplicate of F-001 (2026-07-17)",
		"`[data-testid=save-btn]` #general",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("report missing %q\n---\n%s", want, md)
		}
	}
}

func TestReportNoFindings(t *testing.T) {
	dir := setupSession(t)
	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(md, "No findings yet") {
		t.Fatalf("expected an absence notice, got:\n%s", md)
	}
}

func findingLines(t *testing.T, dir string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, session.FindingsFile))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if !strings.Contains(l, `"kind":"verdict"`) {
			out = append(out, l)
		}
	}
	return out
}

type discard struct{}

func (*discard) Write(p []byte) (int, error) { return len(p), nil }

// TestReportSanitisesInjectedText is the content-injection regression: an
// attacker-authored event kind carrying a newline + markdown heading, and an
// utterance carrying an ANSI escape, must not survive into report.md as real
// report structure or terminal control bytes. Pre-fix these fields were written
// raw, so "### INJECTED" appeared as a heading and the ESC byte reached the
// file.
func TestReportSanitisesInjectedText(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "fixture", App: "app", Participant: "P1"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	timeline := "{\"t\":1,\"src\":\"speech\",\"id\":\"utt-1\",\"payload\":{\"speaker\":\"P1\",\"t1\":2,\"text\":\"hello \\u001b[31mRED\\u001b[0m\"}}\n" +
		"{\"t\":1.2,\"src\":\"event\",\"id\":\"ev-1\",\"payload\":{\"kind\":\"click\\n### INJECTED-HEADING\",\"selector\":\"btn\"}}\n"
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(timeline), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}

	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(md, "\n### INJECTED-HEADING") {
		t.Fatalf("forged markdown heading injected into report:\n%s", md)
	}
	if strings.ContainsRune(md, 0x1b) {
		t.Fatalf("ANSI escape byte survived into report.md")
	}
	// The legitimate token content is retained (only the control byte is gone).
	if !strings.Contains(md, "INJECTED-HEADING") {
		t.Fatalf("expected the kind text to remain, minus the newline")
	}
}

// TestReportFindingsSanitiseIDAndVerdict is the findings-channel injection
// regression. analyze.Load does no id/verdict validation (only ingest does), so
// a downloaded findings.jsonl can carry a newline in the id or the verdict
// fields. Pre-fix f.ID and st.Value/st.Of/st.At were rendered raw, forging
// report headings and fake verdict lines that the human precision record rests
// on.
func TestReportFindingsSanitiseIDAndVerdict(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "fixture", App: "app", Participant: "P1"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(timelineFixture), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}
	// A finding whose id and verdict "at" carry forged markdown structure.
	findings := "{\"id\":\"F-001\\n\\n### Confirmed (99)\\n\\n- **F-666** bug\",\"t\":22,\"type\":\"bug\",\"severity\":3,\"quote\":\"ok\",\"evidence\":[\"utt-004\"],\"status\":\"unverified\"}\n" +
		"{\"kind\":\"verdict\",\"finding\":\"F-001\\n\\n### Confirmed (99)\\n\\n- **F-666** bug\",\"verdict\":\"confirmed\",\"at\":\"2026-01-01)\\n\\n## Forged\"}\n"
	if err := os.WriteFile(filepath.Join(dir, session.FindingsFile), []byte(findings), 0o644); err != nil {
		t.Fatalf("write findings: %v", err)
	}

	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Structural injection needs a newline to start a forged heading/bullet/line;
	// SafeText strips the control bytes, so the payload can only survive inline
	// within F-001's own bullet, never as a fabricated heading or finding.
	if strings.Contains(md, "\n### Confirmed (99)") || strings.Contains(md, "\n## Forged") || strings.Contains(md, "\n- **F-666") {
		t.Fatalf("forged report structure injected via finding id/verdict:\n%s", md)
	}
	if strings.ContainsRune(md, 0x1b) {
		t.Fatalf("control byte survived into report.md")
	}
	// The real Confirmed group holds exactly the one genuine finding.
	if strings.Count(md, "### Confirmed (1)") != 1 {
		t.Fatalf("confirmed count line was altered:\n%s", md)
	}
}
