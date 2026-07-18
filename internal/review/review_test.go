package review

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/REPPL/Testimony/internal/analyze"
	"github.com/REPPL/Testimony/internal/session"
)

const findingsFixture = `{"id":"F-001","t":22,"type":"bug","severity":3,"quote":"I clicked save and nothing happened","evidence":["utt-004","ev-003"],"ui":{"selector":"[data-testid=save-btn]","route":"#general"},"status":"unverified"}
{"id":"F-002","t":38,"type":"preference","severity":2,"quote":"I like this dark mode toggle","evidence":["utt-006"],"status":"unverified"}
{"id":"F-003","t":48,"type":"friction","severity":2,"quote":"The label just says dark mode","evidence":["utt-007"],"status":"unverified"}
`

func writeSession(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, session.FindingsFile), []byte(findingsFixture), 0o644); err != nil {
		t.Fatalf("write findings: %v", err)
	}
	return dir
}

// findingLines returns only the finding (non-verdict) lines, to assert the
// append-only property.
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

func TestParseVerdictFlag(t *testing.T) {
	cases := []struct {
		in, verdict, of string
		wantErr         bool
	}{
		{"confirmed", "confirmed", "", false},
		{"rejected", "rejected", "", false},
		{"duplicate-of-F-002", "duplicate", "F-002", false},
		{"duplicate-of-F-2", "", "", true},
		{"maybe", "", "", true},
	}
	for _, tc := range cases {
		v, of, err := ParseVerdictFlag(tc.in)
		if (err != nil) != tc.wantErr {
			t.Fatalf("%q: err=%v wantErr=%v", tc.in, err, tc.wantErr)
		}
		if err == nil && (v != tc.verdict || of != tc.of) {
			t.Fatalf("%q: got (%q,%q), want (%q,%q)", tc.in, v, of, tc.verdict, tc.of)
		}
	}
}

func TestNonInteractiveConfirm(t *testing.T) {
	dir := writeSession(t)
	before := findingLines(t, dir)
	var out bytes.Buffer
	err := Run(Options{Dir: dir, Finding: "F-001", Verdict: "confirmed", Out: &out, Today: "2026-07-17"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	findings, verdicts, err := analyze.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if analyze.EffectiveStatus(findings, verdicts)["F-001"].Value != "confirmed" {
		t.Fatalf("F-001 not confirmed")
	}
	// Append-only: the finding lines are byte-unchanged.
	after := findingLines(t, dir)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("finding lines changed after verdict:\nbefore %q\nafter %q", before, after)
	}
}

func TestNonInteractiveDuplicate(t *testing.T) {
	dir := writeSession(t)
	var out bytes.Buffer
	if err := Run(Options{Dir: dir, Finding: "F-002", Verdict: "duplicate-of-F-001", Out: &out, Today: "2026-07-17"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	findings, verdicts, _ := analyze.Load(dir)
	st := analyze.EffectiveStatus(findings, verdicts)["F-002"]
	if st.Value != "duplicate" || st.Of != "F-001" {
		t.Fatalf("F-002 status: %+v, want duplicate of F-001", st)
	}
}

func TestNonInteractiveErrors(t *testing.T) {
	dir := writeSession(t)
	cases := []struct {
		finding, verdict, want string
	}{
		{"F-099", "confirmed", "not found"},
		{"F-001", "duplicate-of-F-001", "duplicate of itself"},
		{"F-001", "duplicate-of-F-088", "target F-088 not found"},
		{"F-001", "bogus", "invalid verdict"},
	}
	for _, tc := range cases {
		var out bytes.Buffer
		err := Run(Options{Dir: dir, Finding: tc.finding, Verdict: tc.verdict, Out: &out, Today: "2026-07-17"})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("finding=%s verdict=%s: got %v, want error containing %q", tc.finding, tc.verdict, err, tc.want)
		}
	}
}

func TestInteractiveGatedWhenNotTTY(t *testing.T) {
	dir := writeSession(t)
	before := findingLines(t, dir)
	var out bytes.Buffer
	// No -finding/-verdict, IsTTY false: must skip cleanly and write nothing.
	if err := Run(Options{Dir: dir, In: strings.NewReader(""), Out: &out, IsTTY: false, Today: "2026-07-17"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "not a terminal") {
		t.Fatalf("expected a TTY-gating notice, got %q", out.String())
	}
	_, verdicts, _ := analyze.Load(dir)
	if len(verdicts) != 0 {
		t.Fatalf("gated review wrote %d verdicts, want 0", len(verdicts))
	}
	if strings.Join(before, "\n") != strings.Join(findingLines(t, dir), "\n") {
		t.Fatalf("gated review mutated the file")
	}
}

func TestInteractiveWalk(t *testing.T) {
	dir := writeSession(t)
	// F-001 confirm, F-002 duplicate of F-001, F-003 reject.
	script := "c\nd\nF-001\nr\n"
	var out bytes.Buffer
	if err := Run(Options{Dir: dir, In: strings.NewReader(script), Out: &out, IsTTY: true, Today: "2026-07-17"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	findings, verdicts, _ := analyze.Load(dir)
	eff := analyze.EffectiveStatus(findings, verdicts)
	if eff["F-001"].Value != "confirmed" {
		t.Fatalf("F-001: %+v", eff["F-001"])
	}
	if eff["F-002"].Value != "duplicate" || eff["F-002"].Of != "F-001" {
		t.Fatalf("F-002: %+v", eff["F-002"])
	}
	if eff["F-003"].Value != "rejected" {
		t.Fatalf("F-003: %+v", eff["F-003"])
	}
}

// TestInteractiveDuplicateTargetMustExist proves the interactive duplicate
// branch rejects a target finding that does not exist, matching the
// non-interactive path (which errors with "duplicate target ... not found").
// A rejected target re-prompts the same finding rather than persisting a
// verdict that cites a nonexistent finding.
func TestInteractiveDuplicateTargetMustExist(t *testing.T) {
	dir := writeSession(t)
	// F-001: try to mark it a duplicate of the nonexistent F-099 (must be
	// refused and re-prompt), then skip it; skip the rest.
	script := "d\nF-099\ns\ns\ns\n"
	var out bytes.Buffer
	if err := Run(Options{Dir: dir, In: strings.NewReader(script), Out: &out, IsTTY: true, Today: "2026-07-17"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "duplicate target F-099 not found") {
		t.Fatalf("expected a not-found notice for F-099, got %q", out.String())
	}
	_, verdicts, _ := analyze.Load(dir)
	if len(verdicts) != 0 {
		t.Fatalf("bad duplicate target wrote %d verdicts, want 0", len(verdicts))
	}
}

func TestInteractiveQuitStops(t *testing.T) {
	dir := writeSession(t)
	var out bytes.Buffer
	if err := Run(Options{Dir: dir, In: strings.NewReader("q\n"), Out: &out, IsTTY: true, Today: "2026-07-17"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	_, verdicts, _ := analyze.Load(dir)
	if len(verdicts) != 0 {
		t.Fatalf("quit-first wrote %d verdicts, want 0", len(verdicts))
	}
}

// TestPrintFindingSanitisesANSI is the terminal-injection regression: a
// finding's quote/anchor come from an untrusted (downloaded) session, so an
// embedded ESC must not reach the analyst's terminal. Pre-fix f.Quote was
// written with a raw %s.
func TestPrintFindingSanitisesANSI(t *testing.T) {
	f := analyze.Finding{
		ID: "F-001", Type: "bug", Severity: 3, T: 1,
		Quote: "danger \x1b[2J\x1b[31mspoofed",
		UI:    &analyze.UI{Selector: "btn\x1b]0;pwn\a", Route: "#x"},
	}
	var buf bytes.Buffer
	printFinding(&buf, f)
	if bytes.ContainsRune(buf.Bytes(), 0x1b) {
		t.Fatalf("ESC byte reached the terminal output: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "spoofed") {
		t.Fatalf("legitimate quote text should remain: %q", buf.String())
	}
}

// TestAppendVerdictRefusesSymlink is the arbitrary-file-append regression: a
// findings.jsonl planted as a symlink must not be followed.
func TestAppendVerdictRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("original\n"), 0o600); err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, session.FindingsFile)); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	err := AppendVerdict(dir, analyze.Verdict{Kind: "verdict", Finding: "F-001", Verdict: "confirmed", At: "2026-07-17"})
	if err == nil {
		t.Fatal("AppendVerdict followed a symlink; want refusal")
	}
	if b, _ := os.ReadFile(outside); string(b) != "original\n" {
		t.Fatalf("victim file appended through symlink: %q", b)
	}
}
