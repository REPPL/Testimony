// Package session defines the on-disk layout of a testimony session and
// helpers for reading and writing its artefacts.
//
// A session directory contains (see docs/reference/session-directory.md):
//
//	manifest.json       session metadata, including t0_epoch_ms
//	audio.wav           16 kHz mono ASR input (local only)
//	audio.offset.json   audio→session offset for an external recording (local only)
//	screen.mp4          screen recording (local only; -video capture)
//	events.rrweb.jsonl  raw rrweb events (archival; web sessions only)
//	interactions.jsonl  normalised interaction events (epoch ms)
//	transcript.jsonl    word-aligned utterances (session-relative seconds)
//	timeline.jsonl      merged, session-relative timeline
//	findings.jsonl      analysis findings + appended verdicts
//	report.md           human-readable session report
package session

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
)

// Manifest describes a capture session. t0_epoch_ms anchors all
// session-relative times: relative_seconds = (epoch_ms - t0_epoch_ms) / 1000.
type Manifest struct {
	Session     string   `json:"session"`
	App         string   `json:"app,omitempty"`
	Commit      string   `json:"commit,omitempty"`
	Participant string   `json:"participant,omitempty"`
	T0EpochMS   int64    `json:"t0_epoch_ms"`
	Tasks       []string `json:"tasks,omitempty"`
	Notes       string   `json:"notes,omitempty"`
}

// Well-known file names inside a session directory.
const (
	ManifestFile     = "manifest.json"
	AudioFile        = "audio.wav"
	AudioOffsetFile  = "audio.offset.json"
	ScreenFile       = "screen.mp4"
	RawEventsFile    = "events.rrweb.jsonl"
	InteractionsFile = "interactions.jsonl"
	TranscriptFile   = "transcript.jsonl"
	TimelineFile     = "timeline.jsonl"
	FindingsFile     = "findings.jsonl"
	ReportFile       = "report.md"
)

// dirLayout is the timestamped session-directory name format, derived from
// the capture start instant so the directory name and t0_epoch_ms agree.
const dirLayout = "2006-01-02_150405"

// Create makes a fresh, timestamped session directory under outRoot and
// writes its manifest. The directory name and m.T0EpochMS are both derived
// from the single now instant, so t0 is a recorded fact rather than a
// recollection; m.Session is set to the directory's base name. It returns the
// created directory path. Both demo and record call this so the manifest is
// written once, by one code path.
func Create(outRoot string, now time.Time, m Manifest) (dir string, err error) {
	if err := os.MkdirAll(outRoot, 0o755); err != nil {
		return "", err
	}
	dir = filepath.Join(outRoot, now.Format(dirLayout))
	// os.Mkdir (not MkdirAll) fails with EEXIST if the directory already exists,
	// so two captures starting within the same second-granularity instant cannot
	// silently resolve to — and share — one directory. Reusing it would clobber
	// the first session's manifest (its t0 anchor) and conflate the two sessions'
	// append-only streams and capture files.
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", err
	}
	m.Session = filepath.Base(dir)
	m.T0EpochMS = now.UnixMilli()
	if err := SaveManifest(dir, m); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// ErrNoT0 reports a manifest that carries no usable capture anchor. It is
// returned by Manifest.T0 and is worth matching with errors.Is when a caller
// wants to distinguish "this session cannot be placed on a wall clock" from an
// unreadable or malformed manifest.
var ErrNoT0 = errors.New("manifest is missing t0_epoch_ms")

// T0 returns the session's anchor instant in epoch milliseconds, or ErrNoT0
// when the manifest carries none. Every caller that converts epoch-ms times to
// session-relative ones — timeline.BuildEntries, transcribe's audio-offset
// derivation — must obtain t0 through here rather than reading the field
// directly.
//
// The check is needed because T0EpochMS is a value-typed int64: a manifest that
// simply omits t0_epoch_ms decodes to 0, which is indistinguishable from a
// recorded zero and is then subtracted from real epoch-ms timestamps. That
// places every event about fifty-seven years into the session and writes a
// silently corrupt timeline — wrong, plausible-looking numbers, which is worse
// than a refusal, because a report built on them reads as evidence.
//
// Treating 0 as absent is safe rather than merely convenient: a genuine
// t0_epoch_ms of 0 is midnight on 1 January 1970, which is not a capture
// instant any recorder can produce. Create derives t0 from the same now that
// names the session directory, so every manifest this tool writes has one.
// Negative values are refused on the same reasoning — they anchor the session
// before the epoch, and no capture starts there.
//
// The check deliberately lives here and not in LoadManifest. Several consumers
// legitimately load a manifest they need no anchor from — report.Render works
// from an already session-relative timeline.jsonl, and analyze.EmitRequest
// reads only the app, participant, and task context — so refusing at load time
// would fail commands that have no use for t0 and no way to be wrong about it.
func (m Manifest) T0() (int64, error) {
	if m.T0EpochMS <= 0 {
		return 0, fmt.Errorf("%w (session %q); cannot place epoch-millisecond times on the session clock", ErrNoT0, m.Session)
	}
	return m.T0EpochMS, nil
}

// maxManifestBytes caps LoadManifest's read of manifest.json. A genuine manifest
// is a few hundred bytes; 1 MiB is generous for one carrying long notes or a big
// task list. The cap matters because manifest.json in an exchanged session is
// attacker-controllable (see Manifest.T0's threat note), and an attacker ships
// a multi-gigabyte manifest (a few KB once zipped) that any command loading it
// — merge, report, analyze, transcribe — would otherwise buffer into memory
// before parsing and drive the process into OOM. Every sibling reader of an
// untrusted session's JSONL files is bounded the same way: ReadJSONL caps both
// a line (MaxJSONLLine) and the whole file (MaxJSONLBytes), and
// analyze.ParseRecords — findings.jsonl's own scanner, not routed through
// ReadJSONL — carries the same pair of caps. analyze.Ingest caps the untrusted
// answer it validates at maxAnswerBytes, and the demo body caps what it
// accepts at capture time; neither reads a session's own JSONL files back.
const maxManifestBytes = 1 << 20 // 1 MiB

// LoadManifest reads manifest.json from dir.
func LoadManifest(dir string) (Manifest, error) {
	var m Manifest
	// Read through the no-follow guard rather than os.ReadFile: manifest.json in
	// an exchanged session is attacker-controllable, and a FIFO planted there
	// would block os.ReadFile in open(2) for ever waiting for a writer, hanging
	// any command that loads the manifest.
	f, err := OpenFileNoFollowRead(filepath.Join(dir, ManifestFile))
	if err != nil {
		return m, fmt.Errorf("load manifest: %w", err)
	}
	defer f.Close()
	// Read one byte past the cap so an over-large manifest is refused as too big
	// rather than silently truncated and then misreported as malformed JSON.
	b, err := io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if err != nil {
		return m, fmt.Errorf("load manifest: %w", err)
	}
	if len(b) > maxManifestBytes {
		return m, fmt.Errorf("load manifest: %s exceeds %d bytes; refusing to read", ManifestFile, maxManifestBytes)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("parse manifest: %w", err)
	}
	return m, nil
}

// SaveManifest writes manifest.json into dir.
func SaveManifest(dir string, m Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	// Refuse before writing rather than after: without this check, a manifest
	// built from long -task/-app text (or a hand-edited notes field) could
	// exceed maxManifestBytes and still be written successfully, only for
	// every later command that loads it
	// (merge, report, analyze, transcribe) to refuse the session for good — the
	// same write-before-read invariant WriteJSONL enforces for the other
	// session artefacts.
	if len(b) > maxManifestBytes {
		return fmt.Errorf("save manifest: %s would be %d bytes, over the %d-byte limit LoadManifest enforces; refusing to write a session no command could read back", ManifestFile, len(b), maxManifestBytes)
	}
	return WriteFileNoFollow(filepath.Join(dir, ManifestFile), b, 0o644)
}

// openNoFollow is the single symlink-and-regular-file guard shared by every
// session-artefact open, read or write. It opens path with O_NOFOLLOW, so a
// symlink planted at the final path component is refused rather than followed,
// and refuses any opened path that is not a regular file. A session directory is
// an exchange unit (a shared or downloaded session may be attacker-authored);
// without the symlink guard a planted symlink — e.g. a timeline.jsonl pointing
// at a private key file — would redirect a write to an arbitrary file outside
// the session directory, and without the regular-file guard a FIFO planted at
// the same path would hang the CLI in open(2) for ever: on the write side
// waiting for a reader that never arrives, and on the read side waiting for a
// writer that never arrives, so merge, report, or analyze never returns on a
// session the operator merely received.
//
// O_NONBLOCK is what makes the regular-file check reachable at all: opening a
// FIFO — for reading or for writing — normally blocks until the other end is
// present, but with O_NONBLOCK the open returns immediately (a read-only FIFO
// open succeeds at once; a write-only one fails with ENXIO), so fstat can then
// run and refuse it. It has no effect on the ordinary case, because opening a
// regular file never blocks and the flag does not alter subsequent reads or
// writes on one. flag is OR-ed with O_NOFOLLOW and O_NONBLOCK.
//
// verb ("read"/"write") is woven into the refusal messages so they name the
// direction; OpenFileNoFollow keeps verb="write" verbatim so callers and tests
// that assert "refusing to write ..." are undisturbed.
func openNoFollow(path string, flag int, perm os.FileMode, verb string) (*os.File, error) {
	f, err := os.OpenFile(path, flag|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, perm)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("refusing to %s %s: it is a symlink", verb, path)
		}
		return nil, err
	}
	// Stat the descriptor rather than the path, so the answer describes the file
	// that was actually opened and cannot be swapped between the check and the
	// read or write.
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("refusing to %s %s: it is not a regular file", verb, path)
	}
	return f, nil
}

// OpenFileNoFollow opens path for writing under the shared openNoFollow guard,
// refusing a planted symlink or non-regular file (see openNoFollow for the full
// threat model). Callers pass the usual O_CREATE/O_TRUNC/O_APPEND/O_WRONLY set;
// O_NOFOLLOW and O_NONBLOCK are added by the guard.
func OpenFileNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	return openNoFollow(path, flag, perm, "write")
}

// OpenFileNoFollowRead opens path read-only under the same guard, so the read
// side of the pipeline is protected too: a FIFO planted at timeline.jsonl,
// interactions.jsonl, transcript.jsonl, findings.jsonl, or manifest.json in an
// exchanged session is refused immediately rather than blocking ReadJSONL or
// LoadManifest in open(2) for ever, and a symlink is refused rather than
// followed out of the session directory. The caller owns the returned file and
// must Close it.
func OpenFileNoFollowRead(path string) (*os.File, error) {
	return openNoFollow(path, os.O_RDONLY, 0, "read")
}

// WriteFileNoFollow is os.WriteFile that refuses to follow a symlink at path
// (see OpenFileNoFollow). It truncates an existing regular file, as os.WriteFile
// does.
func WriteFileNoFollow(path string, data []byte, perm os.FileMode) error {
	f, err := OpenFileNoFollow(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// WriteFileAtomicNoFollow replaces path with data all-or-nothing: the bytes
// are written to a same-directory temp file and renamed into place, so a
// failure at any point leaves whatever was at path untouched. WriteFileNoFollow
// cannot promise that — its O_TRUNC open destroys the prior bytes before the
// first write, so a write that then fails leaves a truncated file AND an error,
// which for the offset sidecar meant the one durable record of an external
// audio's clock offset was gone with nothing to roll back to. The no-follow
// guarantee is kept by refusing up front when path names a symlink (rename
// would otherwise replace the planted link rather than follow it, but refusal
// matches WriteFileNoFollow so a hostile session directory cannot even retarget
// the name); the temp file is created fresh in the same directory, so the
// rename never crosses a filesystem.
//
// Permission semantics: a NEW file is created with perm filtered by the
// process umask, exactly as WriteFileNoFollow's open(2) would create it; an
// EXISTING regular file's own mode is preserved exactly across the
// replacement (applied with fchmod on the temp file, which the umask does not
// filter), so an operator-tightened sidecar stays tightened and a
// deliberately widened one is not silently narrowed. Two deliberate
// differences from a truncating open, both inherent to rename-into-place: a
// read-only target is replaced (rename consults the directory's permissions,
// not the file's), and a crash between create and rename can leave a
// dot-prefixed ".<name>.tmp-*" file behind — nothing reads it, and the next
// successful write does not depend on it. Temp names carry a random suffix so
// an attacker who can write the directory cannot pre-plant the whole name
// space and deterministically block the write (O_EXCL already stops
// write-through).
func WriteFileAtomicNoFollow(path string, data []byte, perm os.FileMode) error {
	priorPerm, havePrior := os.FileMode(0), false
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write %s: it is a symlink", path)
		}
		if fi.Mode().IsRegular() {
			priorPerm, havePrior = fi.Mode().Perm(), true
		}
	}
	dir, base := filepath.Dir(path), filepath.Base(path)
	var f *os.File
	var tmp string
	for i := 0; ; i++ {
		var rnd [4]byte
		if _, err := rand.Read(rnd[:]); err != nil {
			return err
		}
		tmp = filepath.Join(dir, fmt.Sprintf(".%s.tmp-%x", base, rnd))
		var err error
		f, err = os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) || i >= 100 {
			return err
		}
	}
	cleanup := func() {
		f.Close()
		os.Remove(tmp)
	}
	if havePrior {
		if err := f.Chmod(priorPerm); err != nil {
			cleanup()
			return err
		}
	}
	if _, err := f.Write(data); err != nil {
		cleanup()
		return err
	}
	// Close before rename, and surface the Close error: a filesystem that
	// defers write-back errors to close would otherwise rename a corrupt temp
	// file into place (see WriteJSONL's identical stance).
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// SafeText neutralises untrusted text before it is written into a human-facing
// artefact (report.md) or a terminal line (review). It strips C0/C1 control
// bytes — including the newline and carriage return that could forge report
// structure or split a JSONL record, and the ESC (0x1b) that drives ANSI
// terminal sequences — turns tabs into spaces, and removes every Unicode
// format character (category Cf) along with the line and paragraph
// separators. Cf covers the complete Bidi_Control set behind Trojan-Source
// spoofing (CVE-2021-42574) — so a right-to-left override or an Arabic letter
// mark cannot make a displayed quote or anchor differ from the bytes a verdict
// is recorded against — and equally the invisible characters outside it (ZWSP
// U+200B, word joiner U+2060, ZWNBSP/BOM U+FEFF, soft hyphen U+00AD, the
// U+E00xx tag block) that render as nothing in a terminal or Markdown viewer
// while surviving in the bytes: enumerating the bidi set alone left those as an
// ASCII-smuggling residue, hiding text (for instance an instruction to the
// analysis agent) inside a request or quote the operator reads as clean.
// Attacker-authored transcript, interaction, manifest, and finding text
// therefore cannot inject headings, terminal control sequences, extra lines,
// reordered glyphs, or invisibly-smuggled text. Ordinary text is unchanged.
func SafeText(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
			return -1
		// Every format character, plus the line and paragraph separators (Zl/Zp,
		// outside Cf): the category test subsumes the Bidi_Control enumeration and
		// closes the invisible-Cf residue in one predicate, so no member can be
		// left out to do the reordering or smuggling the rest are stripped to
		// prevent.
		case unicode.Is(unicode.Cf, r),
			r == 0x2028 || r == 0x2029: // line / paragraph separator
			return -1
		default:
			return r
		}
	}, s)
}

// SafeInline renders untrusted text inert for a Markdown INLINE context:
// SafeText first (control/format/bidi bytes, and the newlines that could forge
// block structure), then a backslash before each inline-Markdown trigger
// SafeText deliberately passes through. Without the second step a manifest or
// finding value of `![x](http://host/beacon.png)` renders a live remote image —
// a tracking/exfil beacon fired the instant the artefact is opened in a
// Markdown viewer — and `[label](http://host)` an active link disguised as
// evidence. Backslash-escaping renders the triggers as literal text in a viewer
// and keeps them readable in source; ordinary text carries none of these bytes
// and is byte-for-byte unchanged. This is the one home for the escape set, so
// every Markdown artefact built from untrusted text (report.md, the emitted
// analysis request) neutralises the same constructs the same way.
func SafeInline(s string) string {
	s = SafeText(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\', '`', '*', '_', '[', ']', '(', ')', '!', '<', '>', '~':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// SafeTextLines applies SafeText to s one line at a time, preserving the
// newlines SafeText itself would strip (they fall under r < 0x20). A
// subprocess's captured output — ffmpeg's multi-line metadata dump, a device
// listing — is diagnostic text an operator reads across several lines;
// collapsing it to one line to close the same bidi/invisible-Cf smuggling
// gap SafeText closes elsewhere would cost more readability than the gap is
// worth. Splitting on "\n" first still leaves each line individually safe:
// SafeText strips \r within a line, so no line can reintroduce a carriage
// return to overwrite what precedes it.
func SafeTextLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = SafeText(l)
	}
	return strings.Join(lines, "\n")
}

// MaxJSONLLine is the largest single JSONL record the readers accept. It is the
// shared read-side invariant every writer must respect: a record persisted above
// this size is durably unreadable, breaking merge, report, or analyze for the
// whole session, so the capture endpoints reject anything larger rather than
// accept a line no reader can take back.
const MaxJSONLLine = 4 << 20 // 4 MiB

// MaxJSONLBytes caps a JSONL reader's total read across every line in a file,
// the counterpart to MaxJSONLLine bounding a single one. A per-line cap alone
// leaves total file size unbounded: a session's JSONL artefacts are
// attacker-controllable when exchanged (see ReadJSONL's no-follow comment),
// and a file built from many small, well-formed lines defeats MaxJSONLLine
// while still driving json.Unmarshal's per-line allocation into hundreds of
// megabytes for a file only tens of megabytes on disk. Both ReadJSONL and
// analyze.ParseRecords (findings.jsonl's own scanner) enforce it. 16 MiB
// matches the cap analyze.Ingest already applies to untrusted input at the
// same scale; a genuine session's timeline, interactions, transcript, or
// findings file is a small fraction of that. events.rrweb.jsonl is archival
// only — no command reads it back through either scanner, so this cap does
// not bound it.
const MaxJSONLBytes = 16 << 20 // 16 MiB

// jsonlEncoder returns a json.Encoder configured exactly as WriteJSONL's own
// encoders are, so a size measured against it predicts what WriteJSONL will
// later check and write. HTML escaping is disabled: JSONL artefacts are never
// embedded in HTML, and escaping turns a literal <, >, or & into a six-byte
// \uXXXX sequence — inflation compactLine (the capture-side line canonicaliser)
// never applies, so a record sized against an escaping encoder could pass a
// capture-time guard and still be rejected once WriteJSONL re-encodes it.
func jsonlEncoder(w io.Writer) *json.Encoder {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc
}

// EncodedLen returns the byte length WriteJSONL would give v as one JSONL
// line, including its terminating newline. It exists so a capture-time guard
// can check a value it is about to hand to a later WriteJSONL call — such as
// the timeline entry a captured interaction or utterance will become at merge
// — against the same measurement WriteJSONL's own pre-flight pass makes,
// rather than against the differently-shaped record it received.
func EncodedLen(v any) (int, error) {
	var buf bytes.Buffer
	if err := jsonlEncoder(&buf).Encode(v); err != nil {
		return 0, err
	}
	return buf.Len(), nil
}

// ReadJSONL decodes a JSON-Lines file into a slice of T. Blank lines are
// skipped. A missing file is an error; an empty file yields an empty slice.
func ReadJSONL[T any](path string) ([]T, error) {
	// Open through the no-follow guard rather than os.Open: a session's JSONL
	// artefacts are attacker-controllable when the session was exchanged, and a
	// FIFO planted at one would block os.Open in open(2) for ever waiting for a
	// writer, hanging merge, report, or analyze on a session merely received.
	f, err := OpenFileNoFollowRead(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []T
	var total int64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), MaxJSONLLine)
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		// Counted before the blank-line skip so a file padded with blank lines
		// past the cap is refused rather than scanned past forever. bufio.ScanLines
		// strips a trailing \r along with the \n, so a CRLF file is undercounted by
		// one byte per line against what is actually on disk — benign here, since
		// the extra byte is never itself decoded or retained.
		total += int64(len(raw)) + 1
		if total > MaxJSONLBytes {
			return nil, fmt.Errorf("%s: exceeds %d bytes across %d lines; refusing to read", path, MaxJSONLBytes, line)
		}
		// Skip blank lines, including whitespace-only ones (as may appear in a
		// hand-edited or exchanged session), matching analyze.Load so the two
		// JSONL readers agree on what counts as blank.
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		out = append(out, v)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}

// WriteJSONL writes each value as one JSON line to path. It refuses to follow a
// symlink at path (see OpenFileNoFollow) so writing a session artefact — even
// from an untrusted, downloaded session directory — cannot be redirected to an
// arbitrary file outside the session.
//
// It also holds its two callers — transcribe (transcript.jsonl) and merge
// (timeline.jsonl) — to the read-side invariants ReadJSONL enforces: without
// the checks below, either could persist a record longer than MaxJSONLLine, or
// a set totalling more than MaxJSONLBytes, that ReadJSONL can never scan back,
// report success, and leave the operator with an artefact its own reader — and
// every later merge, report, and analyze run over that session — refuses
// whole. The whole set is measured before the file is opened, so a refusal
// neither truncates an existing artefact nor leaves a prefix of the new one
// behind, matching the all-or-nothing stance of analyze.Ingest and
// demo.appendRecords. That costs a second encoding pass over records that are
// small structs; a durably unreadable session is the worse trade.
//
// findings.jsonl never passes through here: analyze.commitFindings and
// review.AppendVerdict write it through their own locked descriptors instead,
// so neither can share this pre-flight. Both give their write path the matching
// MaxJSONLLine/MaxJSONLBytes checks ParseRecords (its read side) enforces —
// commitFindings via analyze.oversizedFindings, AppendVerdict via its own
// stat-then-append check.
func WriteJSONL[T any](path string, values []T) error {
	// Encode into one reusable buffer so the pre-flight pass holds a single
	// record, not the whole file, in memory.
	var buf bytes.Buffer
	check := jsonlEncoder(&buf)
	var total int64
	for i, v := range values {
		buf.Reset()
		if err := check.Encode(v); err != nil {
			return err
		}
		// Encode's output already ends in the newline, and that newline counts:
		// ReadJSONL's bufio.Scanner buffer caps at MaxJSONLLine bytes and must
		// hold the record *and* its terminator to find the line end, so a record
		// is readable when its bytes including the newline fit within the limit —
		// one byte less payload than the constant's face value. demo's
		// tooLongForJSONL draws the boundary on the same side for the record it
		// receives, but merge later re-frames that record into a timeline entry
		// (see timeline.EventEntry / SpeechEntry) that this same check applies to
		// again — the capture guards call those builders and EncodedLen so a
		// record accepted at capture time is guaranteed to still fit once wrapped.
		if buf.Len() > MaxJSONLLine {
			// 1-based, and named as a line of the file being written: the caller's
			// slice is already merged and time-sorted, so a 0-based slice index
			// pointed the operator at nothing they could count to — "record 0" is
			// no line of any file, and no line of the source transcript either.
			return fmt.Errorf("%s: line %d of the output encodes to %d bytes, over the %d-byte JSONL line limit", path, i+1, buf.Len(), MaxJSONLLine)
		}
		total += int64(buf.Len())
		if total > MaxJSONLBytes {
			// Same write-before-read stance as MaxJSONLLine's check above and
			// SaveManifest's own cap: refuse before opening the file rather than
			// persist a timeline.jsonl or transcript.jsonl that ReadJSONL's matching
			// total-size cap would then refuse to read back.
			return fmt.Errorf("%s: output would be %d bytes across %d lines, over the %d-byte JSONL file limit ReadJSONL enforces; refusing to write a session no command could read back", path, total, i+1, MaxJSONLBytes)
		}
	}

	f, err := OpenFileNoFollow(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}

	w := bufio.NewWriter(f)
	enc := jsonlEncoder(w)
	for _, v := range values {
		if err := enc.Encode(v); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	// Return the Close error: on a filesystem that defers write-back errors to
	// close (NFS close-to-open, or a full device), the final failure surfaces
	// here, not from Flush — mirroring WriteFileNoFollow, so a committed artefact
	// is never reported written when its bytes did not reach disk.
	return f.Close()
}
