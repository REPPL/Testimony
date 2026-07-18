package analyze

import (
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

	var (
		findings []Finding
		errs     []error
	)
	for i, raw := range raws {
		f, derr := decodeFinding(raw)
		if derr != nil {
			errs = append(errs, fmt.Errorf("finding #%d: %v", i+1, derr))
			continue
		}
		findings = append(findings, f)
	}
	errs = append(errs, validate(findings, idx)...)
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	if held, err := holdsVerdicts(dir); err != nil {
		return nil, err
	} else if held {
		return nil, fmt.Errorf("refusing to overwrite %s: it already holds verdict records (the retained precision record)", session.FindingsFile)
	}

	// The model is never trusted: every finding lands unverified.
	for i := range findings {
		findings[i].Status = "unverified"
	}
	if err := session.WriteJSONL(filepath.Join(dir, session.FindingsFile), findings); err != nil {
		return nil, fmt.Errorf("write findings: %w", err)
	}
	return findings, nil
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

// decodeFinding strictly decodes one finding element. DisallowUnknownFields
// closes the shape: a hallucinated or mistyped field is a hard error rather
// than silently dropped, and it applies to the nested ui object too.
func decodeFinding(raw json.RawMessage) (Finding, error) {
	var f Finding
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return f, err
	}
	return f, nil
}

// holdsVerdicts reports whether an existing findings.jsonl already contains any
// verdict record. A missing file is not an error (false, nil).
func holdsVerdicts(dir string) (bool, error) {
	_, verdicts, err := Load(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return len(verdicts) > 0, nil
}
