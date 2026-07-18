package analyze

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/REPPL/Testimony/internal/session"
)

// timelineFixture is a minimal merged timeline: one spoken utterance and two
// events, enough to exercise every validation rule. sessionEnd is 28 (utt-004
// t1).
const timelineFixture = `{"t":22,"src":"speech","id":"utt-004","payload":{"speaker":"P1","t1":28,"text":"Hm. I clicked save and nothing happened. No message."}}
{"t":19.2,"src":"event","id":"ev-003","payload":{"kind":"click","route":"#general","selector":"[data-testid=save-btn]","text":"Save"}}
{"t":24.1,"src":"event","id":"ev-004","payload":{"kind":"click","route":"#general","selector":"[data-testid=save-btn]","text":"Save"}}
`

// goodAnswer is a valid single-finding answer against timelineFixture. Note the
// status:"confirmed" — ingest must launder it to "unverified".
const goodAnswer = `{"rubric":"testimony-analysis/v1","findings":[
 {"id":"F-001","t":22.0,"type":"bug","severity":3,"quote":"I clicked save and nothing happened",
  "evidence":["utt-004","ev-003"],"ui":{"selector":"[data-testid=save-btn]","route":"#general"},
  "status":"confirmed"}
]}`

func writeSession(t *testing.T, timeline string) string {
	t.Helper()
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{
		Session:     "fixture",
		App:         "settings prototype",
		Participant: "P1",
		Tasks:       []string{"Change your display name and save it", "Try the appearance settings"},
	}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(timeline), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}
	return dir
}

func TestIngestGood(t *testing.T) {
	dir := writeSession(t, timelineFixture)
	findings, err := Ingest(dir, strings.NewReader(goodAnswer))
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	// Status is laundered regardless of the JSON's claim.
	if findings[0].Status != "unverified" {
		t.Fatalf("status: got %q, want unverified", findings[0].Status)
	}
	// findings.jsonl is written and reloads as unverified.
	got, _, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || got[0].ID != "F-001" || got[0].Status != "unverified" {
		t.Fatalf("reloaded findings wrong: %+v", got)
	}
}

func TestIngestValidationFailures(t *testing.T) {
	cases := []struct {
		name    string
		finding string
		want    string
	}{
		{"bad id format", `{"id":"F-12","t":22,"type":"bug","severity":3,"quote":"I clicked save and nothing happened","evidence":["utt-004"]}`, "must match"},
		{"non-F id", `{"id":"X-001","t":22,"type":"bug","severity":3,"quote":"I clicked save and nothing happened","evidence":["utt-004"]}`, "must match"},
		{"unknown evidence", `{"id":"F-001","t":22,"type":"bug","severity":3,"quote":"I clicked save and nothing happened","evidence":["utt-999","utt-004"]}`, "not found in the timeline"},
		{"no spoken anchor", `{"id":"F-001","t":22,"type":"bug","severity":3,"quote":"Save","evidence":["ev-003"]}`, "at least one utt-*"},
		{"fabricated quote (real elsewhere)", `{"id":"F-001","t":22,"type":"bug","severity":3,"quote":"Save","evidence":["utt-004"]}`, "verbatim substring"},
		{"quote absent entirely", `{"id":"F-001","t":22,"type":"bug","severity":3,"quote":"I never said this","evidence":["utt-004"]}`, "verbatim substring"},
		{"wrong type enum", `{"id":"F-001","t":22,"type":"annoyance","severity":3,"quote":"I clicked save and nothing happened","evidence":["utt-004"]}`, "not in bug|friction"},
		{"severity out of range", `{"id":"F-001","t":22,"type":"bug","severity":5,"quote":"I clicked save and nothing happened","evidence":["utt-004"]}`, "out of range 1..4"},
		{"phantom selector", `{"id":"F-001","t":22,"type":"bug","severity":3,"quote":"I clicked save and nothing happened","evidence":["utt-004"],"ui":{"selector":"[data-testid=phantom]"}}`, "not present on any timeline event"},
		{"phantom route", `{"id":"F-001","t":22,"type":"bug","severity":3,"quote":"I clicked save and nothing happened","evidence":["utt-004"],"ui":{"route":"#nowhere"}}`, "not present on any timeline event"},
		{"t outside session", `{"id":"F-001","t":999,"type":"bug","severity":3,"quote":"I clicked save and nothing happened","evidence":["utt-004"]}`, "outside the session"},
		{"stray field", `{"id":"F-001","t":22,"type":"bug","severity":3,"quote":"I clicked save and nothing happened","evidence":["utt-004"],"code_refs":["x"]}`, "unknown field"},
		{"non-integer severity", `{"id":"F-001","t":22,"type":"bug","severity":2.5,"quote":"I clicked save and nothing happened","evidence":["utt-004"]}`, "severity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeSession(t, timelineFixture)
			_, err := Ingest(dir, strings.NewReader(`{"findings":[`+tc.finding+`]}`))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
			// Nothing is written on failure.
			if _, statErr := os.Stat(filepath.Join(dir, session.FindingsFile)); statErr == nil {
				t.Fatalf("findings.jsonl was written despite a validation error")
			}
		})
	}
}

func TestIngestDuplicateID(t *testing.T) {
	dir := writeSession(t, timelineFixture)
	dup := `{"findings":[
	 {"id":"F-001","t":22,"type":"bug","severity":3,"quote":"I clicked save and nothing happened","evidence":["utt-004"]},
	 {"id":"F-001","t":22,"type":"friction","severity":2,"quote":"No message","evidence":["utt-004"]}
	]}`
	_, err := Ingest(dir, strings.NewReader(dup))
	if err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("expected duplicate id error, got %v", err)
	}
}

func TestIngestUnknownRubric(t *testing.T) {
	dir := writeSession(t, timelineFixture)
	_, err := Ingest(dir, strings.NewReader(`{"rubric":"testimony-analysis/v99","findings":[]}`))
	if err == nil || !strings.Contains(err.Error(), "unknown rubric") {
		t.Fatalf("expected unknown rubric error, got %v", err)
	}
}

func TestIngestBareArrayAccepted(t *testing.T) {
	dir := writeSession(t, timelineFixture)
	bare := `[{"id":"F-001","t":22,"type":"bug","severity":3,"quote":"I clicked save and nothing happened","evidence":["utt-004"]}]`
	findings, err := Ingest(dir, strings.NewReader(bare))
	if err != nil {
		t.Fatalf("Ingest bare array: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
}

func TestIngestRefusesOverwriteWithVerdicts(t *testing.T) {
	dir := writeSession(t, timelineFixture)
	if _, err := Ingest(dir, strings.NewReader(goodAnswer)); err != nil {
		t.Fatalf("first Ingest: %v", err)
	}
	// Append a verdict, then a re-ingest must be refused.
	path := filepath.Join(dir, session.FindingsFile)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	f.WriteString(`{"kind":"verdict","finding":"F-001","verdict":"confirmed","at":"2026-07-17"}` + "\n")
	f.Close()

	before, _ := os.ReadFile(path)
	if _, err := Ingest(dir, strings.NewReader(goodAnswer)); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("expected overwrite refusal, got %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatalf("findings.jsonl was modified despite the refusal")
	}
}

func TestEffectiveStatusLastWins(t *testing.T) {
	findings := []Finding{{ID: "F-001"}, {ID: "F-002"}}
	verdicts := []Verdict{
		{Kind: "verdict", Finding: "F-001", Verdict: "confirmed", At: "2026-07-17"},
		{Kind: "verdict", Finding: "F-001", Verdict: "rejected", At: "2026-07-18"}, // later wins
	}
	eff := EffectiveStatus(findings, verdicts)
	if eff["F-001"].Value != "rejected" {
		t.Fatalf("F-001: got %q, want rejected (last verdict wins)", eff["F-001"].Value)
	}
	if eff["F-002"].Value != "unverified" {
		t.Fatalf("F-002: got %q, want unverified", eff["F-002"].Value)
	}
}

func TestEmitRequest(t *testing.T) {
	dir := writeSession(t, timelineFixture)
	got, err := EmitRequest(dir)
	if err != nil {
		t.Fatalf("EmitRequest: %v", err)
	}
	wants := []string{
		"testimony-analysis/v1",                                                      // rubric version header
		"**bug**", "**friction**", "**inconsistency**", "**preference**", "**idea**", // five types
		"cosmetic", "blocker", // severity scale
		"Change your display name and save it", // manifest task list
		"utt-004", "ev-003",                    // timeline lines
		`"findings":[`, // output-shape example
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Fatalf("emitted request missing %q", w)
		}
	}
}

// endlessReader yields spaces forever, standing in for a hostile multi-gigabyte
// answer without allocating one in the test.
type endlessReader struct{ read int }

func (e *endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	e.read += len(p)
	return len(p), nil
}

// TestIngestRejectsOversizedAnswer is the resource-exhaustion regression: the
// untrusted answer read is bounded, so an unbounded stream is refused rather
// than buffered into memory. Pre-fix io.ReadAll had no cap.
func TestIngestRejectsOversizedAnswer(t *testing.T) {
	dir := writeSession(t, timelineFixture)
	r := &endlessReader{}
	_, err := Ingest(dir, r)
	if err == nil || !strings.Contains(err.Error(), "refusing to ingest") {
		t.Fatalf("expected an over-size refusal, got %v", err)
	}
	if r.read > maxAnswerBytes+64*1024 {
		t.Fatalf("read %d bytes; the cap (%d) was not enforced", r.read, maxAnswerBytes)
	}
}
