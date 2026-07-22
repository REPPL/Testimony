// Package session defines the on-disk layout of a testimony session and
// helpers for reading and writing its artefacts.
//
// A session directory contains (see docs/reference/session-directory.md):
//
//	manifest.json       session metadata, including t0_epoch_ms
//	audio.wav           16 kHz mono ASR input (local only)
//	screen.mp4          screen recording (local only; -video capture)
//	events.rrweb.jsonl  raw rrweb events (archival; web sessions only)
//	interactions.jsonl  normalised interaction events (epoch ms)
//	transcript.jsonl    word-aligned utterances (session-relative seconds)
//	timeline.jsonl      merged, session-relative timeline
//	report.md           human-readable session report
package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
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
	b, err := io.ReadAll(f)
	if err != nil {
		return m, fmt.Errorf("load manifest: %w", err)
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
	return WriteFileNoFollow(filepath.Join(dir, ManifestFile), append(b, '\n'), 0o644)
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

// SafeText neutralises untrusted text before it is written into a human-facing
// artefact (report.md) or a terminal line (review). It strips C0/C1 control
// bytes — including the newline and carriage return that could forge report
// structure or split a JSONL record, and the ESC (0x1b) that drives ANSI
// terminal sequences — turns tabs into spaces, and removes the complete Unicode
// Bidi_Control set (U+061C, U+200E, U+200F, U+202A-U+202E, U+2066-U+2069) along
// with the line and paragraph separators, the formatting controls behind
// Trojan-Source spoofing (CVE-2021-42574), so a right-to-left override or an
// Arabic letter mark cannot make a
// displayed quote or anchor differ from the bytes a verdict is recorded
// against. Attacker-authored transcript, interaction, manifest, and finding
// text therefore cannot inject headings, terminal control sequences, extra
// lines, or reordered glyphs. Ordinary text is unchanged.
func SafeText(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
			return -1
		// The complete Unicode Bidi_Control set, plus the line and paragraph
		// separators: leaving any member out would let that one character do the
		// reordering the rest are stripped to prevent.
		case r == 0x061c, // ALM
			r == 0x200e || r == 0x200f, // LRM, RLM
			r >= 0x202a && r <= 0x202e, // LRE, RLE, PDF, LRO, RLO
			r >= 0x2066 && r <= 0x2069, // LRI, RLI, FSI, PDI
			r == 0x2028 || r == 0x2029: // line / paragraph separator
			return -1
		default:
			return r
		}
	}, s)
}

// MaxJSONLLine is the largest single JSONL record the readers accept. It is the
// shared read-side invariant every writer must respect: a record persisted above
// this size is durably unreadable, breaking merge, report, and analyze for the
// whole session, so the capture endpoints reject anything larger rather than
// accept a line no reader can take back.
const MaxJSONLLine = 4 << 20 // 4 MiB

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
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), MaxJSONLLine)
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Bytes()
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
// It also holds the writers to MaxJSONLLine, the read-side invariant: without
// the check merge could persist a timeline.jsonl (or analyze a findings.jsonl)
// carrying a record longer than ReadJSONL can scan back, report success, and
// leave the operator with an artefact its own reader — and every later merge,
// report, and analyze run over that session — refuses whole. The whole set is
// measured before the file is opened, so a refusal neither truncates an
// existing artefact nor leaves a prefix of the new one behind, matching the
// all-or-nothing stance of analyze.Ingest and demo.appendRecords. That costs a
// second encoding pass over records that are small structs; a durably
// unreadable session is the worse trade.
func WriteJSONL[T any](path string, values []T) error {
	// Encode into one reusable buffer so the pre-flight pass holds a single
	// record, not the whole file, in memory.
	var buf bytes.Buffer
	check := json.NewEncoder(&buf)
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
		// tooLongForJSONL draws the boundary on the same side, so the capture and
		// artefact writers accept exactly the same set of records.
		if buf.Len() > MaxJSONLLine {
			return fmt.Errorf("%s: record %d encodes to %d bytes, over the %d-byte JSONL line limit", path, i, buf.Len(), MaxJSONLLine)
		}
	}

	f, err := OpenFileNoFollow(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}

	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
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
