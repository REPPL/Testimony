package coderefs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/REPPL/Testimony/internal/analyze"
	"github.com/REPPL/Testimony/internal/session"
	"github.com/REPPL/Testimony/internal/timeline"
)

const samplePath = "../../examples/sample-session"

// sampleSession copies the bundled sample's records into a scratch directory
// and merges its timeline there, so the golden test never depends on the
// generated timeline.jsonl being present in the checkout.
func sampleSession(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{session.ManifestFile, session.FindingsFile, session.TranscriptFile, session.InteractionsFile, session.RefsFile} {
		b, err := os.ReadFile(filepath.Join(samplePath, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if _, _, err := timeline.Merge(dir); err != nil {
		t.Fatalf("merge: %v", err)
	}
	return dir
}

// TestRenderGoldenFromSampleSession is AC3, pinned byte for byte: the issue
// draft carries the title, the steps from the window, the quote, and the
// suspected files with their current status.
func TestRenderGoldenFromSampleSession(t *testing.T) {
	got, err := Render(sampleSession(t))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := string(fixture(t, "issues.md"))
	if got != want {
		t.Fatalf("rendered draft does not match testdata/issues.md:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	for _, s := range []string{
		"## F-001 — bug: I clicked save and nothing happened",
		"> “I clicked save and nothing happened”",
		"1. Open `#general`.",
		"4. Click `[data-testid=save-btn]`.",
		"- `src/settings/ProfileForm.tsx:46` (owner) — accepted 2026-09-16",
		"- `src/settings/saveProfile.ts:12` (handler) — proposed",
	} {
		if !strings.Contains(got, s) {
			t.Errorf("render lacks %q", s)
		}
	}
}

// Review does not gate the render: a proposed-only refs.jsonl renders, with the
// reference visibly unreviewed.
func TestRenderDoesNotWaitForReview(t *testing.T) {
	dir := writeSession(t)
	if _, err := Ingest(dir, repoPath, strings.NewReader(answer(goodRef))); err != nil {
		t.Fatal(err)
	}
	got, err := Render(dir)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(got, "(owner) — proposed") || !strings.Contains(got, "0 of 1 references accepted") {
		t.Fatalf("a proposed reference is not shown as such:\n%s", got)
	}
}

// Steps stop at the finding's t: ev-004, the second save click after the
// utterance, is not a step. A finding with no preceding event says so.
func TestRenderStepsEndAtTheFinding(t *testing.T) {
	dir := writeSession(t)
	if _, err := Ingest(dir, repoPath, strings.NewReader(answer(goodRef))); err != nil {
		t.Fatal(err)
	}
	got, err := Render(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, "Click `[data-testid=save-btn]`.") != 1 {
		t.Fatalf("the save click after the finding became a step:\n%s", got)
	}
	// F-007 is anchored on a route with one click event (ev-005) at its t.
	r7 := strings.Replace(strings.Replace(goodRef, `"F-001"`, `"F-007"`, 1), `"R-001"`, `"R-002"`, 1)
	r7 = strings.Replace(r7, "ProfileForm.tsx", "../routes.ts", 1)
	r7 = strings.Replace(r7, `"line":46`, `"line":3`, 1)
	r7 = strings.Replace(r7, `"owner"`, `"route"`, 1)
	r7 = strings.Replace(r7, "src/settings/../routes.ts", "src/routes.ts", 1)
	os.Remove(refsPath(dir))
	if _, err := Ingest(dir, repoPath, strings.NewReader(answer(goodRef, r7))); err != nil {
		t.Fatal(err)
	}
	got, err = Render(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "## F-007 — friction: Where did the appearance tab go") || !strings.Contains(got, "`src/routes.ts:3` (route)") {
		t.Fatalf("F-007 block missing:\n%s", got)
	}
	if !strings.Contains(got, "**Anchor** `#appearance` ·") {
		t.Fatalf("route-only anchor renders wrongly:\n%s", got)
	}
}

func TestRenderRefusesWithNoMappedFinding(t *testing.T) {
	dir := writeSession(t)
	refs := `{"id":"R-001","finding":"F-999","session":"fixture-session","path":"a.ts","role":"owner","status":"proposed"}` + "\n"
	if err := os.WriteFile(refsPath(dir), []byte(refs), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Render(dir)
	if !errors.Is(err, ErrNoMappedFindings) {
		t.Fatalf("err = %v", err)
	}
}

func TestRenderHintsMissingArtefacts(t *testing.T) {
	dir := writeSession(t)
	if _, err := Render(dir); err == nil || !strings.Contains(err.Error(), "map -ingest") {
		t.Fatalf("missing refs: %v", err)
	}
	if _, err := Ingest(dir, repoPath, strings.NewReader(answer(goodRef))); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, session.TimelineFile))
	if _, err := Render(dir); err == nil || !strings.Contains(err.Error(), "merge") {
		t.Fatalf("missing timeline: %v", err)
	}
	os.Remove(filepath.Join(dir, session.FindingsFile))
	if _, err := Render(dir); err == nil || !strings.Contains(err.Error(), "analyze -ingest") {
		t.Fatalf("missing findings: %v", err)
	}
}

// The render neither opens nor writes the repository: it works with the
// repository gone.
func TestRenderNeedsNoRepository(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "a.ts"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := writeSession(t)
	if _, err := Ingest(dir, repo, strings.NewReader(answer(strings.Replace(strings.Replace(goodRef, "src/settings/ProfileForm.tsx", "a.ts", 1), `"line":46`, `"line":1`, 1)))); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(repo)
	if _, err := Render(dir); err != nil {
		t.Fatalf("render with the repository gone: %v", err)
	}
}

func TestRenderEscapesUntrustedText(t *testing.T) {
	fnd := `{"id":"F-001","t":22,"type":"bug","severity":3,"mode":"A","quote":"[x](http://h) hi","evidence":["utt-004"],"ui":{"selector":"[data-testid=a]` + "`" + `b","route":"#g"},"status":"unverified"}` + "\n" +
		`{"kind":"verdict","finding":"F-001","verdict":"confirmed","at":"2026-09-12"}` + "\n"
	dir := writeSession(t, session.FindingsFile, fnd)
	if _, err := Ingest(dir, repoPath, strings.NewReader(answer(goodRef))); err != nil {
		t.Fatal(err)
	}
	got, err := Render(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "[x](http://h)") {
		t.Fatal("an active link survived in the quote")
	}
	if strings.Contains(got, "`[data-testid=a]`b`") {
		t.Fatal("a backtick closed the anchor's code span early")
	}
}

// An event's kind is attacker-authorable and is the one value the step verb is
// built from: it must be escaped like every other value, and capitalised by
// rune so a non-ASCII kind cannot corrupt the document's encoding.
func TestRenderEscapesAndEncodesEventKind(t *testing.T) {
	tl := `{"t":20,"src":"event","id":"ev-003","payload":{"kind":"![x](http://h/b.png)","route":"#general"}}` + "\n" +
		`{"t":21,"src":"event","id":"ev-004","payload":{"kind":"émettre","selector":"[data-testid=a]"}}` + "\n" +
		`{"t":22,"src":"speech","id":"utt-004","payload":{"speaker":"P1","t1":28,"text":"x"}}` + "\n"
	dir := writeSession(t, session.TimelineFile, tl)
	if _, err := Ingest(dir, repoPath, strings.NewReader(answer(goodRef))); err != nil {
		t.Fatal(err)
	}
	got, err := Render(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "![x](http://h/b.png)") {
		t.Fatalf("an image beacon in an event kind survived:\n%s", got)
	}
	if !utf8.ValidString(got) {
		t.Fatal("the render is not valid UTF-8")
	}
	if !strings.Contains(got, "Émettre `[data-testid=a]`.") {
		t.Fatalf("non-ASCII kind not capitalised by rune:\n%s", got)
	}
}

func TestTitleCapsAndClauses(t *testing.T) {
	long := strings.Repeat("word ", 40)
	f := fixtureFinding("bug", long)
	if got := title(f); len([]rune(got)) > maxTitleRunes || !strings.HasSuffix(got, "…") {
		t.Fatalf("title = %q", got)
	}
	if got := title(fixtureFinding("", "First clause, second clause.")); got != "finding: First clause" {
		t.Fatalf("title = %q", got)
	}
	if got := title(fixtureFinding("idea", "")); got != "idea" {
		t.Fatalf("title = %q", got)
	}
	if got := title(fixtureFinding("bug", ".NET crashed, twice")); got != "bug: NET crashed" {
		t.Fatalf("title = %q", got)
	}
}

func fixtureFinding(typ, quote string) analyze.Finding {
	return analyze.Finding{ID: "F-001", Type: typ, Quote: quote}
}
