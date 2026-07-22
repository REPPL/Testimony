package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
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
	if err := AppendVerdict(dir, rec, nil); err != nil {
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

// TestVerifyTargetErrorSanitisesFindingID is the terminal-injection regression for
// verifyTarget's error strings. A finding id in a hand-authored or exchanged
// findings.jsonl is attacker-controlled; review SafeTexts it at every other terminal
// sink (printFinding, describe), but the verifyTarget/AppendVerdict error strings
// embedded it raw via %s, and cli.fail prints those to stderr — so an ESC-bearing id
// drove ANSI escape sequences into the operator's terminal. Every finding-id error
// path must route the id through session.SafeText. Here the verdict targets an id not
// present in the file (cur == nil), firing the "no longer in" error.
func TestVerifyTargetErrorSanitisesFindingID(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, session.FindingsFile), []byte(findingsFixture), 0o644); err != nil {
		t.Fatalf("write findings: %v", err)
	}
	// An id carrying an ESC (0x1b) and a bell (0x07) — an ANSI/OSC sequence — that is
	// not present in the file, so verifyTarget takes its cur==nil branch.
	evilID := "\x1b]0;pwned\x07F-404"
	rec := analyze.Verdict{Kind: "verdict", Finding: evilID, Verdict: "confirmed", At: "2026-07-17"}
	expect := &analyze.Finding{ID: evilID, T: 1, Type: "bug", Severity: 3, Quote: "x", Evidence: []string{"utt-001"}}
	err := AppendVerdict(dir, rec, expect)
	if err == nil {
		t.Fatal("AppendVerdict accepted a verdict for an absent finding; want a mismatch error")
	}
	if strings.ContainsRune(err.Error(), '\x1b') || strings.ContainsRune(err.Error(), '\x07') {
		t.Fatalf("verifyTarget error carries raw terminal-control bytes from the finding id: %q", err.Error())
	}
	// The id's printable tail still identifies the finding to the operator.
	if !strings.Contains(err.Error(), "F-404") {
		t.Fatalf("sanitised error dropped the finding id entirely: %q", err.Error())
	}
}

// TestReviewErrorPathsSanitiseFindingIDs pins every remaining finding-id error
// sink this package sanitises — checkTargets (both ids), AppendVerdict's
// oversized-line refusal, and verifyTarget's identity-changed and
// duplicate-target-gone branches. TestVerifyTargetErrorSanitisesFindingID
// covers only the cur==nil branch; each branch here reverted to a raw %s
// independently while the whole suite stayed green, and the verifyTarget pair
// carry ids sourced straight from an attacker-authored findings.jsonl through
// review's normal flow.
func TestReviewErrorPathsSanitiseFindingIDs(t *testing.T) {
	const evilPrefix = "\x1b]0;pwned\x07"
	assertClean := func(t *testing.T, err error, wantSub string) {
		t.Helper()
		if err == nil {
			t.Fatal("want an error carrying the sanitised id, got nil")
		}
		if strings.ContainsRune(err.Error(), '\x1b') || strings.ContainsRune(err.Error(), '\x07') {
			t.Fatalf("error carries raw terminal-control bytes: %q", err.Error())
		}
		if !strings.Contains(err.Error(), wantSub) {
			t.Fatalf("sanitised error dropped the id's printable tail %q: %q", wantSub, err.Error())
		}
	}

	t.Run("checkTargets finding not found", func(t *testing.T) {
		findings := []analyze.Finding{{ID: "F-001"}}
		assertClean(t, checkTargets(findings, evilPrefix+"F-404", "confirmed", ""), "F-404")
	})

	t.Run("checkTargets duplicate target not found", func(t *testing.T) {
		findings := []analyze.Finding{{ID: "F-001"}}
		assertClean(t, checkTargets(findings, "F-001", "duplicate", evilPrefix+"F-405"), "F-405")
	})

	t.Run("AppendVerdict oversized line", func(t *testing.T) {
		v := analyze.Verdict{Kind: "verdict", Finding: evilPrefix + "F-001", Verdict: "confirmed",
			At: strings.Repeat("x", session.MaxJSONLLine)}
		assertClean(t, AppendVerdict(t.TempDir(), v, nil), "F-001")
	})

	t.Run("verifyTarget identity changed", func(t *testing.T) {
		dir := t.TempDir()
		evilID := evilPrefix + "F-001"
		line := `{"id":"` + "\\u001b]0;pwned\\u0007" + `F-001","t":22,"type":"bug","severity":3,"quote":"a","evidence":["utt-004"],"status":"unverified"}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, session.FindingsFile), []byte(line), 0o644); err != nil {
			t.Fatalf("write findings: %v", err)
		}
		rec := analyze.Verdict{Kind: "verdict", Finding: evilID, Verdict: "confirmed", At: "2026-07-22"}
		// The snapshot differs in quote: the finding was rewritten since review started.
		expect := &analyze.Finding{ID: evilID, T: 22, Type: "bug", Severity: 3, Quote: "different", Evidence: []string{"utt-004"}}
		assertClean(t, AppendVerdict(dir, rec, expect), "F-001")
	})

	t.Run("verifyTarget duplicate target gone", func(t *testing.T) {
		dir := t.TempDir()
		line := `{"id":"F-001","t":22,"type":"bug","severity":3,"quote":"a","evidence":["utt-004"],"status":"unverified"}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, session.FindingsFile), []byte(line), 0o644); err != nil {
			t.Fatalf("write findings: %v", err)
		}
		rec := analyze.Verdict{Kind: "verdict", Finding: "F-001", Verdict: "duplicate", Of: evilPrefix + "F-999", At: "2026-07-22"}
		expect := &analyze.Finding{ID: "F-001", T: 22, Type: "bug", Severity: 3, Quote: "a", Evidence: []string{"utt-004"}}
		assertClean(t, AppendVerdict(dir, rec, expect), "F-999")
	})
}

// shortWriteFile is a verdictFile whose Write persists a prefix and then errors,
// standing in for a full disk (write(2) fills the remaining space, returns a
// short count, and the next write returns ENOSPC — os.File.Write persists the
// truncated prefix before returning the error). Seek always reports the current
// length, so it also stands in for the O_APPEND descriptor writeVerdict holds.
type shortWriteFile struct {
	buf  []byte
	fail bool // when true, Write keeps only a prefix then returns an error
}

func (f *shortWriteFile) Seek(offset int64, whence int) (int64, error) {
	return int64(len(f.buf)), nil
}

func (f *shortWriteFile) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(f.buf)) {
		return 0, io.EOF
	}
	return copy(p, f.buf[off:]), nil
}

func (f *shortWriteFile) Truncate(size int64) error {
	f.buf = f.buf[:size]
	return nil
}

func (f *shortWriteFile) Write(p []byte) (int, error) {
	if f.fail {
		half := len(p) / 2
		f.buf = append(f.buf, p[:half]...)
		return half, errors.New("no space left on device")
	}
	f.buf = append(f.buf, p...)
	return len(p), nil
}

// TestWriteVerdictRollsBackPartialWrite is the ENOSPC regression on the verdict
// path: a short write that persists a newline-less prefix must be truncated
// away, so findings.jsonl never retains a partial line that would fuse with the
// next verdict into one malformed physical record and make the whole file — the
// human verdict record the package exists to protect — unparseable to every
// reader. Pre-fix AppendVerdict wrote directly with no rollback, so the prefix
// survived, exactly as demo.appendLines did before its own fix.
func TestWriteVerdictRollsBackPartialWrite(t *testing.T) {
	f := &shortWriteFile{}
	first, err := json.Marshal(analyze.Verdict{Kind: "verdict", Finding: "F-001", Verdict: "confirmed", At: "2026-07-17"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := writeVerdict(f, append(first, '\n')); err != nil {
		t.Fatalf("first verdict: %v", err)
	}
	good := string(f.buf)

	f.fail = true
	second, err := json.Marshal(analyze.Verdict{Kind: "verdict", Finding: "F-002", Verdict: "rejected", At: "2026-07-17"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := writeVerdict(f, append(second, '\n')); err == nil {
		t.Fatalf("expected a write error on a full disk")
	}
	if string(f.buf) != good {
		t.Fatalf("partial line survived: file is %q, want the clean prefix %q", f.buf, good)
	}
	if !strings.HasSuffix(string(f.buf), "\n") {
		t.Fatalf("file does not end on a newline: %q", f.buf)
	}

	// The rolled-back file still parses one record per line, so a later verdict
	// lands cleanly rather than fusing onto a fragment.
	f.fail = false
	if err := writeVerdict(f, append(second, '\n')); err != nil {
		t.Fatalf("verdict after rollback: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(f.buf), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), f.buf)
	}
	for i, l := range lines {
		var v analyze.Verdict
		if err := json.Unmarshal([]byte(l), &v); err != nil {
			t.Fatalf("line %d is not one JSON record: %v (%q)", i+1, err, l)
		}
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
	err := AppendVerdict(dir, analyze.Verdict{Kind: "verdict", Finding: "F-001", Verdict: "confirmed", At: "2026-07-17"}, nil)
	if err == nil {
		t.Fatal("AppendVerdict followed a symlink; want refusal")
	}
	if b, _ := os.ReadFile(outside); string(b) != "original\n" {
		t.Fatalf("victim file appended through symlink: %q", b)
	}
}

// TestPrintFindingRendersNegativeSessionRelativeTimes is the clamped-clock
// regression, the sibling of the one fixed in internal/report. A recording
// whose creation_time predates the manifest t0 yields a negative offset, and
// analyze.indexTimeline admits findings anchored there. Pre-fix review.clock
// clamped every negative time to zero, so the review prompt stamped a pre-t0
// finding as [00:00] — the wrong moment, shown on the surface where the analyst
// actually records the verdict.
func TestPrintFindingRendersNegativeSessionRelativeTimes(t *testing.T) {
	f := analyze.Finding{
		ID: "F-001", Type: "bug", Severity: 3, T: -90,
		Quote: "before the clock started",
		UI:    &analyze.UI{Selector: "btn", Route: "#x"},
	}
	var buf bytes.Buffer
	printFinding(&buf, f)
	if !strings.Contains(buf.String(), "[-01:30]") {
		t.Fatalf("review prompt missing signed clock [-01:30]:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "[00:00]") {
		t.Fatalf("a pre-t0 finding time was clamped to zero:\n%s", buf.String())
	}
}

// TestClockRoundsSymmetrically guards the sign-splitting arithmetic in clock: a
// negative time must round by magnitude the way its positive twin does, so the
// digits never disagree across the sign and a time a fraction of a second
// before t0 never prints the nonsense "-00:00". The expectations match
// report.clock deliberately — the two helpers are byte-identical, and if a
// third caller ever needs one they belong in a shared home (a small formatting
// helper in internal/session, alongside SafeText) rather than a third copy.
func TestClockRoundsSymmetrically(t *testing.T) {
	for _, tc := range []struct {
		sec  float64
		want string
	}{
		{0, "00:00"},
		{-0.4, "00:00"},
		{-0.6, "-00:01"},
		{61.5, "01:02"},
		{-61.5, "-01:02"},
		{-90, "-01:30"},
		{-3600, "-60:00"},
		// Out-of-range and non-finite times (from a hand-authored findings.jsonl
		// f.T that analyze.Load does not bound) must render a placeholder, never an
		// implementation-defined out-of-range int conversion. Sibling of
		// report.TestClockRefusesOutOfRangeTime.
		{1e300, "--:--"},
		{-1e300, "--:--"},
		{math.Inf(1), "--:--"},
		{math.NaN(), "--:--"},
	} {
		if got := clock(tc.sec); got != tc.want {
			t.Fatalf("clock(%g) = %q, want %q", tc.sec, got, tc.want)
		}
	}
}

// TestAppendVerdictRefusesOversizedLine is the write-side twin of
// analyze.oversizedFindings. Every JSONL reader scans with a MaxJSONLLine-capped
// buffer, so a verdict line past that cap is durably unreadable and bricks the
// whole findings.jsonl — the verdict history this package exists to protect — on
// the next Load/review/report. A finding id can arrive just under the cap from an
// exchanged or hand-edited file (the finding line loads; the verdict's framing
// tips it over). AppendVerdict must refuse rather than append. Pre-fix it wrote
// the line unchecked.
func TestAppendVerdictRefusesOversizedLine(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, session.FindingsFile), []byte(findingsFixture), 0o644); err != nil {
		t.Fatalf("write findings: %v", err)
	}
	hugeID := "F-" + strings.Repeat("9", session.MaxJSONLLine)
	rec := analyze.Verdict{Kind: "verdict", Finding: hugeID, Verdict: "confirmed", At: "2026-07-17"}
	err := AppendVerdict(dir, rec, nil)
	if err == nil || !strings.Contains(err.Error(), "line limit") {
		t.Fatalf("expected an over-limit refusal, got %v", err)
	}
	// The refusal is pre-write: the existing findings.jsonl is byte-unchanged.
	b, rerr := os.ReadFile(filepath.Join(dir, session.FindingsFile))
	if rerr != nil {
		t.Fatalf("read findings: %v", rerr)
	}
	if string(b) != findingsFixture {
		t.Fatalf("findings.jsonl was modified despite the refusal")
	}
}

// TestAppendVerdictRefusesReingestedFinding is the verdict-misattribution
// regression. review.Run snapshots findings once and then blocks on the operator
// for the whole interactive walk; a concurrent `analyze -ingest` may
// truncate-and-rewrite findings.jsonl in that gap (permitted until the first
// verdict exists), and because finding ids restart at F-001 the verdict the
// analyst gives to the finding they were shown would otherwise attach to a
// different finding now holding the same id — silent corruption of the human
// decision record. AppendVerdict now re-reads under its lock and refuses when the
// targeted id no longer names the finding that was judged. Pre-fix (no re-check)
// the verdict was appended and misattributed.
func TestAppendVerdictRefusesReingestedFinding(t *testing.T) {
	dir := writeSession(t)
	path := filepath.Join(dir, session.FindingsFile)

	// The finding the analyst was shown (F-001 from the fixture).
	shown := analyze.Finding{
		ID: "F-001", T: 22, Type: "bug", Severity: 3,
		Quote:    "I clicked save and nothing happened",
		Evidence: []string{"utt-004", "ev-003"},
		UI:       &analyze.UI{Selector: "[data-testid=save-btn]", Route: "#general"},
		Status:   "unverified",
	}

	// A concurrent re-ingest rewrote findings.jsonl: F-001 now names a DIFFERENT
	// finding (same id, different content), exactly what commitFindings permits
	// before any verdict exists.
	const reingested = `{"id":"F-001","t":48,"type":"friction","severity":2,"quote":"The menu ordering confused me","evidence":["utt-009"],"status":"unverified"}
`
	if err := os.WriteFile(path, []byte(reingested), 0o644); err != nil {
		t.Fatalf("rewrite findings: %v", err)
	}

	rec := analyze.Verdict{Kind: "verdict", Finding: "F-001", Verdict: "confirmed", At: "2026-07-17"}
	err := AppendVerdict(dir, rec, &shown)
	if err == nil || !strings.Contains(err.Error(), "changed since review started") {
		t.Fatalf("expected a changed-finding refusal, got %v", err)
	}
	// No verdict was written: the re-ingested file is byte-unchanged, so the
	// analyst's confirmation cannot be silently pinned to the wrong finding.
	b, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("read findings: %v", rerr)
	}
	if string(b) != reingested {
		t.Fatalf("findings.jsonl was modified despite the refusal:\n%s", b)
	}
}

// TestAppendVerdictAcceptsUnchangedFinding is the positive control: when the
// finding still matches what was judged, the verdict is recorded normally, so
// the new guard does not block the ordinary path.
func TestAppendVerdictAcceptsUnchangedFinding(t *testing.T) {
	dir := writeSession(t)
	shown := analyze.Finding{
		ID: "F-001", T: 22, Type: "bug", Severity: 3,
		Quote:    "I clicked save and nothing happened",
		Evidence: []string{"utt-004", "ev-003"},
		UI:       &analyze.UI{Selector: "[data-testid=save-btn]", Route: "#general"},
		Status:   "unverified",
	}
	rec := analyze.Verdict{Kind: "verdict", Finding: "F-001", Verdict: "confirmed", At: "2026-07-17"}
	if err := AppendVerdict(dir, rec, &shown); err != nil {
		t.Fatalf("AppendVerdict on an unchanged finding: %v", err)
	}
	_, verdicts, err := analyze.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(verdicts) != 1 || verdicts[0].Finding != "F-001" || verdicts[0].Verdict != "confirmed" {
		t.Fatalf("verdict not recorded as expected: %+v", verdicts)
	}
}
