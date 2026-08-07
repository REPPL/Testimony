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
- 2026-08-01 — Bug-hunt round 16: 5 substantive fixes, 7 nitpicks, 8 refuted.
  `record` removes the session directory `session.Create` just wrote when the
  recorder that follows fails to start (ffmpeg missing, no usable device),
  unless a real partial capture from an earlier stream in the same run is
  already on disk; `merge` refuses to overwrite an existing `timeline.jsonl`
  with zero entries when `transcript.jsonl` and `interactions.jsonl` are both
  missing, the same overwrite refusal `transcribe`/`analyze.Ingest` already
  give a run that yields nothing, though only when an existing timeline is
  there to protect; the demo app's `display-name`/`notify-toggle`/
  `theme-toggle` rows carry `data-testid` on the row rather than the bare
  control, so a captured click or change always anchors to the same durable
  selector and label instead of an empty label or, for the theme toggle's
  invisible checkbox, a fragile `span.slider` class selector — verified
  against a headless-Chromium run to match the bundled sample session
  fixture's selector and text fields. Doc fixes: the instrument-your-own-app
  guide's archival capture snippet is placed in the scope it depends on and
  gains the same
  `rrweb`-availability guard the demo carries, and now notes that a bare
  `testimony demo` session is labelled with the built-in demo's own app/task,
  not the reader's; `how-alignment-works.md`'s stamping attribution, the
  verification brief's CI gate list, release.yml's header comment,
  `internal/session`'s package doc comment, and the platform/dependencies
  briefs corrected against the shipped CLI; a `-notes` flag named in the
  CHANGELOG and a code comment, which never existed, corrected to the real
  `-task`/`-app` flags.
- 2026-08-02 — Bug-hunt round 17: `-audio`'s unsupported-extension check now
  runs CLI-side (exit 2) before `detectEngine`, matching `-engine`/`-device`/
  `-vad`; an ASR engine's segment start/end is now bounded to the same
  ±1e9s magnitude every other externally-sourced time in the pipeline uses,
  closing a `+Inf` transcript-write failure and a finite-but-absurd time that
  wrote a transcript `merge` only refused one command later; `install.sh`'s
  PATH-fix advice now routes on `$SHELL` instead of assuming zsh, which was
  wrong guidance on a bash-default Linux install; `merge`'s zero-entries
  refusal condition corrected in `cli.md` and the merge brief page (the guard
  is "zero entries", not "both files missing"); `ci.yml`'s header comment,
  the verification brief's release-only gate list, the instrument-your-own-app
  how-to's `t` validation list, and the tutorial's installer-prompt claims
  corrected against the code.
- 2026-08-02 — Bug-hunt round 18: `analyze`'s selector/route index gated on
  the raw string's emptiness but stored under its SafeText key, so an
  invisible-only-Unicode `ui.selector`/`ui.route` validated against nothing
  real, mirroring an id-index bug already fixed for ids; `report` and
  `review` decided a finding's on-screen anchor on the same raw-vs-rendered
  gap, rendering a blank anchor (invisible-only, or for `report`'s
  code-rendered selectors, backticks-only) instead of falling back to the
  evidence ids; `audio.offset.json`'s `offset_seconds`
  was value-typed, so a sidecar missing the key decoded to a silent `0`
  instead of refusing, contradicting the documented "refuses rather than
  guess"; `mapSegments` now bounds the offset+segment-time sum, closing the
  narrow band where two individually in-bounds values summed past what
  `merge` accepts; `record -out ""`/`demo -out ""` now exit 2 naming the
  flag instead of a bare `mkdir` error at exit 1; release.yml gained an
  end-to-end `install.sh` smoke test — every prior gate only checked syntax
  and the four early-return flag paths, never the real fetch/verify/extract
  path; `ci.yml`'s Go module cache disabled (no dependencies to cache);
  `instrument-your-own-app.md`'s `record -demo` alternative now notes its
  ffmpeg prerequisite; the record brief page's non-macOS `-demo` wording and
  missing SIGHUP corrected, and the purpose brief's codebase-mapping claim
  qualified as a future goal, matching the analysis brief and delivery
  phases. Recorded, not fixed: `go.mod`'s EOL Go 1.22 pin (a go.mod change
  is out of scope for an autonomous round) and `DECISIONS.md`'s few
  out-of-calendar-order entries (reordering an append-only log would itself
  break the append-only property; noted, not corrected).
- 2026-08-02 — Bug-hunt round 19: an explicitly-empty `analyze -ingest`/`-out`
  now refuses at exit 2 instead of silently switching mode (empty `-ingest`)
  or redirecting to stdout (empty `-out`); `review -finding F-NNN -verdict
  duplicate-of-F-NNN` (self-duplicate) is refused at exit 2 alongside
  review's other pairing checks instead of failing at exit 1 inside
  `review.checkTargets`, where it was masked entirely on a session with no
  `findings.jsonl` yet; `report` and `analyze`'s emitted request now render a
  placeholder (`—`/`(none)`) for an event `kind` or manifest App/Participant
  that is whitespace-only or invisible-only Unicode, instead of a blank
  field — presence was decided on the raw value before `session.SafeText`
  reduced it to nothing. Docs corrected against the code: the README
  architecture diagram no longer attributes `interactions.jsonl` to rrweb
  (it is fed only by the app's own capture hooks; rrweb feeds the archival
  `events.rrweb.jsonl` stream); `session-directory.md`'s transcript `t0`/`t1`
  rows now document the same ±1e9s magnitude bound their sibling `words` and
  interaction `t` rows already state; `cli.md` and the analyse-a-session
  how-to now mention the finding id `review` prints first. Internal-doc
  fixes: `itd-1-record-command.md` moved from `intents/planned/` to
  `intents/shipped/` — the capability shipped in v0.2.0 and directory
  location is the intents' single source of truth for lifecycle state, per
  `intents/README.md` (its linked spec, `spc-1`, remains in `specs/open/`:
  no spec-lifecycle directory convention exists to move it into) — and its
  stale
  "consent reference" manifest-field claim removed (no such field exists;
  participant consent is itself out of scope, deferred to itd-5);
  `spc-2-analysis-findings.md`'s "TTY-gated" framing for `review`'s
  interactive gate corrected to "gated on stdin being a character device",
  matching the correction round 18 made to `cli.go`'s usage text. Recorded,
  not fixed: `go.mod`'s EOL Go 1.22 pin (per round 18; still out of scope for
  an autonomous round).
- 2026-08-02 — Bug-hunt round 20: `analyze.ParseRecords`'s duplicate-finding-id
  check now compares ids in their `session.SafeText` form, matching the
  timeline id checks, closing the last id-uniqueness check in the codebase
  still keyed on the raw string — two finding ids differing only by
  invisible-Unicode bytes (e.g. a zero-width space) both rendered under the
  same visible id in `report`/`review`, one confirmed and one unverified,
  without a duplicate refusal; `POST /api/events` now refuses a literal JSON
  `null` body with 400 instead of decoding it to a nil slice and answering
  204, matching the array-required contract `instrument-your-own-app.md`
  documents. Docs/internal-doc nitpicks: `CHANGELOG.md`'s `[Unreleased]`
  section had accumulated duplicate and near-duplicate group headings
  ("Evidence integrity:" twice; "Invocation contract:"/"CLI invocation:" and
  "Checks and installer:"/"Installer:" as separate groups) from rounds
  appending new headings instead of extending existing ones — consolidated
  without dropping any bullet; the review surface brief
  (`04-surfaces/07-review.md`) now names the finding id `review` prints
  first, the correction round 19 made to `cli.md` and the analyse-a-session
  how-to but missed on this sibling page; `spc-1-record-command.md`
  corrected against the shipped code (the microphone capture argv uses
  avfoundation's `:default` device, not a resolved index — a fixed index
  can capture a virtual audio driver instead of the real microphone; signal
  handling includes SIGHUP alongside SIGINT/SIGTERM, matching round 18's
  correction of the same gap in the record surface brief); the README
  architecture diagram's `screen ──► rrweb` line
  relabelled `page ──► rrweb` — rrweb records DOM/pointer activity from the
  instrumented page, not the screen capture, which is a separate stream
  (`screen.mp4`) the diagram does not depict. Refuted: a `release.yml`
  private-repo attestation-guard asymmetry (the claimed failure mechanism
  does not occur — an unauthenticated `curl`/`wget` tarball fetch against a
  private repo 404s before the `gh attestation verify` branch is ever
  reached); `AGENTS.md` claiming CI runs plain `go test ./...` (the `-race`
  leg is a strict superset, already refuted in round 13); the CHANGELOG
  having no per-round documentation entry (recent rounds have not
  maintained one consistently — rounds 8 and 13 also landed doc-only
  commits with no CHANGELOG bullet, though most rounds since 12 have);
  the `.gitignore` rule for `examples/*/report.md` (the file is not
  tracked, so the rule is correct and exactly parallel to the
  `timeline.jsonl` sibling); persona role-label wording drift across intent
  drafts (the repo's naming rule constrains persona names, not role
  labels, and the varying labels are compatible facets of each persona's
  definition).
- 2026-08-02 — Bug-hunt round 21: an explicitly-empty `transcribe -audio`
  now refuses at exit 2 instead of silently switching to the in-place
  branch (transcribing the session's own `audio.wav`, or claiming `-audio`
  was never given), the last unguarded empty-flag site in that family;
  `timeline.ReadEntries` now bounds an entry's `t` and a speech entry's
  payload `t1` to the same ±1e9s magnitude `checkedUtterances` already
  enforces on `transcript.jsonl` — a hand-edited or exchanged
  `timeline.jsonl` reaches this reader without passing through that check
  at all, and an oversized `t1` previously misattributed every later event
  to the poisoned utterance in `report.md` and widened `analyze`'s accepted
  finding range to match, both silently at exit 0. Nitpicks: `report`'s
  title now falls back to a placeholder on its rendered (SafeText) form,
  matching the App/Participant pattern, while the Tasks line drops entries
  that render empty and is omitted entirely once none survive; `record
  -demo` removes the session directory it just created when `serveDemoFn` fails
  after `session.Create` succeeds, mirroring the cleanup its sibling
  `startRecordersFn` failure path already had; a findings.jsonl line with
  no id is now refused with its own message instead of a `duplicate
  finding id ""` on the second one; `how-alignment-works.md` and
  `session-directory.md` now state the join's first-utterance-wins
  tie-break explicitly, rather than a per-utterance predicate a reader
  could apply to conclude an event went missing from a later utterance it
  overlapped; `ci.yml`'s cross-compile check gained a version-stamp ldflags
  check — the Go linker silently ignores `-X` for a symbol that no longer
  exists, so a `cli.Version` rename would stay green through every gate
  and only surface in `release.yml` after a tag is already pushed;
  `spc-2-analysis-findings.md` corrected against the shipped code (`ingest`
  reads `timeline.jsonl` only, not `manifest.json`; the finding-`t` floor
  is `0` unless the timeline holds a negative-time entry, and `sessionEnd`
  includes event times, not only speech `t1`). Refuted: a `cli.md`
  `-offset` table-row imprecision already reviewed and left twice across
  rounds 8 and 14 (self-correcting via the adjacent paragraph); a
  `spc-1-record-command.md` flag synopsis "missing" `-commit` (the
  synopsis already omits `-addr` too — an illustrative sketch, not a
  promised-complete invocation contract).
- 2026-08-02 — Bug-hunt round 22: an explicitly-empty `review
  -finding`/`-verdict` pair is refused at exit 2 instead of being
  indistinguishable from omitting both flags — on a character-device stdin
  it could append a verdict for a finding the caller never named; `analyze`
  now filters a blank/invisible-only manifest task from the emitted
  request's numbered task list, matching `report`'s already-filtered task
  rendering; `install.sh --dir`/`--version` refuse an explicitly-empty value
  (previously only a missing one), closing the same class of gap the CLI's
  own empty-flag guards cover; `itd-2-analysis-findings.md` moved from
  `intents/planned/` to `intents/shipped/` (analyze/review shipped in
  v0.2.0, the same rule round 19 applied to sibling `itd-1`), with its AC3
  narrowed to the request-level keyframe flag that actually shipped,
  matching `spc-2`'s own flagged divergence. Recorded, not fixed:
  `go.mod`'s EOL Go 1.22 pin, re-surfaced and re-verified this round but
  still out of scope for an autonomous round per rounds 18/19. Refuted:
  `itd-8-local-analysis.md`'s "local vs cloud" framing, read as
  contradicting the host-delegated architecture — the same framing is
  already load-bearing in the committed brief's own constraints/ethics
  pages and in the shipped `docs/explanation/privacy.md`, dated after the
  host-delegation decision.
- 2026-08-02 — Bug-hunt round 23: whisper.cpp's `-model` is now
  resolved alongside `-engine`, before the external-audio conversion —
  previously resolved inside the whisper.cpp runner, after the conversion
  had already replaced a record-origin `audio.wav`, so the ordinary
  first-run "model not found" failure destroyed the irreplaceable capture;
  `transcribe -engine`/`-device`/`-vad` refuse an explicitly-empty value at
  exit 2, closing the last gap in that empty-flag-guard family
  (`-compute_type`'s open-ended set is unaffected);
  `itd-2-analysis-findings.md`'s press release no longer claims
  task-boundary chunking the shipped pass does not do — `spc-2`'s own
  flagged divergence already recorded it, now named in itd-2's out-of-scope
  list alongside the keyframe-extraction divergence. Nitpick: `AGENTS.md`'s
  CI gate enumeration now names the version-stamp ldflags check `ci.yml`
  gained in round 21. Refuted: an empty `transcribe -model`/`-language`
  reaching the engine verbatim (fails loudly either way, same as any other
  bad value); `analyze` emitting a content-free request for an empty
  timeline at exit 0 (a stateless renderer, not a destructive writer —
  `report` treats an empty timeline the same way); `.abcd/work/DECISIONS.md`
  carrying two entries one day out of order near its start (a genuine
  parallel-branch merge artefact, not a discipline lapse, and any fix would
  violate the file's own append-only contract).
- 2026-08-02 — Bug-hunt round 24: `demo`/`record -demo`'s `-addr` now refuses
  a numeric port outside 0-65535 (e.g. `:99999`) as a usage error at exit 2,
  instead of letting `net.Listen` reject it during address resolution at
  exit 1 — indistinguishable from a genuine bind failure such as a taken
  port; a named service port (e.g. `:http`) is left to the runtime's own
  lookup, unaffected. Docs: `02-verification.md`'s CI gate
  enumeration now names the version-stamp ldflags check `AGENTS.md` already
  listed (round 23); `01-phases.md`'s Phase 2 status cell no longer
  unqualifiedly claims chunking shipped, matching the flagged-divergence
  wording round 23 already applied to `itd-2`. Refuted: an intents-README
  "always they/them" persona-quote rule read as contradicting
  `03-personas.md`'s gendered narrative pronouns (different registers for
  the same names, co-authored in one commit, and the intents rule is
  honoured with zero exceptions across all nine intent drafts); a claimed
  staleness in `spc-1`'s non-macOS `record -demo` "exits 0" wording (split
  verdict between refuters — discarded per the loop's tie-breaking rule,
  not a confirmed finding); AGENTS.md's two dangling `.abcd/development/`
  links inside the abcd-managed fence (upstream plugin boilerplate, per
  round 8's identical precedent); a "four" vs "six" installer flag-path
  count (four code paths, six invocations — both accurate at their own
  granularity); no Go test reading `examples/sample-session` directly (a
  coverage preference, nothing currently inconsistent); `mode`'s "default
  A" wording and `-offset`'s "0 when derivation is impossible" wording
  (both scoped correctly by their surrounding text); two CHANGELOG
  `[Unreleased]` group headings ("Capture and diagnostics:"/"Capture
  integrity:") that read as overlapping but cover distinct topics.
- 2026-08-03 — Bug-hunt round 25: `report`'s `speaker` and its verdict
  suffix's `at`/`of` fields now decide presence on their rendered
  (SafeText) form, closing the last raw-emptiness check in `report.go` —
  a diarisation label or verdict date that is non-empty raw but renders to
  nothing or whitespace only used to skip the `P?` fallback (blank speaker,
  no attribution) or leave a dangling empty `()` after a verdict. Nitpick:
  `review`'s "unrecognised choice" message now echoes the trimmed value it
  actually matched against, instead of the raw, newline-terminated input
  line. `02-scope.md`'s "Current status" section no longer claims Phase 2
  shipped unqualified against its own deliverable list naming chunking — the
  last remaining sibling of `01-phases.md`/`04-analysis.md` still missing
  the flagged-divergence wording they already carry.
- 2026-08-05 — Bug-hunt round 26: `report`'s `findingAnchor` and `review`'s
  `anchor` now filter the evidence-id fallback to ids that render non-empty
  and fall back to "no evidence" when none remain — the one sink in both
  files with no rendered-form guard of its own, previously rendering the
  dangling label "evidence " (or "evidence , ") for an empty or
  whitespace/invisible-only evidence list. `ci.yml`'s header and
  `release.yml`'s verify-job comment now both name the version-stamp ldflags
  check in their gate enumerations, closing two more siblings in the chain
  rounds 21/23/24 already corrected elsewhere. `itd-1`'s shipped intent no
  longer claims a bare `record` starts screen capture by default in either
  its Press Release or its acceptance criteria — audio-only is the shipped
  default (`-video` opts in), and its Out-of-Scope list now points at spc-1's
  own "Default: audio-only" section for the divergence. Nitpicks:
  `02-transcribe.md`'s "Nothing touches the network" now carries the same
  model-fetch caveat `docs/explanation/privacy.md` documents; the intents
  README's file-shape section now describes the frontmatter accurately per
  file — `kind: standalone` on the two shipped intents, the four newer
  `kind`/`suggested_kind`/`reclassification_history`/`builds_on` placeholder
  keys only on itd-6 onward — and splits Open Questions from Audit Notes,
  which is populated at ship time, not while a draft is still open.
- 2026-08-05 — Bug-hunt round 27: `report`/`review` render "no words" for an
  utterance whose `text` is empty, whitespace-only, or invisible-only
  Unicode, "no quote" for a finding's empty/invisible-only `quote`, and "—"
  for an empty/invisible-only `type` — `type`, `quote`, and utterance `text`
  were the last fields on these two rendering paths with no rendered-form
  fallback, previously rendering a blank quotation next to a real speaker
  and timestamp, or a dangling separator with no type. `transcribe`'s
  `mapSegments` now drops a segment on the same rendered-form check
  (`session.SafeText`), not raw `strings.TrimSpace` alone, so an
  invisible-only-Unicode segment cannot reach `transcript.jsonl` from a real
  transcription run in the first place. ffmpeg/whisperx/whisper.cpp's
  captured output, and `record`'s device-probe and recorder-stderr tails,
  now pass through a new `session.SafeTextLines` (SafeText applied line by
  line, keeping line breaks) before reaching the operator's terminal — the
  one class of terminal sink SafeText's earlier hardening rounds had not
  reached. `itd-1`'s In Scope bullet no longer claims the launcher "starts
  screen and audio capture" by default, the one sibling of its Press
  Release/AC1 round 26 quoted as the problem but never actually edited.
  `02-dependencies.md`'s "Transcription never touches the network" is
  qualified with the same model-fetch exception its siblings carry.
  CHANGELOG.md gains the entry round 26's evidence-anchor fallback should
  have carried. Refuted: `report -window`'s missing magnitude bound (an
  ordinary window wider than the session span already produces the same
  "join everything" output as an unbounded one — no bound short of the
  session's own span would change the outcome, and it is documented,
  intended behaviour); `session-directory.md`'s `t1`/`text`/`status` rows
  marked "Required: yes" beside a documented default or drop (no reader
  refuses their absence either, matching `session`, finding `type`,
  `severity`, and `quote`, none of which are flagged; the table's
  "Required" column is not a strict writer/reader-contract split — its
  `speaker` row is the counter-example, marked "no" despite `transcribe`
  always emitting it — so this is closer to established, if imprecise,
  usage than a fresh inconsistency worth acting on this round).
- 2026-08-05 — Bug-hunt round 28: `review`'s `findByID`/`contains` now compare
  a finding id in its `session.SafeText` rendered form, matching every display
  path — a hand-edited or exchanged `findings.jsonl` carrying an invisible
  character in an id (`analyze.Load` never re-validates `^F-\d{3}$`; only
  ingest does) displayed as a clean id but was unreachable by that same id via
  `-finding` or an interactive duplicate-of target; a verdict recorded for such
  a finding now also carries its actual raw id rather than the operator's
  clean flag/typed value, so `analyze.EffectiveStatus` (keyed on the raw id)
  attaches it instead of silently dropping it from the report. The correctness
  adversarial reviewer caught a gap the initial fix left open: `checkTargets`'
  "cannot be a duplicate of itself" guard still compared raw ids, so a
  duplicate-of target matching the finding's own dirty id under SafeText slid
  past it and recorded a finding as a duplicate of itself; that guard now
  compares the same rendered form, fixed before merge. `cli.go`'s merge/report
  success messages now reference `session.TimelineFile`/`ReportFile` instead
  of independently-spelled string literals for the same filenames.
  `01-purpose.md`'s Mode B keyframe-extraction mention now reads "as a future
  goal", matching the same phrase the page's own opening paragraph already
  uses for its other unshipped item (codebase mapping) — the docs-accuracy
  reviewer caught that the round's first pass had instead coined "a planned
  fallback", a phrase found nowhere else in the repo and inaccurate in this
  Mode B context (keyframes stand in for the missing event stream here, per
  the architecture note and `itd-4`, not a fallback from a cheaper primary
  path the way `04-analysis.md` frames them for Mode A). Refuted:
  `report.go`'s `orDash` fallback branch being unreachable (true, but a
  deliberate locally-redundant guard against a future caller invariant
  change, per the same rationale `review.go`'s `checkTargets` states for its
  own SafeText calls); `demo`'s `-addr ":"` bypassing `CheckAddr`'s numeric
  range guard (not silent — the real bound ephemeral port is reported — and
  an empty port is `net.Listen`'s own deferred-to-runtime case, the same as
  a named service port, settled by round 24's identical precedent); `itd-2`'s
  AC2 wording ("the finding's status becomes...") contradicting the
  append-only invariant (AC2's own second clause names the append-only
  mechanism; "status" at intent altitude is the effective status the pipeline
  derives and displays, not the stored field the invariant docs constrain);
  `02-verification.md`'s "kept under `sessions/`" line being unverifiable
  (`sessions/` is gitignored by design — committing a real captured session
  would violate the repo's own privacy rule). Also excluded before
  verification, as precedent duplicates of findings already discussed in
  earlier rounds: `AGENTS.md`'s dangling `03-configuration.md` link inside the
  abcd-managed fence (round 8); `AGENTS.md` claiming CI runs plain
  `go test ./...` (round 13); persona role-label wording drift across intent
  drafts (round 20); the intents-README "always they/them"
  persona-quote rule read against `03-personas.md`'s gendered narrative
  pronouns (round 24).
- 2026-08-06 — Bug-hunt round 30: `transcribe.mapSegments`'s word-level
  emptiness check now decides presence on `session.SafeText`'s rendered form,
  matching the segment-level guard five lines above it — a word that is
  entirely invisible-only Unicode (e.g. ZWSP U+200B) was non-empty raw and
  survived `strings.TrimSpace`, so it reached `transcript.jsonl`,
  `timeline.jsonl`, and the analysis request as a timestamped word with no
  visible content. `install.sh`'s `install_ffmpeg_local` error-handling
  comment no longer claims the EXIT trap "covers only `install_binary`'s
  `$tmp`" — that trap has swept `$tmp2` since round 21's fix; the comment was
  stale from the moment it landed in the same commit that widened the trap.
  Refuted: an `analyze.indexTimeline` map write keying `idx.uttText` under an
  unguarded raw id (the read path is gated behind `idx.ids`, which is itself
  gated, so the empty key is write-only dead data with no observable effect);
  `session.WriteFileAtomicNoFollow` refusing only symlinks, not other
  non-regular files, at its target path (the function never opens the
  pre-existing file — it renames a temp file into place, and `rename(2)`
  neither opens, blocks on, nor writes through a FIFO/device/socket, so the
  hazard the sibling `openNoFollow`'s stricter check exists to prevent is
  absent by construction); `cli.md`'s `-offset` table cell compressing the
  "external audio vs the session's own `audio.wav`" split as "with/without
  `-audio`" (the surrounding prose states the exception twice within ten
  lines, and the table already compresses two other branches the same way);
  `ci.yml`'s cross-compile step comment ("linux/amd64 already covered by
  Build above") read as a claim about `CGO_ENABLED`, which it never makes —
  the `CGO_ENABLED=0` clause and the coverage clause are two independent
  statements joined by a semicolon, and the coverage claim holds regardless;
  `analyse-a-session.md`'s "four steps" intro read against its five numbered
  headings (the fifth, re-rendering the report, is grammatically set off by
  an em dash as a follow-on outside the enumerated list, and invokes a
  different pipeline command, `report`, from the four analysis-layer steps
  proper); `session-directory.md`'s abbreviated `utt-003` example text read
  against the fuller fixture sentence (the page's other examples are
  established as illustrative reductions too — the manifest example already
  drops three fixture fields — and no `findings.jsonl` quote cites `utt-003`,
  so nothing depends on the byte-exact form). Also excluded before
  verification, as a precedent duplicate: `AGENTS.md` claiming CI runs plain
  `go test ./...` (round 13).
- 2026-08-06 — Bug-hunt round 31: `CHANGELOG.md` gains the `[Unreleased]`
  entry round 30's word-level `SafeText` filtering change should have
  carried — `transcribe.mapSegments`'s per-word emptiness guard
  (`transcribe.go:532`) is a real, user-visible change to what reaches
  `transcript.jsonl`/`timeline.jsonl`, and the round's commit touched
  `transcribe.go` and its test but not `CHANGELOG.md`, leaving only the
  round-27 segment-level sibling documented. Same precedent as round 27's
  own back-fill of round 26's evidence-anchor fallback. Refuted:
  `session-directory.md`'s `words` row omitting the same drop cause from
  its omission-cause list (one refuter killed it as pre-existing — the
  empty-word drop predates round 30 by 30 rounds, so round 30 introduced no
  fresh doc staleness — the other found it survives on the row's own
  precedent of documenting the equivalent `text`-row cause; discarded on
  the split verdict); `session-directory.md`'s `quote` row's "no
  normalisation" claim read against `validate.go`'s SafeText-based
  comparison (both refuters: `SafeText` is a rune-local, substring-preserving
  map — no case-folding, no whitespace-collapsing, no Unicode NFC/NFD — so
  it is not "normalisation" in the sense the row rules out, and the row's
  own 2026-07-17 decision-log origin coordinates "no normalisation" with
  "not corpus-joined" as a matching-leniency claim, not a byte-purity one);
  `analyze.go`'s duplicate-finding-id error message printing the raw id
  instead of the `SafeText`-compared form, unlike three claimed siblings
  (both refuters: the claim inverts on inspection — `%q` already escapes
  every character `SafeText` would strip, so the raw form is the more
  diagnostic one, and the "three consistent siblings" premise doesn't
  hold — `timeline.go:514` prints neither form's first occurrence either,
  and `validate.go:155` prints raw like `analyze.go` does).
- 2026-08-06 — Bug-hunt round 32: two confirmed defects. `POST
  /api/interactions` (`demo.go`) and `transcribe` now check the *timeline
  entry* a record becomes — via new `timeline.EventEntry`/`SpeechEntry` and
  `session.EncodedLen` — against the JSONL line limit, not just the record
  itself: `merge`'s `src`/`id`/`payload` wrapping (`timeline.BuildEntries`)
  could push an entry over the limit that its source record, checked alone,
  passed, so a record was durably persisted at 204/exit 0 and then
  permanently unmergeable: `merge` refused to write its timeline entry on
  every re-run, leaving `report` and `analyze` with no timeline to read, with
  no CLI-level repair. `session.WriteJSONL`'s encoder also stopped HTML-escaping
  `<`, `>`, `&` into six-byte `\uXXXX` sequences (`SetEscapeHTML(false)`,
  matching `compactLine`'s existing non-escaping behaviour): the escaping
  alone could inflate an accepted record's wrapped entry up to sixfold,
  pulling the failure window down from a ~30-byte sliver at the very top of
  the range to any record from roughly 700 KiB up. Second: `record -video`
  excluded only the single recorder `anyExit`'s select happened to observe
  (`if c != dead`) from the missing-output sweep; when a second recorder also
  exited on its own at the same moment, it fell through to
  `classifyMissingOutput`'s "stayed blocked on the permission prompt"
  narrative — disproved by its own exit — with its real exit status never
  surfaced. Now every child whose `done` channel is already closed when the
  exit is observed (sampled before `stopAll`'s SIGINT reaches the others, so
  a live recorder's clean shutdown is never mistaken for a self-exit) is
  excluded and gets its own `classifyRecorderExit` diagnosis. Refuted:
  `ci.yml`'s `check`-job comment omitting gofmt/ldflags/installer steps from
  its own prose (both refuters: gofmt was already in the job when the
  comment was first written, so its omission was never staleness, and the
  comment was rewritten in round 15 — after the installer steps already
  existed — without naming them either: a rationale note about
  Ubuntu-only/single-job-name, not an enumeration, unlike the file header
  ten lines above which round 26 fixed for exactly this reason); `release.yml`'s header omitting the install-e2e/version-assert/
  no-branch-commit-tripwire steps (both refuters: the header states the
  tripwire as an invariant ("nothing is pushed to any branch"), predates
  none of the "omitted" steps — the post-publish attestation verify is
  equally unnamed and equally day-one — and a full rewrite would duplicate
  the fuller per-step comments 200 lines below). Not re-raised:
  `session-directory.md`'s `words` row omission-cause gap, already
  adjudicated and discarded on a split refuter verdict in round 31.
  Post-hoc adversarial review of the round's own PR (correctness; docs
  accuracy) independently caught a regression the second finding's fix
  introduced: the sibling early-exited child's `atStartup` was sampled
  after `stopAll`/`stopDemo`, reopening the exact slow-stop-poisons-
  classification bug `dead`'s own pre-`stopAll` sampling exists to avoid.
  Fixed by moving that sampling into the same pre-`stopAll` loop, with a
  new regression test. Both reviewers' verdicts were BLOCK, so per the
  loop's merge gate the PR (#48) stays open for the human rather than
  auto-merging, even with the fix pushed and CI green.
- 2026-08-07 — Bug-hunt round 33: one confirmed defect (two readers), two
  nitpicks. `session.ReadJSONL` capped a single line at `MaxJSONLLine` but
  never the whole file, contradicting the comment on `maxManifestBytes` that
  listed it as an already-bounded sibling reader; a file built from many
  small, individually-legal lines defeated the per-line cap and OOM'd
  `merge`, `report`, and `analyze`. New `session.MaxJSONLBytes` (16 MiB,
  matching `analyze.Ingest`'s existing cap) bounds the running total in
  `ReadJSONL`, with a matching `WriteJSONL` pre-flight check for its two
  actual callers (transcript.jsonl, timeline.jsonl). Post-hoc adversarial
  review of the round's own PR (correctness; docs accuracy) independently
  caught that `analyze.ParseRecords` — findings.jsonl's own scanner, never
  routed through `ReadJSONL` — carried the identical gap and was missed by
  the round's own comment claiming every sibling reader was already bounded;
  `ParseRecords` now enforces `MaxJSONLBytes` too, and the comments on
  `maxManifestBytes`/`WriteJSONL` were corrected to name the readers and
  writers accurately (`WriteJSONL` never writes findings.jsonl;
  `analyze.commitFindings`/`review.AppendVerdict` do, through their own
  locked descriptors). Nitpicks fixed: `AGENTS.md` claimed CI runs both the
  plain and race-enabled `go test` lines, but CI only runs the race-enabled
  one (no test differs between them); two 2026-07-18 `DECISIONS.md` entries
  had been spliced ahead of a run of 2026-07-17 entries they were committed
  after, breaking the file's own "newest last" rule by both date and commit
  order, and were moved back. Reverted before merge: `report.eventLine`'s
  `orDash` wrapper was initially removed as unreachable dead code, but round
  28 (2026-08-05, above) had already considered and explicitly rejected this
  exact claim as a deliberate locally-redundant guard against a future
  caller invariant change — the same rationale `review.go`'s `checkTargets`
  states for its own SafeText calls. An adversarial PR reviewer caught the
  reintroduction before merge; the wrapper stays. Refuted: unbounded
  error-accumulation in `analyze.Ingest`/`errors.Join` "defeating"
  `maxAnswerBytes` (one refuter showed the same OOM reproduces from
  `json.Unmarshal` alone before a single error accumulates, and a realistic
  degenerate answer stays around 255 MB, well within bounds — split verdict,
  discarded); a dangling-link nitpick in `AGENTS.md`'s abcd-managed fence
  (split verdict on whether `.abcd/rules.json` gives an indirect fix path;
  discarded as out of scope).
- 2026-08-07 — Bug-hunt round 34: one confirmed substantive defect and four
  confirmed nitpicks. This round's hunt ran concurrently with another
  session's round 33 (above, PR #49) and picked the same next number before
  either merged; renumbered to 34 on rebase, and its independent AGENTS.md
  finding (below) dropped as a duplicate of round 33's own fix once that
  overlap became visible. `record`'s `<-ctx.Done()` shutdown branch sampled
  no early-exit state before `stopAll`, unlike the sibling `anyExit` branch
  (round 32's fix): a recorder whose `done` channel closed in the
  sub-millisecond window before the interrupt reached the select fell
  through to `finaliseOutputs` unconditionally, either misdiagnosed via
  `classifyMissingOutput`'s "stayed blocked on the permission prompt"
  narrative when it captured nothing, or silently exited 0 with a
  truncated recording presented as a clean session when it left partial
  data. Fixed by factoring the pre-`stopAll` sampling both branches need
  into a shared `sampleEarlyExits` helper, with two new regression tests;
  `CHANGELOG.md` gains the matching `[Unreleased]` entry, the sibling of
  round 32's for the recorder-exit path. A post-hoc adversarial review of
  the round's own PR caught that one of the two new tests made both
  `select` cases ready by construction, so it asserted on `opts.Log` alone
  when Go's non-deterministic case choice can route the diagnosis to the
  returned error instead — fixed to check both. The same review caught that
  removing `analyze.Validate` (below) orphaned its sole caller-side helper,
  `atPositions`; removed that too, along with its stale comment naming
  `Validate`. Nitpicks: `session-directory.md`'s `words` row omitted that
  empty/whitespace/invisible-only text is also dropped — round 31
  split-discarded the identical finding (one refuter called the drop rule
  pre-existing with no staleness fresh from round 30, the other found it
  survives on the row's own precedent of documenting the equivalent
  `text`-row cause); this round's two independent refuters, examining it
  fresh, both reached the surviving refuter's conclusion. The same page's
  `timeline.jsonl` table had no `Required` column and did not state `t`'s
  (or a speech payload's `t1`'s) ±1e9-second bound — corrected using
  `report`/`analyze`'s user-facing names rather than the internal
  `ReadEntries` that first enforces it, matching the rest of the page. Its
  `utt-003` example trimmed the utterance text to start mid-sentence while
  keeping the untrimmed fixture's `t0` and word times, leaving the first
  shown word 1.6s after its own `t0`: round 30 refuted a claim about this
  same example's abbreviated text against the fuller fixture sentence (the
  page's examples are established illustrative reductions, and no
  `findings.jsonl` quote cites `utt-003`), but that reasoning doesn't reach
  this narrower defect — the abbreviation itself is fine, the self-
  contradiction between the shown `t0` and the shown first word's time is
  the actual gap — so restored the opening words to agree with `t0` again,
  matching real `merge` output's integer formatting (`"t":16`, not
  `"t":16.0`) rather than transcript.jsonl's rounded literal. Also removed
  `analyze.Validate`, an exported function with zero callers anywhere in
  the module. Refuted: a claimed round-32 regression desynchronising
  `timeline.jsonl` from `analyze`'s emitted request via HTML-escaping (one
  refuter proved `EmitRequest`'s output byte-identical before and after
  round 32 — it re-decodes the timeline and re-marshals, so `WriteJSONL`'s
  encoder choice, round 32's actual change, never reaches the request an
  operator or model is shown, even though round 32 did newly desynchronise
  the two artefacts' own on-disk/in-source encoding on this axis); an
  inverted transcription segment span, a CI cross-compile comment's
  CGO-flag mismatch, `cli.md`'s "ingest reads timeline.jsonl only"
  phrasing, and the `manifest.json` example's field trimming (all four on
  split or unanimous refuter verdicts, with no misleading or behavioural
  consequence found).
- 2026-08-07 — Bug-hunt round 35: one confirmed substantive defect, three
  nitpicks. `analyze.oversizedFindings` bounded a single finding's
  `findings.jsonl` line against `session.MaxJSONLLine` but never the sum
  across an answer's findings, unlike `WriteJSONL`'s two callers
  (transcript.jsonl, timeline.jsonl): a set of individually valid findings
  could still serialise to a file over `session.MaxJSONLBytes` that `report`
  and `review` both refuse to read back (`holdsVerdicts`, the re-ingest
  recovery path, bounds only a line, not the total, so it can still probe
  and overwrite such a file), with `Ingest` reporting success at the moment
  it wrote the unreadable file. `oversizedFindings` now also accumulates
  and refuses an over-total set before any write, joining the existing
  transactional error set; the `WriteJSONL` doc comment, which had
  documented findings.jsonl as lacking this write-side pre-flight, is
  corrected to name where it now lives. Nitpicks fixed:
  `session-directory.md`'s `report.md` `MM:SS`
  description didn't note the leading `-` a time preceding `t0` renders
  with; `install.sh`'s trap-roster comment has named `$tmp`, `$tmp2`,
  `$gnupg`, and `$uvd` since round 9 introduced it, and round 10 added
  `$staged` to the traps themselves without updating the comment to match
  — the one cleanup target that touches the user's install directory rather
  than a temp dir, now named;
  `analyse-a-session.md` said "the CLI ... reaches no network", the sole
  outlier of the repo's five other "adds no network dependency" instances
  (`CLAUDE.md` is a symlink to `AGENTS.md`, not a distinct sixth) and, read
  literally as unscoped, contradicted by `demo`'s CDN-loaded rrweb
  recorder and the ASR engine's model fetch — reworded to name `analyze` and
  match the canonical phrasing, as rounds 26 and 27 did for the same
  overreach on other pages. Caught before verification: a fresh hunt for
  `report.eventLine`'s `orDash` fallback resurfaced the identical
  unreachable-branch claim round 28 explicitly rejected as a deliberate
  locally-redundant guard and round 33 had to revert a reintroduced removal
  of — checking round 33's own entry against the fix before verification
  showed the precedent, and the removal was not repeated. Refuted (all on
  unanimous or split adversarial verdicts): a claimed answer-vs-timeline
  quote/selector mismatch from `EmitRequest`'s `json.Marshal` HTML-escaping
  (re-raised independently of round 34's identical refutation of the same
  claim — both refuters this round decoded the escape and reproduced a
  validated ingest); `transcribe.checkEntriesFit` lacking a total-size
  counterpart to its per-line check (transcribe cannot see interactions.jsonl,
  so the total is `merge`'s invariant to hold, not transcribe's, and
  `WriteJSONL` already caps transcript.jsonl's own total); `AGENTS.md`'s
  "every push and pull request" omitting `merge_group` and tag pushes
  (`ci.yml`'s own header uses the identical phrasing, and AGENTS.md never
  discusses the release workflow tag pushes go through); and the
  `manifest.json` example's field trimming, re-raised independently of round
  34's identical refutation of the same claim.
- 2026-08-07 — Round 35's own PR (#52) picked up two adversarial reviews
  before merge. The docs-accuracy reviewer caught that the round's own
  `MM:SS` fix said the leading `-` marks a time preceding `sessionStart`
  (`sessionStart` is itself a minimum, so nothing can precede it — the sign
  actually marks a time preceding `t0`) and pointed its cross-reference
  "below" at a `t` note that is above; both corrected, and the note widened
  to say it covers the header's duration `MM:SS` too, not only the Timeline
  bullet's. It also caught `session.go`'s reworded `WriteJSONL` comment
  overclaiming that `review.AppendVerdict` shares `oversizedFindings`'
  total-size check — `AppendVerdict` still bounds only the one verdict
  record it appends against `MaxJSONLLine`; rescoped. `CHANGELOG.md`'s new
  entry was moved from the `Capture integrity` group into `Evidence
  integrity`, where the other `findings.jsonl` integrity entries live. The
  correctness reviewer then reproduced the round's own fix being reachable
  through the public `Ingest` API at a fraction of `maxAnswerBytes`, not
  only through direct unit testing as the new test's own comment had
  claimed: `oversizedFindings` and `writeFindings` both encode with Go's
  default HTML-escaping JSON encoder, so a quote or evidence id containing
  `<`, `>`, or `&` inflates roughly sixfold between the answer's raw bytes
  and the line actually measured/written — five findings quoting a
  ~600,000-byte run of `<` encode to ~17 MiB written from a ~3 MiB answer,
  under a fifth of the read-side cap. Added `TestIngestRejectsOversizedFindingsTotal`
  to exercise that path end-to-end and corrected the unit test's comment.
  Also fixed two nitpicks the same review raised: the total-size error
  message counted all findings in its denominator while `total` itself
  excludes any already flagged as over-long, so the two numbers disagreed
  whenever both errors fired in the same answer; and `total` was `int`
  where its sibling `WriteJSONL` uses `int64` (unreachable overflow given
  `maxAnswerBytes`, but inconsistent). A second pair of post-fix reviews
  caught that the `CHANGELOG.md` move landed one group short —
  `Invocation contract`, the group directly before `Capture integrity`, not
  `Evidence integrity` four groups earlier, which both reviewers had to
  re-derive from the file's actual headings rather than trust the first
  round's own description of its fix; moved to the right group this time.
  Also fixed: the "four other" `adds no network dependency` count was
  itself wrong (a naive single-line grep misses instances the phrase wraps
  across; round 4 found this round's own recount of six was also off by
  one — `CLAUDE.md` is a symlink to `AGENTS.md`, not a distinct file — so
  the real count is five); two `~18 MiB` figures
  (the new test's comment and this file's own entry) were decimal-MB
  numbers under a MiB label, corrected to `~17 MiB`; `session.go`'s
  `AppendVerdict` description called the verdict record it appends
  "already-small", contradicted by `review.go`'s own comment on why that
  record's size is checked at all; and the `MM:SS` sign note didn't say a
  time that rounds to zero renders unsigned, or that "this page" meant
  `report.md` rather than the reference page carrying the note. A third
  pair of reviews, re-deriving every prior fix from the files themselves
  rather than trusting either round's own description, confirmed the
  `CHANGELOG.md` group placement was finally correct but caught two more
  defects: `oversizedFindings`'s own doc comment, the CHANGELOG entry, the
  new test's comment, and this file's first-round entry all claimed an
  over-total `findings.jsonl` is unreadable to "report, review, and the
  re-ingest recovery path" alike — false for the total case, where
  `holdsVerdicts` (the recovery path) still scans and can overwrite the
  file, because its scanner bounds only a line, not the total; only report
  and review (via `ParseRecords`) actually refuse it. Reworded in
  `oversizedFindings`'s doc comment, the CHANGELOG entry, and the new
  test's comment, and `commitFindings`'s own doc comment (which only named
  the per-line bound as pre-checked) now names both. Separately, this
  file's claim that `install.sh`'s trap-roster comment was "already widened
  once, in round 30, from naming only `$tmp`" was itself wrong: that
  comment has named `$tmp`/`$tmp2`/`$gnupg`/`$uvd` since round 9 introduced
  it; round 30 fixed a different comment entirely (`install_ffmpeg_local`'s
  error-handling note); corrected to say `$staged` was added to the traps
  in round 10 without a matching comment update. Also softened this file's
  "where every other `findings.jsonl` entry lives" to "the other
  `findings.jsonl` integrity entries live" (`CHANGELOG.md`'s `Invocation
  contract` group holds `findings.jsonl` entries too). A fourth pair of
  reviews found the third round's own "reworded everywhere the claim
  appeared" was itself not quite true: this file's first-round entry
  (above) still carried the old "report, review, and analyze's own
  re-ingest recovery path all refuse" wording after the other three sites
  were fixed — corrected here too. It also caught that round 2's "six"
  recount of `adds no network dependency` instances double-counted
  `CLAUDE.md`, a symlink to `AGENTS.md`; the distinct-file count is five.
