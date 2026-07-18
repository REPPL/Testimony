# DECISIONS

Append-only, one line per decision, newest last. Date-prefixed.
Architecture-shaping decisions graduate to an ADR under
[`../development/decisions/adrs/`](../development/decisions/adrs/).

- 2026-07-17 — Adopt the three-tier working-state layout (`.abcd/development/`
  durable, `.abcd/work/` shared, `.abcd/.work.local/` local-only) and the
  working conventions recorded in `AGENTS.md`.
- 2026-07-17 — Pin the commit identity (`.abcd/config/identity.json`) to the
  repository's GitHub noreply identity; repo-local git config matches it.
- 2026-07-17 — `transcribe` engine order: WhisperX preferred (word-level
  timestamps), whisper.cpp fallback; both invoked as subprocesses whose JSON
  output is the contract, never their human-readable stdout.
- 2026-07-17 — Audio→session offset defaults to ffprobe `creation_time` minus
  manifest `t0_epoch_ms` (best-effort, never fatal); the `-offset` flag
  overrides.
- 2026-07-17 — Architecture §11 aligned to code: `manifest.json` and
  `events.rrweb.jsonl` + `interactions.jsonl` (divergence flagged in PR #2).
- 2026-07-17 — Repository made public; `.abcd/config.json` visibility updated.
  Releases ship static binaries with a `SHA256SUMS` asset; `install.sh` pins the
  release checksums and defaults to a user-local install (`~/.local/bin`, no
  admin rights), with dependency installs offered via Homebrew or verified
  admin-free downloads (evermeet.cx GPG-verified ffmpeg; whisperx via uv).
- 2026-07-17 — WhisperX VAD defaults to silero (`-vad` overrides): pyannote's
  checkpoint trips newer torch's `weights_only` load and aborts every run;
  found in the first live end-to-end session on the target Mac.
- 2026-07-17 — `record` uses ffmpeg avfoundation for both screen and microphone
  capture, not `screencapture -v`: ffmpeg is already a hard dependency (mic +
  transcribe), its SIGINT→finalise-container behaviour is battle-tested and
  identical for audio and video (the clean-stop the acceptance criteria need),
  and one argv shape gives one pure, uniformly testable builder;
  `screencapture -v` stays a documented future quality-upgrade path. Microphone
  writes canonical 16 kHz mono `audio.wav`, so `transcribe -audio` becomes
  optional and reuses it in place; default capture is audio-only with `-video`
  opt-in (screen video is retained evidence, not yet consumed downstream).
- 2026-07-17 — Session creation and the demo server are extracted into shared,
  reusable pieces (`session.Create` derives the dir name + `t0_epoch_ms` from one
  `now`; `demo.Serve` binds and serves non-blocking) so `record` and `demo` write
  a session by one code path rather than duplicating it. `demo` now blocks on
  SIGINT/SIGTERM and shuts the server down gracefully (exit 0) instead of being
  hard-killed; its printed output and on-disk artefacts are unchanged.
- 2026-07-17 — `analyze` is host-delegated and emit-or-ingest: `analyze -session
  DIR [-out FILE]` emits a single self-contained request (versioned rubric
  `testimony-analysis/v1`, two-pass coding, the whole timeline plus the manifest
  task list) and `-ingest FILE` is the sole validation boundary. The CLI never
  calls a model or the network. Ingest decodes with `DisallowUnknownFields`
  (closed shape), validates transactionally (all errors at once, nothing written
  on failure), and forces `status:"unverified"` on every finding regardless of
  input; it refuses to overwrite a `findings.jsonl` that already holds verdicts.
- 2026-07-17 — Findings validation rules: id `^F-\d{3}$` unique; `type` in the
  five-value set; `severity` Nielsen-style integer `1..4`; `quote` an exact
  substring of one *evidence* utterance's text (per-utterance, not corpus-joined,
  no normalisation), so every finding cites at least one `utt-*`; `evidence` ids
  must exist in `timeline.jsonl`; `ui` selector/route validated against the
  timeline's events; `t` within `[0, sessionEnd]`.
- 2026-07-17 — Verdicts are stored as appended, non-destructive verdict records
  (`{"kind":"verdict","finding":…,"verdict":confirmed|rejected|duplicate,"of":…,
  "at":date}`), never by rewriting the finding line, so the birth state and full
  decision history survive as the precision measure; latest verdict wins for
  display, and `report`'s Findings section groups by effective status
  (confirmed, unverified, duplicate, rejected). Flagged divergences from the
  note: task-boundary chunking is deferred behind a seam (timeline carries no
  task markers), and keyframe extraction (AC3) is deferred to a later intent.
