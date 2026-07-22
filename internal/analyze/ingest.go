package analyze

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/REPPL/Testimony/internal/session"
	"github.com/REPPL/Testimony/internal/timeline"
)

// maxAnswerBytes caps the untrusted answer read in Ingest, mirroring the
// bounded reads elsewhere (the demo server's 8 MiB body cap, the 4 MiB JSONL
// line cap). It is generous for a genuine multi-finding answer.
const maxAnswerBytes = 16 << 20

// loadTimeline reads the merged timeline, hinting to run merge first when it is
// missing (matching report).
func loadTimeline(dir string) ([]timeline.Entry, error) {
	entries, err := session.ReadJSONL[timeline.Entry](filepath.Join(dir, session.TimelineFile))
	if err != nil {
		return nil, fmt.Errorf("read timeline (run `testimony merge` first?): %w", err)
	}
	return entries, nil
}

// Ingest validates the model's answer JSON from r against the findings schema
// and, only if every finding passes, writes findings.jsonl with status forced
// to "unverified". It is the sole validation boundary: unknown evidence,
// fabricated quotes, bad enums, out-of-range severity, phantom selectors, and
// stray fields are all rejected here, transactionally (all errors reported,
// nothing written on any failure). To protect the retained precision record it
// refuses to overwrite a findings.jsonl that already holds verdict records.
func Ingest(dir string, r io.Reader) ([]Finding, error) {
	entries, err := loadTimeline(dir)
	if err != nil {
		return nil, err
	}
	idx := indexTimeline(entries)

	// The answer is untrusted LLM output (this is the validation boundary) and
	// -ingest reads it from stdin/a file, so cap the read: a multi-gigabyte
	// answer must not OOM the process before validation runs. maxAnswerBytes is
	// generous for a real answer; anything larger is rejected, not buffered.
	data, err := io.ReadAll(io.LimitReader(r, maxAnswerBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAnswerBytes {
		return nil, fmt.Errorf("answer exceeds %d bytes: refusing to ingest", maxAnswerBytes)
	}
	raws, rubric, err := parseContainer(data)
	if err != nil {
		return nil, err
	}
	if rubric != "" && !knownRubrics[rubric] {
		return nil, fmt.Errorf("unknown rubric %q (expected %s)", rubric, RubricVersion)
	}
	// An empty findings array (a bare `[]`, `{"findings":[]}`, or a truncated
	// answer file) is a no-op, not a truncating write: the write below opens with
	// O_TRUNC, so proceeding would erase a prior good findings.jsonl and report
	// success. Refuse it, mirroring the verdict-overwrite guard.
	if len(raws) == 0 {
		return nil, fmt.Errorf("answer contains no findings; refusing to overwrite %s", session.FindingsFile)
	}

	// Undecodable elements are dropped before validation, so the surviving slice
	// no longer aligns with the answer. Each survivor therefore carries the
	// position it held in the answer, and validate labels from that: otherwise a
	// failure in the third finding of an answer whose second one was undecodable
	// would be reported as "finding #2" — an index into a filtered slice the
	// operator never sees, pointing them at the wrong finding to fix.
	var (
		decoded []positioned
		errs    []error
	)
	for i, raw := range raws {
		f, derr := decodeFinding(raw)
		if derr != nil {
			errs = append(errs, fmt.Errorf("finding #%d: %v", i+1, derr))
			continue
		}
		decoded = append(decoded, positioned{finding: f, at: i + 1})
	}
	errs = append(errs, validate(decoded, idx)...)

	// The model is never trusted: every finding lands unverified. Laundering the
	// status here, before the size check below, is what makes that check measure
	// the line actually written rather than the one the answer proposed.
	findings := make([]Finding, len(decoded))
	for i, p := range decoded {
		findings[i] = p.finding
		findings[i].Status = "unverified"
	}
	errs = append(errs, oversizedFindings(findings, decoded)...)

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	if held, err := holdsVerdicts(dir); err != nil {
		return nil, err
	} else if held {
		return nil, fmt.Errorf("refusing to overwrite %s: it already holds verdict records (the retained precision record)", session.FindingsFile)
	}

	if err := session.WriteJSONL(filepath.Join(dir, session.FindingsFile), findings); err != nil {
		return nil, fmt.Errorf("write findings: %w", err)
	}
	return findings, nil
}

// oversizedFindings reports any finding whose findings.jsonl line — its JSON
// encoding plus the newline WriteJSONL appends — would exceed
// session.MaxJSONLLine, the shared invariant every reader scans to. maxEvidence
// bounds how many ids a finding may cite, but nothing bounds the length of a
// verbatim quote, so a finding with a perfectly valid evidence array and an
// enormous quote still serialises to a line no reader can take back: report,
// review, and the re-ingest recovery path would all fail on the file Ingest had
// just reported writing successfully. The check therefore runs before any write
// and joins the transactional error set, so an over-long finding leaves the
// previous findings.jsonl untouched rather than bricking it. Labels come from
// each finding's answer position for the same reason validate's do.
func oversizedFindings(findings []Finding, decoded []positioned) []error {
	var errs []error
	for i, f := range findings {
		label := findingLabel(f, decoded[i].at)
		line, err := json.Marshal(f)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: cannot encode as JSON: %w", label, err))
			continue
		}
		if len(line)+1 > session.MaxJSONLLine {
			errs = append(errs, fmt.Errorf("%s: encodes to %d bytes, exceeding the %d-byte %s line limit", label, len(line)+1, session.MaxJSONLLine, session.FindingsFile))
		}
	}
	return errs
}

// parseContainer accepts either a top-level object with a "findings" array (the
// preferred container, optionally carrying a "rubric") or a bare array of
// findings. It returns the raw finding elements and the rubric string (empty
// for the bare-array form).
func parseContainer(data []byte) ([]json.RawMessage, string, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, "", fmt.Errorf("empty answer: expected a JSON object or array of findings")
	}
	switch trimmed[0] {
	case '[':
		var arr []json.RawMessage
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, "", fmt.Errorf("parse findings array: %w", err)
		}
		return arr, "", nil
	case '{':
		var doc struct {
			Rubric   string            `json:"rubric"`
			Findings []json.RawMessage `json:"findings"`
		}
		if err := json.Unmarshal(trimmed, &doc); err != nil {
			return nil, "", fmt.Errorf("parse answer: %w", err)
		}
		if doc.Findings == nil {
			return nil, "", fmt.Errorf("answer object has no \"findings\" array")
		}
		return doc.Findings, doc.Rubric, nil
	default:
		return nil, "", fmt.Errorf("expected a JSON object or array of findings")
	}
}

// rawFinding is how one element of the untrusted answer is decoded before it is
// trusted. Its T is a pointer so that an absent "t" stays distinguishable from a
// genuine 0: a finding anchored at the very start of the session legitimately
// carries t equal to 0, so a value-typed field cannot tell the two apart. With
// one, an answer omitting "t" decodes to 0 and sails through validate's range
// check — whose floor is 0 for any normal session — and the finding is filed at
// [00:00] by report and review, tens of seconds from the utterance it quotes,
// with nothing on the record to say the model never placed it. Every other
// required field's absence is already caught by its own rule, so t is the lone
// one needing this. Everything else mirrors Finding, which is the shape
// DisallowUnknownFields is closed against.
type rawFinding struct {
	ID       string   `json:"id"`
	T        *float64 `json:"t"`
	Type     string   `json:"type"`
	Severity int      `json:"severity"`
	Mode     string   `json:"mode,omitempty"`
	Quote    string   `json:"quote"`
	Evidence []string `json:"evidence"`
	UI       *UI      `json:"ui,omitempty"`
	Status   string   `json:"status"`
}

// decodeFinding strictly decodes one finding element. DisallowUnknownFields
// closes the shape: a hallucinated or mistyped field is a hard error rather
// than silently dropped, and it applies to the nested ui object too. A missing
// "t" is rejected here rather than in validate, because by the time a finding
// reaches validate its unset t is indistinguishable from an honest 0 (see
// rawFinding).
func decodeFinding(raw json.RawMessage) (Finding, error) {
	var rf rawFinding
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rf); err != nil {
		return Finding{}, err
	}
	if rf.T == nil {
		return Finding{}, fmt.Errorf("missing t; a finding must name the moment it is anchored to")
	}
	return Finding{
		ID:       rf.ID,
		T:        *rf.T,
		Type:     rf.Type,
		Severity: rf.Severity,
		Mode:     rf.Mode,
		Quote:    rf.Quote,
		Evidence: rf.Evidence,
		UI:       rf.UI,
		Status:   rf.Status,
	}, nil
}

// holdsVerdicts reports whether an existing findings.jsonl already contains any
// verdict record. A missing file is not an error (false, nil). It scans for raw
// kind:"verdict" lines rather than reusing analyze.Load, whose verdict slice is
// filtered to the closed enum (confirmed|rejected|duplicate): a hand-edited or
// shared file whose only verdict lines carry a foreign or typo'd value would
// otherwise slip past the guard and have its human-decision records truncated by
// a re-ingest — exactly the precision history the guard exists to protect.
func holdsVerdicts(dir string) (bool, error) {
	path := filepath.Join(dir, session.FindingsFile)
	// Read-side no-follow guard rather than plain os.Open: a FIFO planted at
	// findings.jsonl in an exchanged session would otherwise hang this open for
	// ever. A missing file still satisfies os.ErrNotExist and is not an error here.
	f, err := session.OpenFileNoFollowRead(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
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
		if probe.Kind == "verdict" {
			return true, nil
		}
	}
	if err := sc.Err(); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return false, nil
}
