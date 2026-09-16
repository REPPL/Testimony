package coderefs

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

// goodRef is a schema-clean reference for F-001, the fixture's first eligible
// finding, into the fixture repository. The failure table mutates one rule at a
// time from it.
const goodRef = `{"id":"R-001","finding":"F-001","session":"fixture-session",` +
	`"path":"src/settings/ProfileForm.tsx","line":46,"role":"owner"}`

func answer(refs ...string) string {
	return `{"rubric":"testimony-coderefs/v1","refs":[` + strings.Join(refs, ",") + `]}`
}

// with returns goodRef with one field replaced (or, with a "-" prefix, removed).
func with(t *testing.T, field string, value any) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(goodRef), &m); err != nil {
		t.Fatalf("unmarshal goodRef: %v", err)
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

func refsPath(dir string) string { return filepath.Join(dir, session.RefsFile) }

// TestIngestGood is the happy path: a clean reference validates, lands in
// refs.jsonl, and is forced to "proposed" even though the answer claimed
// "accepted".
func TestIngestGood(t *testing.T) {
	dir := writeSession(t)
	refs, err := Ingest(dir, repoPath, strings.NewReader(answer(with(t, "status", "accepted"))))
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(refs) != 1 || refs[0].Status != "proposed" || refs[0].Line != 46 {
		t.Fatalf("refs = %+v", refs)
	}
	b, err := os.ReadFile(refsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"R-001","finding":"F-001","session":"fixture-session","path":"src/settings/ProfileForm.tsx","line":46,"role":"owner","status":"proposed"}` + "\n"
	if string(b) != want {
		t.Fatalf("refs.jsonl = %q, want %q", b, want)
	}
}

// A reference may name the file alone; an absent line is not a 0 line.
func TestIngestLineIsOptional(t *testing.T) {
	dir := writeSession(t)
	refs, err := Ingest(dir, repoPath, strings.NewReader(answer(with(t, "-line", nil))))
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	b, _ := os.ReadFile(refsPath(dir))
	if strings.Contains(string(b), `"line"`) || refs[0].Line != 0 {
		t.Fatalf("an absent line was written: %s", b)
	}
}

// The record carries the path the check ran on: SafeText-stripped and trimmed,
// so stray whitespace or an invisible character in the answer cannot land a
// path in refs.jsonl that no editor opens and that the boundary never saw.
func TestIngestStoresTheCheckedPath(t *testing.T) {
	for _, raw := range []string{"  src/settings/ProfileForm.tsx  ", "src/settings/Profile\u200bForm.tsx"} {
		dir := writeSession(t)
		refs, err := Ingest(dir, repoPath, strings.NewReader(answer(with(t, "path", raw))))
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		if refs[0].Path != "src/settings/ProfileForm.tsx" {
			t.Fatalf("%q: stored path %q", raw, refs[0].Path)
		}
		b, _ := os.ReadFile(refsPath(dir))
		if !strings.Contains(string(b), `"path":"src/settings/ProfileForm.tsx"`) {
			t.Fatalf("%q: refs.jsonl = %s", raw, b)
		}
	}
}

// The reference count is bounded before any reference is checked, because
// each check reads the repository.
func TestIngestCapsReferenceCount(t *testing.T) {
	dir := writeSession(t)
	refs := make([]string, maxRefs+1)
	for i := range refs {
		refs[i] = with(t, "id", fmt.Sprintf("R-%03d", i%1000))
	}
	_, err := Ingest(dir, repoPath, strings.NewReader(answer(refs...)))
	if err == nil || !strings.Contains(err.Error(), "exceeding the limit of 1000") {
		t.Fatalf("err = %v", err)
	}
	if _, serr := os.Stat(refsPath(dir)); !errors.Is(serr, os.ErrNotExist) {
		t.Fatal("an over-long answer wrote refs.jsonl")
	}
}

func TestIngestBareArrayAccepted(t *testing.T) {
	dir := writeSession(t)
	if _, err := Ingest(dir, repoPath, strings.NewReader("["+goodRef+"]")); err != nil {
		t.Fatalf("bare array: %v", err)
	}
}

// TestIngestValidationFailures is AC2's rule table: each case breaks exactly one
// rule and must be refused, naming the reference and the field.
func TestIngestValidationFailures(t *testing.T) {
	cases := []struct {
		name  string
		field string
		value any
		want  string
	}{
		{"bad id", "id", "R-1", `id "R-1" must match`},
		{"unverified finding", "finding", "F-002", `finding "F-002" is not a confirmed finding with a selector or route`},
		{"rejected finding", "finding", "F-003", `finding "F-003" is not a confirmed`},
		{"mode B finding", "finding", "F-004", `finding "F-004" is not a confirmed`},
		{"duplicate finding", "finding", "F-005", `finding "F-005" is not a confirmed`},
		{"anchorless finding", "finding", "F-006", `finding "F-006" is not a confirmed finding with a selector or route`},
		{"wrong session", "session", "other", `session "other" is not this session`},
		{"bad role", "role", "maybe", `role "maybe" must be one of owner|handler|route|test`},
		{"missing role", "-role", nil, `role "" must be one of`},
		{"empty path", "path", "", "path must be non-empty"},
		{"parent segment", "path", "../secret.txt", "must not contain a . or .. segment"},
		{"dot segment", "path", "./src/routes.ts", "must not contain a . or .. segment"},
		{"absolute path", "path", "/etc/hosts", "must be repo-relative, not absolute"},
		{"drive path", "path", "C:/x.ts", "must be repo-relative, not absolute"},
		{"backslash", "path", `src\routes.ts`, "must use forward slashes"},
		{"empty segment", "path", "src//routes.ts", "has an empty segment"},
		{"missing file", "path", "src/nope.ts", "does not exist under the repository"},
		{"directory", "path", "src/settings", "is not a regular file"},
		{"line zero", "line", 0, "line 0 must be at least 1"},
		{"line past end", "line", 999, "line 999 is past the end of src/settings/ProfileForm.tsx (51 lines)"},
		{"line on short file", "path", "src/routes.ts", "line 46 is past the end of src/routes.ts (5 lines)"},
		{"colon in a file name is not a drive", "path", "src/a:b.ts", "does not exist under the repository"},
		{"unknown field confidence", "confidence", "high", `unknown field "confidence"`},
		{"unknown field snippet", "snippet", "x", `unknown field "snippet"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := writeSession(t)
			_, err := Ingest(dir, repoPath, strings.NewReader(answer(with(t, c.field, c.value))))
			if err == nil {
				t.Fatalf("accepted")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %q, want it to contain %q", err, c.want)
			}
			if _, serr := os.Stat(refsPath(dir)); !errors.Is(serr, os.ErrNotExist) {
				t.Fatal("a refused answer wrote refs.jsonl")
			}
		})
	}
}

// A symlink at the final component is refused as not a regular file rather than
// followed, and a path through a symlinked directory pointing outside the
// repository is refused as escaping it.
func TestIngestRefusesSymlinks(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "real.ts"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(repo, "real.ts"), filepath.Join(repo, "link.ts")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "vendor")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	dir := writeSession(t)
	_, err := Ingest(dir, repo, strings.NewReader(answer(with(t, "path", "link.ts"))))
	if err == nil || !strings.Contains(err.Error(), "is not a regular file") {
		t.Fatalf("symlinked file: err = %v", err)
	}
	_, err = Ingest(dir, repo, strings.NewReader(answer(with(t, "path", "vendor/secret.txt"))))
	if err == nil || !strings.Contains(err.Error(), "through a symlinked directory") {
		t.Fatalf("symlinked directory: err = %v", err)
	}
	// The real file, through no symlink, is fine: the repository itself may sit
	// under a symlinked temp root (macOS /var → /private/var) and must still pass.
	real := strings.Replace(with(t, "path", "real.ts"), `"line":46`, `"line":2`, 1)
	if _, err := Ingest(dir, repo, strings.NewReader(answer(real))); err != nil {
		t.Fatalf("a regular file under a symlinked temp root: %v", err)
	}
}

// A line is counted on an unterminated last line too, and a file past the
// read bound is refused for a line check rather than counted.
func TestIngestLineCountRules(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "u.ts"), []byte("a\nb"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := writeSession(t)
	twoLines := with(t, "path", "u.ts")
	if _, err := Ingest(dir, repo, strings.NewReader(answer(strings.Replace(twoLines, `"line":46`, `"line":2`, 1)))); err != nil {
		t.Fatalf("line 2 of an unterminated two-line file: %v", err)
	}
	os.Remove(refsPath(dir))
	if _, err := Ingest(dir, repo, strings.NewReader(answer(strings.Replace(twoLines, `"line":46`, `"line":3`, 1)))); err == nil || !strings.Contains(err.Error(), "(2 lines)") {
		t.Fatalf("line 3: err = %v", err)
	}
	big, err := os.Create(filepath.Join(repo, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := big.Truncate(session.MaxJSONLBytes + 1); err != nil {
		t.Fatal(err)
	}
	big.Close()
	_, err = Ingest(dir, repo, strings.NewReader(answer(strings.Replace(with(t, "path", "big.bin"), `"line":46`, `"line":1`, 1))))
	if err == nil || !strings.Contains(err.Error(), "the bound for a line check") {
		t.Fatalf("oversized file: err = %v", err)
	}
	// Without a line the same file is referenceable.
	noLine := strings.Replace(with(t, "-line", nil), "src/settings/ProfileForm.tsx", "big.bin", 1)
	if _, err := Ingest(dir, repo, strings.NewReader(answer(noLine))); err != nil {
		t.Fatalf("oversized file without a line: %v", err)
	}
}

func TestIngestRejectsDuplicateID(t *testing.T) {
	dir := writeSession(t)
	_, err := Ingest(dir, repoPath, strings.NewReader(answer(goodRef, goodRef)))
	if err == nil || !strings.Contains(err.Error(), "duplicate id (first seen at reference #1)") {
		t.Fatalf("err = %v", err)
	}
}

// TestIngestIsTransactional is AC2's "refused as a whole": a second, good
// reference in the same answer is not written because the first failed.
func TestIngestIsTransactional(t *testing.T) {
	dir := writeSession(t)
	bad := with(t, "path", "src/nope.ts")
	good := with(t, "id", "R-002")
	_, err := Ingest(dir, repoPath, strings.NewReader(answer(bad, good)))
	if err == nil {
		t.Fatal("accepted")
	}
	if _, serr := os.Stat(refsPath(dir)); !errors.Is(serr, os.ErrNotExist) {
		t.Fatal("a partly-bad answer wrote refs.jsonl")
	}
	// Every error is reported at once: the finding rule and the path rule both.
	twoBad := strings.Replace(with(t, "path", "src/nope.ts"), `"F-001"`, `"F-002"`, 1)
	_, err = Ingest(dir, repoPath, strings.NewReader(answer(twoBad)))
	if err == nil || !strings.Contains(err.Error(), "does not exist") || !strings.Contains(err.Error(), "is not a confirmed") {
		t.Fatalf("errors are not exhaustive: %v", err)
	}
}

func TestIngestLabelsUndecodableNeighbourByAnswerPosition(t *testing.T) {
	dir := writeSession(t)
	_, err := Ingest(dir, repoPath, strings.NewReader(answer("null", with(t, "id", "bad"))))
	if err == nil || !strings.Contains(err.Error(), "reference #2: id \"bad\"") {
		t.Fatalf("err = %v", err)
	}
}

func TestIngestRefusesEmptyAnswer(t *testing.T) {
	dir := writeSession(t)
	// Seed a good file, then confirm an empty answer cannot erase it.
	if _, err := Ingest(dir, repoPath, strings.NewReader(answer(goodRef))); err != nil {
		t.Fatal(err)
	}
	for _, empty := range []string{"[]", `{"refs":[]}`, "", "   "} {
		if _, err := Ingest(dir, repoPath, strings.NewReader(empty)); err == nil {
			t.Errorf("%q: accepted", empty)
		}
	}
	b, _ := os.ReadFile(refsPath(dir))
	if !strings.Contains(string(b), "R-001") {
		t.Fatal("an empty answer erased refs.jsonl")
	}
}

func TestIngestUnknownRubric(t *testing.T) {
	dir := writeSession(t)
	_, err := Ingest(dir, repoPath, strings.NewReader(`{"rubric":"testimony-coderefs/v9","refs":[`+goodRef+`]}`))
	if err == nil || !strings.Contains(err.Error(), "unknown rubric") {
		t.Fatalf("err = %v", err)
	}
}

// TestIngestRefusesWithNoMappableFinding is AC5's ingest half, and fires before
// the answer is read.
func TestIngestRefusesWithNoMappableFinding(t *testing.T) {
	fnd := `{"id":"F-001","t":22,"type":"bug","severity":3,"quote":"x","evidence":["utt-004"],"ui":{"route":"#g"},"status":"unverified"}` + "\n"
	dir := writeSession(t, session.FindingsFile, fnd)
	_, err := Ingest(dir, repoPath, strings.NewReader("this is not even json"))
	if !errors.Is(err, ErrNoMappableFindings) {
		t.Fatalf("err = %v", err)
	}
}

func TestIngestRefusesBadRepo(t *testing.T) {
	dir := writeSession(t)
	if _, err := Ingest(dir, filepath.Join(t.TempDir(), "missing"), strings.NewReader(answer(goodRef))); err == nil {
		t.Fatal("a missing repository was accepted")
	}
	if _, err := Ingest(dir, filepath.Join(repoPath, "src/routes.ts"), strings.NewReader(answer(goodRef))); err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("a file as repository: %v", err)
	}
}

// TestIngestRefusesOverwriteWithDecisions is AC4's guard: once a decision exists
// the machine record cannot be replaced under it.
func TestIngestRefusesOverwriteWithDecisions(t *testing.T) {
	dir := writeSession(t)
	if _, err := Ingest(dir, repoPath, strings.NewReader(answer(goodRef))); err != nil {
		t.Fatal(err)
	}
	if err := AppendDecision(dir, Decision{Kind: "decision", Ref: "R-001", Decision: "accepted", At: "2026-09-16"}, nil); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(refsPath(dir))
	_, err := Ingest(dir, repoPath, strings.NewReader(answer(with(t, "line", 12))))
	if err == nil || !strings.Contains(err.Error(), "already holds decision records") {
		t.Fatalf("err = %v", err)
	}
	after, _ := os.ReadFile(refsPath(dir))
	if string(before) != string(after) {
		t.Fatal("a refused re-ingest changed refs.jsonl")
	}
	// A foreign decision value counts too.
	dir2 := writeSession(t)
	if _, err := Ingest(dir2, repoPath, strings.NewReader(answer(goodRef))); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(refsPath(dir2), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`{"kind":"decision","ref":"R-001","decision":"maybe","at":"2026-09-16"}` + "\n")
	f.Close()
	if _, err := Ingest(dir2, repoPath, strings.NewReader(answer(goodRef))); err == nil {
		t.Fatal("a foreign decision did not protect the file")
	}
}

func TestIngestRejectsOversizedAnswer(t *testing.T) {
	dir := writeSession(t)
	r := strings.NewReader("[" + strings.Repeat(" ", session.MaxAnswerBytes) + "]")
	if _, err := Ingest(dir, repoPath, r); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v", err)
	}
}

// The bundled sample's references, re-ingested against the fixture repository
// they point into, come back byte for byte. The sample is documentation, and
// documentation that would not survive the tool's own boundary is wrong.
func TestSampleRefsJSONLPassesIngest(t *testing.T) {
	const sample = "../../examples/sample-session"
	refs, decisions, err := Load(sample)
	if err != nil {
		t.Fatalf("Load the bundled sample: %v", err)
	}
	if len(refs) != 2 || len(decisions) != 1 {
		t.Fatalf("the sample holds %d refs and %d decisions, want 2 and 1", len(refs), len(decisions))
	}
	dir := t.TempDir()
	for _, name := range []string{session.ManifestFile, session.FindingsFile} {
		b, rerr := os.ReadFile(filepath.Join(sample, name))
		if rerr != nil {
			t.Fatal(rerr)
		}
		if werr := os.WriteFile(filepath.Join(dir, name), b, 0o644); werr != nil {
			t.Fatal(werr)
		}
	}
	var lines []string
	for _, r := range refs {
		b, merr := json.Marshal(r)
		if merr != nil {
			t.Fatal(merr)
		}
		lines = append(lines, string(b))
	}
	if _, err := Ingest(dir, repoPath, strings.NewReader(answer(lines...))); err != nil {
		t.Fatalf("the bundled sample's references do not pass Ingest: %v", err)
	}
	written, _ := os.ReadFile(refsPath(dir))
	sampleBytes, _ := os.ReadFile(filepath.Join(sample, session.RefsFile))
	wantPrefix := strings.Join(strings.Split(strings.TrimRight(string(sampleBytes), "\n"), "\n")[:2], "\n") + "\n"
	if string(written) != wantPrefix {
		t.Fatalf("re-ingesting the sample's references does not reproduce them:\n got %q\nwant %q", written, wantPrefix)
	}
}
