# Changelog

All notable changes to Testimony are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and Testimony
uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html) with a
leading `v`.

Before v1.0.0, minor releases may make breaking changes; each one is
called out in a **Breaking** section.

## [0.4.0] - 2026-07-24

A second robustness pass over the same capture → analysis pipeline, closing the
defects a multi-round review surfaced after v0.3.0. Every fix carries a
regression test; the review finished with two consecutive passes finding only
nitpicks.

### Fixed

Evidence-record and validation integrity:

- A failed write of `findings.jsonl` (a full disk part-way through) no longer
  leaves a truncated fragment that bricks the file against its own recovery —
  the re-ingest path could not even parse it to overwrite it. The write now rolls
  back to an empty, re-ingestable file.
- `report.md`, the shareable evidence artefact, no longer passes attacker-authored
  text through as live Markdown: an image or link in a transcript quote (e.g. a
  remote-image tracking beacon) is neutralised so it renders as literal text.
- A finding whose verbatim quote consists only of stripped control/bidi
  characters is rejected, rather than passing the quote check against an empty
  string.
- A hand-edited `findings.jsonl` carrying two findings with the same `id` is
  refused on load, naming the line, instead of silently collapsing them under one
  status in the report.
- An interaction whose epoch-millisecond time is astronomically large is refused
  at merge (matching the existing utterance-side bound), rather than producing a
  session span the report can only render as `--:--`.

Robustness against malformed or exchanged sessions:

- `analyze -ingest FILE` and `analyze -out FILE` now go through the same
  symlink/FIFO-refusing guard every other session-artefact read and write uses, so
  a pointer into an untrusted session directory cannot redirect the write out of
  the session or hang the read on a planted FIFO.
- The session's own `audio.wav` is refused when it is a symlink, so a shared
  session cannot redirect transcription at an out-of-session file whose words would
  then land in the (re-shareable) `transcript.jsonl`.
- A missing-vs-unreadable `audio.wav` is distinguished, so a permissions or
  symlink-loop error is no longer misreported as "no audio, run record first".
- A finding id embedded in a `review` error message is sanitised before it reaches
  the terminal, closing an ANSI-escape path through a hand-authored id.
- A derived or persisted audio offset beyond a plausible magnitude is refused at
  `transcribe`, where the bad recording metadata enters, rather than one command
  later at `merge`.

Capture reliability (`record`/`transcribe`, macOS):

- Device enumeration now runs under a timeout, so a wedged capture device or
  driver no longer hangs `record` before it can be interrupted.
- A recorder's captured stderr is bounded, so a device-stall log flood over a long
  session cannot exhaust memory and orphan the recorders.
- Voice-recording conversion writes to a temp file and renames on success, so an
  interrupted or crashed `ffmpeg` never leaves a partial `audio.wav` a later run
  would transcribe as if complete.
- A recorder that had to be force-stopped (it missed the finalisation grace) is
  flagged, so a truncated, unplayable `screen.mp4` is no longer reported as good.

Further adversarial review passes over that same commit closed what it had
itself missed:

- Text sanitisation now strips every invisible Unicode format character (zero
  width space, word joiner, BOM, soft hyphen, the tag block), not only the bidi
  controls — closing the remaining gap between what a terminal or `report.md`
  displays and the bytes actually recorded (invisible-text smuggling).
- A finding quote that sanitises to whitespace alone is rejected like one that
  sanitises to nothing, closing the remaining trivially-satisfied verbatim
  check.
- A recorder that finalised cleanly right at the shutdown grace boundary is no
  longer misclassified as force-stopped — the flag now reflects whether the
  escalation SIGKILL actually terminated it — so a complete, playable recording
  is not reported as truncated with a spurious failure exit.
- Device enumeration no longer relies on the enumeration child being killable:
  a child that survives SIGKILL (a wedged kernel driver) is abandoned with an
  actionable error instead of hanging `record` forever, and a listing that
  completed just as the deadline fired is used rather than discarded. Both
  enumeration paths are now covered by hermetic tests against a fake `ffmpeg`,
  as are the force-stop classification, the derived-offset bound, the
  missing-vs-unreadable audio split, the report code-span escape, the review
  error-path sanitisation, and the atomic-conversion call-site wiring — fixes
  whose tests previously stayed green when the fix was reverted.
- A converted `audio.wav` is written with the operator's umask-masked mode, like
  every other session artefact and the record-side `audio.wav`, rather than the
  temp file's private `0600` or a flat `0644` wider than the umask allows.
- The recorder shutdown no longer hangs on a wedged capture device: the wait
  after the escalation `SIGKILL` is now bounded, so a child pinned in an
  uninterruptible kernel wait is abandoned (and its artefact distrusted) instead
  of stalling the whole sequential shutdown before `record` can finalise its
  outputs and print the follow-up commands.
- Device enumeration survives an unresponsive child rather than hanging on it:
  the wait is structured so an expired deadline always takes effect even when the
  child cannot be reaped, closing a residual hang the first timeout could not.
- The bounded enumeration-output sink honours the `io.Writer` contract on
  overflow, so a flood past its cap can no longer abort the capture of the
  listing partway through.

### Changed

- **Behaviour:** `analyze -ingest` and `analyze -out` now require a regular file
  and reject named pipes, shell process substitution (`-ingest <(…)`), and
  `/dev/stdout`. Use `-ingest -` to read the answer from stdin, and omit `-out` to
  write the request to stdout — the supported equivalents.

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

[0.4.0]: https://github.com/REPPL/Testimony/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/REPPL/Testimony/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/REPPL/Testimony/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/REPPL/Testimony/releases/tag/v0.1.0
