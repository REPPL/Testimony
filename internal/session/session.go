// Package session defines the on-disk layout of a testimony session and
// helpers for reading and writing its artefacts.
//
// A session directory contains (see docs/architecture.md §11):
//
//	manifest.json       session metadata, including t0_epoch_ms
//	events.rrweb.jsonl  raw rrweb events (archival; web sessions only)
//	interactions.jsonl  normalised interaction events (epoch ms)
//	transcript.jsonl    word-aligned utterances (session-relative seconds)
//	timeline.jsonl      merged, session-relative timeline
//	report.md           human-readable session report
package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	RawEventsFile    = "events.rrweb.jsonl"
	InteractionsFile = "interactions.jsonl"
	TranscriptFile   = "transcript.jsonl"
	TimelineFile     = "timeline.jsonl"
	ReportFile       = "report.md"
)

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
	return os.WriteFile(filepath.Join(dir, ManifestFile), append(b, '\n'), 0o644)
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

// WriteJSONL writes each value as one JSON line to path.
func WriteJSONL[T any](path string, values []T) error {
	f, err := os.Create(path)
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
