# Changelog

All notable changes to Testimony are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and Testimony
uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html) with a
leading `v`.

Before v1.0.0, minor releases may make breaking changes; each one is
called out in a **Breaking** section.

## [Unreleased]

## [0.3.0] - 2026-07-22

A robustness release. No new commands: the pipeline gains no surface, but the
existing one no longer accepts malformed or hostile session input in silence.
Every fix below carries a regression test, and the changes were confirmed by a
multi-round review that finished with two consecutive passes finding nothing.

### Fixed

Evidence-record integrity — the report is the artefact of record, so a wrong
number in it is the worst kind of bug:

- A transcript with missing or duplicate utterance `id`s no longer renders every
  event under every utterance. Events attach by position, not by an unvalidated
  id.
- A finding, utterance, interaction, or ASR segment that omits its required time
  is now rejected rather than silently anchored at the session start (`[00:00]`).
  Absent and a genuine zero are distinguished throughout.
- A manifest whose `t0_epoch_ms` is absent or negative is refused wherever a time
  is placed on the session clock — `merge` and `transcribe` alike — instead of
  shifting every event by roughly the whole Unix epoch and reporting success.
- Pre-`t0` times (a recording that predates the manifest anchor) render with a
  signed clock rather than being clamped to `[00:00]`.

Robustness against malformed or exchanged sessions — a session directory is an
exchange unit and may be attacker-authored:

- A symlink or FIFO planted at any session artefact is refused on both the read
  and the write path, rather than redirecting a write or hanging the CLI in
  `open(2)` for ever.
- No writer emits a JSONL line larger than the readers can take back; an
  over-long record is refused at the point of capture and of write, not
  discovered as a permanently unreadable session later.
- `analyze` no longer truncates a findings file on an empty answer, and no longer
  loses a human verdict whose value falls outside the known set.
- Untrusted manifest, transcript, and finding text can no longer inject terminal
  escape sequences or forge document structure; the sanitiser now covers the
  complete Unicode Bidi_Control set.

Resource and process lifecycle:

- `record` and the capture server shut down under a deadline, so a stalled
  connection can no longer hang session finalisation after Ctrl+C.
- A partial write (a full disk) leaves no truncated, unreadable line behind in
  any append path.
- `record` classifies a start-up permission denial correctly instead of
  misreporting it as an unexpected mid-session stop.
- An empty capture address no longer binds the unauthenticated endpoints on
  every network interface.

### Changed

- Validation is stricter at the pipeline's boundaries. Inputs that violate the
  documented session schema — a missing required time, an absent or negative
  anchor, an over-long record — are now refused with a clear error where earlier
  versions accepted them and produced a silently wrong artefact. Well-formed
  sessions, including the bundled sample, are unaffected.

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
