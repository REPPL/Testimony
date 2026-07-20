package review

import (
	"bytes"
	"encoding/json"
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

// TestDescribeSanitisesID is the verdict-echo regression: after the analyst
// confirms/rejects a finding, describe() prints the recorded verdict — whose
// Finding id comes from the (unvalidated) loaded findings.jsonl — to the
// terminal. Pre-fix those fields were formatted raw, so an ESC in the id
// reached the terminal even though printFinding had sanitised the same id.
func TestDescribeSanitisesID(t *testing.T) {
	v := analyze.Verdict{Finding: "F-001\x1b[2Jspoofed", Verdict: "confirmed", At: "2026-01-01"}
	if strings.ContainsRune(describe(v), 0x1b) {
		t.Fatalf("ESC byte from the verdict id reached the echo: %q", describe(v))
	}
	d := analyze.Verdict{Finding: "F-002\x1b[31m", Verdict: "duplicate", Of: "F-001\x1b[0m", At: "2026-01-01"}
	if strings.ContainsRune(describe(d), 0x1b) {
		t.Fatalf("ESC byte from a duplicate verdict reached the echo: %q", describe(d))
	}
}

// TestPrintFindingSanitisesID is the id-channel terminal-injection regression:
// analyze.Load does not validate a finding's id (only ingest does), so a
// downloaded session can carry an ESC in the id itself. Pre-fix f.ID was
// written with a raw %s while its siblings were sanitised.
func TestPrintFindingSanitisesID(t *testing.T) {
	f := analyze.Finding{
		ID: "F-001\x1b[2J\x1b[1;1Hspoofed", Type: "bug", Severity: 3, T: 1,
		Quote: "ok",
	}
	var buf bytes.Buffer
	printFinding(&buf, f)
	if bytes.ContainsRune(buf.Bytes(), 0x1b) {
		t.Fatalf("ESC byte from the id reached the terminal: %q", buf.String())
	}
}

// TestInteractiveWalkFailsWhenVerdictCannotBePersisted is the lost-verdict
// regression: an AppendVerdict I/O failure used to be returned through the same
// channel as an invalid-keystroke validation error, so the walk printed it as a
// retry hint, swallowed it with `continue`, and `testimony review` exited 0 —
// leaving the analyst believing Alice's confirmed finding was on the record when
// nothing had reached disk. The walk must now abort and surface the error.
func TestInteractiveWalkFailsWhenVerdictCannotBePersisted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: file permissions do not deny writes")
	}
	dir := writeSession(t)
	path := filepath.Join(dir, session.FindingsFile)
	// Read-only findings.jsonl: analyze.Load still succeeds, but the append
	// cannot open the file for writing.
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	var out bytes.Buffer
	err := Run(Options{Dir: dir, In: strings.NewReader("c\n"), Out: &out, IsTTY: true, Today: "2026-07-17"})
	if err == nil {
		t.Fatalf("Run returned nil after a failed verdict append; output was %q", out.String())
	}
	if !strings.Contains(err.Error(), "recording the verdict failed") {
		t.Fatalf("error does not name the persistence failure: %v", err)
	}
}

// TestAppendVerdictTerminatesAnUnterminatedLastLine is the file-fusing
// regression: a findings.jsonl whose final line lacks its trailing newline —
// hand edited, externally produced, or truncated by an earlier crash — used to
// have the verdict concatenated straight onto it, yielding one physical line
// with two JSON objects and rendering the whole file unparseable for every
// reader. The append must start a fresh line instead.
func TestAppendVerdictTerminatesAnUnterminatedLastLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, session.FindingsFile)
	unterminated := strings.TrimRight(findingsFixture, "\n")
	if err := os.WriteFile(path, []byte(unterminated), 0o644); err != nil {
		t.Fatalf("write findings: %v", err)
	}

	rec := analyze.Verdict{Kind: "verdict", Finding: "F-003", Verdict: "confirmed", At: "2026-07-17"}
	if err := AppendVerdict(dir, rec); err != nil {
		t.Fatalf("AppendVerdict: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4 (3 findings + 1 verdict): %q", len(lines), string(b))
	}
	for i, l := range lines {
		var v any
		if err := json.Unmarshal([]byte(l), &v); err != nil {
			t.Fatalf("line %d is not one JSON record: %v (%q)", i+1, err, l)
		}
	}

	// The verdict is still readable through the normal loader.
	_, verdicts, err := analyze.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(verdicts) != 1 || verdicts[0].Finding != "F-003" {
		t.Fatalf("verdicts: %+v, want one for F-003", verdicts)
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
