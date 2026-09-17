// Package coderefs implements the codebase-mapping layer: it emits a
// self-contained, host-delegated mapping request (a versioned rubric, the
// session context, the path to the application's repository, and each confirmed
// finding that carries a selector or route anchor together with its event
// window) and is the sole validation boundary for the model's answer, writing
// validated references to refs.jsonl. The CLI never calls a model, holds no
// keys, and adds no network dependency, exactly as for internal/analyze and
// internal/drafttests.
//
// Resolution is the host's job. The CLI hands over the anchor and the
// repository path; it never greps, never reads a router table, and never
// interprets the source. Its only reads of the repository are the existence and
// line-count checks at ingest, and it never writes into it.
//
// The step sits downstream of verification: a reference may only ever name a
// finding a human already confirmed, so a session with none is staged loudly
// rather than mapped. A reference is born a proposal — ingest forces every
// reference to status:"proposed" regardless of what the answer JSON claims — and
// the human's accept / reject decision is appended as a separate,
// non-destructive record, so the proposal and the decision both survive and the
// reference's link to its finding and session is unreachable by any later write.
package coderefs

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

// RubricVersion pins the mapping scheme so references are comparable across
// sessions and future rubric revisions are explicit.
const RubricVersion = "testimony-coderefs/v1"

// DefaultWindow is the event-window half-width, in seconds, that emit uses when
// the operator gives none and that render always uses: the issue draft's
// reproduction steps need exactly the window the test draft needs, so the
// default is drafttests' for the same reason (a repro needs the lead-up and the
// aftermath, not only the moment).
const DefaultWindow = 10

// maxPathBytes bounds a reference's repo-relative path. A real source path is
// tens of bytes; the bound stops a hostile answer from smuggling a reference
// that is individually well-formed yet serialises past the JSONL line limit its
// readers scan to.
const maxPathBytes = 512

// maxRefs bounds the number of references one answer may carry. A real answer
// holds a handful per finding; the bound is checked before any reference is
// validated, because validation reads the repository (an existence check and,
// with a line, a bounded file read per distinct path) and an answer is untrusted
// model output that could otherwise buy that work a hundred thousand times over
// from one file.
const maxRefs = 1000

// maxClockSeconds bounds a value clock will format, mirroring report, review,
// and drafttests: a real session stamp is minutes to hours, and 1e9 seconds
// (~31 years) stays well inside int64 so the float64→int conversion in clock can
// never go out of range on an attacker-authored findings.jsonl time.
const maxClockSeconds = 1e9

// Ref is one proposed reference from a confirmed finding to a source location —
// one line of refs.jsonl. Reference lines carry no "kind" field; the schema is
// closed (ingest decodes with DisallowUnknownFields). Line is optional: 0 on
// disk means the reference names the file alone, and ingest never writes a 0
// line because a present line must be at least 1.
type Ref struct {
	ID      string `json:"id"`
	Finding string `json:"finding"`
	Session string `json:"session"`
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	Role    string `json:"role"` // owner | handler | route | test
	Status  string `json:"status"`
}

// Decision is an appended, non-destructive human decision on a reference. It is
// discriminated by kind:"decision"; the last decision for a reference wins.
// There is no "edited": a wrong path is rejected and a corrected one is
// ingested, never patched, so the reference's path is unreachable by any later
// write.
type Decision struct {
	Kind     string `json:"kind"` // literal "decision"
	Ref      string `json:"ref"`
	Decision string `json:"decision"` // accepted | rejected
	At       string `json:"at"`       // YYYY-MM-DD
}

// Status is a reference's effective status for display.
type Status struct {
	Value string // proposed | accepted | rejected
	At    string // decision date, when a decision exists
}

var (
	refIDRe      = regexp.MustCompile(`^R-\d{3}$`)
	decisionSet  = map[string]bool{"accepted": true, "rejected": true}
	roleSet      = map[string]bool{"owner": true, "handler": true, "route": true, "test": true}
	knownRubrics = map[string]bool{RubricVersion: true}
)

// IsRefID reports whether s is a well-formed reference id (R-NNN).
func IsRefID(s string) bool { return refIDRe.MatchString(s) }

// ParseDecisionFlag validates a -decision flag value against the closed enum.
func ParseDecisionFlag(s string) (string, error) {
	if !decisionSet[s] {
		return "", fmt.Errorf("invalid decision %q (want accepted|rejected)", s)
	}
	return s, nil
}

// Load reads refs.jsonl from dir, splitting reference lines from appended
// decision lines. A missing file returns an error satisfying fs.ErrNotExist so
// callers can render an absence notice.
func Load(dir string) ([]Ref, []Decision, error) {
	path := filepath.Join(dir, session.RefsFile)
	// Route through the read-side no-follow guard, not plain os.Open: refs.jsonl
	// in an exchanged (attacker-authored) session may be a symlink or a FIFO, and
	// a FIFO would block this open in open(2) for ever.
	f, err := session.OpenFileNoFollowRead(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	return ParseRecords(f, path)
}

// ParseRecords splits a refs.jsonl stream into reference and decision records,
// mirroring drafttests.ParseRecords rule for rule: blank lines are skipped, a
// decision carrying an out-of-enum value is ignored rather than applied, and the
// file is held to both the per-line and the total-size JSONL caps. name labels
// errors. AppendDecision reuses it to re-read the current references through
// its own already-locked descriptor, so the re-check and the append observe the
// same file under one lock.
func ParseRecords(r io.Reader, name string) ([]Ref, []Decision, error) {
	var refs []Ref
	var decisions []Decision
	// Reference ids must be unique across the file: EffectiveStatus and refByID
	// both key on the id, so two references sharing one would collapse onto a
	// single status entry and the walk would only ever reach the first. Ids are
	// compared in their session.SafeText form, the form every surface renders
	// them in.
	seenID := map[string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), session.MaxJSONLLine)
	line := 0
	var total int64
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		total += int64(len(raw)) + 1
		if total > session.MaxJSONLBytes {
			return nil, nil, fmt.Errorf("%s: exceeds %d bytes across %d lines; refusing to read", name, session.MaxJSONLBytes, line)
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var probe struct {
			Kind string  `json:"kind"`
			Path *string `json:"path"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, nil, fmt.Errorf("%s:%d: %w", name, line, err)
		}
		if probe.Kind == "decision" {
			var d Decision
			if err := json.Unmarshal(raw, &d); err != nil {
				return nil, nil, fmt.Errorf("%s:%d: %w", name, line, err)
			}
			// The decision enum is closed (accepted|rejected). A decision carrying
			// any other value is not representable, so it is ignored rather than
			// applied: the reference keeps "proposed" and still appears in the
			// review queue instead of vanishing from every surface.
			if !decisionSet[d.Decision] {
				continue
			}
			decisions = append(decisions, d)
			continue
		}
		// A line that is JSON null (or {}) decodes cleanly into a value-typed Ref
		// as its zero value, so a hand-edited or exchanged refs.jsonl carrying one
		// would silently inject a phantom reference into the review queue and the
		// render. probe.Path is a pointer for exactly this reason: every reference
		// this tool writes names a path, so its absence means the line was never a
		// reference at all.
		if probe.Path == nil {
			return nil, nil, fmt.Errorf("%s:%d: not a reference or decision record (missing path)", name, line)
		}
		var ref Ref
		if err := json.Unmarshal(raw, &ref); err != nil {
			return nil, nil, fmt.Errorf("%s:%d: %w", name, line, err)
		}
		id := session.SafeText(ref.ID)
		if strings.TrimSpace(id) == "" {
			return nil, nil, fmt.Errorf("%s:%d: reference has no id; every reference must have a unique id", name, line)
		}
		if seenID[id] {
			return nil, nil, fmt.Errorf("%s:%d: duplicate reference id %q; each reference must have a unique id", name, line, ref.ID)
		}
		seenID[id] = true
		refs = append(refs, ref)
	}
	if err := sc.Err(); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", name, err)
	}
	return refs, decisions, nil
}

// SameIdentity reports whether a and b are the same reference — equal in every
// field a human decision is recorded against. Status is excluded: it is the one
// field a decision is meant to change, and Ingest launders it to "proposed" on
// every written reference regardless. AppendDecision uses it under the append
// lock to confirm a decision still targets the reference the operator was
// shown, rather than a different one a concurrent re-ingest slid under the same
// id.
func SameIdentity(a, b Ref) bool {
	a.Status, b.Status = "", ""
	return reflect.DeepEqual(a, b)
}

// EffectiveStatus maps each reference id to its effective status: every
// reference starts "proposed"; decision records are applied in file order and
// the last one for an id wins. A decision naming an unknown reference is
// ignored for display. The review walk (to pick the work queue) and the render
// (to label each reference) share this one helper.
func EffectiveStatus(refs []Ref, decisions []Decision) map[string]Status {
	m := make(map[string]Status, len(refs))
	for _, r := range refs {
		m[r.ID] = Status{Value: "proposed"}
	}
	for _, d := range decisions {
		if _, ok := m[d.Ref]; !ok {
			continue // a decision referencing an unknown reference is ignored for display
		}
		m[d.Ref] = Status{Value: d.Decision, At: d.At}
	}
	return m
}

// refByID returns a pointer to the reference with the given id, or nil. Ids are
// compared in their session.SafeText form, matching ParseRecords' load-time
// uniqueness check: a reference's id renders through SafeText everywhere it is
// shown, so an operator matching it via -ref only ever has the rendered form to
// type. The returned pointer is into a copy, safe to retain.
func refByID(refs []Ref, id string) *Ref {
	want := session.SafeText(id)
	for i := range refs {
		if session.SafeText(refs[i].ID) == want {
			r := refs[i]
			return &r
		}
	}
	return nil
}

func findingByID(findings []analyze.Finding, id string) *analyze.Finding {
	want := session.SafeText(id)
	for i := range findings {
		if session.SafeText(findings[i].ID) == want {
			f := findings[i]
			return &f
		}
	}
	return nil
}

// hasAnchor reports whether f carries something the mapping step can hand over:
// a selector or a route that renders as non-empty. Presence is decided on the
// SafeText form, the form the request shows, so a selector that is non-empty
// raw but strips to nothing does not make a finding mappable on the strength of
// an anchor the host would never see. A terminal finding carries no ui at all
// and is the subject of its own intent (itd-2609152113364815).
func hasAnchor(f analyze.Finding) bool {
	if f.UI == nil {
		return false
	}
	return strings.TrimSpace(session.SafeText(f.UI.Selector)) != "" ||
		strings.TrimSpace(session.SafeText(f.UI.Route)) != ""
}

// eligible returns the findings a reference may name: those whose effective
// status is "confirmed", whose mode is not "B", and which carry a selector or
// route anchor, in id order.
//
// Effective status is not recomputed here — analyze.EffectiveStatus is the
// single helper review, report, and draft-tests already use — so "the last
// verdict wins" is true for free. A duplicate is never eligible even when its
// target is confirmed: the canonical finding carries the evidence. Mode B
// (reference capture of a third-party app) is excluded because there is no
// codebase to map into. On top of the draft-tests rule, a finding with no
// anchor is not eligible: there is nothing to hand over.
func eligible(findings []analyze.Finding, verdicts []analyze.Verdict) []analyze.Finding {
	eff := analyze.EffectiveStatus(findings, verdicts)
	var out []analyze.Finding
	for _, f := range findings {
		if eff[f.ID].Value == "confirmed" && f.Mode != "B" && hasAnchor(f) {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// findingCounts renders the by-status tally the no-mappable-finding refusal
// names. The confirmed count carries the anchored count beside it, so an
// operator can tell a session with no confirmed finding from one whose confirmed
// findings carry no selector or route.
func findingCounts(findings []analyze.Finding, verdicts []analyze.Verdict) string {
	eff := analyze.EffectiveStatus(findings, verdicts)
	n := map[string]int{}
	anchored := 0
	for _, f := range findings {
		st := eff[f.ID].Value
		n[st]++
		if st == "confirmed" && f.Mode != "B" && hasAnchor(f) {
			anchored++
		}
	}
	return fmt.Sprintf("%d findings: %d confirmed (%d with a selector or route), %d unverified, %d duplicate, %d rejected",
		len(findings), n["confirmed"], anchored, n["unverified"], n["duplicate"], n["rejected"])
}

// refCounts is findingCounts' sibling for the nothing-to-render refusal.
func refCounts(refs []Ref, decisions []Decision) string {
	eff := EffectiveStatus(refs, decisions)
	n := map[string]int{}
	for _, r := range refs {
		n[eff[r.ID].Value]++
	}
	return fmt.Sprintf("%d references: %d accepted, %d proposed, %d rejected",
		len(refs), n["accepted"], n["proposed"], n["rejected"])
}

// clock renders a session-relative time as [MM:SS]. Negative times are
// legitimate — an external recording whose creation_time predates the manifest
// t0 yields a negative offset — so the sign is rendered rather than clamped
// away. This mirrors report.clock, review.clock, and drafttests.clock.
func clock(sec float64) string {
	if math.IsNaN(sec) || math.Abs(sec) > maxClockSeconds {
		return "--:--"
	}
	neg := sec < 0
	if neg {
		sec = -sec
	}
	s := int(sec + 0.5)
	sign := ""
	if neg && s > 0 {
		sign = "-"
	}
	return fmt.Sprintf("%s%02d:%02d", sign, s/60, s%60)
}

// ErrNoMappableFindings marks the refusal that stages an empty mapping step
// loudly: a session with no confirmed finding that carries a selector or route
// has nothing a reference could legally name, so emit and ingest both refuse,
// name the finding count by status, and write nothing. It is a sentinel so a
// caller can tell a well-formed invocation whose work cannot be done from a
// genuine failure; the CLI maps both to exit 1.
var ErrNoMappableFindings = errors.New("no mappable finding")

// ErrNoMappedFindings is its render-side twin: refs.jsonl names no finding that
// is present in findings.jsonl, so the issue draft would be an empty document,
// and writing one over an existing file (with -out) would erase it.
var ErrNoMappedFindings = errors.New("no mapped finding to render")

func noMappableFindings(dir string, findings []analyze.Finding, verdicts []analyze.Verdict) error {
	return fmt.Errorf("%w in %s; a finding is mappable when its verdict is confirmed and it carries a selector or route (confirm one with `testimony review -session %s` first)",
		ErrNoMappableFindings, findingCounts(findings, verdicts), dir)
}

func noMappedFindings(dir string, refs []Ref, decisions []Decision) error {
	return fmt.Errorf("%w (%s, none naming a finding in %s); ingest a mapping answer with `testimony map -session %s -repo DIR -ingest FILE` first",
		ErrNoMappedFindings, refCounts(refs, decisions), session.FindingsFile, dir)
}

// loadFindings reads the session's findings, hinting to run `analyze -ingest`
// first when there is none — every mode of this package needs them, because a
// reference is only ever a proposal about a finding a human confirmed.
func loadFindings(dir string) ([]analyze.Finding, []analyze.Verdict, error) {
	_, findings, verdicts, err := analyze.Load(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, fmt.Errorf("no %s (run `testimony analyze -ingest` first)", session.FindingsFile)
		}
		return nil, nil, err
	}
	return findings, verdicts, nil
}

// loadRefs reads the session's references and decisions, hinting to run
// `map -ingest` first when there is no refs.jsonl yet.
func loadRefs(dir string) ([]Ref, []Decision, error) {
	refs, decisions, err := Load(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, fmt.Errorf("no %s (run `testimony map -ingest` first)", session.RefsFile)
		}
		return nil, nil, err
	}
	return refs, decisions, nil
}
