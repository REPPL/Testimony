# Changelog

All notable changes to Testimony are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and Testimony
uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html) with a
leading `v`.

Before v1.0.0, minor releases may make breaking changes; each one is
called out in a **Breaking** section.

## [Unreleased]

## [0.2.0] - 2026-07-18

### Added

- **`testimony record`** — one-command capture. It starts the recorders and
  writes the session `manifest.json` with the shared `t0` wall-clock anchor, so
  a session is ready for `transcribe` → `merge` → `report` with no hand-noted
  clocks. `-video` opts into screen capture; `-demo` composes the instrumented
  demo app into the same run; non-macOS hosts degrade honestly, recording what
  they can and reporting what they skipped.
- **`testimony analyze`** — host-delegated first-pass analysis. `analyze` emits
  a versioned rubric plus the session timeline as a self-contained prompt any
  assistant can answer; `-ingest` validates that answer against the timeline
  (evidence must exist, quotes must be verbatim, status is forced to
  `unverified`) into `findings.jsonl`. The CLI holds no API keys and makes no
  network calls.
- **`testimony review`** — records `confirmed` / `rejected` / `duplicate`
  verdicts append-only, never rewriting the original finding; interactive walk
  or non-interactive flags. The report renders findings grouped by status.
- **`transcribe -audio` is now optional** — a session whose `audio.wav` was
  captured in place (by `record`) is reused directly, with no re-conversion.

### Security

- A full hardening pass fixed twelve defects across the codebase, each with a
  regression test: the demo capture server now binds loopback and guards its
  write endpoints against CSRF and DNS-rebinding; JSON bodies are canonicalised
  to one line each; session writers refuse to follow symlinks; untrusted
  transcript, event, and finding text is stripped of terminal-control and
  Unicode bidirectional (Trojan-Source) sequences before it reaches a report or
  a terminal; the analysis ingest read is bounded and a finding's evidence list
  is capped; and the installer pins the ffmpeg publisher's signing key.

### Changed

- **Releases are automated.** Pushing a `vX.Y.Z` tag runs a workflow that
  verifies the pushed commit, cross-compiles the four platform binaries,
  generates `SHA256SUMS`, attaches SLSA build-provenance attestations, and
  publishes the release. The installer verifies the download against the
  release's published checksums and — when the GitHub CLI is present — against
  the build-provenance attestation, so it no longer carries per-release hashes.
- CI adds full-history secret scanning and a workflow-security audit as gates.

## [0.1.0] - 2026-07-17

### Added

- **`testimony demo`** — serves a small instrumented settings app and streams a
  participant's clicks and inputs (normalised interactions with `data-testid`
  selectors, plus the raw rrweb archive) into a fresh session directory.
- **`testimony transcribe`** — turns a voice recording (`.m4a`, `.mov`, `.wav`)
  into a word-aligned `transcript.jsonl` with local speech recognition. It
  extracts 16 kHz mono `audio.wav` via ffmpeg, runs WhisperX (word-level
  timestamps, preferred) or whisper.cpp, and anchors the result to the session
  clock. Audio never leaves the machine.
- **`testimony merge`** — merges `transcript.jsonl` and `interactions.jsonl`
  into one session-relative `timeline.jsonl`.
- **`testimony report`** — renders the timeline as a Markdown record that
  interleaves what was said with what was done, joining each utterance to the
  interface events around it.
- A one-line installer and a checksummed release of static binaries for macOS
  and Linux.

[Unreleased]: https://github.com/REPPL/Testimony/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/REPPL/Testimony/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/REPPL/Testimony/releases/tag/v0.1.0
