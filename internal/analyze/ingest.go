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
	"syscall"

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

	if err := commitFindings(dir, findings); err != nil {
		return nil, err
	}
	return findings, nil
}

// commitFindings runs the verdict guard and the truncating write as one locked
// step. Probing with holdsVerdicts and then calling session.WriteJSONL as two
// separate opens left a TOCTOU window: a concurrent `testimony review` commits a
// verdict (under its own lock, see review.AppendVerdict) between the probe and
// the O_TRUNC open, and the rewrite destroys it — precisely the human-decision
// record the guard exists to protect. Holding one exclusive advisory lock across
// probe, truncate, and write forecloses the interleaving: AppendVerdict blocks
// until the commit completes, so a verdict is either visible to the probe (and
// the re-ingest refused) or appended after the new findings. The findings were
// already held to session.MaxJSONLLine by oversizedFindings, so writing through
// the locked descriptor keeps the read-side invariant WriteJSONL enforces.
func commitFindings(dir string, findings []Finding) error {
	path := filepath.Join(dir, session.FindingsFile)
	f, err := session.OpenFileNoFollow(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return err
	}
	held, err := holdsVerdicts(f, path)
	if err != nil {
		f.Close()
		return err
	}
	if held {
		f.Close()
		return fmt.Errorf("refusing to overwrite %s: it already holds verdict records (the retained precision record)", session.FindingsFile)
	}
	if err := writeFindings(f, findings); err != nil {
		f.Close()
		return err
	}
	// Close releases the lock with the descriptor.
	if err := f.Close(); err != nil {
		return fmt.Errorf("write findings: %w", err)
	}
	return nil
}

// findingsFile is the subset of *os.File writeFindings needs; a fake satisfies it
// in tests to exercise the truncate-then-write rollback.
type findingsFile interface {
	io.Writer
	Truncate(size int64) error
	Seek(offset int64, whence int) (int64, error)
}

// writeFindings replaces f's contents with the findings, one JSON object per line,
// rolling the file back to empty if the write only partly lands.
//
// The whole set is encoded into one buffer before f is truncated, so the truncate
// and the write are a single Write of pre-built bytes rather than a streamed series
// of encodes that a mid-way I/O error (ENOSPC) could leave half-flushed. That
// matters because commitFindings ran f.Truncate(0) first: without the rollback a
// short write left findings.jsonl holding a truncated, newline-less JSON fragment,
// which not only breaks every reader but blocks the tool's own recovery — the next
// analyze -ingest calls holdsVerdicts, which json.Unmarshals every line and errors
// on the fragment before it can conclude "no verdicts" and rewrite. On any write
// error the file is therefore truncated back to empty: an empty findings.jsonl is
// parseable (zero findings, zero verdicts) and re-ingestable, so the failure state
// no longer forecloses its own repair. This is the same partial-write rollback
// review.writeVerdict and demo.appendRecords already apply; commitFindings was the
// one writer without it. The set is bounded by oversizedFindings before this runs,
// so buffering it whole holds one bounded answer, not an unbounded stream.
func writeFindings(f findingsFile, findings []Finding) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, v := range findings {
		if err := enc.Encode(v); err != nil {
			return fmt.Errorf("write findings: %w", err)
		}
	}
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("write findings: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("write findings: %w", err)
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		// Best-effort roll back to an empty (parseable, re-ingestable) file, then
		// surface the original error.
		f.Truncate(0)
		return fmt.Errorf("write findings: %w", err)
	}
	return nil
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

// holdsVerdicts reports whether the findings.jsonl open on f already contains
// any verdict record. It reads through the caller's descriptor — opened under
// the no-follow guard and exclusively locked by commitFindings — rather than
// opening the path itself, so the probe and the write it gates observe the same
// locked file. It scans for raw kind:"verdict" lines rather than reusing
// analyze.Load, whose verdict slice is filtered to the closed enum
// (confirmed|rejected|duplicate): a hand-edited or shared file whose only
// verdict lines carry a foreign or typo'd value would otherwise slip past the
// guard and have its human-decision records truncated by a re-ingest — exactly
// the precision history the guard exists to protect.
func holdsVerdicts(f io.Reader, path string) (bool, error) {
	sc := bufio.NewScanner(f)
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
		if probe.Kind == "verdict" {
			return true, nil
		}
	}
	if err := sc.Err(); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return false, nil
}
