# Changelog

All notable changes to Testimony are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and Testimony
uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html) with a
leading `v`.

Before v1.0.0, minor releases may make breaking changes; a change that can
break an existing invocation is called out in the entry that records it.

## [Unreleased]

### Fixed

Evidence integrity:

- **Behaviour:** `merge` refuses a transcript whose utterance ids repeat or
  collide with the event ids it synthesises, and `analyze` refuses a timeline
  carrying duplicate entry ids — inputs earlier versions accepted. Of two
  utterances sharing an id, only the later one reached the quote validator,
  so an honest verbatim quote of the first was rejected while a quote of the
  second validated for a finding anchored at the first one's time. `report`,
  whose join is positional, still renders such a timeline.
- **Behaviour:** `report` and `analyze` refuse a timeline entry whose `src`
  is neither `speech` nor `event` — previously accepted at exit 0 — instead
  of dropping it from the rendered timeline while counting its time into the
  header duration and keeping its id citable as evidence.
- `analyze` orders a hand-edited or exchanged `timeline.jsonl` by time before
  emitting it, matching `report`; it used to present entries in file order
  under a "read the timeline in order" instruction, so a reversed file coded
  the session in the wrong narrative order.
- The audio offset sidecar is written atomically (temp file and rename): a
  write failure part-way through used to leave a truncated sidecar behind,
  blocking every later bare `transcribe` with the prior offset
  unrecoverable from the session.

Capture and diagnostics:

- A recorder start-up failure is headlined as a likely permissions issue
  only when the ffmpeg output carries a device-failure signature; the module
  banner alone — printed on every successful open — used to route a full
  disk or a missing encoder to the System Settings pane.
- `demo -addr :0` prints the actually-bound address instead of the
  unopenable `http://localhost:0`; the same applies to `record -demo`.
- The demo page falls back to `fetch` when `sendBeacon` refuses to queue a
  capture batch, which used to drop the batch silently; its seeded display
  name now uses the Bob persona, so the tutorial's rename to Alice is a real
  change.
- Diagnostic stderr tails cut on rune boundaries rather than mid-character;
  a record refused by the JSONL writer is named as a 1-based line of the
  output file; `transcribe` defaults its log sink instead of panicking when
  a caller leaves it unset.
- `record` no longer prints the live-session banner ("Say \"session
  start\"…") for a run that is about to exit immediately (a platform with no
  capture and no `-demo`), and its no-audio guidance only suggests
  re-granting the microphone permission on a platform that has one to grant
  — a platform with no capture support at all is pointed at an external
  recording instead.

Checks and installer:

- The pipeline smoke test asserts the event half of the pipeline (the
  header counts and an event-only selector); every prior assertion was
  satisfiable from the transcript and findings alone, so a regression that
  dropped all events kept the gate green.
- The release workflow downloads a published tarball and requires the
  binary's own version output to name the tag; the checks parse `install.sh` with
  `sh -n` and `bash -n`, now in the release workflow's own verify job too — a
  tag pointing at a commit never pushed to a branch previously skipped that
  check entirely.
- `install.sh` verifies the release binary runs and reports the pinned
  release before installing it; an unrunnable binary (a wrong-platform
  asset) used to replace any previously installed one and print a success
  line at exit 0. Its trust-model comments now state precisely which
  attestation failures refuse the install (a verification attempted and
  rejected, or failed mid-way) and which fall back to the checksum with a
  note (a gh that cannot attempt it).

Documentation:

- Reference and how-to corrections against the code: the report header
  duration definition, `report`'s inputs, `record`'s ffmpeg prerequisite,
  the loopback `Origin` rule, the command that changes a recorded verdict,
  and the word-timestamp claim scoped to the utterance boundaries the join
  operates on. The README states that voice and screen capture need macOS,
  and its pipeline diagram joins up.
- `transcribe`'s reference entry states that `manifest.json` is required, as
  its sibling `merge`/`report`/`analyze` entries already do; `record`'s exit-1
  paragraph describes all of its actual diagnoses, not only the no-artefact
  case, and no longer implies the System Settings pane is always named. The
  `timeline.jsonl` example in the session-directory reference is in time
  order, matching the page's own "stably sorted by `t`" claim. The
  instrument-your-own-app capture snippet ignores untrusted (script-forwarded)
  clicks, matching the demo app it says it follows the conventions of.

Invocation contract:

- Every usage error now exits 2, as the CLI reference documents: a missing
  required `-session`, a disallowed flag combination (`analyze -out` with
  `-ingest`), and a non-finite `report -window` were reported at the runtime
  status 1, indistinguishable to a script from a session that could not be
  read.
- `report -window NaN`/`±Inf` is refused instead of silently rendering a
  report whose events are detached from (or all filed under) the speech they
  accompanied.
- `transcribe -audio` resolves the audio→session offset before the conversion
  mutates the session, so a refused run no longer destroys a record-origin
  `audio.wav`; an explicit `-offset` now rewrites an existing sidecar.
- `report` renders a hand-ordered (or hand-edited) `timeline.jsonl` in time
  order instead of trusting the file's line order.
- `install.sh` installs the current release: its version pin had been left at
  `v0.1.0`, handing new users a three-release-old binary, and the release
  workflow now gates the pin against the tag so it cannot go stale again. The
  installer also renders single-option prompts correctly and names the actual
  cause when a release download fails.
- A stray positional argument is refused as a usage error; it used to be
  silently swallowed together with every flag after it, so the command ran
  with defaults at exit 0.
- The remaining invalid-flag-value paths exit 2: `review`'s
  `-finding`/`-verdict` pairing and verdict syntax, an unknown
  `transcribe -engine`, and a malformed capture `-addr` (which also no longer
  creates a session directory before refusing).
- An explicit `transcribe -offset` is held to the same bound as a derived or
  persisted one: a non-finite value used to fail only after the conversion or
  engine work was already spent, with a bare JSON encoding error at the
  runtime status, and a finite but absurd one wrote a transcript at exit 0
  that `merge` refuses one command later — naming `transcript.jsonl` rather
  than the flag — while an external run persisted a sidecar `transcribe`'s
  own reader refuses.
- A `demo` whose well-formed address fails to bind (the port is already taken)
  no longer creates a session directory first, which left a stray session —
  manifest plus two empty stream files — behind at every refused bind.

Capture integrity:

- `POST /api/interactions` refuses with 400 any record `merge` would refuse —
  a non-object body, or a missing/implausible `t` or missing `kind` — instead
  of persisting with 204 a line that later fails the whole session's merge.
- A refused capture write and a deliberately wider (non-loopback) bind are
  reported on stderr; the page posts via `sendBeacon`, so the terminal is the
  only place a refusal can surface.
- The audio offset sidecar is persisted before the conversion's rename
  replaces `audio.wav`, so a refused sidecar write leaves the session
  byte-for-byte as it was found, and a failed rename rolls the sidecar back.
- A recorder that exits on its own mid-session still validates the artefacts
  the other recorders left and prints the next-command block.
- The demo capture write guard checks the request's actual remote address,
  not only its `Host` header: on a deliberately wider bind, a non-browser
  client could forge a loopback `Host` and have its writes accepted, even
  though the server states that capture posts are accepted from loopback
  clients only.

Installer:

- An unauthenticated (or attestation-incapable) `gh` no longer refuses the
  install as a false provenance failure: it falls back to the verified
  checksum, exactly like no `gh`; a verification `gh` actually performed and
  rejected still refuses, and gh's own message is shown instead of being
  swallowed.
- An optional-dependency failure (an unreachable ffmpeg or uv host, a failed
  unpack or brew install) skips its step with guidance instead of aborting
  the whole installer with the child's raw exit code and a leaked temp
  directory; Ctrl+C stops the installer instead of being read as "skip";
  `--help` works through the documented pipe invocation; `--dir`/`--version`
  without a value are refused cleanly; the whisper.cpp model recipe
  downloads into a directory `-model NAME` actually searches.

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

[Unreleased]: https://github.com/REPPL/Testimony/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/REPPL/Testimony/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/REPPL/Testimony/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/REPPL/Testimony/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/REPPL/Testimony/releases/tag/v0.1.0
