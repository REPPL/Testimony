package report

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/REPPL/Testimony/internal/analyze"
	"github.com/REPPL/Testimony/internal/review"
	"github.com/REPPL/Testimony/internal/session"
)

const timelineFixture = `{"t":22,"src":"speech","id":"utt-004","payload":{"speaker":"P1","t1":28,"text":"Hm. I clicked save and nothing happened. No message."}}
{"t":19.2,"src":"event","id":"ev-003","payload":{"kind":"click","route":"#general","selector":"[data-testid=save-btn]","text":"Save"}}
{"t":38,"src":"speech","id":"utt-006","payload":{"speaker":"P1","t1":45,"text":"Oh, I like this dark mode toggle. This is how the save button should feel."}}
`

const answerFixture = `{"rubric":"testimony-analysis/v1","findings":[
 {"id":"F-001","t":22,"type":"bug","severity":3,"quote":"I clicked save and nothing happened","evidence":["utt-004","ev-003"],"ui":{"selector":"[data-testid=save-btn]","route":"#general"}},
 {"id":"F-002","t":38,"type":"preference","severity":2,"quote":"I like this dark mode toggle","evidence":["utt-006"]}
]}`

func setupSession(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "fixture", App: "settings prototype", Participant: "P1"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(timelineFixture), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}
	return dir
}

// TestRoundTrip exercises the golden path: ingest → review (two verdicts) →
// report, asserting the append-only property and every status group.
func TestRoundTrip(t *testing.T) {
	dir := setupSession(t)

	if _, err := analyze.Ingest(dir, strings.NewReader(answerFixture)); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	findingsBefore := findingLines(t, dir)

	// Two verdicts non-interactively: confirm F-001, mark F-002 a duplicate.
	if err := review.Run(review.Options{Dir: dir, Finding: "F-001", Verdict: "confirmed", Out: &discard{}, Today: "2026-07-17"}); err != nil {
		t.Fatalf("review confirm: %v", err)
	}
	if err := review.Run(review.Options{Dir: dir, Finding: "F-002", Verdict: "duplicate-of-F-001", Out: &discard{}, Today: "2026-07-17"}); err != nil {
		t.Fatalf("review duplicate: %v", err)
	}

	// Append-only: the original finding lines are byte-unchanged.
	if before, after := strings.Join(findingsBefore, "\n"), strings.Join(findingLines(t, dir), "\n"); before != after {
		t.Fatalf("finding lines changed after review:\nbefore %q\nafter %q", before, after)
	}

	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"## Findings",
		"### Confirmed (1)",
		"### Unverified (0)",
		"### Duplicate (1)",
		"### Rejected (0)",
		"**F-001** bug",
		"confirmed (2026-07-17)",
		"duplicate of F-001 (2026-07-17)",
		"`[data-testid=save-btn]` #general",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("report missing %q\n---\n%s", want, md)
		}
	}
}

func TestReportNoFindings(t *testing.T) {
	dir := setupSession(t)
	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(md, "No findings yet") {
		t.Fatalf("expected an absence notice, got:\n%s", md)
	}
}

// TestReportKeepsFindingWithUnknownVerdict is the vanishing-finding regression.
// findings.jsonl is a shared/hand-editable artefact; a verdict line carrying a
// value outside the closed enum (here a "confirm" typo) must not push its finding
// into an unrendered status group. The finding must still appear, falling back to
// Unverified, rather than disappearing from the report entirely.
func TestReportKeepsFindingWithUnknownVerdict(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "fixture", App: "app", Participant: "P1"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(timelineFixture), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}
	findings := "{\"id\":\"F-001\",\"t\":22,\"type\":\"bug\",\"severity\":3,\"quote\":\"ok\",\"evidence\":[\"utt-004\"],\"status\":\"unverified\"}\n" +
		"{\"kind\":\"verdict\",\"finding\":\"F-001\",\"verdict\":\"confirm\",\"at\":\"2026-07-17\"}\n"
	if err := os.WriteFile(filepath.Join(dir, session.FindingsFile), []byte(findings), 0o644); err != nil {
		t.Fatalf("write findings: %v", err)
	}

	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(md, "**F-001**") {
		t.Fatalf("finding with an unrecognised verdict vanished from the report:\n%s", md)
	}
	if !strings.Contains(md, "### Unverified (1)") {
		t.Fatalf("finding with an unrecognised verdict should fall back to Unverified:\n%s", md)
	}
}

// TestReportAttachesEventsPerUtteranceWithoutIDs is the duplicated-events
// regression. timeline.Merge copies a transcript's id verbatim into Entry.ID and
// never validates it, so a transcript whose lines omit "id" — or repeats one —
// yields several utterances sharing an ID. Pre-fix the attachment map was keyed
// by that ID, so all such utterances shared a single bucket and each of them
// rendered every event attached to any of them: here three id-less utterances
// and three clicks produced nine event lines instead of three. Each event must
// appear exactly once, under the utterance whose window actually contains it.
func TestReportAttachesEventsPerUtteranceWithoutIDs(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "fixture", App: "app", Participant: "Alice"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	// Three utterances at 1/10/20 s, each with an empty id, and one click just
	// after each of them.
	tl := `{"t":1,"src":"speech","id":"","payload":{"speaker":"Alice","t1":3,"text":"first"}}
{"t":2,"src":"event","id":"ev-001","payload":{"kind":"click","selector":"one"}}
{"t":10,"src":"speech","id":"","payload":{"speaker":"Alice","t1":12,"text":"second"}}
{"t":11,"src":"event","id":"ev-002","payload":{"kind":"click","selector":"two"}}
{"t":20,"src":"speech","id":"","payload":{"speaker":"Alice","t1":22,"text":"third"}}
{"t":21,"src":"event","id":"ev-003","payload":{"kind":"click","selector":"three"}}
`
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(tl), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}

	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, sel := range []string{"`one`", "`two`", "`three`"} {
		if n := strings.Count(md, sel); n != 1 {
			t.Fatalf("event %s rendered %d times, want 1:\n%s", sel, n, md)
		}
	}
	// Each event sits under its own utterance: the selector follows its speech
	// line and precedes the next one.
	for _, pair := range [][2]string{{"first", "`one`"}, {"second", "`two`"}, {"third", "`three`"}} {
		utt, sel := strings.Index(md, pair[0]), strings.Index(md, pair[1])
		if utt < 0 || sel < utt {
			t.Fatalf("event %s is not attached under utterance %q:\n%s", pair[1], pair[0], md)
		}
	}
	if strings.Index(md, "`one`") > strings.Index(md, "second") {
		t.Fatalf("first event drifted past the second utterance:\n%s", md)
	}
	if strings.Index(md, "`two`") > strings.Index(md, "third") {
		t.Fatalf("second event drifted past the third utterance:\n%s", md)
	}
}

// TestReportRendersNegativeSessionRelativeTimes is the clamped-clock regression.
// A recording whose creation_time predates the manifest t0 yields a negative
// offset, so utterances, events and findings legitimately sit before t0 —
// analyze.indexTimeline admits findings anchored there. Pre-fix report.clock
// clamped every negative time to zero and report.end grew its maximum from the
// zero value, so this fully pre-t0 session rendered every line as [00:00] and
// claimed a duration of 00:00 rather than its true span ending at -35 s.
func TestReportRendersNegativeSessionRelativeTimes(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "fixture", App: "app", Participant: "Alice"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	// Every entry precedes t0: an utterance spanning -90 s to -35 s, one click
	// inside its window, and one standalone click well before it.
	tl := `{"t":-125,"src":"event","id":"ev-001","payload":{"kind":"click","selector":"early"}}
{"t":-90,"src":"speech","id":"utt-001","payload":{"speaker":"Alice","t1":-35,"text":"before the clock started"}}
{"t":-88,"src":"event","id":"ev-002","payload":{"kind":"click","selector":"attached"}}
`
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(tl), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}
	findings := "{\"id\":\"F-001\",\"t\":-90,\"type\":\"bug\",\"severity\":3,\"quote\":\"before the clock\",\"evidence\":[\"utt-001\"],\"status\":\"unverified\"}\n"
	if err := os.WriteFile(filepath.Join(dir, session.FindingsFile), []byte(findings), 0o644); err != nil {
		t.Fatalf("write findings: %v", err)
	}

	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"[-02:05]", // the standalone event at -125 s
		"[-01:30]", // the utterance at -90 s
		"[-01:28]", // the event attached to it
		"**Duration:** -00:35",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("report missing signed clock %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "[00:00]") {
		t.Fatalf("a pre-t0 time was clamped to zero:\n%s", md)
	}
}

// TestReportClockRoundsSymmetrically guards the sign-splitting arithmetic in
// clock: a negative time must round by magnitude the way its positive twin
// does, so the digits never disagree across the sign.
func TestReportClockRoundsSymmetrically(t *testing.T) {
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
	} {
		if got := clock(tc.sec); got != tc.want {
			t.Fatalf("clock(%g) = %q, want %q", tc.sec, got, tc.want)
		}
	}
}

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

type discard struct{}

func (*discard) Write(p []byte) (int, error) { return len(p), nil }

// TestReportSanitisesInjectedText is the content-injection regression: an
// attacker-authored event kind carrying a newline + markdown heading, and an
// utterance carrying an ANSI escape, must not survive into report.md as real
// report structure or terminal control bytes. Pre-fix these fields were written
// raw, so "### INJECTED" appeared as a heading and the ESC byte reached the
// file.
func TestReportSanitisesInjectedText(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "fixture", App: "app", Participant: "P1"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	timeline := "{\"t\":1,\"src\":\"speech\",\"id\":\"utt-1\",\"payload\":{\"speaker\":\"P1\",\"t1\":2,\"text\":\"hello \\u001b[31mRED\\u001b[0m\"}}\n" +
		"{\"t\":1.2,\"src\":\"event\",\"id\":\"ev-1\",\"payload\":{\"kind\":\"click\\n### INJECTED-HEADING\",\"selector\":\"btn\"}}\n"
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(timeline), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}

	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(md, "\n### INJECTED-HEADING") {
		t.Fatalf("forged markdown heading injected into report:\n%s", md)
	}
	if strings.ContainsRune(md, 0x1b) {
		t.Fatalf("ANSI escape byte survived into report.md")
	}
	// The legitimate token content is retained (only the control byte is gone).
	if !strings.Contains(md, "INJECTED-HEADING") {
		t.Fatalf("expected the kind text to remain, minus the newline")
	}
}

// TestReportFindingsSanitiseIDAndVerdict is the findings-channel injection
// regression. analyze.Load does no id/verdict validation (only ingest does), so
// a downloaded findings.jsonl can carry a newline in the id or the verdict
// fields. Pre-fix f.ID and st.Value/st.Of/st.At were rendered raw, forging
// report headings and fake verdict lines that the human precision record rests
// on.
func TestReportFindingsSanitiseIDAndVerdict(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "fixture", App: "app", Participant: "P1"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(timelineFixture), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}
	// A finding whose id and verdict "at" carry forged markdown structure.
	findings := "{\"id\":\"F-001\\n\\n### Confirmed (99)\\n\\n- **F-666** bug\",\"t\":22,\"type\":\"bug\",\"severity\":3,\"quote\":\"ok\",\"evidence\":[\"utt-004\"],\"status\":\"unverified\"}\n" +
		"{\"kind\":\"verdict\",\"finding\":\"F-001\\n\\n### Confirmed (99)\\n\\n- **F-666** bug\",\"verdict\":\"confirmed\",\"at\":\"2026-01-01)\\n\\n## Forged\"}\n"
	if err := os.WriteFile(filepath.Join(dir, session.FindingsFile), []byte(findings), 0o644); err != nil {
		t.Fatalf("write findings: %v", err)
	}

	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Structural injection needs a newline to start a forged heading/bullet/line;
	// SafeText strips the control bytes, so the payload can only survive inline
	// within F-001's own bullet, never as a fabricated heading or finding.
	if strings.Contains(md, "\n### Confirmed (99)") || strings.Contains(md, "\n## Forged") || strings.Contains(md, "\n- **F-666") {
		t.Fatalf("forged report structure injected via finding id/verdict:\n%s", md)
	}
	if strings.ContainsRune(md, 0x1b) {
		t.Fatalf("control byte survived into report.md")
	}
	// The real Confirmed group holds exactly the one genuine finding.
	if strings.Count(md, "### Confirmed (1)") != 1 {
		t.Fatalf("confirmed count line was altered:\n%s", md)
	}
}

// TestReportDoesNotLeakPathOnUnreadableFindings covers the info-disclosure on the
// findings-unavailable path. findings.jsonl exists but cannot be read — here a
// symlink, which session's no-follow guard refuses with an error naming the full
// path. That path is absolute when the operator passed an absolute -session and
// on macOS embeds the username. report.md is the artefact a session directory is
// built to share, so the raw error (the one string in renderFindings not routed
// through SafeText) must not land in it. Render still succeeds; the report shows a
// generic notice and no filesystem path. Pre-fix the whole error, path and all,
// was written into report.md.
func TestReportDoesNotLeakPathOnUnreadableFindings(t *testing.T) {
	dir := setupSession(t)
	findings := filepath.Join(dir, session.FindingsFile)
	if err := os.Symlink(filepath.Join(dir, "elsewhere"), findings); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render should stay non-fatal on an unreadable findings file: %v", err)
	}
	if strings.Contains(md, dir) {
		t.Fatalf("report leaked the session path into report.md:\n%s", md)
	}
	if !strings.Contains(md, "could not be read") {
		t.Fatalf("expected a generic findings-unavailable notice, got:\n%s", md)
	}
}

// TestClockRefusesOutOfRangeTime is the sink half of the time-magnitude class.
// timeline.jsonl and findings.jsonl are attacker-authorable and reach clock
// without passing timeline.checkedUtterances. A finite-but-astronomical t makes
// int(sec+0.5) an out-of-range float64→int conversion (implementation-defined:
// arm64 saturates to MaxInt64 and prints "153722867280912930:07", amd64 wraps
// negative), planting a nonsensical stamp in the human evidence artefact. clock
// must render a visibly-broken placeholder instead. Pre-fix it did the raw
// conversion.
func TestClockRefusesOutOfRangeTime(t *testing.T) {
	for _, tc := range []struct {
		name string
		sec  float64
	}{
		{"huge positive", 1e300},
		{"huge negative", -1e300},
		{"positive inf", math.Inf(1)},
		{"nan", math.NaN()},
	} {
		got := clock(tc.sec)
		if got != "--:--" {
			t.Errorf("clock(%s)=%q, want %q (out-of-range must not reach int conversion)", tc.name, got, "--:--")
		}
	}
	// The ordinary range is unaffected.
	if got := clock(125); got != "02:05" {
		t.Fatalf("clock(125)=%q, want 02:05", got)
	}
}

// TestReportRendersHugeTimeAsPlaceholder is the end-to-end guard: a hand-authored
// timeline.jsonl whose speech carries t=1e300 must render a placeholder Duration,
// not a saturated-integer garbage stamp, and Render must still exit cleanly.
func TestReportRendersHugeTimeAsPlaceholder(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "x", Participant: "P1"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	const tl = `{"t":1e300,"src":"speech","id":"u1","payload":{"speaker":"P1","t1":1e300,"text":"planted"}}
`
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(tl), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}
	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(md, "153722867280912930") {
		t.Fatalf("report rendered a saturated-integer garbage stamp:\n%s", md)
	}
	if !strings.Contains(md, "--:--") {
		t.Fatalf("expected a placeholder stamp for the out-of-range time:\n%s", md)
	}
}

// TestReportFlushesEventPastLegacySentinel covers the sentinel bug: the trailing
// standalone-event flush used a finite 1e18 bound, so any event with t at or past
// it was silently omitted from the report while merge and report both exited 0.
// The flush is now +Inf-bounded, so every finite-t event appears. Pre-fix the
// event below (t=1e18) was dropped.
func TestReportFlushesEventPastLegacySentinel(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "x", Participant: "P1"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	// One ordinary utterance and a standalone event at exactly the old sentinel.
	const tl = `{"t":5,"src":"speech","id":"u1","payload":{"speaker":"P1","t1":6,"text":"hello"}}
{"t":1e18,"src":"event","id":"ev-001","payload":{"kind":"click","selector":"#late","route":"#r"}}
`
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(tl), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}
	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(md, "#late") {
		t.Fatalf("standalone event at the legacy 1e18 sentinel was dropped from the report:\n%s", md)
	}
}
