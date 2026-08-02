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

// TestReportAttachesEventsByPositionNotDuplicateID is the event-side twin of
// TestReportAttachesEventsPerUtteranceWithoutIDs. report reads timeline.jsonl
// directly, so an exchanged or hand-edited one can carry two events sharing an id
// that merge would have made unique. Pre-fix the attach loop matched events by id,
// so an in-window event pulled its far-away same-id twin under the utterance too and
// the id-keyed standalone dedup then hid it — fabricating what the participant did
// while speaking. Attaching by position must keep the far event standalone, at its
// own time, and off the utterance.
func TestReportAttachesEventsByPositionNotDuplicateID(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "fixture", App: "app", Participant: "Alice"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	// Two events share id "ev-001": one at 10.5 s (inside the utterance window at
	// 10 s), one at 9000 s (far outside). Only the near one may attach.
	tl := `{"t":10,"src":"speech","id":"utt-001","payload":{"speaker":"Alice","t1":11,"text":"spoke here"}}
{"t":10.5,"src":"event","id":"ev-001","payload":{"kind":"click","selector":"near"}}
{"t":9000,"src":"event","id":"ev-001","payload":{"kind":"click","selector":"far"}}
`
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(tl), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}
	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// The far event renders exactly once, as a standalone at its own [150:00] stamp,
	// never nested under the utterance. Pre-fix it appeared under the utterance and
	// was suppressed from the standalone flush.
	if n := strings.Count(md, "`far`"); n != 1 {
		t.Fatalf("far event rendered %d times, want exactly 1:\n%s", n, md)
	}
	// It renders as a top-level standalone ("- ", column 0), never nested under the
	// utterance ("  - ", indented) — the pre-fix bug attached it under the utterance.
	if !strings.Contains(md, "\n- [150:00] click `far`") {
		t.Fatalf("far same-id event was not rendered as a standalone at its own time:\n%s", md)
	}
	if strings.Contains(md, "  - [150:00]") {
		t.Fatalf("far same-id event was wrongly nested under the utterance:\n%s", md)
	}
	// The near one attaches under the utterance (indented list item at its own 00:11).
	if !strings.Contains(md, "  - [00:11] click `near`") {
		t.Fatalf("near event did not attach to its utterance:\n%s", md)
	}
}

// TestReportNeutralisesInlineMarkdown is the beacon-injection regression. report.md
// is the shareable evidence artefact; an attacker-authored quote or event text of
// `![x](http://evil/beacon.png)` used to survive verbatim, so opening the report in
// any Markdown viewer fired a remote-image request — a tracking/exfil beacon. The
// active image/link markup must be neutralised (backslash-escaped) so it renders as
// literal text, while the words stay legible.
func TestReportNeutralisesInlineMarkdown(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "fixture", App: "app", Participant: "Alice"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	// An utterance whose text is an image-beacon, a finding whose quote is one,
	// and an event whose selector smuggles one through the code span: the inner
	// backticks would close the span early and let the image markup render live.
	tl := "{\"t\":1,\"src\":\"speech\",\"id\":\"utt-001\",\"payload\":{\"speaker\":\"Alice\",\"t1\":2,\"text\":\"![t](http://evil/a.png)\"}}\n" +
		"{\"t\":1.5,\"src\":\"event\",\"id\":\"ev-001\",\"payload\":{\"kind\":\"click\",\"selector\":\"x` ![p](http://span.example/c.png) `y\"}}\n"
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(tl), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}
	findings := "{\"id\":\"F-001\",\"t\":1,\"type\":\"bug\",\"severity\":3,\"quote\":\"![q](http://evil/b.png)\",\"evidence\":[\"utt-001\"],\"status\":\"unverified\"}\n"
	if err := os.WriteFile(filepath.Join(dir, session.FindingsFile), []byte(findings), 0o644); err != nil {
		t.Fatalf("write findings: %v", err)
	}
	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// No active image markup survives: the `!` immediately followed by `[` and the
	// `](` link opener are the beacon triggers, and neither may appear unescaped.
	if strings.Contains(md, "![t](http://evil/a.png)") || strings.Contains(md, "![q](http://evil/b.png)") {
		t.Fatalf("active image-beacon markdown survived into report.md:\n%s", md)
	}
	if strings.Contains(md, "](http://evil") {
		t.Fatalf("an unescaped link/image opener survived into report.md:\n%s", md)
	}
	// The words are still present (escaped, not stripped), so the evidence stays legible.
	if !strings.Contains(md, "evil/a.png") || !strings.Contains(md, "evil/b.png") {
		t.Fatalf("neutralisation dropped the text instead of escaping it:\n%s", md)
	}
	// The selector's inner backticks are stripped, so the whole selector stays one
	// inert code span (span content is literal — inert — so the image markup may
	// remain as text there). Pre-strip, "x` ![p](...) `y" split into `x` + live
	// image markup + `y`, firing the beacon from inside the "code" rendering.
	if !strings.Contains(md, "`x ![p](http://span.example/c.png) y`") {
		t.Fatalf("selector did not survive as a single backtick-free code span:\n%s", md)
	}
	if strings.Contains(md, "`x` ") {
		t.Fatalf("selector code span was closed early by an embedded backtick:\n%s", md)
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

// TestReportRefusesHugeTime is the end-to-end guard for the class
// TestClockRefusesOutOfRangeTime pins at the sink: a hand-authored
// timeline.jsonl whose speech carries t=1e300 must now be refused by
// timeline.ReadEntries's own ±maxUtteranceSeconds bound before it ever
// reaches Render's join or clock — superseding the placeholder this test
// used to observe from Render's return value, back when ReadEntries let the
// value through unbounded and only clock's saturated-integer defense caught
// it at render time.
func TestReportRefusesHugeTime(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "x", Participant: "P1"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	const tl = `{"t":1e300,"src":"speech","id":"u1","payload":{"speaker":"P1","t1":1e300,"text":"planted"}}
`
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(tl), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}
	_, err := Render(dir, 2.5)
	if err == nil || !strings.Contains(err.Error(), "exceed") {
		t.Fatalf("expected Render to refuse the oversized time, got %v", err)
	}
}

// TestReportFlushesEventPastLegacySentinel covers the sentinel bug: the trailing
// standalone-event flush used a finite 1e18 bound, so any event with t at or past
// it was silently omitted from the report while merge and report both exited 0.
// The flush is now +Inf-bounded, so every finite-t event appears. The event below
// sits at 9e8 seconds (~28.5 years) — the largest magnitude timeline.ReadEntries'
// own ±1e9 bound still admits — to prove the flush isn't limited by some smaller
// sentinel of its own within that range.
func TestReportFlushesEventPastLegacySentinel(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "x", Participant: "P1"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	// One ordinary utterance and a standalone event near the read boundary.
	const tl = `{"t":5,"src":"speech","id":"u1","payload":{"speaker":"P1","t1":6,"text":"hello"}}
{"t":9e8,"src":"event","id":"ev-001","payload":{"kind":"click","selector":"#late","route":"#r"}}
`
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(tl), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}
	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(md, "#late") {
		t.Fatalf("standalone event near the read boundary was dropped from the report:\n%s", md)
	}
}

// TestRenderSortsByTime is the out-of-order-report regression. Render consumed
// timeline.jsonl in file order, so a hand-edited or exchanged timeline — the case
// Render's own join step already defends against — rendered utterances and
// standalone events in whatever order the lines happened to sit in, printing
// [00:50] before [00:10]. report.md is read as the chronological record of the
// session, so the file order silently rewrote when things happened. Render now
// applies the same stable sort by t that timeline.Merge does.
func TestRenderSortsByTime(t *testing.T) {
	const unsorted = `{"t":50,"src":"speech","id":"utt-002","payload":{"speaker":"P1","t1":52,"text":"Second remark."}}
{"t":10,"src":"speech","id":"utt-001","payload":{"speaker":"P1","t1":12,"text":"First remark."}}
{"t":40,"src":"event","id":"ev-002","payload":{"kind":"scroll"}}
{"t":5,"src":"event","id":"ev-001","payload":{"kind":"click"}}
`
	dir := setupSession(t)
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(unsorted), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}

	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Every entry is outside every join window here, so all four appear at the top
	// level and must read in chronological order.
	prev := -1
	for _, want := range []string{"[00:05] click", "[00:10]", "First remark", "[00:40] scroll", "[00:50]", "Second remark"} {
		at := strings.Index(md, want)
		if at < 0 {
			t.Fatalf("report is missing %q:\n%s", want, md)
		}
		if at < prev {
			t.Fatalf("report is out of chronological order at %q:\n%s", want, md)
		}
		prev = at
	}
}

// TestRenderRefusesUnknownSrc pins the CheckSrc gate on report's read path.
// Before it, an entry whose src was neither "speech" nor "event" (reachable
// only from a hand-edited or exchanged timeline.jsonl) fell out of both render
// buckets and vanished from the rendered timeline, while end() still counted
// its time — the report claimed a Duration its own timeline never reached, at
// exit 0, silently omitting the entry from the human evidence artefact.
func TestRenderRefusesUnknownSrc(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "fixture"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	const tl = `{"t":10,"src":"speech","id":"utt-001","payload":{"t1":12,"speaker":"P1","text":"hello"}}
{"t":11,"src":"event","id":"ev-002","payload":{"kind":"scroll"}}
{"t":100,"src":"Event","id":"ev-009","payload":{"kind":"click","selector":"[data-testid=save-btn]"}}
`
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(tl), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}
	_, err := Render(dir, 2.5)
	if err == nil {
		t.Fatal("Render silently accepted a timeline entry with unknown src")
	}
	if !strings.Contains(err.Error(), "unknown src") || !strings.Contains(err.Error(), "entry 3") {
		t.Fatalf("error must name the unknown src and its entry: %v", err)
	}
}

// TestReportPlaceholdersEventWithNoRecognisedPayload pins eventLine's fallback
// for an event entry whose payload carries none of kind/selector/text/value/
// route (reachable only from a hand-edited or exchanged timeline.jsonl; merge
// itself refuses a kind-less interaction record). Pre-fix, the joined parts
// were empty and the rendered bullet was a bare timestamp with nothing after
// it — a silent gap in the human evidence artefact rather than a visible
// placeholder.
func TestReportPlaceholdersEventWithNoRecognisedPayload(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "fixture"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	const tl = `{"t":2,"src":"event","id":"ev-001","payload":null}
`
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(tl), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}

	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(md, "[00:02] \n") {
		t.Fatalf("event with no recognised payload rendered as a blank bullet:\n%s", md)
	}
	if !strings.Contains(md, "[00:02] —") {
		t.Fatalf("report is missing the placeholder for the empty event:\n%s", md)
	}
}

// TestReportFindingAnchorFallsBackOnBlankUI is the render-side half of the
// blank-anchor class: findingAnchor used to decide the ui-vs-evidence branch
// on the raw selector/route, then render through mdCode, which strips
// backticks. A selector made entirely of backticks is non-empty raw but
// renders to nothing, so pre-fix the anchor was a bare, empty code span and
// the evidence-id fallback never printed.
func TestReportFindingAnchorFallsBackOnBlankUI(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "fixture"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(timelineFixture), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}
	findings := `{"id":"F-001","t":22,"type":"bug","severity":3,"quote":"I clicked save and nothing happened","evidence":["utt-004"],"ui":{"selector":"` + "```" + `"},"status":"unverified"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, session.FindingsFile), []byte(findings), 0o644); err != nil {
		t.Fatalf("write findings: %v", err)
	}

	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(md, "— ``\n") {
		t.Fatalf("finding anchor rendered as a blank code span with no evidence fallback:\n%s", md)
	}
	if !strings.Contains(md, "— evidence utt-004") {
		t.Fatalf("report is missing the evidence-id fallback for a ui that renders empty:\n%s", md)
	}
}

// TestReportEventLineOmitsSelectorThatRendersEmpty is the eventLine sibling:
// an event selector made entirely of Unicode format characters (here U+200B
// ZERO WIDTH SPACE) is non-empty raw but strips to "" under session.SafeText.
// Pre-fix, eventLine appended mdCode(sel) unconditionally, rendering a bare,
// empty code span ("“") in the middle of the line instead of omitting the
// field.
func TestReportEventLineOmitsSelectorThatRendersEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "fixture"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	zeroWidthSpace := string(rune(0x200B))
	tl := `{"t":2,"src":"event","id":"ev-001","payload":{"kind":"click","selector":"` + zeroWidthSpace + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(tl), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}

	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(md, "``") {
		t.Fatalf("event line rendered an empty code span for an invisible-only selector:\n%s", md)
	}
	if !strings.Contains(md, "[00:02] click") {
		t.Fatalf("event line lost its kind:\n%s", md)
	}
}

// TestReportFindingAnchorFallsBackOnWhitespaceOnlyUI is the literal-whitespace
// sibling of TestReportFindingAnchorFallsBackOnBlankUI: session.SafeText maps
// a tab to a space rather than stripping it, so a selector of "\t" is
// non-empty even after SafeText and backtick removal — codeRendersEmpty must
// judge it on the TRIMMED form to still fall back to the evidence ids, not
// render a code span holding only a space.
func TestReportFindingAnchorFallsBackOnWhitespaceOnlyUI(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "fixture"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(timelineFixture), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}
	findings := "{\"id\":\"F-001\",\"t\":22,\"type\":\"bug\",\"severity\":3,\"quote\":\"I clicked save and nothing happened\",\"evidence\":[\"utt-004\"],\"ui\":{\"selector\":\"\\t\"},\"status\":\"unverified\"}\n"
	if err := os.WriteFile(filepath.Join(dir, session.FindingsFile), []byte(findings), 0o644); err != nil {
		t.Fatalf("write findings: %v", err)
	}

	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(md, "— ` `\n") {
		t.Fatalf("finding anchor rendered as a whitespace-only code span with no evidence fallback:\n%s", md)
	}
	if !strings.Contains(md, "— evidence utt-004") {
		t.Fatalf("report is missing the evidence-id fallback for a ui that renders as whitespace only:\n%s", md)
	}
}

// TestReportEventLinePlaceholdersInvisibleOnlyKind is the kind sibling of
// TestReportEventLineOmitsSelectorThatRendersEmpty: timeline.checkInteraction
// only requires kind's raw bytes be non-empty, so an event whose kind is
// invisible-only Unicode (here U+200B ZERO WIDTH SPACE) passes validation.
// Pre-fix, eventLine appended mdInline(raw("kind")) unconditionally — the one
// field on the line with no rendered-form check — leaving a blank double
// space where the event name belongs instead of the "—" placeholder every
// other empty-rendering field falls back to.
func TestReportEventLinePlaceholdersInvisibleOnlyKind(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "fixture"}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	zeroWidthSpace := string(rune(0x200B))
	tl := `{"t":2,"src":"event","id":"ev-001","payload":{"kind":"` + zeroWidthSpace + `","selector":"#buy"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), []byte(tl), 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}

	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(md, "[00:02]  `#buy`") {
		t.Fatalf("event line rendered a blank kind instead of a placeholder:\n%s", md)
	}
	if !strings.Contains(md, "[00:02] — `#buy`") {
		t.Fatalf("report is missing the placeholder for an invisible-only kind:\n%s", md)
	}
}

// TestReportHeaderPlaceholdersInvisibleOnlyManifestFields is the manifest
// header sibling of the finding-anchor and event-line rendered-form fixes:
// the App/Participant header used orDash(man.App) — deciding presence on the
// raw string, then rendering with mdInline — so an App/Participant that is
// invisible-only Unicode or whitespace-only was non-empty raw and skipped the
// "—" placeholder, then rendered to nothing under mdInline. Neither field is
// validated by session.SaveManifest, so an operator-supplied value reaches
// this unmodified.
func TestReportHeaderPlaceholdersInvisibleOnlyManifestFields(t *testing.T) {
	dir := t.TempDir()
	zeroWidthSpace := string(rune(0x200B))
	if err := session.SaveManifest(dir, session.Manifest{Session: "fixture", App: zeroWidthSpace, Participant: "  "}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), nil, 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}

	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(md, "**App:**  ·") || strings.Contains(md, "**Participant:**  \n") {
		t.Fatalf("header rendered a blank App/Participant instead of a placeholder:\n%s", md)
	}
	if !strings.Contains(md, "**App:** — · **Participant:** —") {
		t.Fatalf("report is missing the placeholder for an invisible/whitespace-only App/Participant:\n%s", md)
	}
}

// TestReportTitlePlaceholdersInvisibleOnlySession is the Session-field sibling
// of TestReportHeaderPlaceholdersInvisibleOnlyManifestFields: the title and
// Tasks line bypassed the same rendered-form placeholder pattern App and
// Participant already use, so a whitespace-only Session and all-empty Tasks
// rendered a bare trailing dash and a lone "; " with nothing either side.
func TestReportTitlePlaceholdersInvisibleOnlySession(t *testing.T) {
	dir := t.TempDir()
	if err := session.SaveManifest(dir, session.Manifest{Session: "   ", Participant: "P1", Tasks: []string{"", "  "}}); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, session.TimelineFile), nil, 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}

	md, err := Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(md, "# Session report — —\n") {
		t.Fatalf("report is missing the placeholder for a whitespace-only Session:\n%s", md)
	}
	if strings.Contains(md, "**Tasks:**") {
		t.Fatalf("report rendered a Tasks line with no non-empty task:\n%s", md)
	}
}
