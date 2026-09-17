package coderefs

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ingestTwo lays a session whose refs.jsonl holds two proposed references for
// F-001, which is what lets the walk exercise both decisions.
func ingestTwo(t *testing.T) string {
	t.Helper()
	dir := writeSession(t)
	second := strings.Replace(strings.Replace(goodRef, `"R-001"`, `"R-002"`, 1), "ProfileForm.tsx\",\"line\":46", "saveProfile.ts\",\"line\":12", 1)
	second = strings.Replace(second, `"owner"`, `"handler"`, 1)
	if _, err := Ingest(dir, repoPath, strings.NewReader(answer(goodRef, second))); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	return dir
}

// refLines returns only the reference (non-decision) lines.
func refLines(t *testing.T, dir string) []string {
	t.Helper()
	b, err := os.ReadFile(refsPath(dir))
	if err != nil {
		t.Fatal(err)
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
	refs, decisions, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return EffectiveStatus(refs, decisions)
}

// TestDecisionIsAppendedAndRefLinesUnchanged is AC4, asserted byte for byte: a
// decision is a new line, the reference line is never rewritten, and after two
// decisions the reference's finding, session, and path are exactly what ingest
// wrote.
func TestDecisionIsAppendedAndRefLinesUnchanged(t *testing.T) {
	dir := ingestTwo(t)
	before := refLines(t, dir)
	var out bytes.Buffer
	for _, d := range []string{"rejected", "accepted"} {
		if err := Review(ReviewOptions{Dir: dir, Ref: "R-001", Decision: d, Out: &out, Today: "2026-09-16"}); err != nil {
			t.Fatalf("Review %s: %v", d, err)
		}
	}
	after := refLines(t, dir)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("reference lines changed:\nbefore %v\nafter  %v", before, after)
	}
	if st := effective(t, dir)["R-001"]; st.Value != "accepted" || st.At != "2026-09-16" {
		t.Fatalf("R-001 = %+v, want the last decision", st)
	}
	if !strings.Contains(out.String(), "recorded: R-001 rejected (2026-09-16)") || !strings.Contains(out.String(), "recorded: R-001 accepted (2026-09-16)") {
		t.Fatalf("output = %q", out.String())
	}
	b, _ := os.ReadFile(refsPath(dir))
	if !strings.HasSuffix(string(b), `{"kind":"decision","ref":"R-001","decision":"accepted","at":"2026-09-16"}`+"\n") {
		t.Fatalf("refs.jsonl tail = %q", b)
	}
}

func TestSingleDecisionErrors(t *testing.T) {
	dir := ingestTwo(t)
	for name, o := range map[string]ReviewOptions{
		"ref without decision":        {Ref: "R-001"},
		"decision without ref":        {Decision: "accepted"},
		"edited is not allowed":       {Ref: "R-001", Decision: "edited"},
		"unknown reference":           {Ref: "R-009", Decision: "accepted"},
		"decision out of enum":        {Ref: "R-001", Decision: "maybe"},
		"repo with a single decision": {Ref: "R-001", Decision: "accepted", Repo: repoPath},
	} {
		o.Dir, o.Out, o.Today = dir, &bytes.Buffer{}, "2026-09-16"
		if err := Review(o); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := Review(ReviewOptions{Dir: dir + "/missing", Ref: "R-001", Decision: "accepted", Out: &bytes.Buffer{}}); err == nil || !strings.Contains(err.Error(), "session directory") {
		t.Fatalf("missing dir: %v", err)
	}
	if err := Review(ReviewOptions{Dir: writeSession(t), Ref: "R-001", Decision: "accepted", Out: &bytes.Buffer{}}); err == nil || !strings.Contains(err.Error(), "map -ingest") {
		t.Fatalf("no refs.jsonl: %v", err)
	}
}

// The decision is bound to the reference the operator was shown: a re-ingest
// that slid a different reference under the same id is refused under the lock.
func TestAppendDecisionRefusesWhenRefChangedUnderTheLock(t *testing.T) {
	dir := ingestTwo(t)
	shown := ref("R-001")
	shown.Line = 45 // not what is on disk
	err := AppendDecision(dir, Decision{Kind: "decision", Ref: "R-001", Decision: "accepted", At: "2026-09-16"}, &shown)
	if err == nil || !strings.Contains(err.Error(), "changed since review started") {
		t.Fatalf("err = %v", err)
	}
	gone := ref("R-009")
	err = AppendDecision(dir, Decision{Kind: "decision", Ref: "R-009", Decision: "accepted", At: "2026-09-16"}, &gone)
	if err == nil || !strings.Contains(err.Error(), "no longer in") {
		t.Fatalf("err = %v", err)
	}
	if len(refLines(t, dir)) != 2 || len(effective(t, dir)) != 2 {
		t.Fatal("a refused decision changed the file")
	}
}

func TestInteractiveGatedWhenNotTTY(t *testing.T) {
	dir := ingestTwo(t)
	var out bytes.Buffer
	if err := Review(ReviewOptions{Dir: dir, In: strings.NewReader("a\n"), Out: &out, IsTTY: false, Today: "2026-09-16"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "stdin is not a terminal") {
		t.Fatalf("output = %q", out.String())
	}
	if st := effective(t, dir)["R-001"].Value; st != "proposed" {
		t.Fatal("a non-TTY run recorded a decision")
	}
}

// TestInteractiveWalk: accept, then an unrecognised key re-prompts, then reject;
// the walk shows the finding's quote and anchor and, with the repository given,
// the source around the line.
func TestInteractiveWalk(t *testing.T) {
	dir := ingestTwo(t)
	var out bytes.Buffer
	err := Review(ReviewOptions{Dir: dir, Repo: repoPath, In: strings.NewReader("a\nx\nr\n"), Out: &out, IsTTY: true, Today: "2026-09-16"})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	o := out.String()
	for _, want := range []string{
		"(1/2) R-001 — src/settings/ProfileForm.tsx:46 (owner), from F-001",
		"“I clicked save and nothing happened”",
		"anchor: [data-testid=save-btn] on #general [00:22]",
		`>   46  ` + "      <button data-testid=\"save-btn\"",
		"unrecognised choice \"x\"",
		"(2/2) R-002 — src/settings/saveProfile.ts:12 (handler), from F-001",
		">   12    await fetch(",
		"recorded: R-002 rejected (2026-09-16)",
	} {
		if !strings.Contains(o, want) {
			t.Errorf("walk output lacks %q:\n%s", want, o)
		}
	}
	eff := effective(t, dir)
	if eff["R-001"].Value != "accepted" || eff["R-002"].Value != "rejected" {
		t.Fatalf("effective = %+v", eff)
	}
}

func TestInteractiveSkipQuitAndEndOfInput(t *testing.T) {
	dir := ingestTwo(t)
	var out bytes.Buffer
	if err := Review(ReviewOptions{Dir: dir, In: strings.NewReader("s\nq\n"), Out: &out, IsTTY: true, Today: "2026-09-16"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "skipped.") {
		t.Fatalf("output = %q", out.String())
	}
	for id, st := range effective(t, dir) {
		if st.Value != "proposed" {
			t.Fatalf("%s = %+v after skip and quit", id, st)
		}
	}
	out.Reset()
	if err := Review(ReviewOptions{Dir: dir, In: strings.NewReader(""), Out: &out, IsTTY: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "(end of input) stopping.") {
		t.Fatalf("output = %q", out.String())
	}
	// Nothing left to review once both are decided.
	for _, id := range []string{"R-001", "R-002"} {
		if err := Review(ReviewOptions{Dir: dir, Ref: id, Decision: "accepted", Out: &out, Today: "2026-09-16"}); err != nil {
			t.Fatal(err)
		}
	}
	out.Reset()
	if err := Review(ReviewOptions{Dir: dir, In: strings.NewReader("a\n"), Out: &out, IsTTY: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No proposed references to review.") {
		t.Fatalf("output = %q", out.String())
	}
}

// The snippet re-checks the path through the ingest containment rule, so a
// hand-edited refs.jsonl cannot make the walk open a file outside the
// repository, whether by a lexical escape or through a symlinked directory that
// points outside; the walk degrades to a note and still takes the decision.
func TestSnippetHonoursContainment(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s3cret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repo, "vendor")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	dir := writeSession(t)
	refs := `{"id":"R-001","finding":"F-001","session":"fixture-session","path":"../../../../etc/hosts","line":1,"role":"owner","status":"proposed"}` + "\n" +
		`{"id":"R-002","finding":"F-001","session":"fixture-session","path":"vendor/secret.txt","line":1,"role":"owner","status":"proposed"}` + "\n"
	if err := os.WriteFile(refsPath(dir), []byte(refs), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Review(ReviewOptions{Dir: dir, Repo: repo, In: strings.NewReader("r\nr\n"), Out: &out, IsTTY: true, Today: "2026-09-16"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"(source unavailable: path must not contain a . or .. segment)",
		"(source unavailable: path resolves outside the repository (through a symlinked directory))",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "s3cret") {
		t.Fatal("the walk showed the file outside the repository")
	}
	eff := effective(t, dir)
	if eff["R-001"].Value != "rejected" || eff["R-002"].Value != "rejected" {
		t.Fatalf("decisions = %+v", eff)
	}
}
