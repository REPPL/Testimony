package drafttests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ingestThree lays a session whose tests.jsonl holds three proposed drafts of
// F-001 — a finding may legitimately yield more than one test case — which is
// what lets the walk and the render exercise all three decisions.
func ingestThree(t *testing.T) string {
	t.Helper()
	dir := writeSession(t)
	var drafts []string
	for i := 1; i <= 3; i++ {
		drafts = append(drafts, with(t, "id", fmt.Sprintf("T-%03d", i)))
	}
	if _, err := Ingest(dir, strings.NewReader(answer(drafts...))); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	return dir
}

// draftLines returns only the draft (non-decision) lines, to assert the
// append-only property.
func draftLines(t *testing.T, dir string) []string {
	t.Helper()
	b, err := os.ReadFile(testsPath(dir))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if !strings.Contains(l, `"kind":"decision"`) {
			out = append(out, l)
		}
	}
	return out
}

func effective(t *testing.T, dir string) map[string]Status {
	t.Helper()
	drafts, decisions, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return EffectiveStatus(drafts, decisions)
}

// TestDecisionIsAppendedAndDraftLinesUnchanged is AC3's first mechanism, asserted
// byte-for-byte: a decision is a new line, the draft line is never rewritten, so
// finding, session, severity, rationale_quote, and id are unreachable by any
// later write.
func TestDecisionIsAppendedAndDraftLinesUnchanged(t *testing.T) {
	dir := ingestThree(t)
	before := draftLines(t, dir)
	var out bytes.Buffer
	if err := Review(ReviewOptions{Dir: dir, Test: "T-001", Decision: "accepted", Out: &out, Today: "2026-09-12"}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if got := out.String(); got != "recorded: T-001 accepted (2026-09-12)\n" {
		t.Fatalf("echo = %q", got)
	}
	if effective(t, dir)["T-001"].Value != "accepted" {
		t.Fatal("T-001 is not accepted")
	}
	after := draftLines(t, dir)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("draft lines changed after a decision:\nbefore %q\nafter  %q", before, after)
	}
}

func TestSingleDecisionRejected(t *testing.T) {
	dir := ingestThree(t)
	var out bytes.Buffer
	if err := Review(ReviewOptions{Dir: dir, Test: "T-003", Decision: "rejected", Out: &out, Today: "2026-09-12"}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if effective(t, dir)["T-003"].Value != "rejected" {
		t.Fatal("T-003 is not rejected")
	}
}

// TestSingleDecisionEdited covers the non-interactive twin of the interactive
// edit: every interactive path in this repo has one, and making "edited" the
// exception would put the only lossy decision out of reach of a script or an
// agent host.
func TestSingleDecisionEdited(t *testing.T) {
	dir := ingestThree(t)
	var out bytes.Buffer
	err := Review(ReviewOptions{
		Dir: dir, Test: "T-002", Decision: "edited",
		EditIn: bytes.NewReader(fixture(t, "edit.json")),
		Out:    &out, Today: "2026-09-12",
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	st := effective(t, dir)["T-002"]
	if st.Value != "edited" {
		t.Fatalf("T-002 status %+v, want edited", st)
	}
	if st.Edit == nil || st.Edit.Title == nil || *st.Edit.Title != "Saving a display name gives no confirmation" {
		t.Fatalf("the edit did not survive the round trip: %+v", st.Edit)
	}
	// The draft line itself is untouched: the edit is applied at render time.
	drafts, _, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, d := range drafts {
		if d.ID == "T-002" && d.Title != "Saving gives no confirmation" {
			t.Fatalf("the edit rewrote the draft line: %+v", d)
		}
	}
}

// TestEditCannotNameFindingOrSessionOrSeverityOrQuoteOrID is AC3's second
// mechanism: the edit object is a closed four-field subset decoded with
// DisallowUnknownFields, so there is no path through which a human edit can
// re-point a draft at a different finding or session. Each is a hard error, not a
// silently dropped key.
func TestEditCannotNameFindingOrSessionOrSeverityOrQuoteOrID(t *testing.T) {
	for _, field := range []string{"finding", "session", "severity", "rationale_quote", "id", "status"} {
		t.Run(field, func(t *testing.T) {
			dir := ingestThree(t)
			payload := fmt.Sprintf(`{"title":"ok","%s":"x"}`, field)
			var out bytes.Buffer
			err := Review(ReviewOptions{
				Dir: dir, Test: "T-002", Decision: "edited",
				EditIn: strings.NewReader(payload), Out: &out, Today: "2026-09-12",
			})
			if err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("an edit naming %s: got %v, want a hard error naming the field", field, err)
			}
			if effective(t, dir)["T-002"].Value != "proposed" {
				t.Fatal("a refused edit still recorded a decision")
			}
		})
	}
}

func TestEditRejectsEmptyAndWeakMembers(t *testing.T) {
	cases := []struct{ name, payload, want string }{
		{"no members", `{}`, "edit names no field"},
		{"empty title", `{"title":"  "}`, "title must be non-empty"},
		{"empty steps", `{"steps":[]}`, "steps must be non-empty"},
		{"blank step", `{"steps":["ok"," "]}`, "step 2 must be non-empty"},
		{"empty expected", `{"expected":""}`, "expected must be non-empty"},
		{"empty observed", `{"observed":""}`, "observed must be non-empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseEdit(strings.NewReader(tc.payload)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestSingleDecisionErrors(t *testing.T) {
	dir := ingestThree(t)
	cases := []struct{ name, test, decision, want string }{
		{"unknown draft", "T-404", "accepted", "test draft T-404 not found"},
		{"bad decision", "T-001", "maybe", "invalid decision"},
		{"decision without test", "", "accepted", "-test is required"},
		{"test without decision", "T-001", "", "-decision is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := Review(ReviewOptions{Dir: dir, Test: tc.test, Decision: tc.decision, Out: &out, Today: "2026-09-12"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestSingleDecisionEditedRequiresEdit(t *testing.T) {
	dir := ingestThree(t)
	var out bytes.Buffer
	err := Review(ReviewOptions{Dir: dir, Test: "T-001", Decision: "edited", Out: &out, Today: "2026-09-12"})
	if err == nil || !strings.Contains(err.Error(), "-edit is required") {
		t.Fatalf("got %v, want an -edit requirement", err)
	}
}

// TestLastDecisionWinsOnDisk: a decision may be appended even when one already
// exists (append-only correction), and the later one wins.
func TestLastDecisionWinsOnDisk(t *testing.T) {
	dir := ingestThree(t)
	var out bytes.Buffer
	for _, d := range []string{"accepted", "rejected"} {
		if err := Review(ReviewOptions{Dir: dir, Test: "T-001", Decision: d, Out: &out, Today: "2026-09-12"}); err != nil {
			t.Fatalf("Review %s: %v", d, err)
		}
	}
	if got := effective(t, dir)["T-001"].Value; got != "rejected" {
		t.Fatalf("effective status %q, want rejected (the later decision)", got)
	}
	drafts, decisions, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(drafts) != 3 || len(decisions) != 2 {
		t.Fatalf("got %d drafts and %d decisions, want 3 and 2 (both retained)", len(drafts), len(decisions))
	}
}

// TestAppendDecisionRefusesWhenDraftChangedUnderTheLock is the
// decision-misattribution regression. `review -kind tests` snapshots the drafts
// once and then blocks on the operator; a concurrent `draft-tests -ingest` may
// truncate-and-rewrite in that gap (permitted until the first decision exists),
// and because draft ids restart at T-001 the decision would otherwise attach to a
// different draft.
func TestAppendDecisionRefusesWhenDraftChangedUnderTheLock(t *testing.T) {
	dir := ingestThree(t)
	drafts, _, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	shown := drafts[0]

	// A concurrent re-ingest replaces T-001 with a different draft under the same id.
	replacement := strings.Replace(with(t, "id", "T-001"), `"Saving gives no confirmation"`, `"Something else entirely"`, 1)
	if _, err := Ingest(dir, strings.NewReader(answer(replacement))); err != nil {
		t.Fatalf("re-ingest: %v", err)
	}

	rec := Decision{Kind: "decision", Test: "T-001", Decision: "accepted", At: "2026-09-12"}
	err = AppendDecision(dir, rec, &shown)
	if err == nil || !strings.Contains(err.Error(), "changed since review started") {
		t.Fatalf("got %v, want a changed-under-the-lock refusal", err)
	}
	if _, decisions, lerr := Load(dir); lerr != nil || len(decisions) != 0 {
		t.Fatalf("the refused decision was written anyway (%d decisions, err %v)", len(decisions), lerr)
	}
}

func TestAppendDecisionRefusesWhenDraftVanished(t *testing.T) {
	dir := ingestThree(t)
	drafts, _, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	shown := drafts[0]
	// A re-ingest that keeps only T-002 — T-001 is gone.
	if _, err := Ingest(dir, strings.NewReader(answer(with(t, "id", "T-002")))); err != nil {
		t.Fatalf("re-ingest: %v", err)
	}
	err = AppendDecision(dir, Decision{Kind: "decision", Test: "T-001", Decision: "accepted", At: "2026-09-12"}, &shown)
	if err == nil || !strings.Contains(err.Error(), "no longer in") {
		t.Fatalf("got %v, want a vanished-draft refusal", err)
	}
}

// TestAppendDecisionSanitisesDraftID: a draft id in an exchanged tests.jsonl is
// attacker-controlled and these errors reach the operator's terminal through
// cli.fail, so every id-bearing error path routes it through session.SafeText.
func TestAppendDecisionSanitisesDraftID(t *testing.T) {
	dir := ingestThree(t)
	evil := "\x1b]0;pwned\x07T-404"
	shown := draft(evil)
	err := AppendDecision(dir, Decision{Kind: "decision", Test: evil, Decision: "accepted", At: "2026-09-12"}, &shown)
	if err == nil {
		t.Fatal("want a refusal for an absent draft")
	}
	if strings.ContainsRune(err.Error(), '\x1b') || strings.ContainsRune(err.Error(), '\x07') {
		t.Fatalf("error carries raw terminal-control bytes from the draft id: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "T-404") {
		t.Fatalf("sanitised error dropped the id's printable tail: %q", err.Error())
	}
}

func TestAppendDecisionRefusesSymlink(t *testing.T) {
	dir := writeSession(t)
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("original\n"), 0o600); err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	if err := os.Symlink(outside, testsPath(dir)); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	err := AppendDecision(dir, Decision{Kind: "decision", Test: "T-001", Decision: "accepted", At: "2026-09-12"}, nil)
	if err == nil {
		t.Fatal("AppendDecision followed a symlink; want refusal")
	}
	if b, _ := os.ReadFile(outside); string(b) != "original\n" {
		t.Fatalf("victim file appended through symlink: %q", b)
	}
}

// --- the interactive walk --------------------------------------------------

func TestInteractiveGatedWhenNotTTY(t *testing.T) {
	dir := ingestThree(t)
	before := draftLines(t, dir)
	var out bytes.Buffer
	if err := Review(ReviewOptions{Dir: dir, In: strings.NewReader(""), Out: &out, IsTTY: false, Today: "2026-09-12"}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !strings.Contains(out.String(), "not a terminal") {
		t.Fatalf("expected a TTY-gating notice, got %q", out.String())
	}
	if _, decisions, _ := Load(dir); len(decisions) != 0 {
		t.Fatal("a gated walk recorded a decision")
	}
	if strings.Join(before, "\n") != strings.Join(draftLines(t, dir), "\n") {
		t.Fatal("a gated walk mutated the file")
	}
}

// TestInteractiveWalk covers a, e and r in one pass over three drafts, with the
// printed block asserted in full: the block is the surface the operator actually
// decides from.
func TestInteractiveWalk(t *testing.T) {
	dir := ingestThree(t)
	script := strings.Join([]string{
		"a",
		"e", "Saving a display name gives no confirmation", "Open #general.", "Click Save.", "",
		"New expectation.", "New observation.",
		"r",
		"",
	}, "\n")
	var out bytes.Buffer
	if err := Review(ReviewOptions{Dir: dir, In: strings.NewReader(script), Out: &out, IsTTY: true, Today: "2026-09-12"}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	eff := effective(t, dir)
	if eff["T-001"].Value != "accepted" || eff["T-002"].Value != "edited" || eff["T-003"].Value != "rejected" {
		t.Fatalf("statuses: %+v", eff)
	}
	got := out.String()
	for _, want := range []string{
		"(1/3) T-001 — from F-001 (bug, severity 3), [00:22]\n",
		"  Saving gives no confirmation\n",
		"  steps:\n    1. Open #general.\n    2. Click Save.\n",
		"  expected: The save is confirmed on screen.\n",
		"  observed: Nothing visibly changes.\n",
		"  “I clicked save and nothing happened”\n",
		"[a]ccept [e]dit [r]eject [s]kip [q]uit: ",
		"  recorded: T-001 accepted (2026-09-12)\n",
		"  recorded: T-002 edited (2026-09-12)\n",
		"  recorded: T-003 rejected (2026-09-12)\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the walk's output is missing %q:\n%s", want, got)
		}
	}
	st := eff["T-002"]
	if st.Edit == nil || st.Edit.Steps == nil || len(*st.Edit.Steps) != 2 || (*st.Edit.Steps)[1] != "Click Save." {
		t.Fatalf("the interactive edit did not capture the steps: %+v", st.Edit)
	}
	if st.Edit.Expected == nil || *st.Edit.Expected != "New expectation." {
		t.Fatalf("the interactive edit did not capture expected: %+v", st.Edit)
	}
	if strings.Contains(got, "(4/3)") {
		t.Fatal("the walk continued past its queue")
	}
}

// TestInteractiveEditBlankKeepsEachField: a blank answer keeps the current value,
// so an operator correcting one field does not have to retype the rest.
func TestInteractiveEditBlankKeepsEachField(t *testing.T) {
	dir := ingestThree(t)
	// title kept, steps kept (blank first line), expected replaced, observed kept.
	script := strings.Join([]string{"e", "", "", "Only the expectation changes.", "", "q", ""}, "\n")
	var out bytes.Buffer
	if err := Review(ReviewOptions{Dir: dir, In: strings.NewReader(script), Out: &out, IsTTY: true, Today: "2026-09-12"}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	st := effective(t, dir)["T-001"]
	if st.Value != "edited" || st.Edit == nil {
		t.Fatalf("T-001 status %+v, want edited", st)
	}
	if st.Edit.Title != nil || st.Edit.Steps != nil || st.Edit.Observed != nil {
		t.Fatalf("a blank answer replaced a field: %+v", st.Edit)
	}
	if st.Edit.Expected == nil || *st.Edit.Expected != "Only the expectation changes." {
		t.Fatalf("the replaced field did not survive: %+v", st.Edit)
	}
	// The prompt shows the current value, so the operator can see what blank keeps.
	if !strings.Contains(out.String(), "  title [Saving gives no confirmation]: ") {
		t.Fatalf("the edit prompt does not show the current title:\n%s", out.String())
	}
}

// TestInteractiveEditWithNoChangesRecordsAccepted: an "edited" decision with an
// empty edit records a change that did not happen, and is not representable — so
// a pass through the prompts that changed nothing is recorded as the acceptance
// it actually was.
func TestInteractiveEditWithNoChangesRecordsAccepted(t *testing.T) {
	dir := ingestThree(t)
	script := strings.Join([]string{"e", "", "", "", "", "q", ""}, "\n")
	var out bytes.Buffer
	if err := Review(ReviewOptions{Dir: dir, In: strings.NewReader(script), Out: &out, IsTTY: true, Today: "2026-09-12"}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !strings.Contains(out.String(), "  no changes; recorded as accepted.\n") {
		t.Fatalf("expected the no-changes notice:\n%s", out.String())
	}
	st := effective(t, dir)["T-001"]
	if st.Value != "accepted" || st.Edit != nil {
		t.Fatalf("T-001 status %+v, want accepted with no edit", st)
	}
}

func TestInteractiveSkipAndQuit(t *testing.T) {
	dir := ingestThree(t)
	var out bytes.Buffer
	// skip the first, quit on the second.
	if err := Review(ReviewOptions{Dir: dir, In: strings.NewReader("s\nq\n"), Out: &out, IsTTY: true, Today: "2026-09-12"}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !strings.Contains(out.String(), "  skipped.\n") {
		t.Fatalf("expected a skip notice:\n%s", out.String())
	}
	if _, decisions, _ := Load(dir); len(decisions) != 0 {
		t.Fatal("skip or quit recorded a decision")
	}
	if !strings.Contains(out.String(), "(2/3)") || strings.Contains(out.String(), "(3/3)") {
		t.Fatalf("quit did not stop the walk at the second draft:\n%s", out.String())
	}
}

func TestInteractiveUnrecognisedChoiceReprompts(t *testing.T) {
	dir := ingestThree(t)
	var out bytes.Buffer
	if err := Review(ReviewOptions{Dir: dir, In: strings.NewReader("x\nq\n"), Out: &out, IsTTY: true, Today: "2026-09-12"}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !strings.Contains(out.String(), `unrecognised choice "x"`) {
		t.Fatalf("expected an unrecognised-choice hint:\n%s", out.String())
	}
	if strings.Count(out.String(), "[a]ccept") < 2 {
		t.Fatalf("the walk did not re-prompt after an unrecognised choice:\n%s", out.String())
	}
}

func TestInteractiveEndOfInputStopsCleanly(t *testing.T) {
	dir := ingestThree(t)
	var out bytes.Buffer
	if err := Review(ReviewOptions{Dir: dir, In: strings.NewReader(""), Out: &out, IsTTY: true, Today: "2026-09-12"}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !strings.Contains(out.String(), "(end of input) stopping.") {
		t.Fatalf("expected an end-of-input notice:\n%s", out.String())
	}
}

func TestInteractiveNoProposedDrafts(t *testing.T) {
	dir := ingestThree(t)
	for _, id := range []string{"T-001", "T-002", "T-003"} {
		var out bytes.Buffer
		if err := Review(ReviewOptions{Dir: dir, Test: id, Decision: "rejected", Out: &out, Today: "2026-09-12"}); err != nil {
			t.Fatalf("Review %s: %v", id, err)
		}
	}
	var out bytes.Buffer
	if err := Review(ReviewOptions{Dir: dir, In: strings.NewReader("a\n"), Out: &out, IsTTY: true, Today: "2026-09-12"}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !strings.Contains(out.String(), "No proposed test drafts to review.") {
		t.Fatalf("expected the empty-queue notice:\n%s", out.String())
	}
}

func TestReviewHintsIngestWhenTestsMissing(t *testing.T) {
	dir := writeSession(t)
	err := Review(ReviewOptions{Dir: dir, In: strings.NewReader(""), Out: io.Discard, IsTTY: true, Today: "2026-09-12"})
	if err == nil || !strings.Contains(err.Error(), "testimony draft-tests -ingest") {
		t.Fatalf("got %v, want a draft-tests -ingest hint", err)
	}
}

// TestReviewRefusesNonexistentSessionDir: a wrong -session path names the actual
// problem rather than sending the operator to run an ingest that would fail
// identically one command later.
func TestReviewRefusesNonexistentSessionDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	err := Review(ReviewOptions{Dir: dir, Test: "T-001", Decision: "accepted", Out: io.Discard, Today: "2026-09-12"})
	if err == nil {
		t.Fatal("want an error for a nonexistent session directory")
	}
	if strings.Contains(err.Error(), "draft-tests -ingest") {
		t.Fatalf("a missing session directory must not be reported as an un-ingested one: %v", err)
	}
	if !strings.Contains(err.Error(), dir) {
		t.Fatalf("error must name the session directory: %v", err)
	}
}

// TestPrintDraftSanitisesAndPlaceholders: a draft in a downloaded session is
// untrusted and ParseRecords validates none of its text, so every field is routed
// through session.SafeText and a field that renders as nothing falls through to a
// placeholder rather than printing as a blank.
func TestPrintDraftSanitisesAndPlaceholders(t *testing.T) {
	var out bytes.Buffer
	d := Draft{
		ID: "T-001", Finding: "F-001", Severity: 3,
		Title:    "\x1b]0;pwned\x07hijacked",
		Steps:    []string{"\u200b"},
		Expected: "  ",
		Observed: "",
		Status:   "proposed",
	}
	printDraft(&out, d, nil)
	got := out.String()
	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\x07') {
		t.Fatalf("printDraft emitted raw terminal-control bytes: %q", got)
	}
	for _, want := range []string{"    1. —\n", "  expected: no expected behaviour\n", "  observed: no observed behaviour\n", "  “no quote”\n", "(—, severity 3), [--:--]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("printDraft is missing %q:\n%s", want, got)
		}
	}
}

// TestEditRoundTripsThroughJSON pins the decision record's shape on disk, since
// it is the schema the session-directory reference documents.
func TestEditRoundTripsThroughJSON(t *testing.T) {
	steps := []string{"Open #general.", "Click Save."}
	rec := Decision{Kind: "decision", Test: "T-003", Decision: "edited", At: "2026-09-12",
		Edit: &Edit{Title: ptr("Saving a display name gives no confirmation"), Steps: &steps}}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"kind":"decision","test":"T-003","decision":"edited","at":"2026-09-12","edit":{"title":"Saving a display name gives no confirmation","steps":["Open #general.","Click Save."]}}`
	if string(b) != want {
		t.Fatalf("decision record:\n got %s\nwant %s", b, want)
	}
}

// TestInteractiveEditRefusesTooManySteps: the steps prompt reads to the
// terminating blank line whatever the count, so an over-long list is reported
// against the limit rather than silently truncated at 32 with the rest of the
// operator's typing consumed as the next prompt's answer.
func TestInteractiveEditRefusesTooManySteps(t *testing.T) {
	// The count in the message is what the operator typed, not where the slice
	// stopped growing: the prompt reads to the terminating blank line whatever the
	// count but holds the slice one past the cap, so reporting len(steps) told
	// someone who typed forty steps that they had listed 33.
	for _, typed := range []int{maxSteps + 1, 40} {
		t.Run(fmt.Sprintf("%d steps", typed), func(t *testing.T) {
			dir := ingestThree(t)
			lines := []string{"e", ""} // edit, then a blank title (keep it)
			for i := 0; i < typed; i++ {
				lines = append(lines, fmt.Sprintf("Step %d.", i+1))
			}
			lines = append(lines, "", "", "") // end the steps, then blank expected and observed
			var out bytes.Buffer
			if err := Review(ReviewOptions{Dir: dir, In: strings.NewReader(strings.Join(lines, "\n") + "\n"), Out: &out, IsTTY: true, Today: "2026-09-12"}); err != nil {
				t.Fatalf("Review: %v", err)
			}
			want := fmt.Sprintf("steps lists %d entries, exceeding the limit of %d", typed, maxSteps)
			if !strings.Contains(out.String(), want) {
				t.Fatalf("expected the over-long steps refusal (%q):\n%s", want, out.String())
			}
			if _, decisions, _ := Load(dir); len(decisions) != 0 {
				t.Fatal("a refused edit recorded a decision")
			}
		})
	}
}

// TestSingleDecisionRefusesEditOutsideEdited: an edit carried alongside
// "accepted" or "rejected" would be silently discarded, so it is refused. The CLI
// refuses it at the usage status too; the check lives here as well so the refusal
// is a property of the API rather than of one caller's invariants.
func TestSingleDecisionRefusesEditOutsideEdited(t *testing.T) {
	dir := ingestThree(t)
	var out bytes.Buffer
	err := Review(ReviewOptions{
		Dir: dir, Test: "T-001", Decision: "accepted",
		EditIn: strings.NewReader(`{"title":"sneaky"}`), Out: &out, Today: "2026-09-12",
	})
	if err == nil || !strings.Contains(err.Error(), "-edit applies only to -decision edited") {
		t.Fatalf("got %v, want a refusal naming the flag pairing", err)
	}
	if _, decisions, _ := Load(dir); len(decisions) != 0 {
		t.Fatal("a refused decision was recorded")
	}
}
