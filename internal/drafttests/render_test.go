package drafttests

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/REPPL/Testimony/internal/session"
	"github.com/REPPL/Testimony/internal/timeline"
)

const samplePath = "../../examples/sample-session"

// TestRenderGoldenFromSampleSession holds the bundled sample's rendered test plan
// to a golden file. The sample is the reference instance of the schema, so the
// artefact an operator copies into their own docs-as-code plan is pinned here
// byte-for-byte.
func TestRenderGoldenFromSampleSession(t *testing.T) {
	got, err := Render(samplePath)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := string(fixture(t, "tests.md"))
	if got != want {
		t.Fatalf("rendered plan does not match testdata/tests.md:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestRenderOmitsProposedAndRejected: a proposal is not a test, and a rejected
// draft is retained in tests.jsonl for the record, not for the plan.
func TestRenderOmitsProposedAndRejected(t *testing.T) {
	dir := ingestThree(t)
	var out bytes.Buffer
	if err := Review(ReviewOptions{Dir: dir, Test: "T-001", Decision: "accepted", Out: &out, Today: "2026-09-12"}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if err := Review(ReviewOptions{Dir: dir, Test: "T-003", Decision: "rejected", Out: &out, Today: "2026-09-12"}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	md, err := Render(dir)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(md, "## T-001") {
		t.Fatalf("the accepted draft is missing:\n%s", md)
	}
	for _, id := range []string{"## T-002", "## T-003"} {
		if strings.Contains(md, id) {
			t.Fatalf("%s is proposed or rejected but was rendered:\n%s", id, md)
		}
	}
	if !strings.Contains(md, "1 of 3 drafts accepted.") {
		t.Fatalf("the counts line is wrong:\n%s", md)
	}
}

// TestRenderAppliesLastEdit: the rendered fields are the last edited decision's
// edit applied over the draft, computed at render time — the draft line on disk
// stays untouched, which is what keeps its link fields unreachable.
func TestRenderAppliesLastEdit(t *testing.T) {
	dir := ingestThree(t)
	var out bytes.Buffer
	for _, payload := range []string{
		`{"title":"First edit","expected":"First expectation."}`,
		`{"title":"Second edit","steps":["Only step."]}`,
	} {
		err := Review(ReviewOptions{
			Dir: dir, Test: "T-001", Decision: "edited",
			EditIn: strings.NewReader(payload), Out: &out, Today: "2026-09-12",
		})
		if err != nil {
			t.Fatalf("Review: %v", err)
		}
	}
	md, err := Render(dir)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(md, "## T-001 — Second edit") {
		t.Fatalf("the last edit's title was not applied:\n%s", md)
	}
	if strings.Contains(md, "First edit") || strings.Contains(md, "First expectation.") {
		t.Fatalf("an earlier edit leaked into the plan:\n%s", md)
	}
	if !strings.Contains(md, "1. Only step.\n") {
		t.Fatalf("the last edit's steps were not applied:\n%s", md)
	}
	// A field the last edit does not name keeps the draft's own value.
	if !strings.Contains(md, "**Expected:** The save is confirmed on screen.") {
		t.Fatalf("a field absent from the last edit did not fall back to the draft:\n%s", md)
	}
	// The draft line is byte-unchanged.
	drafts, _, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, d := range drafts {
		if d.ID == "T-001" && d.Title != "Saving gives no confirmation" {
			t.Fatalf("the render's edit was written back to the draft: %+v", d)
		}
	}
}

// TestRenderRefusesWithNoAcceptedDrafts keeps `-out FILE` from truncating an
// existing test plan into an empty document.
func TestRenderRefusesWithNoAcceptedDrafts(t *testing.T) {
	dir := ingestThree(t)
	var out bytes.Buffer
	if err := Review(ReviewOptions{Dir: dir, Test: "T-003", Decision: "rejected", Out: &out, Today: "2026-09-12"}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	_, err := Render(dir)
	if !errors.Is(err, ErrNoAcceptedDrafts) {
		t.Fatalf("got %v, want ErrNoAcceptedDrafts", err)
	}
	want := "no accepted test drafts to render (3 drafts: 0 accepted, 0 edited, 2 proposed, 1 rejected); accept one with `testimony review -session " + dir + " -kind tests` first"
	if err.Error() != want {
		t.Fatalf("refusal message:\n got %q\nwant %q", err.Error(), want)
	}
}

func TestRenderHintsIngestWhenTestsMissing(t *testing.T) {
	dir := writeSession(t)
	_, err := Render(dir)
	if err == nil || !strings.Contains(err.Error(), "testimony draft-tests -ingest") {
		t.Fatalf("got %v, want a draft-tests -ingest hint", err)
	}
}

// TestRenderRendersNegativeClock is the clamped-clock sibling: a recording whose
// creation_time predates the manifest t0 yields negative session-relative times,
// and a finding anchored there must be stamped with its real (signed) moment in
// the plan, not misreported as 00:00.
func TestRenderRendersNegativeClock(t *testing.T) {
	findings := `{"id":"F-001","t":-90,"type":"bug","severity":3,"mode":"A","quote":"I clicked save and nothing happened","evidence":["utt-004"],"status":"unverified"}
{"kind":"verdict","finding":"F-001","verdict":"confirmed","at":"2026-09-12"}
`
	dir := writeSession(t, session.FindingsFile, findings)
	if _, err := Ingest(dir, strings.NewReader(answer(goodDraft))); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if err := Review(ReviewOptions{Dir: dir, Test: "T-001", Decision: "accepted", Out: io.Discard, Today: "2026-09-12"}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	md, err := Render(dir)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(md, "at [-01:30]") {
		t.Fatalf("a pre-t0 finding was not stamped with its signed clock:\n%s", md)
	}
}

// TestRenderEscapesInlineMarkdown is the beacon regression for the hand-off
// artefact: the plan is a document the operator pastes into their own repository,
// so an attacker-authored draft field must not survive as an active link or an
// image beacon, and a backtick must not close a code span early and let the tail
// render as markup.
func TestRenderEscapesInlineMarkdown(t *testing.T) {
	dir := writeSession(t)
	d := with(t, "title", "![x](http://example.test/beacon.png)")
	if _, err := Ingest(dir, strings.NewReader(answer(d))); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if err := Review(ReviewOptions{Dir: dir, Test: "T-001", Decision: "accepted", Out: io.Discard, Today: "2026-09-12"}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	md, err := Render(dir)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(md, "![x](http://example.test/beacon.png)") {
		t.Fatalf("a draft title survived as a live image beacon:\n%s", md)
	}
	if !strings.Contains(md, `\!\[x\]\(http://example.test/beacon.png\)`) {
		t.Fatalf("the draft title is not backslash-escaped:\n%s", md)
	}
}

func TestRenderStripsBackticksFromCodeSpans(t *testing.T) {
	man := "{\"session\":\"fixture-session\",\"app\":\"ev`il`\",\"participant\":\"P1\",\"t0_epoch_ms\":1784300400000}"
	dir := writeSession(t, session.ManifestFile, man)
	if _, err := Ingest(dir, strings.NewReader(answer(goodDraft))); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if err := Review(ReviewOptions{Dir: dir, Test: "T-001", Decision: "accepted", Out: io.Discard, Today: "2026-09-12"}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	md, err := Render(dir)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(md, "(app `evil`, ") {
		t.Fatalf("a backtick in a code span was not stripped:\n%s", md)
	}
}

// TestRoundTripGolden is the whole pipeline over a copy of the bundled sample:
// merge → draft-tests (emit) → ingest a known-good answer → three decisions
// (accepted / edited / rejected) → render. It asserts the golden Markdown, the
// append-only property of tests.jsonl, and that findings.jsonl is untouched
// throughout — the drafting layer reads the verified record and never writes to
// it.
func TestRoundTripGolden(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{session.ManifestFile, session.FindingsFile, session.TranscriptFile, session.InteractionsFile} {
		b, err := os.ReadFile(filepath.Join(samplePath, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	findingsBefore, err := os.ReadFile(filepath.Join(dir, session.FindingsFile))
	if err != nil {
		t.Fatalf("read findings: %v", err)
	}

	if _, _, err := timeline.Merge(dir); err != nil {
		t.Fatalf("merge: %v", err)
	}

	// Emit: the request carries the one confirmed finding and its event window.
	req, err := EmitRequest(dir, 10)
	if err != nil {
		t.Fatalf("EmitRequest: %v", err)
	}
	if !strings.Contains(req, "Finding F-001 — bug, severity 3, at [00:22]:") {
		t.Fatalf("the emitted request does not carry F-001:\n%s", req)
	}

	// Ingest: the answer is the bundled sample's own three drafts.
	sampleDrafts, sampleDecisions, err := Load(samplePath)
	if err != nil {
		t.Fatalf("Load the bundled sample: %v", err)
	}
	var lines []string
	for _, d := range sampleDrafts {
		b, merr := json.Marshal(d)
		if merr != nil {
			t.Fatalf("marshal: %v", merr)
		}
		lines = append(lines, string(b))
	}
	if _, err := Ingest(dir, strings.NewReader(answer(lines...))); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	draftsAfterIngest := draftLines(t, dir)

	// Three decisions, replayed from the sample's own decision records.
	for _, dec := range sampleDecisions {
		opts := ReviewOptions{Dir: dir, Test: dec.Test, Decision: dec.Decision, Out: io.Discard, Today: dec.At}
		if dec.Decision == "edited" {
			b, merr := json.Marshal(dec.Edit)
			if merr != nil {
				t.Fatalf("marshal edit: %v", merr)
			}
			opts.EditIn = bytes.NewReader(b)
		}
		if err := Review(opts); err != nil {
			t.Fatalf("Review %s %s: %v", dec.Test, dec.Decision, err)
		}
	}

	// Append-only: every draft line is byte-unchanged by the three decisions.
	if got, want := strings.Join(draftLines(t, dir), "\n"), strings.Join(draftsAfterIngest, "\n"); got != want {
		t.Fatalf("draft lines changed across the decisions:\n got %q\nwant %q", got, want)
	}

	md, err := Render(dir)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if want := string(fixture(t, "tests.md")); md != want {
		t.Fatalf("the round-trip plan does not match testdata/tests.md:\n--- got ---\n%s\n--- want ---\n%s", md, want)
	}

	// findings.jsonl is untouched: the drafting layer reads the verified record.
	findingsAfter, err := os.ReadFile(filepath.Join(dir, session.FindingsFile))
	if err != nil {
		t.Fatalf("read findings: %v", err)
	}
	if !bytes.Equal(findingsBefore, findingsAfter) {
		t.Fatal("the drafting pipeline modified findings.jsonl")
	}
}
