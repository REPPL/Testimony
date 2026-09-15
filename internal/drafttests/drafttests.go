// Package drafttests implements the regression-test drafting layer: it emits a
// self-contained, host-delegated drafting request (a versioned rubric, the
// session context, and each confirmed finding with its event window) and is the
// sole validation boundary for the model's answer, writing validated drafts to
// tests.jsonl. The CLI never calls a model, holds no keys, and adds no network
// dependency, exactly as for internal/analyze.
//
// The step sits downstream of verification: a draft may only ever reference a
// finding a human already confirmed, so a session with none is staged loudly
// rather than drafted from. A draft is born a proposal — ingest forces every
// draft to status:"proposed" regardless of what the answer JSON claims — and the
// human's accept / edit / reject decision is appended as a separate,
// non-destructive record, so the drafted proposal and the decision both survive
// and the draft's link to its source finding and session is unreachable by any
// later write.
package drafttests

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/REPPL/Testimony/internal/analyze"
	"github.com/REPPL/Testimony/internal/session"
)

// RubricVersion pins the drafting scheme so drafts are comparable across
// sessions and future rubric revisions are explicit.
const RubricVersion = "testimony-testdraft/v1"

// maxTitle and maxSteps bound the two free-text fields that carry no other
// limit. A genuine test case names itself in one line and reproduces in a
// handful of actions; the bounds stop a hostile answer from smuggling a draft
// that is individually well-formed yet serialises past the JSONL line limit its
// readers scan to, and keep the rendered test plan readable.
const (
	maxTitle = 200
	maxSteps = 32
)

// maxClockSeconds bounds a value clock will format, mirroring report and review:
// a real session stamp is minutes to hours, and 1e9 seconds (~31 years) stays
// well inside int64 so the float64→int conversion in clock can never go out of
// range on an attacker-authored findings.jsonl time.
const maxClockSeconds = 1e9

// Draft is one proposed regression test — one line of tests.jsonl. Draft lines
// carry no "kind" field; the schema is closed (ingest decodes with
// DisallowUnknownFields).
type Draft struct {
	ID             string   `json:"id"`
	Finding        string   `json:"finding"`
	Session        string   `json:"session"`
	Title          string   `json:"title"`
	Steps          []string `json:"steps"`
	Expected       string   `json:"expected"`
	Observed       string   `json:"observed"`
	RationaleQuote string   `json:"rationale_quote"`
	Severity       int      `json:"severity"`
	Status         string   `json:"status"`
}

// Decision is an appended, non-destructive human decision on a draft. It is
// discriminated by kind:"decision"; the last decision for a draft wins.
type Decision struct {
	Kind     string `json:"kind"` // literal "decision"
	Test     string `json:"test"`
	Decision string `json:"decision"` // accepted | edited | rejected
	At       string `json:"at"`       // YYYY-MM-DD
	Edit     *Edit  `json:"edit,omitempty"`
}

// Edit is the closed subset of a draft's fields a human decision may replace.
// It deliberately cannot reach id, finding, session, severity, or
// rationale_quote: those are the draft's link to the evidence it came from, and
// the only way to change the link is to reject the draft and ingest a new one.
// Each field is a pointer so an absent member stays distinguishable from one
// present but empty — an empty replacement is refused rather than silently
// treated as "keep".
type Edit struct {
	Title    *string   `json:"title,omitempty"`
	Steps    *[]string `json:"steps,omitempty"`
	Expected *string   `json:"expected,omitempty"`
	Observed *string   `json:"observed,omitempty"`
}

// Status is a draft's effective status for display.
type Status struct {
	Value string // proposed | accepted | edited | rejected
	At    string // decision date, when a decision exists
	Edit  *Edit  // the winning decision's replacement fields, when Value == "edited"
}

var (
	draftIDRe    = regexp.MustCompile(`^T-\d{3}$`)
	decisionSet  = map[string]bool{"accepted": true, "edited": true, "rejected": true}
	knownRubrics = map[string]bool{RubricVersion: true}
)

// IsDraftID reports whether s is a well-formed draft id (T-NNN).
func IsDraftID(s string) bool { return draftIDRe.MatchString(s) }

// ParseDecisionFlag validates a -decision flag value against the closed enum.
func ParseDecisionFlag(s string) (string, error) {
	if !decisionSet[s] {
		return "", fmt.Errorf("invalid decision %q (want accepted|edited|rejected)", s)
	}
	return s, nil
}

// Load reads tests.jsonl from dir, splitting draft lines from appended decision
// lines. A missing file returns an error satisfying fs.ErrNotExist so callers
// can render an absence notice.
func Load(dir string) ([]Draft, []Decision, error) {
	path := filepath.Join(dir, session.TestsFile)
	// Route through the read-side no-follow guard, not plain os.Open: tests.jsonl
	// in an exchanged (attacker-authored) session may be a symlink or a FIFO, and
	// a FIFO would block this open in open(2) for ever.
	f, err := session.OpenFileNoFollowRead(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	return ParseRecords(f, path)
}

// ParseRecords splits a tests.jsonl stream into draft and decision records,
// mirroring analyze.ParseRecords rule for rule: blank lines are skipped, a
// decision carrying an out-of-enum value is ignored rather than applied, and the
// file is held to both the per-line and the total-size JSONL caps. name labels
// errors. Load is ParseRecords over the on-disk file opened through the
// no-follow guard; AppendDecision reuses it to re-read the current drafts
// through its own already-locked descriptor, so the re-check and the append
// observe the same file under one lock.
func ParseRecords(r io.Reader, name string) ([]Draft, []Decision, error) {
	var drafts []Draft
	var decisions []Decision
	// Draft ids must be unique across the file, for the reason finding ids must
	// be: EffectiveStatus and draftByID both key on the id, so two drafts sharing
	// one would collapse onto a single status entry — one decision painting both
	// drafts — and the walk would only ever reach the first. Ids are compared in
	// their session.SafeText form, the form every surface renders them in, so two
	// raw ids distinct only by stripped bytes count as the collision they display
	// as.
	seenID := map[string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), session.MaxJSONLLine)
	line := 0
	var total int64
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		// A per-line cap alone leaves the file's total size unbounded: a
		// hand-edited or exchanged tests.jsonl built from many small,
		// individually-legal lines would otherwise drive this loop's per-line
		// allocation well past the bytes on disk.
		total += int64(len(raw)) + 1
		if total > session.MaxJSONLBytes {
			return nil, nil, fmt.Errorf("%s: exceeds %d bytes across %d lines; refusing to read", name, session.MaxJSONLBytes, line)
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var probe struct {
			Kind     string `json:"kind"`
			Severity *int   `json:"severity"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, nil, fmt.Errorf("%s:%d: %w", name, line, err)
		}
		if probe.Kind == "decision" {
			var d Decision
			if err := json.Unmarshal(raw, &d); err != nil {
				return nil, nil, fmt.Errorf("%s:%d: %w", name, line, err)
			}
			// The decision enum is closed (accepted|edited|rejected). A decision
			// carrying any other value — a typo, an empty string, or a foreign value
			// from a shared or hand-edited session — is not representable, so it is
			// ignored rather than applied. The draft then keeps "proposed" and still
			// appears in the review queue, instead of landing in a status group
			// neither the walk nor the render shows and silently vanishing from both.
			if !decisionSet[d.Decision] {
				continue
			}
			decisions = append(decisions, d)
			continue
		}
		// A line that is JSON null (or {}) decodes cleanly into a value-typed Draft
		// as its zero value, so a hand-edited or exchanged tests.jsonl carrying one
		// would silently inject a phantom draft — id "", severity 0 — into the
		// review queue and the rendered plan. probe.Severity is a pointer for
		// exactly this reason (mirroring rawDraft and analyze's rawFinding): every
		// draft this tool ever writes restates its finding's severity, so its
		// absence means the line was never a draft at all.
		if probe.Severity == nil {
			return nil, nil, fmt.Errorf("%s:%d: not a test draft or decision record (missing severity)", name, line)
		}
		var d Draft
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, nil, fmt.Errorf("%s:%d: %w", name, line, err)
		}
		id := session.SafeText(d.ID)
		// An empty (or whitespace-only) id is refused on its own terms rather than
		// folded into the duplicate check below: a draft's id is never optional, and
		// reporting the second one as `duplicate test draft id ""` would misname
		// what is actually wrong with the first.
		if strings.TrimSpace(id) == "" {
			return nil, nil, fmt.Errorf("%s:%d: test draft has no id; every draft must have a unique id", name, line)
		}
		if seenID[id] {
			return nil, nil, fmt.Errorf("%s:%d: duplicate test draft id %q; each draft must have a unique id", name, line, d.ID)
		}
		seenID[id] = true
		drafts = append(drafts, d)
	}
	if err := sc.Err(); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", name, err)
	}
	return drafts, decisions, nil
}

// SameIdentity reports whether a and b are the same draft — equal in every field
// a human decision is recorded against. Status is excluded: it is the one field
// a decision is meant to change, and Ingest launders it to "proposed" on every
// written draft regardless. AppendDecision uses it under the append lock to
// confirm a decision still targets the draft the operator was shown, rather than
// a different draft a concurrent re-ingest slid under the same id.
func SameIdentity(a, b Draft) bool {
	a.Status, b.Status = "", ""
	return reflect.DeepEqual(a, b)
}

// EffectiveStatus maps each draft id to its effective status: every draft starts
// "proposed"; decision records are applied in file order and the last one for an
// id wins. A decision naming an unknown draft is ignored for display. This
// single helper is used by the review walk (to pick the work queue) and by the
// render (to pick which drafts belong in the plan, and which edit to apply).
func EffectiveStatus(drafts []Draft, decisions []Decision) map[string]Status {
	m := make(map[string]Status, len(drafts))
	for _, d := range drafts {
		m[d.ID] = Status{Value: "proposed"}
	}
	for _, dec := range decisions {
		if _, ok := m[dec.Test]; !ok {
			continue // a decision referencing an unknown draft is ignored for display
		}
		st := Status{Value: dec.Decision, At: dec.At}
		if dec.Decision == "edited" {
			st.Edit = dec.Edit
		}
		m[dec.Test] = st
	}
	return m
}

// Apply returns d with the edit's present members substituted, leaving every
// other field — and d itself — untouched. It is how an "edited" draft is
// rendered: the decision is a separate appended record, so the substitution is
// computed at render time and the draft line on disk is never rewritten.
func (e *Edit) Apply(d Draft) Draft {
	if e == nil {
		return d
	}
	if e.Title != nil {
		d.Title = *e.Title
	}
	if e.Steps != nil {
		d.Steps = append([]string(nil), (*e.Steps)...)
	}
	if e.Expected != nil {
		d.Expected = *e.Expected
	}
	if e.Observed != nil {
		d.Observed = *e.Observed
	}
	return d
}

// empty reports whether the edit names no member at all. An "edited" decision
// with an empty edit is not representable — it records a change that did not
// happen — so both the interactive and the non-interactive paths refuse it.
func (e *Edit) empty() bool {
	return e == nil || (e.Title == nil && e.Steps == nil && e.Expected == nil && e.Observed == nil)
}

// draftByID returns a pointer to the draft with the given id, or nil. Ids are
// compared in their session.SafeText form, matching ParseRecords' load-time
// uniqueness check and review.findByID: a draft's id renders through SafeText
// everywhere it is shown, so an operator matching it via -test only ever has the
// rendered form to type. The returned pointer is into a copy, safe to retain.
func draftByID(drafts []Draft, id string) *Draft {
	want := session.SafeText(id)
	for i := range drafts {
		if session.SafeText(drafts[i].ID) == want {
			d := drafts[i]
			return &d
		}
	}
	return nil
}

// eligible returns the findings a draft may reference: those whose effective
// status is "confirmed" and whose mode is not "B", in id order.
//
// Effective status is not recomputed here — analyze.EffectiveStatus is the
// single helper review and report already use — so "the last verdict wins" is
// true for free: a finding confirmed and later rejected is not eligible, and one
// rejected and later confirmed is. A duplicate is never eligible even when its
// target is confirmed: the canonical finding is the one that carries the
// evidence. Mode B (reference capture) is excluded because a design preference
// has nothing to regress against; nothing produces Mode B today, so the
// exclusion is a guard rather than a live filter. type is deliberately not
// filtered: the acceptance criterion names a confirmed finding without
// qualification, the request carries each finding's type so the model can
// calibrate, and a draft with nothing to regress against is what the reject verb
// is for.
func eligible(findings []analyze.Finding, verdicts []analyze.Verdict) []analyze.Finding {
	eff := analyze.EffectiveStatus(findings, verdicts)
	var out []analyze.Finding
	for _, f := range findings {
		if eff[f.ID].Value == "confirmed" && f.Mode != "B" {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// findingCounts renders the by-status tally the no-confirmed-findings refusal
// names, so an operator staged empty can see why.
func findingCounts(findings []analyze.Finding, verdicts []analyze.Verdict) string {
	eff := analyze.EffectiveStatus(findings, verdicts)
	n := map[string]int{}
	for _, f := range findings {
		n[eff[f.ID].Value]++
	}
	return fmt.Sprintf("%d findings: %d confirmed, %d unverified, %d duplicate, %d rejected",
		len(findings), n["confirmed"], n["unverified"], n["duplicate"], n["rejected"])
}

// draftCounts is findingCounts' sibling for the no-accepted-drafts refusal.
func draftCounts(drafts []Draft, decisions []Decision) string {
	eff := EffectiveStatus(drafts, decisions)
	n := map[string]int{}
	for _, d := range drafts {
		n[eff[d.ID].Value]++
	}
	return fmt.Sprintf("%d drafts: %d accepted, %d edited, %d proposed, %d rejected",
		len(drafts), n["accepted"], n["edited"], n["proposed"], n["rejected"])
}

// clock renders a session-relative time as [MM:SS]. Negative times are
// legitimate — an external recording whose creation_time predates the manifest
// t0 yields a negative offset, and analyze.indexTimeline deliberately admits
// findings anchored there — so the sign is rendered rather than clamped away.
// This mirrors report.clock and review.clock; see the note in drafttests_test.go
// about the duplication.
func clock(sec float64) string {
	// Defend the float64→int conversion below against a non-finite or
	// astronomically large sec from a hand-authored findings.jsonl: int(sec+0.5)
	// would be an out-of-range conversion the Go spec leaves
	// implementation-defined, printing a nonsensical stamp on the surface where
	// the operator decides a draft's fate.
	if math.IsNaN(sec) || math.Abs(sec) > maxClockSeconds {
		return "--:--"
	}
	neg := sec < 0
	if neg {
		sec = -sec
	}
	s := int(sec + 0.5)
	sign := ""
	// The sign is taken from the rounded value, not the raw one, so a time a
	// fraction of a second before t0 prints as 00:00 rather than "-00:00".
	if neg && s > 0 {
		sign = "-"
	}
	return fmt.Sprintf("%s%02d:%02d", sign, s/60, s%60)
}

// ErrNoConfirmedFindings marks the refusal that stages an empty drafting step
// loudly: a session whose findings are all unverified, rejected, or duplicates
// has nothing a draft could legally reference, so emit and ingest both refuse,
// name the finding count by status, and write nothing. It is a sentinel so a
// caller can tell a well-formed invocation whose work cannot be done from a
// genuine failure; the CLI maps both to exit 1, the status such a refusal
// already takes, and does not branch on it.
var ErrNoConfirmedFindings = errors.New("no confirmed findings to draft tests from")

// ErrNoAcceptedDrafts is its render-side twin: a plan with no accepted or edited
// draft in it is an empty document, and writing one over an existing test plan
// (with -out) would erase it — the same reasoning behind the empty-answer
// refusal.
var ErrNoAcceptedDrafts = errors.New("no accepted test drafts to render")

func noConfirmedFindings(dir string, findings []analyze.Finding, verdicts []analyze.Verdict) error {
	return fmt.Errorf("%w (%s); confirm one with `testimony review -session %s` first",
		ErrNoConfirmedFindings, findingCounts(findings, verdicts), dir)
}

func noAcceptedDrafts(dir string, drafts []Draft, decisions []Decision) error {
	return fmt.Errorf("%w (%s); accept one with `testimony review -session %s -kind tests` first",
		ErrNoAcceptedDrafts, draftCounts(drafts, decisions), dir)
}

// loadFindings reads the session's findings, hinting to run `analyze -ingest`
// first when there is none — every mode of this package needs them, because a
// draft is only ever a proposal about a finding a human confirmed.
func loadFindings(dir string) ([]analyze.Finding, []analyze.Verdict, error) {
	// The analysis provenance is discarded: the drafting layer has its own rubric
	// and its own record, and which backend coded a finding changes no instruction
	// in the drafting request.
	_, findings, verdicts, err := analyze.Load(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, fmt.Errorf("no %s (run `testimony analyze -ingest` first)", session.FindingsFile)
		}
		return nil, nil, err
	}
	return findings, verdicts, nil
}

// loadDrafts reads the session's drafts and decisions, hinting to run
// `draft-tests -ingest` first when there is no tests.jsonl yet.
func loadDrafts(dir string) ([]Draft, []Decision, error) {
	drafts, decisions, err := Load(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, fmt.Errorf("no %s (run `testimony draft-tests -ingest` first)", session.TestsFile)
		}
		return nil, nil, err
	}
	return drafts, decisions, nil
}
