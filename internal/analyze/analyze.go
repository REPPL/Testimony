// Package analyze implements the first-pass analysis layer: it emits a
// self-contained, host-delegated analysis request (a versioned rubric plus the
// session's timeline) and is the sole validation boundary for the model's
// answer. The CLI never calls a model, holds no keys, and adds no network
// dependency (architecture note §7, brief 01-product/04-analysis.md).
//
// A finding is born a candidate: ingest forces every finding to
// status:"unverified" regardless of what the answer JSON claims. Human verdicts
// (internal/review) are appended as separate, non-destructive records so the
// birth state and full decision history survive as the method's precision
// measure.
package analyze

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"

	"github.com/REPPL/Testimony/internal/session"
)

// RubricVersion pins the coding scheme so answers are comparable across
// sessions and future rubric revisions are explicit.
const RubricVersion = "testimony-analysis/v1"

// Finding is one candidate finding — one line of findings.jsonl. Finding lines
// carry no "kind" field; the schema is closed (ingest decodes with
// DisallowUnknownFields).
type Finding struct {
	ID       string   `json:"id"`
	T        float64  `json:"t"`
	Type     string   `json:"type"`
	Severity int      `json:"severity"`
	Mode     string   `json:"mode,omitempty"`
	Quote    string   `json:"quote"`
	Evidence []string `json:"evidence"`
	UI       *UI      `json:"ui,omitempty"`
	Status   string   `json:"status"`
}

// UI is an optional on-screen referent. Both fields are validated against the
// timeline's events when present.
type UI struct {
	Selector string `json:"selector,omitempty"`
	Route    string `json:"route,omitempty"`
}

// Verdict is an appended, non-destructive human decision on a finding. It is
// discriminated by kind:"verdict"; the last verdict for a finding wins.
type Verdict struct {
	Kind    string `json:"kind"` // literal "verdict"
	Finding string `json:"finding"`
	Verdict string `json:"verdict"` // confirmed | rejected | duplicate
	Of      string `json:"of,omitempty"`
	At      string `json:"at"` // YYYY-MM-DD
}

// Status is a finding's effective status for display.
type Status struct {
	Value string // confirmed | unverified | duplicate | rejected
	Of    string // duplicate target, when Value == "duplicate"
	At    string // verdict date, when a verdict exists
}

var (
	findingIDRe = regexp.MustCompile(`^F-\d{3}$`)
	typeSet     = map[string]bool{
		"bug": true, "friction": true, "inconsistency": true,
		"preference": true, "idea": true,
	}
	verdictSet   = map[string]bool{"confirmed": true, "rejected": true, "duplicate": true}
	knownRubrics = map[string]bool{RubricVersion: true}
)

// IsFindingID reports whether s is a well-formed finding id (F-NNN).
func IsFindingID(s string) bool { return findingIDRe.MatchString(s) }

// Load reads findings.jsonl from dir, splitting finding lines from appended
// verdict lines. A missing file returns an error satisfying fs.ErrNotExist so
// callers can render an absence notice.
func Load(dir string) ([]Finding, []Verdict, error) {
	path := filepath.Join(dir, session.FindingsFile)
	// Route through the read-side no-follow guard, not plain os.Open: findings.jsonl
	// in an exchanged (attacker-authored) session may be a symlink or a FIFO, and a
	// FIFO would block this open in open(2) for ever. A missing file still returns
	// an fs.ErrNotExist-satisfying error, which callers render as an absence notice.
	f, err := session.OpenFileNoFollowRead(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	var findings []Finding
	var verdicts []Verdict
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var probe struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if probe.Kind == "verdict" {
			var v Verdict
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, nil, fmt.Errorf("%s:%d: %w", path, line, err)
			}
			// The verdict enum is closed (confirmed|rejected|duplicate). A verdict
			// carrying any other value — a typo, an empty string, or a foreign
			// value from a shared/hand-edited session — is not representable, so it
			// is ignored rather than applied. The finding then keeps its
			// "unverified" status and still appears in the report and the review
			// queue, instead of landing in a status group nothing renders and
			// silently vanishing from both.
			if !verdictSet[v.Verdict] {
				continue
			}
			verdicts = append(verdicts, v)
			continue
		}
		var fnd Finding
		if err := json.Unmarshal(raw, &fnd); err != nil {
			return nil, nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		findings = append(findings, fnd)
	}
	if err := sc.Err(); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return findings, verdicts, nil
}

// EffectiveStatus maps each finding id to its effective status: every finding
// starts "unverified"; verdict records are applied in file order and the last
// one for an id wins. This single helper is used by both review (to pick the
// work queue) and report (to group).
func EffectiveStatus(findings []Finding, verdicts []Verdict) map[string]Status {
	m := make(map[string]Status, len(findings))
	for _, f := range findings {
		m[f.ID] = Status{Value: "unverified"}
	}
	for _, v := range verdicts {
		if _, ok := m[v.Finding]; !ok {
			continue // a verdict referencing an unknown finding is ignored for display
		}
		m[v.Finding] = Status{Value: v.Verdict, Of: v.Of, At: v.At}
	}
	return m
}
