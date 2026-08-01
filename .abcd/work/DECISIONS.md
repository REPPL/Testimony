# DECISIONS

Append-only, one line per decision, newest last. Date-prefixed.
Architecture-shaping decisions graduate to an ADR under
`.abcd/development/decisions/adrs/` (created with the first ADR).

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
- 2026-07-18 — Sanitise the finding `id` and verdict fields (`value`/`of`/`at`)
  through `SafeText` when rendered to `report.md` and the review terminal: a
  shared session's `findings.jsonl` is not revalidated by `analyze.Load`, so
  those channels could still inject forged report structure / ANSI. Residual of
  the earlier control-byte hardening, caught by a confirmation hunt.
- 2026-07-18 — Third hardening pass (confirmation hunt): `review.describe`'s
  verdict echo now `SafeText`s the id/verdict fields (the sibling of the fix
  above, on the record path); `SafeText` also strips the Unicode BiDi/isolate
  and line-separator controls (Trojan-Source, CVE-2021-42574); and `validate`
  caps a finding's evidence at 64 ids, so a hostile answer cannot write a single
  findings.jsonl line larger than the JSONL reader's buffer and brick the file.
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
- 2026-07-18 — Security hardening (harden branch). Demo capture server: binds
  loopback by default (a bare `:port` normalises to `127.0.0.1:port`, opt into a
  wider bind with an explicit host); the write endpoints now require a loopback
  `Host`, a same-origin/absent `Origin`, and `Content-Type: application/json`,
  closing the CSRF and DNS-rebinding forgery paths (the demo page and the
  instrument-your-own-app snippet set the JSON content type on their fetch
  fallback); each accepted body is re-encoded with `json.Compact` so an embedded
  newline can no longer split one logical record into corrupt JSONL lines that
  break `merge`.
- 2026-07-18 — Session artefact writes refuse to follow symlinks. New
  `session.OpenFileNoFollow`/`WriteFileNoFollow` (O_NOFOLLOW) back `WriteJSONL`,
  `SaveManifest`, the `report.md` write, the demo stream files, and review's
  `AppendVerdict`; `transcribe` lstat-guards `audio.wav` before invoking ffmpeg.
  A downloaded/shared session can no longer redirect a write to an arbitrary
  file outside the session directory via a pre-planted symlink.
- 2026-07-18 — Untrusted display text is sanitised. `session.SafeText` strips
  C0/C1 control bytes (newline, CR, ESC/ANSI, DEL) from attacker-influenceable
  fields before they reach `report.md` (utterance/event/finding/manifest text)
  or the analyst's terminal (`review`), so forged report headings and ANSI
  terminal injection are neutralised. `analyze -ingest` bounds the untrusted
  answer read at 16 MiB (`io.LimitReader`) to prevent a memory-exhaustion DoS.
- 2026-07-18 — install.sh: the macOS ffmpeg path pins the evermeet publisher key
  fingerprint (`20F6EA3E0CFD6B4C53447A73476C4B611A660874`), importing only that
  key into a throwaway keyring and asserting the good signature's VALIDSIG
  carries it — `--auto-key-retrieve` (which trusts any key the signature names)
  is dropped, so an attacker-signed substitute build is refused. The uv
  installer is downloaded+executed inside a private `mktemp -d` instead of a
  fixed, world-writable `/tmp/uv-install.sh`, closing the shared-host TOCTOU/
  symlink race.
- 2026-07-18 — CI adopts two abcd-managed supply-chain gates alongside the
  existing format/build/vet/`go test -race`/pipeline-smoke `check` job (now run
  on Linux AND macOS): a `gitleaks` job full-history-scans for committed secrets
  (pinned, checksum-verified CLI — self-contained, no marketplace-action
  caveat), and a `zizmor` job audits the workflows (public repo, so via
  zizmor-action with SARIF upload to Code Scanning). All third-party actions are
  pinned by commit SHA with `persist-credentials: false` and minimal per-job
  permissions.
- 2026-07-18 — Release is tag-triggered (`.github/workflows/release.yml`,
  `on: push: tags: ['v*']`). A `verify` job re-runs the full gate against the
  pushed commit (`github.sha`, never the re-pointable tag name), then a `release`
  job cross-compiles the four `testimony_<TAG>_<os>_<arch>.tar.gz` tarballs
  (CGO-off, `-trimpath`, version-stamped via `-X …internal/cli.Version`) + LICENSE
  from that same commit, generates a `SHA256SUMS` manifest, attaches SLSA
  build-provenance attestation (`actions/attest-build-provenance`, guarded to
  no-op if the repo is ever private), and publishes with `gh release create
  --verify-tag --generate-notes`. A no-branch-commit tripwire asserts the job
  pushes nothing to the default branch.
- 2026-07-18 — install.sh drops the per-release pinned SHA-256 constants and the
  pinned-vs-version branching. It now fetches the release's `SHA256SUMS` and
  verifies the tarball against it (integrity), and when `gh` is present runs
  `gh attestation verify --signer-workflow REPPL/Testimony/.github/workflows/release.yml`
  (authenticity — the strong anchor); without `gh` it installs on the checksum
  alone and prints that installing `gh` enables provenance verification. The
  dependency section (ffmpeg pinned-GPG path, whisperx/whisper.cpp, private-mktemp
  uv install) is unchanged.
- 2026-07-18 — bughunt-1 correctness fixes. `timeline.Merge` now treats a
  missing `transcript.jsonl`/`interactions.jsonl` as zero records (via
  `readOptionalJSONL`, tolerating `fs.ErrNotExist`), so the documented default
  audio-only `record` → `merge` pipeline no longer aborts with "no such file";
  brief 04-surfaces/03-merge.md and docs/reference/cli.md updated.
- 2026-07-18 — Demo capture handler (`appendLines`) now checks the append
  write error and answers `500` instead of a false `204`, and writes each
  record + newline as one buffer so a partial write cannot leave a truncated,
  unparseable JSONL line; brief 04-surfaces/01-demo.md updated.
- 2026-07-18 — `analyze.Load` validates the verdict enum (the previously-unused
  `verdictSet`): a verdict value outside `confirmed|rejected|duplicate` is
  ignored, so its finding stays `unverified` and no longer vanishes from the
  report and review queue into an unrendered status group; schema doc noted.
- 2026-07-18 — `session.WriteJSONL` and `review.AppendVerdict` now return the
  file `Close()` error (matching `WriteFileNoFollow`), so a write-back failure
  deferred to close is not masked as success on committed artefacts.
- 2026-07-18 — Demo/record banners derive the display URL via `demo.DisplayURL`
  instead of concatenating `-addr` after a literal "localhost", fixing the
  broken `http://localhost0.0.0.0:8737` shown for an explicit-host bind.
- 2026-07-18 — `session.Create` uses `os.Mkdir` (after `MkdirAll(outRoot)`)
  instead of `os.MkdirAll(dir)`, so two captures starting within the same
  wall-clock second fail with EEXIST rather than silently sharing one directory
  (which clobbered the first manifest's t0 and conflated append-only streams).
- 2026-07-18 — `session.ReadJSONL` skips whitespace-only lines
  (`bytes.TrimSpace`), matching `analyze.Load`, so an exchanged/hand-edited
  session's blank line is skipped as documented rather than crashing merge/report
  with "unexpected end of JSON input".
- 2026-07-18 — `analyze.validate` derives the finding-`t` lower bound from the
  timeline (earliest entry time, floored at 0) instead of hard-coding 0, so a
  finding faithfully anchored to a legitimately negative-time utterance — an
  external recording whose `creation_time` predates `t0`, giving a negative
  `deriveOffset` — no longer fails the whole transactional ingest.
- 2026-07-18 — `analyze.Ingest` refuses an answer with no findings (bare `[]`,
  `{"findings":[]}`, or a truncated file) rather than writing an empty slice with
  O_TRUNC, which previously erased a prior good `findings.jsonl` and reported
  success.
- 2026-07-18 — `analyze.holdsVerdicts` scans `findings.jsonl` for any raw
  `kind:"verdict"` line instead of consulting the enum-filtered `analyze.Load`
  slice, so the overwrite guard still fires for a hand-edited/shared file whose
  only verdict carries an out-of-enum value, protecting the retained precision
  record from a truncating re-ingest.
- 2026-07-18 — `demo.appendRecords` truncates a stream file back to its
  pre-write length when a write fails, so a short write (ENOSPC persists a
  newline-less prefix) can no longer leave a partial JSONL line that corrupts one
  physical record and breaks merge's reader; corrected the false comment claiming
  `os.File.Write` gives newline atomicity.
- 2026-07-18 — `analyze.indexTimeline` seeds `idx.end` on the first entry (`i == 0
  || end > idx.end`), matching how `idx.start` is seeded, so a fully-negative
  timeline (an external recording predating manifest t0) reports its true latest
  (still-negative) entry end as `sessionEnd` instead of flooring it at the zero
  value 0. Fixes an over-permissive finding-time upper bound that admitted a
  finding anchored after the real session end.
- 2026-07-18 — `timeline.Merge` rejects a session that has interactions but a
  manifest lacking `t0_epoch_ms` (T0EpochMS == 0), since epoch-millisecond
  interaction times cannot be placed on the session clock without it; previously
  it used the zero-value anchor and wrote a silently corrupt timeline (~55-year
  offsets, nonsense report duration) with exit 0. Transcript-only sessions are
  unaffected.
- 2026-07-22 — Close the Ingest/AppendVerdict TOCTOU on `findings.jsonl`:
  `Ingest` now runs its verdict-guard probe, `O_TRUNC`, and rewrite as one step
  under a `LOCK_EX` advisory lock (`commitFindings`), matching the lock
  `review.AppendVerdict` already holds. Previously the probe and the truncating
  write were two lock-free opens, so a concurrent `testimony review` could commit
  a verdict between them and have the re-ingest destroy it — the human-decision
  record the guard exists to protect. Sibling sweep: `findings.jsonl` is the only
  session file with both an appender and a truncating rewriter; the other
  `WriteJSONL` sites (timeline, transcript) are truncate-only, no sibling.
- 2026-07-22 — Bind each review verdict to the finding the analyst judged.
  `review.AppendVerdict` now takes the shown finding and, under its existing
  `LOCK_EX` on findings.jsonl, re-reads the current findings and refuses if the
  targeted id is gone or now names a different finding (`verifyTarget`,
  `analyze.SameIdentity`). Previously the walk validated the target only against
  the in-memory snapshot from `analyze.Load`, so a concurrent `analyze -ingest`
  (permitted until the first verdict exists) that restarts finding ids at F-001
  could slide a different finding under the same id and misattribute the
  operator's verdict — silent corruption of the precision record. Refactored
  `analyze.Load` to expose `ParseRecords(io.Reader)` so the re-check reads through
  the already-locked descriptor. Sibling sweep: `AppendVerdict` is the sole
  verdict writer; both production callers now bind the judged finding.
- 2026-07-22 — Guard the untrusted-time-magnitude class end to end. Utterance
  t0/t1 (session-relative float64 seconds) now have a magnitude bound in
  timeline.checkedUtterances (1e9 s), the speech-side twin of the interaction
  t<=0 guard; report.clock and review.clock refuse non-finite / out-of-range
  seconds before the float64→int conversion (rendering `--:--`), defending the
  display sink for a hand-authored timeline.jsonl/findings.jsonl that bypasses
  merge; and report's trailing standalone-event flush uses a +Inf sentinel
  instead of a finite 1e18 that silently dropped events at/after it. Sibling
  sweep: report.clock and review.clock were the two duplicated float→int time
  sinks; both guarded.
- 2026-07-22 — Validate findings against the SafeText form of the timeline.
  EmitRequest shows the agent each timeline line through session.SafeText, but
  analyze.validate indexed and compared the raw bytes, so an id/selector/route/
  quote carrying a stripped Bidi_Control or control character could never be
  matched by an honest verbatim-copied answer (fail-closed, hostile/hand-edited
  transcripts and genuine RTL speech). indexTimeline now stores SafeText keys and
  validate compares SafeText(quote/selector/route/id). SafeText is a no-op on
  ordinary text. Resolves the selector/route/id sibling of the earlier
  emit-quote asymmetry.
- 2026-07-22 — Surface ffmpeg's own diagnostic when the avfoundation device
  listing is empty (record.probeDevices): an ffmpeg built without avfoundation
  exits non-zero as an *exec.ExitError with the cause in its output, previously
  discarded and misreported as "no microphone found". Now a bounded output tail
  is appended to the error.
- 2026-07-22 — transcribe persists the audio→session offset for external
  recordings in a sidecar (`audio.offset.json`) beside audio.wav. A bare re-run
  (different model, reusing audio.wav) reads it back instead of silently assuming
  offset 0 and shifting every utterance; a record-origin audio.wav has no sidecar
  and stays offset 0; a present-but-unusable sidecar refuses with guidance rather
  than guessing. New session-directory artefact, documented in
  docs/reference/session-directory.md.
- 2026-07-22 — record captures the microphone via avfoundation ":default" (the
  system default input) instead of enumerated index 0, so a virtual audio driver
  (BlackHole/Loopback/conferencing device) that enumerates first is no longer
  silently recorded in the real mic's place; startRecorders logs the detected
  audio-input roster so a surprising default stays visible. NOTE: the avfoundation
  capture path is not exercised by CI — verify on macOS hardware before release.
- 2026-07-29 — Bug-hunt round 8 (first loop round): four hunters, six
  adversarial refuters, 21 confirmed findings (10 substantive, 11 nitpick),
  5 candidates refuted. Headlines: transcribe resolves the audio offset
  before the conversion mutates the session (a refused external run no
  longer strands converted audio without its sidecar); install.sh installs
  v0.4.0 and release.yml gates the pin against the tag; usage errors
  uniformly exit 2; docs corrected against the code (installer prompts and
  verification, endpoint caps, offset provenance, session file inventory,
  AGENTS.md current state). The /dev/null TTY-gate nitpick is recorded but
  unfixed (a true isatty check needs a dependency); the AGENTS.md fence's
  dangling brief link is an upstream abcd defect, not fixable here.
- 2026-07-29 — Bug-hunt round 9: four hunters, eight adversarial refuters
  (two per dimension), 27 confirmed findings (11 substantive, 16 nitpick),
  3 candidates refuted, 1 skipped as recorded in round 8. Headlines: stray
  positional arguments no longer silently swallow the flags after them; the
  capture endpoint refuses records merge cannot read (shared validator);
  the offset sidecar persists before the conversion's rename so no refusal
  destroys a record-origin audio.wav; an unauthenticated gh falls back to
  the verified checksum instead of a false provenance refusal; optional
  dependency failures skip instead of aborting the installer; CHANGELOG
  gains its Unreleased section; reference pages corrected against the code
  (endpoint preconditions, evidence cap, sessionEnd bound, ffmpeg's real
  consumers, the demo page's CDN fetch disclosed in privacy). Vendoring
  rrweb (offline capture) needs a dependency decision — reported, not done.
- 2026-07-29 — Bug-hunt round 10: four hunters, two adversarial refuters.
  3 substantive and 22 nitpick findings confirmed and fixed; 3 candidates
  refuted (the -audio sidecar wording, the mode default claim, the
  line-wrap premise). Headlines: analyze refuses duplicate timeline ids
  (the id-keyed quote validator paired quotes with the wrong moment) and,
  with report, refuses unknown-src entries instead of silently omitting
  them; the recorder permissions headline fires only on a device-open
  failure, not on ffmpeg's ordinary banner; the offset sidecar writes
  atomically; the CI smoke gains event-half assertions and the release
  workflow asserts the shipped binary names the tag; install.sh verifies
  the installed binary before announcing success. Recorded, not fixed:
  the VERSION-pin window before a tag exists (process), docs-lint gates
  not running in CI (tooling), doubled CI runs on PR branches (trigger
  set is tied to the merge queue), and the /dev/null TTY gate (per
  round 8).
- 2026-07-30 — Bug-hunt round 11: hunts and refutations ran sequentially in
  one session (the subagent model was persistently overloaded server-side;
  the routine's sequential fallback). One substantive fix: an explicit
  `transcribe -offset` is validated at the CLI (finite, within ±1e9 s) —
  the flag was the one unbounded offset entry point, running the engine
  before a bare JSON error, or writing a transcript merge refuses and a
  sidecar transcribe's own reader refuses. Nitpicks: a demo bind failure
  (port taken) no longer creates a stray session directory; the CHANGELOG's
  Alice-persona claim corrected to Bob. Refuted: documenting the capture
  endpoints' 500 (the enumerated codes are the request-refusal contract).
- 2026-08-01 — Bug-hunt round 12: three substantive fixes. The demo capture
  write guard now checks the request's actual remote address, not only its
  `Host` header, closing a forged-loopback-Host write path on a deliberately
  wider bind; `analyze` orders a hand-edited or exchanged timeline by time
  before emitting it, matching `report`, so a reversed file is no longer
  coded in the wrong narrative order; `record` no longer banners a live
  session for a run about to exit immediately on a platform with no capture,
  and its no-audio guidance only suggests granting a microphone permission
  where one exists to grant. Nitpicks: `record`'s and `transcribe`'s CLI
  reference entries corrected against their actual exit-1 branches and
  manifest requirement; a timeline example reordered to match its own
  "stably sorted by t" claim; the instrument-your-own-app snippet gained the
  demo's `isTrusted` guard; a misleading CI comment corrected; the release
  workflow gained the installer syntax check ci.yml already runs. Refuted:
  a claimed newline-fusion risk in the demo's append writer (the existing
  truncate-on-failure rollback already covers it, and cross-run fusion is
  structurally impossible) and a claimed silent-fallthrough in `review`'s
  flag parsing (whitespace-only `-finding` behaves identically to omitting
  the flag, which is already handled).
- 2026-07-18 — Correction to the same day's CI entry above: the `check` job
  stayed Ubuntu-only rather than gaining a macOS leg (`ff9717e`, "CI: keep
  check ubuntu-only (drop macOS matrix leg)"), and zizmor's SARIF upload to
  Code Scanning was turned off (`5d19a07`, "CI: fix stale zizmor header
  comment (SARIF upload is off)", `advanced-security: false` in `ci.yml`) —
  both reversed within the same day the original entry describes, and left
  unrecorded until now.
- 2026-08-01 — Bug-hunt round 13: seven substantive fixes. `timeline.SpeechEnd`
  now clamps an inverted `t1` to `t` instead of inverting `EventsNear`'s join
  window, mirroring the clamp `checkedUtterances` already applies at merge
  time; a new `timeline.ReadEntries` (decoding through a pointer-typed
  `rawEntry`) refuses a `timeline.jsonl` entry missing `t` instead of reading
  it as `t=0` and placing it at the session's start; `record -demo` now binds
  the demo port through the new `demo.Bind` before creating the session
  directory or starting recorders, closing a stray-session gap `demo.Run`'s
  own bind-first ordering (round 11) did not extend to; `demo.Run` removes
  the session directory if `serveOn` fails after `session.Create` succeeds;
  `review` now distinguishes a nonexistent `-session` directory from a real,
  un-ingested one instead of pointing both at `analyze -ingest`; two stale CI
  claims in the verification brief (the dropped macOS matrix leg, and which
  smoke-test assertion actually guards the merge→report join) corrected
  against `ci.yml`. Nitpicks: `record`'s non-macOS `-demo` exit behaviour and
  `review`'s TTY-gate (actually a character-device gate) corrected in
  `cli.md`; a report.md quote-style mismatch, a per-endpoint JSONL-record
  overclaim, and a startup-timing overclaim corrected across three docs
  pages; the Quickstart's `git clone` prerequisite and install.sh's
  `$TESTIMONY_INSTALL_DIR` flag summary noted; the release workflow's
  version-stamp check now also runs pre-publish, against the local `dist/`
  tarball, rather than only after the tag is public. Refuted: a claimed gap
  between AGENTS.md's local command menu and the gates CI actually runs
  (the menu lists a single-test `go test -run` invocation no one would
  expect CI to run; `go test -race ./...` already covers the same suite).
- 2026-08-01 — Bug-hunt round 14: 13 substantive fixes, 9 nitpicks. `SaveManifest`
  now refuses a manifest that would exceed `LoadManifest`'s read cap instead of
  writing a session no command could load back; `transcribe` refuses to
  overwrite `transcript.jsonl` with zero utterances instead of truncating a
  good transcript to empty; `analyze` rejects a bare JSON `null` line in
  `findings.jsonl` and stops treating an empty timeline entry id as valid
  evidence; `transcribe -device`/`-vad` are validated against their documented
  enums, matching `-engine`. Three CI/release coverage gaps closed: the
  pipeline smoke test now asserts a joined, indented event bullet, not only
  header counts that stayed "10 · 10" through a broken join; `release.yml` runs
  `gitleaks` and `zizmor` against the tag commit, which a never-pushed-to-a-branch
  tag previously skipped; both workflows execute `install.sh --help` and its
  flag-error paths instead of only parsing its syntax. The bundled sample
  session's two tab-click `route` values were backwards relative to what the
  demo's capture-phase listener actually records, corrected. The
  `.abcd/development/brief/` tree — committed durable-record documentation,
  not code-adjacent — is brought back into line with the shipped v0.4.0 CLI
  across six files: four still described `record` as a stub and
  `analyze`/`review` as merely planned; the demo surface page still described
  the pre-round-12 write guard and a uniform 8 MiB body cap; the schema page
  understated a finding's `t` upper bound, its evidence cap, and the session
  file inventory. Four smaller doc/code mismatches also corrected (an
  unconditional `-audio` sidecar-write sentence, a missing `500` status, a
  sessionStart floor scoped too narrowly, and `cli.go`'s usage text disagreeing
  with `cli.md`'s own TTY-gate correction). Refuted: a findings.jsonl/ingest
  schema-drift coverage gap (round-tripped clean, no live drift) and a
  suspected persona/pronoun collision in the brief's personas page (Alice is a
  fictional persona describing a different, fictional maintainer).
- 2026-08-01 — Bug-hunt round 15: `session.Create` removes the directory it
  made when the manifest write it just made is refused, closing a stray,
  unreadable, EEXIST-blocking session directory; `review -finding` is now
  validated against `F-NNN` syntax at the usage status (exit 2), matching
  `-verdict`; a report event with no recognised payload field renders a `—`
  placeholder instead of a blank bullet; `EffectiveStatus` only carries a
  verdict's `of` target for `duplicate` verdicts, closing a "confirmed of
  F-002" nonsense render on hand-edited input; CI now compile-checks the
  three release platforms it does not otherwise build
  (darwin/arm64, darwin/amd64, linux/arm64), closing the gap where a
  GOOS-conditional break would first surface during an actual release.
  Four doc corrections: AGENTS.md now names the CI-only gates its local
  command menu omitted (installer flag handling, gitleaks, zizmor, the new
  cross-compile check); release.yml's verify-job comment now names the
  installer flag-handling step it omitted; install.sh's `--yes` help text no
  longer claims a uniform "brew if present" policy the ASR dependency (always
  whisperx) does not follow; two `.abcd/development/brief/` surface pages
  (analyze, transcribe) corrected against the shipped CLI where they
  contradicted `docs/reference/cli.md`. Three intent drafts reattributed a
  developer-consuming-findings quote from Bob (defined as "not a user of the
  tool at all") to Alice, whose defined role it actually is.
