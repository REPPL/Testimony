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
	"encoding/json"
	"errors"
	"fmt"
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
	dir = filepath.Join(outRoot, now.Format(dirLayout))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	m.Session = filepath.Base(dir)
	m.T0EpochMS = now.UnixMilli()
	if err := SaveManifest(dir, m); err != nil {
		return "", err
	}
	return dir, nil
}

// LoadManifest reads manifest.json from dir.
func LoadManifest(dir string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(filepath.Join(dir, ManifestFile))
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

// OpenFileNoFollow opens path for writing with O_NOFOLLOW, so a symlink planted
// at the final path component is refused rather than followed. A session
// directory is an exchange unit (a shared or downloaded session may be
// attacker-authored); without this guard a planted symlink — e.g. a
// timeline.jsonl pointing at ~/.ssh/authorized_keys — would redirect a write to
// an arbitrary file outside the session directory. flag is OR-ed with
// O_NOFOLLOW; callers pass the usual O_CREATE/O_TRUNC/O_APPEND/O_WRONLY set.
func OpenFileNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	f, err := os.OpenFile(path, flag|syscall.O_NOFOLLOW, perm)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("refusing to write %s: it is a symlink", path)
		}
		return nil, err
	}
	return f, nil
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
// terminal sequences — turns tabs into spaces, and removes the Unicode
// bidirectional/line-separator formatting controls behind Trojan-Source
// spoofing (CVE-2021-42574), so a right-to-left override cannot make a
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
		case r == 0x200e || r == 0x200f, // LRM, RLM
			r >= 0x202a && r <= 0x202e, // LRE, RLE, PDF, LRO, RLO
			r >= 0x2066 && r <= 0x2069, // LRI, RLI, FSI, PDI
			r == 0x2028 || r == 0x2029: // line / paragraph separator
			return -1
		default:
			return r
		}
	}, s)
}

// ReadJSONL decodes a JSON-Lines file into a slice of T. Blank lines are
// skipped. A missing file is an error; an empty file yields an empty slice.
func ReadJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		if len(raw) == 0 {
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
func WriteJSONL[T any](path string, values []T) error {
	f, err := OpenFileNoFollow(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, v := range values {
		if err := enc.Encode(v); err != nil {
			return err
		}
	}
	return w.Flush()
}
