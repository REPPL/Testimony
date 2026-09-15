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
	"io"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/REPPL/Testimony/internal/session"
)

// RubricVersion pins the coding scheme so answers are comparable across
// sessions and future rubric revisions are explicit.
const RubricVersion = "testimony-analysis/v1"

// The closed backend set a Provenance record may carry. BackendUnrecorded is
// written when the operator gives no -backend: the declaration is then
// explicitly absent rather than silently missing, which is what lets every
// findings.jsonl written by this tool say something true about its own origin.
const (
	BackendLocal      = "local"
	BackendCloud      = "cloud"
	BackendUnrecorded = "unrecorded"
)

// MaxModelLength bounds the operator-supplied model name, in runes. Model names
// are not a closed set and never will be — a validated list would refuse an
// honest answer the week after it shipped — so the field is free text, and the
// bound plus the sink sanitisation are what make it safe to carry in an artefact
// designed to be shared. The limit matches drafttests' maxTitle, the sibling
// bound on operator-supplied free text in a session record.
const MaxModelLength = 200

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

// Provenance is the operator's declaration of what answered the analysis
// request a findings file was ingested from. It is one line of findings.jsonl,
// discriminated by kind:"provenance", written by Ingest as the FIRST line of
// the file — ahead of every finding — because ingest replaces the whole file
// while review appends verdicts to its end: a last-position record would be
// overtaken by the first verdict appended after it, and its position would then
// carry no meaning at all.
//
// It is a declaration, not a measurement. The CLI never calls a model and
// cannot observe where the emitted request ran, so every surface that renders
// this record says as much. What the record guarantees is that the claim was
// written down, at the moment it was made, by the person who made it.
type Provenance struct {
	Kind    string `json:"kind"`            // literal "provenance"
	Rubric  string `json:"rubric"`          // the rubric version enforced at ingest
	Backend string `json:"backend"`         // local | cloud | unrecorded
	Model   string `json:"model,omitempty"` // free text, operator-supplied
	At      string `json:"at"`              // YYYY-MM-DD
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
	// The backend set is closed and includes "unrecorded", the value written when
	// the operator names no backend. NewProvenance refuses "unrecorded" from the
	// flag — it is what the absence of a declaration records, not a declaration —
	// but ParseRecords must accept it on read, because it is a value this tool
	// writes.
	backendSet = map[string]bool{BackendLocal: true, BackendCloud: true, BackendUnrecorded: true}
	isoDateRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// IsFindingID reports whether s is a well-formed finding id (F-NNN).
func IsFindingID(s string) bool { return findingIDRe.MatchString(s) }

// NewProvenance validates the operator's declaration and returns the record
// Ingest will write. backend is "" when the flag was not given, which records
// BackendUnrecorded; model is "" when that flag was not given, and is then
// omitted from the written line.
//
// The rules live here rather than in the CLI, following ParseVerdictFlag and
// ParseKindFlag: the package that owns the record owns its rules, so the
// library and the command cannot drift about what a legal declaration is, and
// a caller that is not the CLI cannot write a record the CLI would have
// refused. The command wraps the returned error into its usage-error path, so
// a bad declaration is a wrong invocation (exit 2) rather than a runtime
// failure.
//
// Validating the length here is also what bounds the written line by
// construction: with model held to MaxModelLength runes and backend confined to
// the closed set, the encoded record cannot approach session.MaxJSONLLine, so
// the write path needs no per-line size rule for it (only the file total, which
// oversizedFindings counts it into).
//
// at is a parameter rather than time.Now() inside this package — the
// review.Options.Today precedent — so ingest stays deterministic and its golden
// tests need no clock injection.
func NewProvenance(backend, model, at string) (Provenance, error) {
	switch backend {
	case "", BackendLocal, BackendCloud:
	default:
		// BackendUnrecorded lands here deliberately: it is what the absence of a
		// declaration records, not a declaration an operator states, so accepting
		// it from the flag would let "unrecorded" be claimed as though it were an
		// answer to the question.
		return Provenance{}, fmt.Errorf("invalid -backend %q (want %s or %s)", backend, BackendLocal, BackendCloud)
	}
	// A model without a backend is a half-declaration: the backend is the field
	// that carries the privacy claim, so a model name on its own records nothing
	// about where the request ran while looking, in the report, as though it
	// did. Refused for the same reason review refuses -verdict without -finding.
	if model != "" && backend == "" {
		return Provenance{}, fmt.Errorf("-backend is required with -model")
	}
	if model != "" {
		// Presence is judged on the rendered form, not the raw one, matching every
		// other operator-supplied string that reaches a report or a request: a
		// model of invisible-only Unicode is non-empty raw but renders as nothing,
		// which would print a blank model where a name belongs.
		//
		// The predicate is session.CodeRendersEmpty, the same one report uses to
		// decide whether the model is present in its code span, and deliberately
		// not a local SafeText-only test: report strips backticks when it renders
		// the span, so a model of backticks alone renders as nothing there. With
		// two predicates it was accepted here, echoed on the success line, and then
		// reported as "not recorded" — the tool contradicting itself about what it
		// had just stored. One function, one answer.
		if session.CodeRendersEmpty(model) {
			return Provenance{}, fmt.Errorf("-model must not be blank (it renders as nothing: whitespace, invisible characters, or backticks alone)")
		}
		if n := utf8.RuneCountInString(model); n > MaxModelLength {
			return Provenance{}, fmt.Errorf("-model is %d characters, exceeding the limit of %d", n, MaxModelLength)
		}
	}
	if !isoDateRe.MatchString(at) {
		return Provenance{}, fmt.Errorf("invalid date %q (want YYYY-MM-DD)", at)
	}
	if backend == "" {
		backend = BackendUnrecorded
	}
	return Provenance{
		Kind:    "provenance",
		Rubric:  RubricVersion,
		Backend: backend,
		// The raw operator string, not its SafeText form: report sanitises at the
		// sink like every other untrusted field, and storing the sanitised form
		// would make the record quietly disagree with what the operator typed.
		Model: model,
		At:    at,
	}, nil
}

// Valid reports whether p is a record Ingest may write — the shape ParseRecords
// will read back. It exists because Ingest takes a Provenance by value from its
// caller, so nothing but this check stands between a zero-valued struct and a
// findings.jsonl whose first line no reader accepts: a record with no "kind"
// falls through the discriminator to the finding branch and is refused there for
// its missing "t", which would make Ingest a writer that produces a file its own
// Load cannot open, after reporting success.
//
// The rules are exactly NewProvenance's post-conditions, so the only way to
// satisfy them is to have built the record through it.
func (p Provenance) Valid() error {
	if p.Kind != "provenance" {
		return fmt.Errorf("provenance record has kind %q, want \"provenance\" (build it with analyze.NewProvenance)", p.Kind)
	}
	if !backendSet[p.Backend] {
		return fmt.Errorf("provenance record has backend %q, want %s, %s or %s (build it with analyze.NewProvenance)",
			p.Backend, BackendLocal, BackendCloud, BackendUnrecorded)
	}
	if p.Rubric == "" {
		return fmt.Errorf("provenance record has no rubric (build it with analyze.NewProvenance)")
	}
	if p.At == "" {
		return fmt.Errorf("provenance record has no date (build it with analyze.NewProvenance)")
	}
	return nil
}

// Load reads findings.jsonl from dir, splitting the provenance record from
// finding lines and appended verdict lines. A missing file returns an error
// satisfying fs.ErrNotExist so callers can render an absence notice. The
// returned provenance is nil when the file carries none — a findings.jsonl
// written before the record existed, or one assembled by hand — which every
// caller renders as "not recorded" rather than treating as a failure.
func Load(dir string) (*Provenance, []Finding, []Verdict, error) {
	path := filepath.Join(dir, session.FindingsFile)
	// Route through the read-side no-follow guard, not plain os.Open: findings.jsonl
	// in an exchanged (attacker-authored) session may be a symlink or a FIFO, and a
	// FIFO would block this open in open(2) for ever. A missing file still returns
	// an fs.ErrNotExist-satisfying error, which callers render as an absence notice.
	f, err := session.OpenFileNoFollowRead(path)
	if err != nil {
		return nil, nil, nil, err
	}
	defer f.Close()
	return ParseRecords(f, path)
}

// ParseRecords splits a findings.jsonl stream into the provenance record,
// finding records, and verdict records, applying the same rules Load documents:
// blank lines are skipped, a verdict carrying an out-of-enum value is ignored
// rather than applied, and so is a provenance record whose backend is outside
// the closed set. name labels errors. Load is ParseRecords over the on-disk file
// opened through the no-follow guard; review.AppendVerdict reuses it to re-read
// the current findings through its own already-locked descriptor, so the
// re-check and the append observe the same file under one lock.
//
// This stays a reader, not a validator, exactly as it is for finding fields: a
// hand-edited or exchanged findings.jsonl reaches it directly and each sink
// already defends itself, so the provenance record's model length and date shape
// are not checked here. backend is the one exception, filtered below, because it
// is the field that *is* the claim.
func ParseRecords(r io.Reader, name string) (*Provenance, []Finding, []Verdict, error) {
	var provenance *Provenance
	provenanceLine := 0
	var findings []Finding
	var verdicts []Verdict
	// Finding ids must be unique across the file. Ingest already rejects duplicates
	// in an answer (validate), but a hand-edited or exchanged findings.jsonl can carry
	// two findings sharing one id — and every id-keyed consumer then silently
	// misbehaves: EffectiveStatus collapses them onto one status entry, so one verdict
	// paints both findings in the report, and review's findByID only ever reaches the
	// first. Refusing the file here, naming the offending line, turns that
	// display-collapse into an honest error the operator can repair, and keeps the
	// id-uniqueness EffectiveStatus/findByID assume true actually true on every path
	// that loads findings. Ids are compared in their session.SafeText form, matching
	// the timeline id checks, so two ids distinct only by stripped bytes count as the
	// collision they render as: report and review both render a finding id through
	// SafeText, so two raw ids differing only in invisible characters are otherwise
	// indistinguishable on the page.
	seenID := map[string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), session.MaxJSONLLine)
	line := 0
	var total int64
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		// A per-line cap alone leaves the file's total size unbounded: a
		// hand-edited or exchanged findings.jsonl built from many small,
		// individually-legal lines would otherwise drive this loop's per-line
		// allocation (findings and verdicts both accumulate into slices) well
		// past the bytes on disk, mirroring the amplification session.ReadJSONL
		// guards against for its own callers.
		total += int64(len(raw)) + 1
		if total > session.MaxJSONLBytes {
			return nil, nil, nil, fmt.Errorf("%s: exceeds %d bytes across %d lines; refusing to read", name, session.MaxJSONLBytes, line)
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var probe struct {
			Kind string   `json:"kind"`
			T    *float64 `json:"t"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, nil, nil, fmt.Errorf("%s:%d: %w", name, line, err)
		}
		if probe.Kind == "verdict" {
			var v Verdict
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, nil, nil, fmt.Errorf("%s:%d: %w", name, line, err)
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
		if probe.Kind == "provenance" {
			var p Provenance
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, nil, nil, fmt.Errorf("%s:%d: %w", name, line, err)
			}
			// The backend set is closed (local|cloud|unrecorded), and backend is the
			// field that *is* the claim. A record carrying any other value — a typo,
			// an empty string, or a foreign value from a shared or hand-edited
			// session — states a privacy claim no reader can interpret, so it is
			// ignored rather than surfaced: the file then reads as "not recorded",
			// which is the truthful fallback, instead of putting an uninterpretable
			// claim on the page of the shareable report. This is the same stance the
			// out-of-enum verdict above takes.
			if !backendSet[p.Backend] {
				continue
			}
			// Two readable but conflicting claims are refused rather than resolved by
			// a rule nobody can see. Ignoring one would make a single-valued consumer
			// pick silently, and picking wrong prints a false privacy claim into the
			// artefact people share — an ambiguous attribution is worse than none.
			// This mirrors the duplicate-finding-id refusal below, and rests on the
			// same argument.
			if provenance != nil {
				return nil, nil, nil, fmt.Errorf("%s:%d: duplicate provenance record (first seen at line %d); a findings file records exactly one producer", name, line, provenanceLine)
			}
			provenance = &p
			provenanceLine = line
			continue
		}
		// A line that is JSON null (or {}) decodes cleanly into a value-typed
		// Finding as its zero value, so a hand-edited or exchanged findings.jsonl
		// carrying one silently injects a phantom finding — id "", severity 0 —
		// into the report and the review queue. probe.T is a pointer for exactly
		// this reason (mirroring ingest's rawFinding): every finding this tool ever
		// writes carries a real "t", so its absence means the line was never a
		// finding at all.
		if probe.T == nil {
			return nil, nil, nil, fmt.Errorf("%s:%d: not a finding, verdict, or provenance record (missing t)", name, line)
		}
		var fnd Finding
		if err := json.Unmarshal(raw, &fnd); err != nil {
			return nil, nil, nil, fmt.Errorf("%s:%d: %w", name, line, err)
		}
		id := session.SafeText(fnd.ID)
		// An empty (or, per the TrimSpace check, whitespace-only) id is
		// refused on its own terms, not folded into the duplicate check
		// below: unlike timeline.Merge's and loadTimeline's sibling checks —
		// where an id-less entry is legitimately skipped, because an
		// utterance or event needs no id — a finding's id is never optional
		// (EffectiveStatus and findByID key on it, so two id-less findings
		// would collapse onto one entry), and reporting the second one as
		// `duplicate finding id ""` misnames what is actually wrong with the
		// first. TrimSpace matches every other presence check in this
		// package (report.inlineRendersEmpty, review.printFinding/anchor,
		// validate's quote gate): a whitespace-only id renders with no
		// fallback in report.md and review's interactive walk, so it must be
		// refused here rather than treated as present.
		if strings.TrimSpace(id) == "" {
			return nil, nil, nil, fmt.Errorf("%s:%d: finding has no id; every finding must have a unique id", name, line)
		}
		if seenID[id] {
			return nil, nil, nil, fmt.Errorf("%s:%d: duplicate finding id %q; each finding must have a unique id", name, line, fnd.ID)
		}
		seenID[id] = true
		findings = append(findings, fnd)
	}
	if err := sc.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", name, err)
	}
	return provenance, findings, verdicts, nil
}

// SameIdentity reports whether a and b are the same finding — equal in every
// field a human verdict is recorded against. Status is excluded: it is the one
// field a verdict is meant to change, and Ingest launders it to "unverified" on
// every written finding regardless. review uses it under the append lock to
// confirm a verdict still targets the finding the analyst was shown, rather than
// a different finding a concurrent re-ingest slid under the same id.
func SameIdentity(a, b Finding) bool {
	a.Status, b.Status = "", ""
	return reflect.DeepEqual(a, b)
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
		of := ""
		if v.Verdict == "duplicate" {
			of = v.Of
		}
		m[v.Finding] = Status{Value: v.Verdict, Of: of, At: v.At}
	}
	return m
}
