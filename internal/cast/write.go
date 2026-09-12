package cast

import (
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

// rewriteInteractions replaces interactions.jsonl whole: prior records from
// this importer are dropped, every other line is kept byte-for-byte in file
// order, and the new records are appended. It returns how many prior records
// were replaced.
//
// The rewrite cannot go through session.WriteJSONL: that encodes values, and
// would silently rewrite a foreign record's field order, number formatting, or
// an unknown field it cannot model. The checks WriteJSONL would have applied
// are therefore applied here instead, before anything is written — plus the
// merged-timeline total, which demo's capture endpoint can only estimate and an
// offline importer can measure.
//
// castName is the cast's display name, for the size refusal; t0 anchors the
// merged-timeline measurement.
func rewriteInteractions(dir, castName string, t0 int64, records []timeline.Interaction) (replaced int, err error) {
	path := filepath.Join(dir, session.InteractionsFile)
	prior, err := readInteractionLines(path)
	if err != nil {
		return 0, err
	}

	var buf bytes.Buffer
	for _, l := range prior.kept {
		buf.Write(l)
		// A final line with no terminating newline is the one byte this rewrite
		// adds: without it the first imported record would be appended onto that
		// line and neither would survive a read back.
		buf.WriteByte('\n')
	}
	for i, rec := range records {
		line, err := encodeRecord(rec)
		if err != nil {
			return 0, fmt.Errorf("record %d: %w", i+1, err)
		}
		if len(line)+1 > session.MaxJSONLLine {
			return 0, fmt.Errorf("record %d encodes to %d bytes, over the %d-byte JSONL line limit", i+1, len(line)+1, session.MaxJSONLLine)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if buf.Len() > session.MaxJSONLBytes {
		return 0, fmt.Errorf("importing %s would take %s past its %d-byte limit (%d bytes across %d lines); record shorter terminal sessions, or start a fresh session",
			castName, session.InteractionsFile, session.MaxJSONLBytes, buf.Len(), len(prior.kept)+len(records))
	}
	if err := checkMergedTimelineFits(dir, t0, prior.sized, records); err != nil {
		return 0, err
	}

	// Temp file plus rename, symlink refused up front, an existing file's mode
	// preserved exactly: a failure at any point leaves the prior
	// interactions.jsonl untouched.
	if err := session.WriteFileAtomicNoFollow(path, buf.Bytes(), 0o644); err != nil {
		return 0, err
	}
	return prior.replaced, nil
}

// priorInteractions is what a rewrite keeps of the file it replaces.
type priorInteractions struct {
	kept     [][]byte               // lines preserved verbatim, in file order
	replaced int                    // records from an earlier import, dropped
	sized    []timeline.Interaction // kept lines that decode, for the merged-timeline measurement
}

// readInteractionLines reads the existing interactions.jsonl and classifies its
// lines. A missing file is zero lines, not an error.
//
// A line whose kind equals OutputKind is a record from an earlier import and is
// dropped — that marker, and nothing else, is how a re-run identifies its own
// prior output. Every other line — a demo click, an input, a line this importer
// cannot decode at all, a blank line — is kept exactly as it was read, which is
// what makes a re-import byte-identical and leaves a -demo session's capture
// untouched.
func readInteractionLines(path string) (priorInteractions, error) {
	var prior priorInteractions
	// The no-follow guard, so a FIFO or symlink planted at the name in a received
	// session is refused rather than followed or blocked on.
	f, err := session.OpenFileNoFollowRead(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return prior, nil
		}
		return prior, err
	}
	defer f.Close()

	// Read one byte past the cap so an over-large file is refused as too big
	// rather than silently truncated and then rewritten short — the same pair of
	// bounds session.ReadJSONL enforces, read whole because the replacement is
	// assembled in memory anyway.
	b, err := io.ReadAll(io.LimitReader(f, session.MaxJSONLBytes+1))
	if err != nil {
		return prior, err
	}
	if len(b) > session.MaxJSONLBytes {
		return prior, fmt.Errorf("%s: exceeds %d bytes; refusing to read", path, session.MaxJSONLBytes)
	}
	line := 0
	for len(b) > 0 {
		line++
		raw := b
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			raw, b = b[:i], b[i+1:]
		} else {
			b = nil
		}
		if len(raw)+1 > session.MaxJSONLLine {
			return priorInteractions{}, fmt.Errorf("%s:%d: line exceeds %d bytes; refusing to read", path, line, session.MaxJSONLLine)
		}
		var probe struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.Kind == OutputKind {
			prior.replaced++
			continue
		}
		prior.kept = append(prior.kept, raw)
		// An interaction line that does not decode is not sized: merge will refuse
		// that session for its own, pre-existing reason, and import must not be
		// blamed for it.
		var rec timeline.Interaction
		if json.Unmarshal(raw, &rec) == nil {
			prior.sized = append(prior.sized, rec)
		}
	}
	return prior, nil
}

// checkMergedTimelineFits measures the timeline.jsonl this import implies —
// every decodable interaction plus, when transcript.jsonl is present, every
// utterance — against the same total-size cap session.WriteJSONL applies when
// merge writes it. Like demo's running estimate, the guarantee is "as of import
// time": a transcribe run afterwards adds speech entries this pass cannot see.
func checkMergedTimelineFits(dir string, t0 int64, prior, records []timeline.Interaction) error {
	total := 0
	for _, set := range [][]timeline.Interaction{prior, records} {
		for _, rec := range set {
			n, err := session.EncodedLen(timeline.EventEntry(rec, t0))
			if err != nil {
				return err
			}
			total += n
		}
	}
	// An unreadable or malformed transcript is not sized, for readInteractionLines'
	// reason: merge refuses that session on its own account, and this pre-flight
	// must not turn a pre-existing fault into an import refusal.
	if utts, err := session.ReadJSONL[timeline.Utterance](filepath.Join(dir, session.TranscriptFile)); err == nil {
		for _, u := range utts {
			n, err := session.EncodedLen(timeline.SpeechEntry(u))
			if err != nil {
				return err
			}
			total += n
		}
	}
	if total > session.MaxJSONLBytes {
		return fmt.Errorf("the imported records would take the merged %s past its %d-byte limit; record shorter terminal sessions, or start a fresh session",
			session.TimelineFile, session.MaxJSONLBytes)
	}
	return nil
}

// encodeRecord renders one record as the JSONL line session.WriteJSONL would
// write for it, without the terminating newline. HTML escaping is off, matching
// session.EncodedLen's encoder, so a record's measured size and its written
// bytes cannot disagree.
func encodeRecord(rec timeline.Interaction) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rec); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// stageCast streams the operator's cast into a temp file beside terminal.cast
// and returns its path. import copies the file rather than requiring it to be
// in place already, mirroring transcribe -audio: the hand-off pattern's whole
// point is that the operator hands over a file and the session becomes
// self-contained.
//
// The prior-mode rule is transcribe.atomicConvert's: an existing
// terminal.cast's own mode is reapplied, and a new file takes 0o644 &^ umask,
// so a privacy-conscious operator's umask is honoured. The caller removes the
// temp file on every failure path.
func stageCast(dir, src string) (string, error) {
	target := filepath.Join(dir, session.TerminalCastFile)
	// checkPlainOutput's rule: a symlink planted at the archive name would
	// redirect the copy outside the session, and a FIFO would block the rename's
	// successor for ever. Refused before the temp file is created.
	priorPerm, havePrior := os.FileMode(0), false
	if fi, err := os.Lstat(target); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refusing to write %s: it is a symlink", target)
		}
		if !fi.Mode().IsRegular() {
			return "", fmt.Errorf("refusing to write %s: it is not a regular file", target)
		}
		priorPerm, havePrior = fi.Mode().Perm(), true
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	in, err := os.Open(src)
	if err != nil {
		return "", fmt.Errorf("cast file: %w", err)
	}
	defer in.Close()

	tmp, err := os.CreateTemp(dir, "."+session.TerminalCastFile+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("archive terminal cast: %w", err)
	}
	tmpPath := tmp.Name()
	fail := func(err error) (string, error) {
		tmp.Close()
		os.Remove(tmpPath)
		return "", err
	}
	// One byte past the cap, so a file that grew past the bound between the scan
	// and the copy is refused rather than archived truncated — the archive is the
	// one place this design makes a byte-for-byte claim.
	n, err := io.Copy(tmp, io.LimitReader(in, maxCastBytes+1))
	if err != nil {
		return fail(fmt.Errorf("archive terminal cast: %w", err))
	}
	if n > maxCastBytes {
		return fail(fmt.Errorf("%s: exceeds %d bytes; refusing to read", src, maxCastBytes))
	}
	perm := priorPerm
	if !havePrior {
		// os.CreateTemp reserves the name at 0600; restore the mode a plain create
		// would have given the file, so the archive matches every sibling artefact
		// rather than staying private by accident of the temp file's mode. The
		// brief probe is safe here — import creates no other file concurrently.
		um := syscall.Umask(0)
		syscall.Umask(um)
		perm = 0o644 &^ os.FileMode(um)
	}
	if err := tmp.Chmod(perm); err != nil {
		return fail(fmt.Errorf("archive terminal cast: %w", err))
	}
	// Close before rename, and surface the Close error: a filesystem that defers
	// write-back errors to close would otherwise rename a corrupt copy into place
	// (session.WriteFileAtomicNoFollow's identical stance).
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("archive terminal cast: %w", err)
	}
	return tmpPath, nil
}

// commitCast renames a staged copy over terminal.cast. It runs last, after the
// records are written, so the only residual state a failure can leave is
// records with no archival copy.
func commitCast(tmpPath string) error {
	target := filepath.Join(filepath.Dir(tmpPath), session.TerminalCastFile)
	if err := os.Rename(tmpPath, target); err != nil {
		return fmt.Errorf("archive terminal cast: %w", err)
	}
	return nil
}
