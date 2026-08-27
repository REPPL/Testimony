# Changelog

All notable changes to Testimony are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and Testimony
uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html) with a
leading `v`.

Before v1.0.0, minor releases may make breaking changes; a change that can
break an existing invocation is called out in the entry that records it.

## [Unreleased]

### Added

- `transcribe` prints an elapsed-time status line every 5 seconds while the
  ASR engine is still running, instead of staying silent between the offset
  line and completion — a CPU-only `whisperx`/`whisper-cli` run can take
  several minutes with nothing else printed, indistinguishable from a hang.

### Fixed

- `record -video` resolves the screen-capture device by the anchored
  canonical name `Capture screen N` instead of a loose substring: cameras
  enumerate before screen pseudo-devices and device names are OS-supplied
  strings a crafted USB or virtual camera can set, so a camera whose name
  merely contained the phrase (e.g. a vendor string such as "Elgato Capture
  screen HD") used to be recorded into `screen.mp4` in the genuine display's
  place, silently — the video-side sibling of the microphone's `:default`
  anti-shadowing hardening.
- `analyze`'s emitted request escapes inline Markdown in the manifest's App,
  Participant, and task fields, as `report.md` already does for the identical
  fields: they render as list items outside any code fence, and an
  attacker-authored manifest value such as `[x](http://…/beacon.png)` used to
  survive verbatim — an active link, or a tracking/exfil image beacon fired
  the moment a saved `request.md` is previewed in a Markdown viewer. The
  escape set now has one shared home so the two artefacts cannot drift.
- `record`'s avfoundation device-listing parser no longer mistakes a device
  row for a section header when the device's name — an OS-supplied string a
  crafted USB or virtual-audio device can set — contains the phrase
  "AVFoundation audio devices:" (or the video variant). Such a row used to be
  dropped and the section flipped, misclassifying every device listed after
  it: a false "no microphone found" abort, or a shadowing virtual driver
  silently missing from the roster whose purpose is to make it visible. The
  `[N]` index, which a real header line never carries, now decides.
- `install.sh`'s macOS local-ffmpeg branch now installs `ffprobe` alongside
  `ffmpeg`, fetching evermeet.cx's separately-published build and verifying
  it against the same pinned publisher key: `transcribe -audio`'s offset
  derivation shells out to `ffprobe`, and without it on PATH the offset
  silently fell back to 0 — mis-timing every utterance of an external
  recording — with a provenance note blaming the recording's creation time
  rather than the missing binary. This closes the residual macOS gap the
  Linux-branch fix recorded.

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
- `SaveManifest` refuses a `manifest.json` that would exceed `LoadManifest`'s
  1 MiB read cap instead of writing it: a manifest built from long
  `-task`/`-app` text (or a hand-edited `notes` field) used to write
  successfully at exit 0, after which every later `merge`, `report`,
  `analyze`, and `transcribe` refused to load the session, with no repair
  path.
- `record`/`demo`'s session directory is removed when the manifest write that
  follows creating it is refused, instead of being left behind, empty and
  manifest-less: it used to litter the sessions root permanently and block
  an immediate retry at the same instant with `mkdir ... file exists` against
  the phantom directory.
- `transcribe` refuses to write `transcript.jsonl` when the engine returns
  zero utterances instead of truncating an existing good transcript to
  empty: a re-run whose engine yielded no usable segments (a wrong
  `-language`, a model producing only whitespace) used to report
  "transcribed 0 utterances" at exit 0 and silently destroy the prior file.
- `analyze` refuses a bare JSON `null` line in `findings.jsonl` instead of
  reading it as a phantom zero-value finding — empty id, severity 0 — that
  used to render in `report.md`'s Unverified group and enter `review`'s
  interactive queue.
- `analyze` no longer treats an empty timeline entry id as a valid evidence
  anchor: an evidence citation of `""` used to pass validation merely
  because some id-less entry existed in the timeline, not because the
  cited id was ever a real anchor.
- `merge` refuses to overwrite an existing, non-empty `timeline.jsonl` when
  `transcript.jsonl` and `interactions.jsonl` together yield zero entries —
  missing, empty, or both — instead of truncating it to zero entries at exit
  0: either file is individually optional, but with nothing left to build a
  timeline from there is no honest timeline to write, and
  `transcribe`/`analyze -ingest` already refuse the identical "nothing to
  write" shape rather than destroy an existing artefact.
- An ASR engine's own reported segment start/end time is bounded to the same
  magnitude every other externally-sourced time in the pipeline is: an
  implausibly large value silently overflowed to `+Inf` through the
  two-decimal rounding step, failing the transcript write with a bare JSON
  encoding error, or — for a merely absurd rather than astronomical value —
  wrote a transcript at exit 0 that `merge` only refused one command later,
  naming `transcript.jsonl` rather than the engine that produced it. A
  WhisperX word's own time is bounded the same way at the parser's word loop,
  but dropped rather than refused: an astronomical word time used to fail the
  whole transcript write with the same `+Inf` error, while a merely absurd one
  was written at exit 0 and carried unchallenged into `timeline.jsonl` —
  nothing downstream bounds a word's time as `merge` bounds an utterance's
  `t0`/`t1`. Such a word is now silently omitted from that utterance's
  `words`, the same outcome an unaligned word already had.
- **Behaviour:** `transcribe`'s offset+segment-time sum is bounded, closing
  the residual case the segment- and offset-level bounds above each miss
  individually: a segment time and an offset that were both, on their own,
  within magnitude could still sum past what `merge` accepts, writing a
  transcript at exit 0 that `merge` only refused one command later. An
  oversized word's offset-shifted time is dropped rather than refusing the
  whole segment, matching the existing unaligned/implausible-word policy.
- `analyze`'s `ui.selector`/`ui.route` index is keyed on the same sanitised
  (`SafeText`) form its lookup already uses, closing the sibling of the
  duplicate-id gap `analyze` already refuses: a timeline event whose selector
  or route was entirely invisible-Unicode formatting characters (non-empty
  raw, empty once sanitised) previously indexed under the empty key, letting
  any invisible-only-Unicode `ui.selector`/`ui.route` in an ingested answer
  validate against that phantom entry rather than a real one.
- `report` and `review` fall back to a finding's evidence ids when its `ui`
  selector/route renders to nothing or to whitespace only (invisible-Unicode
  formatting characters or literal whitespace; `report`, which renders
  selectors as code, additionally treats a selector of backticks alone as
  empty) instead of rendering a blank or whitespace-only anchor with no
  fallback; `report`'s event lines apply the same rendered-form check
  uniformly across `selector`, `text`, `value`, and `route`.
- `report` and `review` fall back further to the literal `no evidence` when
  a finding's evidence ids also render to nothing or to whitespace only,
  instead of the dangling `evidence ` label that fallback could still
  produce.
- `report` renders a `no words` placeholder for an utterance whose `text`
  is empty, whitespace-only, invisible-only Unicode, or absent, and
  `no quote` for a finding's empty or invisible-only `quote` (both `report`
  and `review`'s printed quote), and a `—` placeholder for a finding's
  empty or invisible-only `type` in both, instead of a blank quotation or a
  dangling severity label with nothing before it — `type`, `quote`, and
  utterance `text` were the last fields on these two rendering paths with
  no rendered-form fallback. `transcribe` now drops a segment whose text is
  invisible-only Unicode, not only whitespace-only raw text, so the same
  defect cannot reach `transcript.jsonl` from a real transcription run.
- `transcribe` now also drops an individual word whose text is
  invisible-only Unicode, matching the segment-level guard above: a word
  entirely made of Unicode format characters (e.g. a zero-width space) was
  non-empty raw and survived trimming, so it reached `transcript.jsonl`,
  `timeline.jsonl`, and the analysis request as a timestamped word with no
  visible content.
- `report` renders a `—` placeholder for an App, Participant, or event
  `kind` that is whitespace-only or invisible-only Unicode, instead of a
  blank field, and `analyze`'s emitted request does the same with `(none)`
  for App/Participant: presence used to be decided on the raw manifest or
  event value, before `session.SafeText` reduced it to nothing on render.
- `analyze.ParseRecords`'s duplicate-finding-id check compares ids in their
  `session.SafeText` form, matching the timeline id checks: two finding ids
  distinct only by invisible-Unicode bytes (a zero-width space, say) used to
  load without error and render under the same visible id in `report` and
  `review`, one in the Confirmed group and the other in Unverified.
- `review`'s `findByID`/`checkTargets` compare a finding's id in its
  `session.SafeText` rendered form, matching the duplicate-id check above: a
  raw id carrying an invisible character (a hand-edited or exchanged
  `findings.jsonl`; `analyze.Load` never re-validates `^F-\d{3}$`, only
  `-ingest` does) rendered as a clean id everywhere it was shown but was
  unreachable by that same id via `-finding` or an interactive duplicate-of
  target, and a verdict recorded through it stored the operator's typed value
  rather than the finding's actual id, so `analyze.EffectiveStatus` (keyed on
  the raw id) silently failed to attach it. The "cannot be a duplicate of
  itself" guard compares the same rendered form, so a duplicate-of target
  matching a finding's own dirty id under `SafeText` is still refused rather
  than recorded as a self-duplicate.
- **Behaviour:** `timeline.ReadEntries` bounds an entry's `t` and a speech
  entry's payload `t1` to the same ±1e9s magnitude `merge` already enforces
  on `transcript.jsonl` — a hand-edited or exchanged `timeline.jsonl` reaches
  this reader without passing through that check at all. An oversized `t1`
  used to reach `report`'s join window and `analyze`'s session-range bound
  unclamped for magnitude (only inversion was clamped), silently
  misattributing every later event to the poisoned utterance in `report.md`
  and admitting an equally absurd finding time as "inside" the inflated
  session range, both at exit 0.
- `report`'s title falls back to a `—` placeholder on its rendered
  (SafeText) form, matching the App/Participant/`kind` pattern above: a
  whitespace-only or invisible-only-Unicode Session name used to render a
  bare trailing dash with nothing either side. The Tasks line now drops
  entries that render empty and is omitted entirely once none survive,
  instead of rendering a lone `; ` with nothing either side.
- A `findings.jsonl` line with no id is refused with its own message instead
  of `duplicate finding id ""` on a second one: unlike a timeline entry,
  where an id-less line is legitimate and skipped, a finding's id is never
  optional, so the previous message misnamed what was actually wrong with
  the first line.
- `analyze` filters a blank or invisible-only-Unicode manifest task from the
  emitted request's numbered task list, matching `report`'s task rendering:
  it used to print such a task as a content-less numbered item, giving the
  request's "attribute each finding to a task" instruction a referent with
  no content and disagreeing with `report.md`'s task list for the same
  session.
- `report`'s speaker label and its verdict suffix's `at`/`of` fields decide
  presence on their rendered (SafeText) form, matching every other field in
  the file: a diarisation label or a verdict date that is non-empty raw but
  renders to nothing or to whitespace only used to skip the `P?` fallback
  and render a blank speaker with no attribution at all, or render a
  dangling, empty `()` after a finding's verdict instead of omitting the
  suffix as an absent `at` would.
- **Behaviour:** `analyze -ingest` refuses an answer whose findings would
  together exceed `findings.jsonl`'s 16 MiB total-size limit, the same limit
  `WriteJSONL`'s callers are already held to: nothing previously bounded the
  sum across an answer's findings, only the length of one finding's line, so
  a set of individually valid findings could still serialise to a file
  `report` and `review` both refuse to read back. It used to report success
  and write the unreadable file.
- **Behaviour:** `review` refuses to record a verdict that would push
  `findings.jsonl` past its 16 MiB total-size limit, the last of the file's
  two writers left unguarded after the previous entry closed the same gap
  for `analyze -ingest`. A `findings.jsonl` built at or near the cap could
  legally exist; recording a verdict against it used to succeed and silently
  brick every finding and verdict already on file — every later `analyze
  -ingest` and `review` call refused the file outright, and `report` exited
  0 but rendered an explicit "Findings unavailable" notice in place of the
  session's findings, with no in-tool repair (re-ingesting was itself
  refused once a verdict record existed).

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
- ffmpeg/whisperx/whisper.cpp's captured output, and `record`'s device-probe
  and recorder-stderr tails, are passed through the same invisible-Unicode/
  bidi-reordering sanitiser applied everywhere else untrusted text reaches
  the terminal, line by line so a multi-line tail keeps its line breaks —
  the one class of terminal sink SafeText's earlier hardening rounds had
  not reached.
- `record` no longer prints the live-session banner ("Say \"session
  start\"…") for a run that is about to exit immediately (a platform with no
  capture and no `-demo`), and its no-audio guidance only suggests
  re-granting the microphone permission on a platform that has one to grant
  — a platform with no capture support at all is pointed at an external
  recording instead.
- `record`'s session directory is removed when a recorder fails to start
  (ffmpeg missing, no usable device) and nothing was captured yet, instead
  of being left behind, empty and manifest-only, for every retry; a
  directory a recorder had already captured real, partial audio to before a
  later stream failed is kept.
- `record`'s detected audio-device roster is passed through the same
  invisible-Unicode/bidi-reordering sanitiser as the device-probe and
  recorder-stderr tails printed alongside it — a device name that reached
  the terminal unsanitised had gone unnoticed by this package's earlier
  hardening, and it is precisely the string the roster exists to make
  readable, so a crafted device name could hide its own entry from the
  operator it was meant to warn.

Checks and installer:

- The pipeline smoke test asserts the event half of the pipeline (the
  header counts); every prior assertion was satisfiable from the transcript
  and findings alone, so a regression that dropped all events kept the gate
  green.
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
- The pipeline smoke test also asserts a joined, indented event bullet in
  `report.md`, not only the header counts: the counts are raw entry
  counts computed before the event↔utterance join, so they stayed
  "10 · 10" even with every event detached from the speech it accompanied.
- `release.yml` now runs `gitleaks` and `zizmor` against the pushed tag
  commit, gating the release job on both: a tag can point at a commit that
  was never pushed through a branch, so it could previously publish
  without a fresh secret scan or workflow audit.
- CI and the release workflow now actually execute `install.sh --help` and
  its flag-error paths (`--dir`/`--version` without a value, an unknown
  flag), instead of only parsing the script's syntax.
- The release workflow runs `install.sh` end to end against the just-published
  release, into a throwaway directory, and asserts the installed binary
  reports the released tag: every prior check only parsed the script's syntax
  or its early-return flag paths, never the real fetch/verify/extract path
  (the SHA256SUMS lookup, the hash compare, the `gh attestation` branch, and
  the staged probe).
- An unauthenticated (or attestation-incapable) `gh` no longer refuses the
  install as a false provenance failure: it falls back to the verified
  checksum, exactly like no `gh`; a verification `gh` actually performed and
  rejected still refuses, and gh's own message is shown instead of being
  swallowed.
- `install.sh --dir`/`--version` refuse an explicitly-empty value, not only a
  missing one: `--dir ""` used to run the full download and hash
  verification before dying on a bare `mkdir` error naming no flag, and
  `--version ""` built a malformed release URL.
- An optional-dependency failure (an unreachable ffmpeg or uv host, a failed
  unpack or brew install) skips its step with guidance instead of aborting
  the whole installer with the child's raw exit code and a leaked temp
  directory; Ctrl+C stops the installer instead of being read as "skip";
  `--help` works through the documented pipe invocation; `--dir`/`--version`
  without a value are refused cleanly; the whisper.cpp model recipe
  downloads into a directory `-model NAME` actually searches.
- `install.sh`'s Linux local-ffmpeg branch now installs `ffprobe` alongside
  `ffmpeg` from the same tarball: `transcribe -audio`'s offset derivation
  shells out to `ffprobe` to read a recording's `creation_time` tag, and
  without it on PATH external-recording offset derivation fell back to 0,
  reported with the same provenance as a genuinely missing or unreadable tag
  (the macOS local-install branch still lacks `ffprobe`, a residual gap).
  The whisper.cpp model-download recipe, printed by `resolveModel` and
  mirrored in the how-to guide, now uses `curl -fL` instead of `curl -L`:
  without `-f`, an HTTP error response for a moved or withdrawn model asset
  used to be written into the destination `.bin` file at exit 0, a
  "successful" download whisper-cli only rejects later, confusingly, at
  load time.

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
- The bundled `examples/sample-session/interactions.jsonl` fixture's two
  tab-click `route` values are corrected to what the demo app's
  capture-phase listener actually records (the hash the click happened
  from, not its destination) — they had this backwards.
- The `.abcd/development/brief/` tree is brought back into line with the
  shipped v0.4.0 CLI: four files still described `record` as a stub and
  `analyze`/`review` as merely planned, contradicting README.md, AGENTS.md,
  and the CHANGELOG's own v0.2.0 entry; the demo surface page still
  described the pre-round-12 write guard (missing the remote-address
  check) and a uniform 8 MiB body cap where the actual split is 4/8 MiB;
  the schema page understated a finding's `t` upper bound and the session
  directory's file inventory.
- Four small doc/code mismatches corrected: `cli.md`'s unconditional
  `-audio` sidecar-write sentence, the demo endpoint docs' missing `500`
  status, `session-directory.md`'s sessionStart floor scoped to
  utterances rather than any entry, and `cli.go`'s usage text calling
  `review` "(TTY-gated)" — the exact framing `cli.md`'s own reference
  corrects.
- The `instrument-your-own-app` how-to's archival-capture snippet (step 4)
  is placed in the scope it depends on — it calls `post`, defined in step
  3's own script block, but was shown as a separately pasted one — and
  gains the same `rrweb`-availability guard the demo carries, so a blocked
  or absent CDN script cannot break it; the guide now notes that a bare
  `testimony demo` session is labelled with the built-in demo's own app
  and task, not the reader's, and names the `record -demo` alternative
  that honours custom metadata.
- `.abcd/development/brief/`'s platform and dependencies pages corrected
  against the shipped CLI (ffmpeg's avfoundation backend captures voice and
  screen alike, not QuickTime; ffmpeg is also `record`'s live capture
  engine, not only the offline converter to `audio.wav`); the verification
  page, `release.yml`'s header comment, and `internal/session`'s package
  doc comment now name every CI gate and session file they had dropped.
- A `-notes` flag named in the CHANGELOG and in an `internal/session` code
  comment never existed; both now name the real `-task`/`-app` flags (or a
  hand-edited `notes` field).
- `cli.md` and the merge surface brief page corrected against the code:
  `merge`'s zero-entries refusal fires when the two source files together
  yield nothing, not only when both are missing. The instrument-your-own-app
  how-to's 400-response list now names the `t` plausibility rules `cli.md`
  already documented; `ci.yml`'s header comment names the checks it actually
  runs; the verification brief names every release-only gate; and the
  getting-started tutorial states that both dependency prompts accept a
  second install word rather than skipping on anything but the recommended
  reply.
- The instrument-your-own-app how-to scopes `record -demo`'s `ffmpeg`
  prerequisite to macOS (the platform its recorders actually run on) instead
  of stating it unconditionally; `.abcd/development/brief/`'s record surface
  page now lists `SIGHUP` alongside `SIGINT`/`SIGTERM`, and no longer implies
  non-macOS `record -demo` exits cleanly — a served demo app blocks the
  command until interrupted on every platform. The purpose brief no longer
  states codebase mapping as shipped, present-tense; it is named a future
  goal, matching the analysis brief and delivery-phase pages it previously
  contradicted. The verification brief's release-only gate list now names
  the `install.sh` end-to-end smoke test added below.
- `itd-2-analysis-findings.md` moved from `intents/planned/` to
  `intents/shipped/` — `analyze`/`review` shipped in v0.2.0 and directory
  location is the intents' own single source of truth for lifecycle state,
  the same rule round 19 applied to its sibling `itd-1`. Its AC3 is narrowed
  to the request-level keyframe flag that actually shipped, matching
  `spc-2`'s own flagged divergence; extraction is out of scope pending
  maintainer confirmation.
- `itd-2-analysis-findings.md`'s press release no longer claims the shipped
  analysis pass chunks `timeline.jsonl` by task boundaries: `timeline.jsonl`
  carries no task markers, and the pass emits the whole timeline as one
  chunk, a divergence `spc-2` already flags. The out-of-scope list now names
  it, alongside the keyframe-extraction divergence already recorded there.
- `AGENTS.md`'s CI gate enumeration now names the version-stamp ldflags
  check `ci.yml` added in round 21 — the one gate with no local command
  that its "plus checks" list had dropped.
- `01-phases.md`'s Phase 2 status cell no longer claims the analysis layer
  shipped unqualified against a deliverable list that names chunking: v1
  emits the whole timeline as one chunk, the same divergence `itd-2` was
  corrected against above. Its sibling `02-verification.md`'s CI gate
  enumeration now also names the version-stamp ldflags check, as
  `AGENTS.md` already does.
- `02-scope.md`'s "Current status" section no longer claims Phase 2 shipped
  unqualified against its own deliverable list that names chunking — the
  one remaining sibling of `01-phases.md` and `04-analysis.md` still making
  that claim; it now carries the same flagged-divergence caveat they do.
- `itd-1-record-command.md`'s In Scope bullet no longer claims the launcher
  "starts screen and audio capture" by default, contradicting its own
  Out-of-Scope bullet that `-video` is what opts screen capture in; the
  bullet listing a stopped session's expected artefacts is qualified the
  same way.
- `02-dependencies.md`'s "Transcription never touches the network" is
  qualified with the same model-fetch exception `02-transcribe.md` and
  `docs/explanation/privacy.md` already carry — the one internal-brief page
  still making the unqualified claim.
- `spc-2-analysis-findings.md` carried two absolute network claims. The
  Summary's "No LLM and no network anywhere in the CLI" is removed outright
  — the correctly scoped form already appears earlier in the same
  paragraph, so it was a redundant restatement, not new information.
  The Decisions section's separate "the CLI never calls a model or the
  network" is reworded to the same scoped form used everywhere else in the
  repo. `record`'s live capture, the demo page's CDN-loaded rrweb recorder,
  and the ASR engine's model fetch all use the network, so both unscoped
  claims were false the moment they left `analyze`'s own boundary.
- `cli.md`, the instrument-your-own-app how-to, and the session-directory
  reference now name the 16 MiB total-size limit on `timeline.jsonl`,
  `transcript.jsonl`, `interactions.jsonl`, and `findings.jsonl`: the
  readers have enforced it since round 33 and the last writers since round
  36, but no user-facing doc named it, so a capture or ingest refused for
  reaching it had no documented cause or remedy.
- `install.sh`'s version-stamp comment no longer claims v0.1.0 is refused
  there — it was hand-built and hand-stamped before the release workflow
  existed, so it reports its own tag like every later release and passes
  that gate; it is refused earlier, correctly, at the preceding attestation
  check, for lacking a build attestation the workflow did not yet exist to
  create.
- `session-directory.md` and `cli.md` now name `manifest.json`'s own 1 MiB
  size limit, enforced by the reader since round 7 and the writer since
  round 14, but previously stated only in this file's own entry above.
- The bundled `transcript.jsonl` fixture's `.0`-suffixed floats (a form
  `transcribe` never writes, since `encoding/json` marshals `float64(16)`
  as `16`) are corrected to match, as the doc example for the same
  utterance already did since round 40.
- The internal `report` surface brief names the Findings section's third
  state — present but unreadable — that its two user-facing siblings
  already documented since round 40.

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
- `report` renders a `—` placeholder for a timeline event whose payload
  carries none of `kind`/`selector`/`text`/`value`/`route` (reachable from a
  hand-edited or exchanged `timeline.jsonl`), instead of a bullet with a
  timestamp and nothing after it.
- A hand-edited `findings.jsonl` verdict's `of` target is only rendered for a
  `duplicate` verdict, as documented, instead of for any verdict kind that
  happens to carry one: a `confirmed`/`rejected` verdict with a stray `of`
  used to render the nonsensical "confirmed of F-002".
- `install.sh` installs the current release: its version pin had been left at
  `v0.1.0`, handing new users a three-release-old binary, and the release
  workflow now gates the pin against the tag so it cannot go stale again. The
  installer also renders single-option prompts correctly and names the actual
  cause when a release download fails.
- `install.sh`'s PATH-fix advice routes on `$SHELL` instead of unconditionally
  suggesting `~/.zshrc` and `exec zsh`: on a bash-default Linux install (the
  installer supports Linux as well as macOS) the old advice appended to a
  file bash never reads and then tried to exec a shell that may not be
  installed.
- A stray positional argument is refused as a usage error; it used to be
  silently swallowed together with every flag after it, so the command ran
  with defaults at exit 0.
- The remaining invalid-flag-value paths exit 2: `review`'s
  `-finding`/`-verdict` pairing and verdict syntax, an unknown
  `transcribe -engine`, and a malformed capture `-addr` (which also no longer
  creates a session directory before refusing).
- `demo`/`record -demo`'s capture `-addr` refuses a numeric port outside
  0-65535 (e.g. `:99999`) as a usage error at exit 2, instead of letting
  `net.Listen` reject it during address resolution at exit 1 —
  indistinguishable from a genuine bind failure such as a taken port. A
  named service port (e.g. `:http`) is left to the runtime's own lookup,
  unaffected.
- `review -finding`'s value is validated against the `F-NNN` syntax at exit
  2, the one flag value in this family the previous pass left unchecked: a
  malformed id used to fail inside `review.Run` with `"finding ... not
  found"` at exit 1, indistinguishable from a well-formed id genuinely
  absent from the session.
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
- `transcribe -device` and `-vad` are validated against their documented
  enums, as `-engine` already was: an unrecognised value used to run the
  full offset resolution (and, on the `-audio` path, the audio conversion)
  before whisperx itself rejected the literal argument at the runtime
  status, not the exit 2 the CLI reference promises for an invalid flag
  value.
- `transcribe -audio`'s extension is validated at exit 2 before `detectEngine`
  runs, joining `-engine`/`-device`/`-vad`: an unsupported extension used to
  surface only after engine detection, either masked as "no ASR engine
  found" on a machine with none installed, or as the same error one
  step later, at exit 1 either way.
- `record -out`/`demo -out` refuse an empty value at exit 2, naming the flag:
  it used to reach `os.MkdirAll` unvalidated and surface as a bare
  `mkdir : no such file or directory` at exit 1, unlike every other
  validated flag on either command.
- An explicitly-empty `analyze -ingest`/`-out` refuses at exit 2 instead of
  silently falling through: an empty `-ingest` used to switch `analyze` from
  ingest mode to emit mode at exit 0 without validating any answer, and an
  empty `-out` used to redirect the emitted request to stdout at exit 0
  instead of writing a file.
- `review -finding F-NNN -verdict duplicate-of-F-NNN` (the same id on both
  sides) is refused at exit 2 alongside review's other flag-pairing checks,
  instead of failing inside `review.checkTargets` at exit 1 — where, on a
  session with no `findings.jsonl` yet, the contradiction was masked
  entirely behind "run analyze -ingest first".
- An explicitly-empty `transcribe -audio` refuses at exit 2, joining
  `analyze -ingest`/`-out`: it used to silently switch to the in-place
  branch, transcribing the session's own `audio.wav` instead of the
  external recording the caller named (with a session `audio.wav` present),
  or claiming `-audio` was never given at the wrong exit status (without
  one).
- An explicitly-empty `review -finding`/`-verdict` refuses at exit 2,
  joining `transcribe -audio`: it used to be indistinguishable from
  omitting both flags, silently no-oping on piped input or, on a
  character-device stdin, falling through to the interactive walk and
  appending a verdict for a finding the caller never named.
- `transcribe -engine`, `-device`, and `-vad` refuse an explicitly-empty
  value at exit 2, matching `-audio`: their own closed-enum validators
  treated `""` the same as the documented `auto`, so an unset shell
  variable spliced into the flag silently discarded the caller's choice at
  exit 0 instead of refusing it like every other closed-enum flag on the
  command. `-compute_type`'s documented set is open-ended, so it is
  unaffected.
- `review`'s interactive walk echoes the trimmed choice it actually matched
  against in its "unrecognised choice" message, instead of the raw,
  newline-terminated input line: typing `x` used to print `unrecognised
  choice "x\n"`, a value visibly different from the trimmed form the switch
  compared it to.

Capture integrity:

- `POST /api/interactions` refuses with 400 any record `merge` would refuse —
  a non-object body, or a missing/implausible `t` or missing `kind` — instead
  of persisting with 204 a line that later fails the whole session's merge.
- **Behaviour:** `POST /api/interactions` refuses with 413 a record that
  would push `interactions.jsonl` past its 16 MiB total-size limit, matching
  the cap `merge` already enforces on read. Each record was checked only
  against the per-line limit, so a session with enough individually valid
  captures — each answered `204` — could accumulate past the total the
  file's own reader refuses, discovered only once capture ended and `merge`
  could no longer read the session back. `events.rrweb.jsonl` is
  unaffected: it is archival and carries no total-size cap.
- **Behaviour:** `POST /api/interactions` also refuses with 413 a record
  whose own timeline entry, not `interactions.jsonl`'s raw bytes, would push
  the session's merged `timeline.jsonl` past that same 16 MiB limit. The
  guard above tracks `interactions.jsonl`'s own total; `merge` instead
  checks the larger, wrapped form each record becomes once written into
  `timeline.jsonl` (an envelope, plus an id that grows with the
  interaction's position), so a run of records that stayed under the raw
  cap could still cross the wrapped one first, bricking the session before
  the guard above ever fired.
- `POST /api/events` refuses a literal JSON `null` body with 400 instead of
  decoding it to a nil slice and answering 204: a JSON `null` unmarshals
  cleanly into an empty `[]json.RawMessage`, so it slipped past the "body is
  not a JSON array" check the endpoint otherwise applies uniformly.
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
- The demo app's `display-name`, `notify-toggle`, and `theme-toggle` rows
  carry `data-testid` on the row rather than the bare control: a click on
  the theme toggle's visible slider (its checkbox is styled invisible) used
  to fall back to a fragile `span.slider` class selector instead of a
  durable `[data-testid=...]` one, and any click or change captured on a
  bare-control `data-testid` element carried an empty label, since the
  control itself has no text content to read.
- **Behaviour:** `audio.offset.json`'s `offset_seconds` is required, matching
  the documented contract: a sidecar that is valid JSON but omits the key (or
  carries it as `null`) — hand-edited, partially rewritten, or produced by
  another tool — used to decode to a silent `0` and transcribe at exit 0 with
  an affirmative "persisted" provenance line, the exact silent-shift-to-0
  outcome the sidecar exists to prevent. It now refuses with the same
  guidance a malformed sidecar already gives.
- `record -demo` removes the session directory it just created when the demo
  server fails to start after `session.Create` succeeds (e.g. its stream
  files cannot be opened), mirroring the cleanup its sibling recorder-start
  failure path already had; it used to leave a manifest-only directory
  behind for every retry.
- whisper.cpp's `-model` is resolved alongside `-engine`, before the external
  audio conversion, instead of inside the whisper.cpp runner, after the
  conversion had already replaced `audio.wav`: a missing model — the
  ordinary first-run whisper.cpp failure, since it never auto-downloads one —
  used to destroy the record-origin capture for a refusal `-model` alone
  already made knowable in advance.
- **Behaviour:** `POST /api/interactions` and `transcribe` refuse a record
  whose *timeline entry* — not the record itself — would exceed the JSONL
  line limit, and `WriteJSONL`'s encoder no longer HTML-escapes `<`, `>`,
  and `&` into six-byte `\uXXXX` sequences. `merge` wraps an accepted record
  into a larger entry (a `src`/`id`/`payload` envelope, and — because of the
  escaping — up to sixfold inflation for any of those three characters), so
  a record within the raw line limit could still be durably persisted at
  204/exit 0 and then permanently unmergeable: `merge` refused to write its
  timeline entry on every re-run, leaving `report` and `analyze` with no
  timeline to read, with no CLI-level repair.
- `record -video` no longer misdiagnoses a second recorder that also exits
  on its own before being asked to stop: only the recorder `anyExit`'s
  select happened to observe was excluded from the missing-output sweep, so
  any other recorder that self-exited at the same moment was still routed
  through `classifyMissingOutput`'s "stayed blocked on the permission
  prompt" narrative — disproved by its own exit — with its real exit status
  never surfaced.
- **Behaviour:** `record`'s Ctrl+C shutdown path now diagnoses a recorder
  that exited on its own moments before the interrupt arrived, the sibling
  case to the one above for the interrupt path rather than the recorder-exit
  path: it used to fall through to the same missing-output sweep as a
  normally-stopped recorder, either printing `classifyMissingOutput`'s
  "stayed blocked on the permission prompt" narrative for a recorder that
  had already exited and captured nothing, or — for a recorder that left a
  partial artefact — exiting 0 with no word that capture had ended early,
  presenting a truncated recording as a clean session.

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
  `unverified`) into `findings.jsonl`. `analyze` holds no API keys and makes no
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
