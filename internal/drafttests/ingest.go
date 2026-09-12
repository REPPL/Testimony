package drafttests

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/REPPL/Testimony/internal/session"
)

// source is what a draft must restate about the finding it came from: the quote
// byte for byte and the severity unchanged, both held in the session.SafeText
// form the answering agent was shown.
type source struct {
	quote    string
	severity int
	id       string // the finding's raw id, for error messages
}

// Ingest validates the model's answer JSON from r against the draft schema and,
// only if every draft passes, writes tests.jsonl with status forced to
// "proposed". It is the sole validation boundary: a draft naming a finding that
// is not currently confirmed, claiming another session, carrying a quote that is
// not its finding's, restating a different severity, listing no step, or bearing
// a stray field is rejected here, transactionally (all errors reported, nothing
// written on any failure).
//
// It reads manifest.json and findings.jsonl only. Drafts are validated against
// the *findings*, never re-derived from the timeline: the finding is the record a
// human vouched for, and requiring equality against it is what makes this step
// structurally incapable of introducing new evidence.
//
// To protect the retained human record it refuses to overwrite a tests.jsonl
// that already holds decision records.
func Ingest(dir string, r io.Reader) ([]Draft, error) {
	man, err := session.LoadManifest(dir)
	if err != nil {
		return nil, err
	}
	findings, verdicts, err := loadFindings(dir)
	if err != nil {
		return nil, err
	}
	confirmed := eligible(findings, verdicts)
	// Refused before a byte of the answer is read: with no eligible finding there
	// is nothing a draft could legally reference, so every draft in the answer
	// would fail the same rule and the operator would read a wall of errors
	// instead of the one fact that explains them.
	if len(confirmed) == 0 {
		return nil, noConfirmedFindings(dir, findings, verdicts)
	}
	sources := make(map[string]source, len(confirmed))
	for _, f := range confirmed {
		// Keyed and compared in SafeText form, the only form of the finding the
		// answering agent is ever shown (EmitRequest routes each marshalled finding
		// through SafeText). Indexing the raw bytes while the agent copies the
		// sanitised ones would make an honest, verbatim-copied answer impossible to
		// validate on a control-character-bearing session.
		sources[session.SafeText(f.ID)] = source{
			quote:    session.SafeText(f.Quote),
			severity: f.Severity,
			id:       f.ID,
		}
	}

	// The answer is untrusted model output (this is the validation boundary) and
	// -ingest reads it from stdin or a file, so cap the read: a multi-gigabyte
	// answer must not OOM the process before validation runs.
	data, err := io.ReadAll(io.LimitReader(r, session.MaxAnswerBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > session.MaxAnswerBytes {
		return nil, fmt.Errorf("answer exceeds %d bytes: refusing to ingest", session.MaxAnswerBytes)
	}
	raws, rubric, err := parseContainer(data)
	if err != nil {
		return nil, err
	}
	if rubric != "" && !knownRubrics[rubric] {
		return nil, fmt.Errorf("unknown rubric %q (expected %s)", rubric, RubricVersion)
	}
	// An empty tests array (a bare `[]`, `{"tests":[]}`, or a truncated answer
	// file) is a no-op, not a truncating write: the commit below replaces the
	// file whole, so proceeding would erase a prior good tests.jsonl and report
	// success. Refuse it, mirroring the decision-overwrite guard.
	if len(raws) == 0 {
		return nil, fmt.Errorf("answer contains no test drafts; refusing to overwrite %s", session.TestsFile)
	}

	// Undecodable elements are dropped before validation, so the surviving slice
	// no longer aligns with the answer. Each survivor therefore carries the
	// position it held in the answer, and validate labels from that: otherwise a
	// failure in the third draft of an answer whose second one was undecodable
	// would be reported as "draft #2" — an index into a filtered slice the
	// operator never sees, pointing them at the wrong draft to fix.
	var (
		decoded []positioned
		errs    []error
	)
	for i, raw := range raws {
		d, derr := decodeDraft(raw)
		if derr != nil {
			errs = append(errs, fmt.Errorf("draft #%d: %v", i+1, derr))
			continue
		}
		decoded = append(decoded, positioned{draft: d, at: i + 1})
	}
	errs = append(errs, validate(decoded, sources, man.Session)...)

	// The model is never trusted: every draft lands proposed, so a draft can never
	// be born accepted. Laundering the status here, before the size check below,
	// is what makes that check measure the line actually written rather than the
	// one the answer proposed.
	drafts := make([]Draft, len(decoded))
	for i, p := range decoded {
		drafts[i] = p.draft
		drafts[i].Status = "proposed"
	}
	errs = append(errs, oversizedDrafts(drafts, decoded)...)

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	if err := commitDrafts(dir, drafts); err != nil {
		return nil, err
	}
	return drafts, nil
}

// commitDrafts runs the decision guard and the whole-file replacement as one
// locked step, through session.CommitRecords — the same primitive
// analyze.commitFindings uses, so a concurrent `testimony review -kind tests`
// appending a decision cannot slip between the probe and the rewrite and have
// its record destroyed. The drafts were already held to both
// session.MaxJSONLLine and session.MaxJSONLBytes by oversizedDrafts.
func commitDrafts(dir string, drafts []Draft) error {
	path := filepath.Join(dir, session.TestsFile)
	records := make([][]byte, 0, len(drafts))
	for _, d := range drafts {
		b, err := json.Marshal(d)
		if err != nil {
			return fmt.Errorf("write test drafts: %w", err)
		}
		records = append(records, b)
	}
	return session.CommitRecords(session.Commit{
		Path:    path,
		Records: records,
		Guard: func(current io.Reader) error {
			held, err := holdsDecisions(current, path)
			if err != nil {
				return err
			}
			if held {
				return fmt.Errorf("refusing to overwrite %s: it already holds decision records (the retained human record)", session.TestsFile)
			}
			return nil
		},
	})
}

// holdsDecisions reports whether the tests.jsonl open on r already contains any
// decision record. It reads through the caller's descriptor — opened under the
// no-follow guard and exclusively locked by session.CommitRecords — rather than
// opening the path itself, so the probe and the write it gates observe the same
// locked file. It scans for raw kind:"decision" lines rather than reusing Load,
// whose decision slice is filtered to the closed enum: a hand-edited or shared
// file whose only decision lines carry a foreign or typo'd value would otherwise
// slip past the guard and have its human-decision records truncated by a
// re-ingest — exactly the record the guard exists to protect.
func holdsDecisions(r io.Reader, path string) (bool, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), session.MaxJSONLLine)
	for sc.Scan() {
		raw := sc.Bytes()
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var probe struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return false, fmt.Errorf("%s: %w", path, err)
		}
		if probe.Kind == "decision" {
			return true, nil
		}
	}
	if err := sc.Err(); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return false, nil
}

// oversizedDrafts reports any draft whose tests.jsonl line — its JSON encoding
// plus the newline — would exceed session.MaxJSONLLine, the shared invariant
// every reader scans to, and also refuses an answer whose drafts would together
// exceed session.MaxJSONLBytes once written. maxSteps and maxTitle bound two
// fields, but nothing bounds the length of a step or the number of drafts in an
// answer, so a set of individually valid drafts can still serialise to a
// tests.jsonl that ParseRecords refuses to read back. Both checks run before any
// write and join the transactional error set, so an oversized answer leaves the
// previous tests.jsonl untouched rather than bricking it. Labels come from each
// draft's answer position for the same reason validate's do; a line already
// flagged as over-long is excluded from the total so one oversized draft cannot
// also trigger a redundant total-size error.
func oversizedDrafts(drafts []Draft, decoded []positioned) []error {
	var errs []error
	var total int64
	var counted int
	for i, d := range drafts {
		label := draftLabel(d, decoded[i].at)
		line, err := json.Marshal(d)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: cannot encode as JSON: %w", label, err))
			continue
		}
		lineLen := int64(len(line) + 1)
		if lineLen > session.MaxJSONLLine {
			errs = append(errs, fmt.Errorf("%s: encodes to %d bytes, exceeding the %d-byte %s line limit", label, lineLen, session.MaxJSONLLine, session.TestsFile))
			continue
		}
		total += lineLen
		counted++
	}
	if total > session.MaxJSONLBytes {
		errs = append(errs, fmt.Errorf("test drafts encode to %d bytes across %d drafts, exceeding the %d-byte %s file limit ParseRecords enforces; refusing to write a file review and render could not read back", total, counted, session.MaxJSONLBytes, session.TestsFile))
	}
	return errs
}

// parseContainer accepts either a top-level object with a "tests" array (the
// preferred container, optionally carrying a "rubric") or a bare array of
// drafts. It returns the raw draft elements and the rubric string (empty for the
// bare-array form).
func parseContainer(data []byte) ([]json.RawMessage, string, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, "", fmt.Errorf("empty answer: expected a JSON object or array of test drafts")
	}
	switch trimmed[0] {
	case '[':
		var arr []json.RawMessage
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, "", fmt.Errorf("parse test drafts array: %w", err)
		}
		return arr, "", nil
	case '{':
		var doc struct {
			Rubric string            `json:"rubric"`
			Tests  []json.RawMessage `json:"tests"`
		}
		if err := json.Unmarshal(trimmed, &doc); err != nil {
			return nil, "", fmt.Errorf("parse answer: %w", err)
		}
		if doc.Tests == nil {
			return nil, "", fmt.Errorf("answer object has no \"tests\" array")
		}
		return doc.Tests, doc.Rubric, nil
	default:
		return nil, "", fmt.Errorf("expected a JSON object or array of test drafts")
	}
}

// rawDraft is how one element of the untrusted answer is decoded before it is
// trusted. Its Severity is a pointer so that an absent "severity" stays
// distinguishable from a present one (the rawFinding.T precedent): without it an
// answer omitting the field decodes to 0 and is reported as a *mismatch* against
// a value the answer never gave, sending the operator to correct a number
// instead of to supply one. Everything else mirrors Draft, which is the shape
// DisallowUnknownFields is closed against.
type rawDraft struct {
	ID             string   `json:"id"`
	Finding        string   `json:"finding"`
	Session        string   `json:"session"`
	Title          string   `json:"title"`
	Steps          []string `json:"steps"`
	Expected       string   `json:"expected"`
	Observed       string   `json:"observed"`
	RationaleQuote string   `json:"rationale_quote"`
	Severity       *int     `json:"severity"`
	Status         string   `json:"status"`
}

// decodeDraft strictly decodes one draft element. DisallowUnknownFields closes
// the shape: a hallucinated or mistyped field is a hard error rather than
// silently dropped. A missing "severity" is rejected here rather than in
// validate, because by the time a draft reaches validate its unset severity is
// indistinguishable from a stated 0 (see rawDraft).
func decodeDraft(raw json.RawMessage) (Draft, error) {
	var rd rawDraft
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rd); err != nil {
		return Draft{}, err
	}
	if rd.Severity == nil {
		return Draft{}, fmt.Errorf("missing severity; a draft must restate its finding's severity")
	}
	return Draft{
		ID:             rd.ID,
		Finding:        rd.Finding,
		Session:        rd.Session,
		Title:          rd.Title,
		Steps:          rd.Steps,
		Expected:       rd.Expected,
		Observed:       rd.Observed,
		RationaleQuote: rd.RationaleQuote,
		Severity:       *rd.Severity,
		Status:         rd.Status,
	}, nil
}

// positioned pairs a decoded draft with at: its 1-based position in the answer
// the operator actually wrote. The two differ whenever an earlier element failed
// to decode, because Ingest drops those before validation; the pairing is what
// lets an error say "draft #3" and mean the third draft in the answer, which is
// the only index the operator can count to.
type positioned struct {
	draft Draft
	at    int
}

// draftLabel names a draft in an error message: its own id when that id is
// well-formed, and otherwise its position in the answer, which is the only
// handle the operator has on a draft whose id is unusable.
func draftLabel(d Draft, at int) string {
	if IsDraftID(d.ID) {
		return d.ID
	}
	return fmt.Sprintf("draft #%d", at)
}

// validate runs every schema rule against the decoded drafts and returns all
// errors (transactional and exhaustive), each naming the draft, the field, and
// the offending value. Positional labels come from each draft's recorded answer
// position, never from this loop's counter.
//
// Every value the answering agent saw in sanitised form is compared in
// session.SafeText form, and every presence test is decided on the rendered
// form: a value that is non-empty raw but strips to nothing renders as a blank
// in the review prompt and the test plan, so it must be refused here rather than
// admitted as content.
func validate(drafts []positioned, sources map[string]source, wantSession string) []error {
	var errs []error
	seen := map[string]int{}

	for _, p := range drafts {
		d := p.draft
		label := draftLabel(d, p.at)
		if !IsDraftID(d.ID) {
			errs = append(errs, fmt.Errorf("%s: id %q must match ^T-\\d{3}$", label, d.ID))
		} else if prev, dup := seen[d.ID]; dup {
			errs = append(errs, fmt.Errorf("%s: duplicate id (first seen at draft #%d)", d.ID, prev))
		} else {
			seen[d.ID] = p.at
		}

		// finding: must name a currently-confirmed, non-Mode-B finding. Enforced
		// here as well as in the request (emit omits every non-eligible finding),
		// so a hand-written or stale answer cannot smuggle a draft of an
		// unverified, rejected, or duplicate finding past this boundary.
		src, ok := sources[session.SafeText(d.Finding)]
		if !ok {
			errs = append(errs, fmt.Errorf("%s: finding %q is not a confirmed finding in %s", label, d.Finding, session.FindingsFile))
		}

		if session.SafeText(d.Session) != session.SafeText(wantSession) {
			errs = append(errs, fmt.Errorf("%s: session %q is not this session (%q in %s)", label, d.Session, wantSession, session.ManifestFile))
		}

		title := strings.TrimSpace(session.SafeText(d.Title))
		if title == "" {
			errs = append(errs, fmt.Errorf("%s: title must be non-empty", label))
		} else if n := utf8.RuneCountInString(title); n > maxTitle {
			errs = append(errs, fmt.Errorf("%s: title is %d characters, exceeding the limit of %d", label, n, maxTitle))
		}

		if len(d.Steps) == 0 {
			errs = append(errs, fmt.Errorf("%s: steps must be non-empty", label))
		}
		if len(d.Steps) > maxSteps {
			errs = append(errs, fmt.Errorf("%s: steps lists %d entries, exceeding the limit of %d", label, len(d.Steps), maxSteps))
		}
		for i, s := range d.Steps {
			if strings.TrimSpace(session.SafeText(s)) == "" {
				errs = append(errs, fmt.Errorf("%s: step %d must be non-empty", label, i+1))
			}
		}

		if strings.TrimSpace(session.SafeText(d.Expected)) == "" {
			errs = append(errs, fmt.Errorf("%s: expected must be non-empty", label))
		}
		if strings.TrimSpace(session.SafeText(d.Observed)) == "" {
			errs = append(errs, fmt.Errorf("%s: observed must be non-empty", label))
		}

		// rationale_quote and severity must be the source finding's own, not a
		// re-derivation: equality is what makes this step structurally incapable of
		// introducing evidence the human never vouched for, and a mismatch is the
		// cheapest available signal that the draft was linked to the wrong finding.
		// Both are skipped when the finding itself is unknown — there is nothing to
		// compare against, and a second error would only restate the first.
		if ok {
			if session.SafeText(d.RationaleQuote) != src.quote {
				errs = append(errs, fmt.Errorf("%s: rationale_quote %q is not finding %s's quote; it must be copied byte for byte", label, d.RationaleQuote, session.SafeText(src.id)))
			}
			if d.Severity != src.severity {
				errs = append(errs, fmt.Errorf("%s: severity %d does not match finding %s's severity %d", label, d.Severity, session.SafeText(src.id), src.severity))
			}
		}
	}
	return errs
}
