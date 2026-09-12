package drafttests

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/REPPL/Testimony/internal/session"
)

// goodDraft is a schema-clean draft of F-001, the fixture's one eligible
// finding. The validation-failure table below mutates one rule at a time from it.
const goodDraft = `{"id":"T-001","finding":"F-001","session":"fixture-session",` +
	`"title":"Saving gives no confirmation","steps":["Open #general.","Click Save."],` +
	`"expected":"The save is confirmed on screen.","observed":"Nothing visibly changes.",` +
	`"rationale_quote":"I clicked save and nothing happened","severity":3}`

func answer(drafts ...string) string {
	return `{"rubric":"testimony-testdraft/v1","tests":[` + strings.Join(drafts, ",") + `]}`
}

// with returns goodDraft with one field replaced (or, with a "-" prefix on the
// field name, removed), so each failure case differs from the clean draft by
// exactly the rule under test.
func with(t *testing.T, field string, value any) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(goodDraft), &m); err != nil {
		t.Fatalf("unmarshal goodDraft: %v", err)
	}
	if strings.HasPrefix(field, "-") {
		delete(m, strings.TrimPrefix(field, "-"))
	} else {
		m[field] = value
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func testsPath(dir string) string { return filepath.Join(dir, session.TestsFile) }

// TestIngestGood is the happy path: one clean draft validates, lands in
// tests.jsonl, and is forced to "proposed" even though the answer claimed
// "accepted" — a draft can never be born accepted, the same laundering
// analyze -ingest applies to "unverified".
func TestIngestGood(t *testing.T) {
	dir := writeSession(t)
	drafts, err := Ingest(dir, strings.NewReader(string(fixture(t, "answer.json"))))
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("got %d drafts, want 1", len(drafts))
	}
	if drafts[0].Status != "proposed" {
		t.Fatalf("draft status %q, want proposed", drafts[0].Status)
	}
	onDisk, decisions, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(onDisk) != 1 || len(decisions) != 0 {
		t.Fatalf("tests.jsonl holds %d drafts and %d decisions, want 1 and 0", len(onDisk), len(decisions))
	}
	if onDisk[0].Status != "proposed" {
		t.Fatalf("written status %q, want proposed", onDisk[0].Status)
	}
	if onDisk[0].Finding != "F-001" || onDisk[0].Session != "fixture-session" || onDisk[0].Severity != 3 {
		t.Fatalf("written draft lost its link fields: %+v", onDisk[0])
	}
}

func TestIngestBareArrayAccepted(t *testing.T) {
	dir := writeSession(t)
	if _, err := Ingest(dir, strings.NewReader("["+goodDraft+"]")); err != nil {
		t.Fatalf("Ingest of a bare array: %v", err)
	}
}

// TestIngestValidationFailures is one case per schema rule, each proving a
// precise message. Validation is the sole boundary: a hand-written or stale
// answer cannot smuggle a draft past any of these.
func TestIngestValidationFailures(t *testing.T) {
	longTitle := strings.Repeat("x", maxTitle+1)
	tooManySteps := make([]string, maxSteps+1)
	for i := range tooManySteps {
		tooManySteps[i] = "Click Save."
	}
	cases := []struct {
		name, draft, want string
	}{
		{"short id", with(t, "id", "T-12"), `id "T-12" must match`},
		{"foreign id prefix", with(t, "id", "X-001"), `id "X-001" must match`},
		{"unknown finding", with(t, "finding", "F-404"), `finding "F-404" is not a confirmed finding`},
		{"unverified finding", with(t, "finding", "F-002"), `finding "F-002" is not a confirmed finding`},
		{"rejected finding", with(t, "finding", "F-003"), `finding "F-003" is not a confirmed finding`},
		{"mode B finding", with(t, "finding", "F-004"), `finding "F-004" is not a confirmed finding`},
		{"duplicate finding", with(t, "finding", "F-005"), `finding "F-005" is not a confirmed finding`},
		{"session mismatch", with(t, "session", "other-session"), `session "other-session" is not this session`},
		{"empty title", with(t, "title", ""), "title must be non-empty"},
		{"whitespace title", with(t, "title", "   "), "title must be non-empty"},
		{"over-long title", with(t, "title", longTitle), fmt.Sprintf("title is %d characters, exceeding the limit of %d", maxTitle+1, maxTitle)},
		{"absent steps", with(t, "-steps", nil), "steps must be non-empty"},
		{"empty steps", with(t, "steps", []string{}), "steps must be non-empty"},
		{"whitespace-only step", with(t, "steps", []string{"Open #general.", " "}), "step 2 must be non-empty"},
		{"over-long steps", with(t, "steps", tooManySteps), fmt.Sprintf("steps lists %d entries, exceeding the limit of %d", maxSteps+1, maxSteps)},
		{"empty expected", with(t, "expected", ""), "expected must be non-empty"},
		{"empty observed", with(t, "observed", ""), "observed must be non-empty"},
		{"quote off by one byte", with(t, "rationale_quote", "I clicked save and nothing happened."), "is not finding F-001's quote"},
		{"absent severity", with(t, "-severity", nil), "missing severity"},
		{"mismatched severity", with(t, "severity", 4), "severity 4 does not match finding F-001's severity 3"},
		{"unknown field", strings.Replace(goodDraft, `"steps"`, `"stpes"`, 1), "stpes"},
		{"non-object element", `42`, "cannot unmarshal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeSession(t)
			_, err := Ingest(dir, strings.NewReader(answer(tc.draft)))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
			if _, statErr := os.Stat(testsPath(dir)); !os.IsNotExist(statErr) {
				t.Fatalf("a refused ingest wrote %s", session.TestsFile)
			}
		})
	}
}

func TestIngestRejectsDuplicateID(t *testing.T) {
	dir := writeSession(t)
	_, err := Ingest(dir, strings.NewReader(answer(goodDraft, goodDraft)))
	if err == nil || !strings.Contains(err.Error(), "duplicate id (first seen at draft #1)") {
		t.Fatalf("got %v, want a duplicate-id refusal naming the first position", err)
	}
}

// TestIngestRejectsQuoteMatchedInSafeTextForm: the comparison is made in the
// SafeText form, the only form of the finding the answering agent was shown, so
// an honest byte-for-byte copy of the sanitised request validates while a
// different quote does not.
func TestIngestRejectsQuoteMatchedInSafeTextForm(t *testing.T) {
	dir := writeSession(t)
	// A zero-width space inside the quote strips away under SafeText, so this copy
	// is equal in the form both sides are compared in.
	d := with(t, "rationale_quote", "I clicked save\u200b and nothing happened")
	if _, err := Ingest(dir, strings.NewReader(answer(d))); err != nil {
		t.Fatalf("a quote equal in SafeText form was refused: %v", err)
	}
}

// TestIngestRefusesEmptyAnswer: an empty tests array (a bare [], {"tests":[]}, or
// a truncated file) must not erase a prior good tests.jsonl and report success.
func TestIngestRefusesEmptyAnswer(t *testing.T) {
	for _, in := range []string{`[]`, `{"tests":[]}`, `{"rubric":"testimony-testdraft/v1","tests":[]}`} {
		dir := writeSession(t)
		prior := `{"id":"T-009","finding":"F-001","session":"fixture-session","title":"kept","steps":["a"],"expected":"e","observed":"o","rationale_quote":"I clicked save and nothing happened","severity":3,"status":"proposed"}` + "\n"
		if err := os.WriteFile(testsPath(dir), []byte(prior), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		_, err := Ingest(dir, strings.NewReader(in))
		if err == nil || !strings.Contains(err.Error(), "no test drafts") {
			t.Fatalf("%s: got %v, want an empty-answer refusal", in, err)
		}
		b, rerr := os.ReadFile(testsPath(dir))
		if rerr != nil {
			t.Fatalf("read: %v", rerr)
		}
		if string(b) != prior {
			t.Fatalf("%s: the prior tests.jsonl was modified: %q", in, b)
		}
	}
}

func TestIngestUnknownRubric(t *testing.T) {
	dir := writeSession(t)
	in := `{"rubric":"testimony-testdraft/v9","tests":[` + goodDraft + `]}`
	_, err := Ingest(dir, strings.NewReader(in))
	if err == nil || !strings.Contains(err.Error(), "unknown rubric") {
		t.Fatalf("got %v, want an unknown-rubric refusal", err)
	}
}

func TestIngestRejectsOversizedAnswer(t *testing.T) {
	dir := writeSession(t)
	big := strings.Repeat("x", session.MaxAnswerBytes+1)
	_, err := Ingest(dir, strings.NewReader(big))
	if err == nil || !strings.Contains(err.Error(), "refusing to ingest") {
		t.Fatalf("got %v, want an over-size refusal", err)
	}
}

// TestIngestRefusesWithNoConfirmedFindings is the ingest half of the loud
// staging: with no eligible finding there is nothing a draft could legally
// reference, so the refusal comes before a byte of the answer is read rather than
// as a wall of per-draft errors.
func TestIngestRefusesWithNoConfirmedFindings(t *testing.T) {
	findings := `{"id":"F-001","t":22,"type":"bug","severity":3,"quote":"q","evidence":["utt-004"],"status":"unverified"}` + "\n"
	dir := writeSession(t, session.FindingsFile, findings)
	_, err := Ingest(dir, strings.NewReader(answer(goodDraft)))
	if !errors.Is(err, ErrNoConfirmedFindings) {
		t.Fatalf("got %v, want ErrNoConfirmedFindings", err)
	}
	want := "no confirmed findings to draft tests from (1 findings: 0 confirmed, 1 unverified, 0 duplicate, 0 rejected); confirm one with `testimony review -session " + dir + "` first"
	if err.Error() != want {
		t.Fatalf("refusal message:\n got %q\nwant %q", err.Error(), want)
	}
}

func TestIngestHintsIngestWhenFindingsMissing(t *testing.T) {
	dir := writeSession(t, session.FindingsFile, "")
	_, err := Ingest(dir, strings.NewReader(answer(goodDraft)))
	if err == nil || !strings.Contains(err.Error(), "testimony analyze -ingest") {
		t.Fatalf("got %v, want an analyze -ingest hint", err)
	}
}

// TestIngestReadsNoTimeline: ingest validates drafts against the *findings*,
// never re-derives them from the timeline, so a session with no timeline.jsonl
// ingests cleanly.
func TestIngestReadsNoTimeline(t *testing.T) {
	dir := writeSession(t, session.TimelineFile, "")
	if _, err := Ingest(dir, strings.NewReader(answer(goodDraft))); err != nil {
		t.Fatalf("Ingest without a timeline: %v", err)
	}
}

// TestIngestIsTransactional: three bad drafts report three errors in one run, and
// nothing is written.
func TestIngestIsTransactional(t *testing.T) {
	dir := writeSession(t)
	a := answer(
		with(t, "id", "T-12"),
		strings.Replace(with(t, "id", "T-002"), `"expected":"The save is confirmed on screen."`, `"expected":""`, 1),
		strings.Replace(with(t, "id", "T-003"), `"severity":3`, `"severity":2`, 1),
	)
	_, err := Ingest(dir, strings.NewReader(a))
	if err == nil {
		t.Fatal("Ingest accepted an answer with three bad drafts")
	}
	for _, want := range []string{"must match", "expected must be non-empty", "does not match finding"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the joined error is missing %q:\n%v", want, err)
		}
	}
	if _, statErr := os.Stat(testsPath(dir)); !os.IsNotExist(statErr) {
		t.Fatalf("a refused ingest wrote %s", session.TestsFile)
	}
}

// TestIngestLabelsUndecodableNeighbourByAnswerPosition: positions are counted in
// the answer the operator actually wrote, not in the filtered slice validation
// sees, so the error names the draft they can count to.
func TestIngestLabelsUndecodableNeighbourByAnswerPosition(t *testing.T) {
	dir := writeSession(t)
	a := answer(
		with(t, "id", "T-001"),
		`{"id":"T-002","stpes":["x"]}`, // undecodable: unknown field
		with(t, "id", ""),              // third in the answer, unusable id
	)
	_, err := Ingest(dir, strings.NewReader(a))
	if err == nil {
		t.Fatal("Ingest accepted an answer with an undecodable element")
	}
	if !strings.Contains(err.Error(), "draft #2:") {
		t.Fatalf("the undecodable element is not labelled by its answer position:\n%v", err)
	}
	if !strings.Contains(err.Error(), "draft #3:") {
		t.Fatalf("the third draft is not labelled #3:\n%v", err)
	}
}

// TestIngestRefusesOverwriteWithDecisions protects the retained human record: a
// tests.jsonl already holding a decision is never truncated by a re-ingest.
func TestIngestRefusesOverwriteWithDecisions(t *testing.T) {
	dir := writeSession(t)
	if _, err := Ingest(dir, strings.NewReader(answer(goodDraft))); err != nil {
		t.Fatalf("first Ingest: %v", err)
	}
	drafts, _, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rec := Decision{Kind: "decision", Test: "T-001", Decision: "accepted", At: "2026-09-12"}
	if err := AppendDecision(dir, rec, &drafts[0]); err != nil {
		t.Fatalf("AppendDecision: %v", err)
	}
	before, err := os.ReadFile(testsPath(dir))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	_, err = Ingest(dir, strings.NewReader(answer(goodDraft)))
	if err == nil || !strings.Contains(err.Error(), "decision records") {
		t.Fatalf("got %v, want a decision-guard refusal", err)
	}
	after, err := os.ReadFile(testsPath(dir))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("the refused re-ingest modified tests.jsonl:\nbefore %q\nafter %q", before, after)
	}
}

// TestIngestRefusesOverwriteWithForeignDecision: the guard counts any
// kind:"decision" line, including one whose value is outside the closed enum (a
// hand-edited or shared file), so a foreign-valued human decision is never
// silently truncated.
func TestIngestRefusesOverwriteWithForeignDecision(t *testing.T) {
	dir := writeSession(t)
	seed := `{"kind":"decision","test":"T-001","decision":"maybe","at":"2026-09-12"}` + "\n"
	if err := os.WriteFile(testsPath(dir), []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := Ingest(dir, strings.NewReader(answer(goodDraft)))
	if err == nil || !strings.Contains(err.Error(), "decision records") {
		t.Fatalf("got %v, want a decision-guard refusal", err)
	}
}

// TestIngestRejectsOversizedDraftLine holds a single draft to MaxJSONLLine, the
// shared invariant ParseRecords scans to: a line above it makes tests.jsonl
// durably unreadable.
func TestIngestRejectsOversizedDraftLine(t *testing.T) {
	dir := writeSession(t)
	huge := with(t, "steps", []string{strings.Repeat("x", session.MaxJSONLLine)})
	_, err := Ingest(dir, strings.NewReader(answer(huge)))
	if err == nil || !strings.Contains(err.Error(), "line limit") {
		t.Fatalf("got %v, want an over-line refusal", err)
	}
	if _, statErr := os.Stat(testsPath(dir)); !os.IsNotExist(statErr) {
		t.Fatalf("a refused ingest wrote %s", session.TestsFile)
	}
}

// TestOversizedDraftsRejectsOversizedTotal is the total-size check, tested
// directly for a small and exactly predictable byte count (as
// session.TestWriteRecordsRollsBackOnWriteError tests writeRecords): five drafts
// each ~3.5 MiB stay under the 4 MiB line cap and sum past the 16 MiB file cap.
// The refusal names the file, not one draft.
func TestOversizedDraftsRejectsOversizedTotal(t *testing.T) {
	long := strings.Repeat("x", 3_500_000)
	var drafts []Draft
	var decoded []positioned
	for i := 1; i <= 5; i++ {
		d := draft(fmt.Sprintf("T-%03d", i))
		d.Observed = long
		drafts = append(drafts, d)
		decoded = append(decoded, positioned{draft: d, at: i})
	}
	errs := oversizedDrafts(drafts, decoded)
	joined := errors.Join(errs...)
	if joined == nil || !strings.Contains(joined.Error(), "file limit") {
		t.Fatalf("expected a total-size refusal naming the file limit, got %v", joined)
	}
	for _, err := range errs {
		if strings.Contains(err.Error(), "T-001:") {
			t.Fatalf("the total-size refusal was attributed to one draft rather than the file: %v", err)
		}
	}
}

// TestIngestRejectsOversizedDraftsTotal is the same regression reached through
// the public API at a fraction of session.MaxAnswerBytes: commitDrafts and
// oversizedDrafts both encode with Go's default HTML-escaping encoder, so a run
// of '<' inflates roughly sixfold between the answer and the line that would be
// written — the read-side answer cap therefore cannot be relied on to keep this
// path from executing.
func TestIngestRejectsOversizedDraftsTotal(t *testing.T) {
	dir := writeSession(t)
	long := strings.Repeat("<", 600_000)
	var drafts []string
	for i := 1; i <= 5; i++ {
		drafts = append(drafts, with(t, "id", fmt.Sprintf("T-%03d", i)))
	}
	for i := range drafts {
		drafts[i] = strings.Replace(drafts[i], `"Nothing visibly changes."`, fmt.Sprintf("%q", long), 1)
	}
	a := answer(drafts...)
	if len(a) >= session.MaxAnswerBytes {
		t.Fatalf("test setup: answer is %d bytes, at or over session.MaxAnswerBytes (%d)", len(a), session.MaxAnswerBytes)
	}
	_, err := Ingest(dir, strings.NewReader(a))
	if err == nil || !strings.Contains(err.Error(), "file limit") {
		t.Fatalf("expected a total-size refusal naming the file limit, got %v", err)
	}
	if _, statErr := os.Stat(testsPath(dir)); !os.IsNotExist(statErr) {
		t.Fatalf("a refused ingest wrote %s", session.TestsFile)
	}
}

// TestIngestRefusesSymlink is the arbitrary-file-truncation regression: a
// tests.jsonl planted as a symlink in an exchanged session must not be followed.
func TestIngestRefusesSymlink(t *testing.T) {
	dir := writeSession(t)
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("original\n"), 0o600); err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	if err := os.Symlink(outside, testsPath(dir)); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := Ingest(dir, strings.NewReader(answer(goodDraft))); err == nil {
		t.Fatal("Ingest followed a symlink; want refusal")
	}
	if b, _ := os.ReadFile(outside); string(b) != "original\n" {
		t.Fatalf("victim file rewritten through symlink: %q", b)
	}
}

// TestSampleTestsJSONLPassesIngest holds the bundled sample to the schema its own
// validator enforces: the three draft lines in examples/sample-session/tests.jsonl
// are re-ingested against that session's manifest and findings, and the written
// lines must come back byte-for-byte identical. The sample is documentation, and
// documentation that would not survive the tool's own boundary is wrong.
func TestSampleTestsJSONLPassesIngest(t *testing.T) {
	const sample = "../../examples/sample-session"
	drafts, decisions, err := Load(sample)
	if err != nil {
		t.Fatalf("Load the bundled sample: %v", err)
	}
	if len(drafts) != 3 || len(decisions) != 3 {
		t.Fatalf("the sample holds %d drafts and %d decisions, want 3 and 3", len(drafts), len(decisions))
	}

	// A scratch session carrying only the sample's manifest and findings, so the
	// re-ingest is not blocked by the sample's own decision records.
	dir := t.TempDir()
	for _, name := range []string{session.ManifestFile, session.FindingsFile} {
		b, rerr := os.ReadFile(filepath.Join(sample, name))
		if rerr != nil {
			t.Fatalf("read %s: %v", name, rerr)
		}
		if werr := os.WriteFile(filepath.Join(dir, name), b, 0o644); werr != nil {
			t.Fatalf("write %s: %v", name, werr)
		}
	}
	var lines []string
	for _, d := range drafts {
		b, merr := json.Marshal(d)
		if merr != nil {
			t.Fatalf("marshal: %v", merr)
		}
		lines = append(lines, string(b))
	}
	if _, err := Ingest(dir, strings.NewReader(answer(lines...))); err != nil {
		t.Fatalf("the bundled sample's drafts do not pass Ingest: %v", err)
	}

	written, err := os.ReadFile(testsPath(dir))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	sampleBytes, err := os.ReadFile(filepath.Join(sample, session.TestsFile))
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	wantPrefix := strings.Join(strings.Split(strings.TrimRight(string(sampleBytes), "\n"), "\n")[:3], "\n") + "\n"
	if string(written) != wantPrefix {
		t.Fatalf("re-ingesting the sample's drafts does not reproduce them byte for byte:\n got %q\nwant %q", written, wantPrefix)
	}
}
