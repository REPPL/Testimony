---
id: itd-11
slug: terminal-cast-import
spec_id: spc-2609120417486971
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: []
severity: major
---

# A Terminal Recording Arrives the Way a Voice Recording Already Does

## Press Release

> **Testimony turns an operator-recorded terminal session into timeline evidence.** The operator records their shell themselves — `asciinema rec session.cast` in the terminal where they work, ended the way asciinema always ends, while `testimony record` captures narration in another window exactly as it does today. Afterwards they hand the `.cast` file to an import step, the same hand-off `transcribe -audio` already performs for an external voice recording: the cast's own header timestamp anchors every command and line of output to the session clock, the records land in the session's ordinary interaction stream, and `merge`, `report`, `analyze`, and `review` run byte-identical. A spoken "I have no idea what that error is telling me" lands next to the command that produced it and the text it printed.
>
> "I already start my own voice recorder when I want one — Testimony just tells me where to hand the file," said Alice, the maintainer. "Recording my own terminal the same way means nothing about how my shell or my session ends had to change. One extra command afterwards, and the stumble, the command, and my exact words sit on one line of the timeline."

## Why This Matters

The evidence need is the one itd-6 identified, unchanged: the pipeline's model wants a structured, timestamped, text-searchable interaction stream for terminal targets, the codebase-mapping intent's acceptance criteria (itd-3) already assume a cast stream exists, and asciinema's asciicast formats already *are* that stream — no instrumentation of the tool under test. Without it, CLI sessions degrade to the weakest and most expensive evidence channel.

There are now two asciicast formats in the wild, and the import must accept both. asciinema 3.0 (September 2025, a Rust rewrite) made asciicast **v3** the default output format: its event times are *intervals since the previous event*, where v2's are *absolute seconds since recording start* — a file-corrupting difference if a v3 cast is read with v2 semantics, since every event after the first drifts steadily earlier than reality. Homebrew — the recommended install on macOS, the target platform — ships the 3.x line (3.2.1 at the time of writing), so a fresh `brew install asciinema` produces v3 casts by default; meanwhile PyPI's latest release is still 2.4.0 (October 2023) and Debian packages the same, so v2-only recorders remain widespread indefinitely. The 3.x CLI can be told to write v2 (`--output-format asciicast-v2`), but the 2.x CLI rejects that flag outright, so no single recommended invocation works across both lines. The decoupled delivery shape absorbs this cleanly: the operator records with whatever asciinema they have, and the import step sniffs the header's `version` field and anchors each format by its own semantics — the churn is a parsing concern solved once, in one function, rather than a binary-version matrix inside `record`.

What this intent changes is the delivery shape. The wrap-the-shell design (spc-3) pulled the whole recorder lifecycle inside `record`: a pty hijack of the operator's interactive shell, a conditional end-of-session gesture — Ctrl-D on one flag, Ctrl+C otherwise — signal-handling reasoning about Ctrl+C mid-command, an acknowledged SIGTERM/SIGHUP forwarding gap, and a runtime binary dependency, all landing in the one command that must never lose a session. Yet the pipeline already has a normalised pattern for external capture it never manages: `demo`'s printed instructions tell the operator to start QuickTime themselves and hand the file to `transcribe -audio` afterwards, and `record`/`demo` never touch that recorder's lifecycle at all. Applying the same pattern to the terminal removes every item on that list — `record` gains no flag, no wrapped process, no second way to end.

The trade is honest but favourable. The cost is per-session ceremony: the operator starts and stops one more recorder and runs one more command, the same ceremony the external-audio path already asks of them. The wrap-the-shell design demanded operator ceremony too — knowing that one flag silently changes how a session ends — and paid for it again in standing failure modes. The decoupled hand-off is anchored from data inside the artefact rather than from file creation time: an asciicast header carries an absolute Unix timestamp in both v2 and v3. That anchor is honest to a bound, not exact — the header field is an *integer*, whole seconds, in both formats, so the reconstructed clock can sit up to a second adrift of `t0`'s millisecond precision, which is material against `report`'s 2.5-second default join window. The spoken start marker therefore stays recommended for the terminal path as the calibration cross-check, and the explicit `-offset` override stays for correcting a skewed or absent header.

Alternatives considered and set aside: plain `script(1)` is universally available where asciinema is not, but its timing capture splits across two files, carries no absolute timestamp to anchor against `t0`, and diverges between the BSD/macOS and util-linux implementations — buying availability at the cost of exactly the anchoring this evidence needs. Shell history with timestamps records commands but never output, which is half the evidence. A screen recording of the terminal (`record -video`, which works today) is not text-searchable and cannot serve as a mapping anchor. A bespoke pty wrapper would trade a format-parsing concern for owning pty allocation, raw-mode handling, and resize plumbing across platforms — far more surface than a dual-format parser. Asciicast, produced by a recorder the operator runs themselves, keeps the format's strengths without inheriting its process-management costs.

## What's In Scope

- An import step in `transcribe -audio`'s mould: given a session directory and an asciicast file, normalise the cast's output events into the session's interaction stream on the shared clock, and keep the raw `.cast` in the session directory as an archival artefact alongside `events.rrweb.jsonl`.
- Accepting **both asciicast v2 and v3**, distinguished by the header's `version` field: v2 event times are absolute seconds since recording start; v3 event times are intervals since the previous event, reconstructed by a running sum. A cast declaring any other version is refused with a message naming the file and the version found — never guessed at.
- Clock anchoring from the cast header's absolute timestamp (an optional integer in both formats), an explicit `-offset` override, and a printed offset-provenance line matching `transcribe`'s existing pattern — including the whole-second quantisation caveat in the printed line, so the operator knows the anchor's honest precision.
- Output events larger than a JSONL line split across consecutive records at safe boundaries, never truncated — evidence is not silently dropped.
- Output-only capture guidance: the recommended invocation is plain `asciinema rec session.cast` on either CLI line, which records what the terminal displays, never raw keystrokes — input capture is opt-in on both lines (`--stdin` on 2.x; `--capture-input`/`-I`, with `--stdin` kept as an alias, on 3.x) and the guidance says to opt out, so a password typed at a suppressed-echo prompt cannot land in the evidence. Any `i` (input) events present in a handed-over cast are dropped at import, never normalised into the interaction stream.
- Documenting the terminal path: the archival cast in the session-directory reference, a how-to for the two-terminal session (`record` in one, `asciinema rec` in the one where the work happens), and guidance-only install pointers for asciinema as the suggested recorder.
- A privacy warning in the terminal how-to: terminal output routinely carries usernames, hostnames, absolute paths, environment values, and occasionally secrets printed by tools, and `analyze` sends timeline text to a model — the how-to tells the operator to review or redact the session before running `analyze`, in exactly the way the browser path never had to, because a shell shows more of the machine than a demo app does.
- `merge`, `report`, `analyze`, and `review` unchanged — the timeline schema learns no new source type.

## What's Out of Scope

- `record` wrapping, spawning, or supervising any terminal recorder — no `-terminal` flag, no pty, no change to how a session ends. This is the fence that distinguishes this intent from itd-6's spec.
- Keystroke capture (`--stdin` on the 2.x line, `--capture-input`/`-I` on 3.x); the suppressed-echo password hazard identified in spc-3 carries forward unchanged.
- Accepting formats other than asciicast v2 and v3 — asciicast v1, `script(1)` timing pairs, shell history — noted above as considered and set aside.
- Forcing or converting between cast format versions at record time (`--output-format`); the operator records with whatever their asciinema writes, and the importer meets the file where it is.
- Resolving cast-stream anchors to source locations; that is the codebase-mapping step (itd-3), which this intent unblocks rather than performs.
- TUI redraw handling beyond preserving the raw cast; line-oriented CLI sessions are the target.
- The installer behaviour change to `whisper.cpp` that spc-3 bundled — unrelated scope, deliberately not carried into this intent.
- Replaying a `.cast` as video; the stream is evidence and analysis input, not a playback surface.

## Acceptance Criteria

- **Given** a narrated session and an asciicast v2 file recorded alongside it, **when** the import step and then `merge` run, **then** `timeline.jsonl` interleaves the spoken utterances and the cast's commands and output on one session-relative clock derived from the same `t0`, with no separate clock for the terminal stream.
- **Given** the same session recorded as asciicast v3 (a stock `brew install asciinema` recorder), **when** the import step runs, **then** each event's absolute time is reconstructed by summing intervals from the recording start, and the resulting timeline is identical to what the equivalent v2 cast produces — the operator never states, or needs to know, which format their recorder wrote.
- **Given** a cast whose header declares a version other than 2 or 3, **when** the import step runs, **then** it refuses with a message naming the file and the version found, and writes nothing — never a silently misread clock.
- **Given** an imported terminal session, **when** `report` runs, **then** each utterance renders with the commands and output that fall inside its join window, through the existing event rendering unchanged.
- **Given** the import step has run, **when** `merge`, `report`, or `analyze` execute, **then** they behave identically to today for the same inputs — no new source type, flag, or schema field is required of them.
- **Given** a cast whose header timestamp precedes the session's `t0` (the recorder was started early), **when** the timeline is built, **then** its early events carry negative session-relative times and render exactly as an early-started audio recording's utterances already do.
- **Given** a single output event larger than the readable JSONL line limit, **when** it is imported, **then** it is split across multiple records within the limit with every byte preserved, and no record is truncated.
- **Given** a `record` session with a terminal recording underway in another window, **when** the operator presses Ctrl+C in `record`'s terminal, **then** the session finalises exactly as an audio-only session does — nothing about terminal capture appears in `record`'s lifecycle.

## Scope Conditions

- **The operator's asciinema writes asciicast v2 or v3**, and the file reaches the import step as that recorder wrote it. Any other format — asciicast v1, a `script(1)` timing pair, a converted or hand-edited cast declaring another version — is refused by name, so the claim covers the two formats in the wild and nothing else. <!-- cond: cond-2609120432406223 -->
- **The cast header carries an integer `timestamp`, or the operator states the anchor with `-offset`.** Both formats make the field optional. With neither, the terminal stream is placed at offset 0 — the assumption that the recorder started at `t0` — and the spoken start marker is the only cross-check the operator has. <!-- cond: cond-2609120432405024 -->
- **The anchor is honest to a whole second, not to a millisecond.** The header field is an integer in both formats, so the reconstructed clock can sit up to a second adrift of `t0`, against a 2.5-second default join window. <!-- cond: cond-2609120432402714 -->
- **The session directory holds a `manifest.json` with a positive `t0_epoch_ms`.** Interaction times are epoch milliseconds anchored against it; a session without a usable `t0` cannot place any interaction on the session clock, terminal or otherwise. <!-- cond: cond-2609120432402423 -->
- **The narration and the terminal recording belong to one wall-clock session**, so both streams resolve against the same `t0`. Two recordings made at different times do not interleave by being imported into one directory. <!-- cond: cond-2609120432402550 -->
- **The recorded work is line-oriented shell output.** A full-screen TUI, a pager, or a progress bar redrawing over itself keeps its evidence in redraw sequences: they are preserved verbatim, and are not reconstructed into what the screen finally displayed. <!-- cond: cond-2609120432404128 -->
- **One terminal recording per session.** Records from a second, different cast replace the first's, because both are identified as this importer's own. <!-- cond: cond-2609120432407170 -->
- **Input capture is left off at record time**, the recommended invocation on both CLI lines. The importer drops `i` events regardless, but only recording without input capture keeps keystrokes out of the artefact itself. <!-- cond: cond-2609120432408324 -->
- **The operator reviews or redacts the derived timeline before `analyze` runs.** Terminal output routinely carries usernames, hostnames, absolute paths, environment values, and occasionally secrets printed by tools — more of the machine than a demo app ever shows. <!-- cond: cond-2609120432400428 -->

## Open Questions

- Command surface: a new verb, or a flag on an existing command? The `transcribe -audio` analogy suggests a peer command; the name should not imply it transcribes speech.
- Re-run and mixing semantics: a second import of the same session should be idempotent like a re-run of `transcribe`, but a session that also holds browser interactions (a `-demo` session) shares the interaction stream — does import append, refuse, or replace only records it previously wrote?
- Should `install.sh` mention asciinema with the guidance-only pattern (explain and print the install command, never run it), or is the how-to page alone the right home?
- Chunking granularity: a shell echoes typed commands back one keystroke at a time, so the raw `o` stream around a command is a run of one-character events interleaved with prompt redraws — one timeline entry per raw event would fragment a single typed command across dozens of near-empty records. Does the importer coalesce adjacent output events below an inter-event gap threshold into one record (the natural fix, tuned against a real session), and is the coalesced record still a faithful "what the terminal displayed" claim? This is the spec-level question the "commands appear via shell echo" guidance rests on.

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-fcf0bc47445c -->
Fidelity review — receipt rcp-fcf0bc47445c (verifier abcd:intent-auditor claude-opus-5[1m]).

Provenance: abcd:intent-auditor@claude-opus-5[1m] · rubric_hash sha256:43133dfce85f90e6462ffcc449daa41b27c8f4fcc1975710222a5703d3d57946 · prompt_hash sha256:17b9a757f3fcc8c565c184165fadbdb48810c4c4869ddde98b9a683694b87fdb
Input attestations: diff:origin/main..working tree (4e81f718ac5db206ebc4df960a18044331d0396c..4c3f8433e7274010ee305a4b6571e1a18df90236 plus the uncommitted working tree)@sha256:e84082ba9ba36bd43bc91d39d3a02f344c807466ab1c1a3ff049c896c1745c78; review-request:.abcd/.work.local/reviews/rcp-fcf0bc47445c.request.md@sha256:43133dfce85f90e6462ffcc449daa41b27c8f4fcc1975710222a5703d3d57946; intent:.abcd/development/intents/shipped/itd-11-terminal-cast-import.md@sha256:17b9a757f3fcc8c565c184165fadbdb48810c4c4869ddde98b9a683694b87fdb; note:.abcd/.work.local/reviews/rcp-fcf0bc47445c.request.md:1@-; gates:go.mod:1@-;

Acceptance rollup: MET 7 · MET_WITH_CONCERNS 1 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: A v2 cast plus a two-utterance transcript merges into one session-relative clock: TestImportThenMergeInterleaves asserts the exact interleaved entry order and times (-2 event, -1.5 speech, -1.48 event, …) and the records carry only epoch-ms t derived from the same manifest t0 through recordTime, with no per-source offset anywhere in timeline.jsonl.
  evidence: internal/cast/cast_test.go:962
  evidence: internal/cast/coalesce.go:189
  evidence: internal/cast/cast.go:274
- ac-2 — MET: The v2/v3 difference is confined to one accumulator in parseEvent (v3 adds each interval to a running float64 sum), and TestV2AndV3Agree asserts the two fixtures describing the same recording produce a byte-identical interactions.jsonl, while TestImportThenMergeInterleaves runs the identical golden entry table over both fixtures; the operator states nothing about the format, since -cast carries no format flag.
  evidence: internal/cast/scan.go:233
  evidence: internal/cast/cast_test.go:210
  evidence: internal/cast/testdata/v3.cast:1
- ac-3 — MET: parseHeader refuses any version other than 2 or 3 naming both the file and the version as written, the refusal fires in scan step 3 before any of Run's write steps, and TestUnsupportedVersionRefuses (versions 1, "2", absent) plus TestVersion4FixtureRefuses each assert the message names the file and that assertSessionUnchanged holds a full-directory hash snapshot.
  evidence: internal/cast/scan.go:186
  evidence: internal/cast/cast_test.go:255
  evidence: internal/cast/cast_test.go:246
- ac-4 — MET: TestReportRendersTerminalEvents imports, merges and renders, asserting the utterance line at [-00:02] is accompanied by `- [-00:02] terminal\_output` and `[00:00] terminal\_output` bullets carrying the command text, and internal/report has an empty diff against origin/main, so the rendering path is the existing eventLine unchanged.
  evidence: internal/cast/cast_test.go:993
  evidence: internal/report/report.go:1
  evidence: .github/workflows/ci.yml:196
- ac-5 — MET: internal/timeline, internal/report, internal/analyze and internal/review are all absent from the delivered diffstat, the records carry only the already-documented t/kind/text fields (no new src value, no new flag), and every record is put through the exported timeline.CheckInteraction before it is written so import cannot persist anything merge would refuse.
  evidence: internal/cast/cast.go:288
  evidence: internal/cast/testdata/golden.interactions.jsonl:2
  evidence: internal/timeline/timeline.go:1
- ac-6 — MET: TestEarlyStartedCastGoesNegative sets the header timestamp 30 s before t0 and asserts merged entry times of -30 and 0 plus `[-00:30]` in report.Render's output; the record's t stays a positive epoch-ms value (t0 + offsetMS + ms) so timeline.CheckInteraction's positivity rule is satisfied and report.clock signs it exactly as it signs an early-started audio utterance.
  evidence: internal/cast/cast_test.go:1035
  evidence: internal/cast/cast_test.go:1041
  evidence: .github/workflows/ci.yml:197
- ac-7 — MET_WITH_CONCERNS: A 6 MiB output event does split: coalescer.add closes the record when the next rune would breach a per-record encoded budget and TestOversizedEventSplits asserts more than one record, every wrapped timeline entry inside session.MaxJSONLLine, a successful Merge, and that concatenating the records' text reproduces the event's data exactly (utf8.ValidString per record in the coalescer unit) — but the concern is that 'every byte preserved' is honoured at rune granularity over the string encoding/json decoded, not over the cast's raw bytes: invalid UTF-8 becomes U+FFFD in the decoder before the importer sees it, and the byte-exact artefact is the archived terminal.cast (TestCastArchivedVerbatim). The spec records this as the one place it deliberately narrows a criterion.
  evidence: internal/cast/coalesce.go:94
  evidence: internal/cast/cast_test.go:816
  evidence: internal/cast/coalesce_test.go:210
  evidence: .abcd/development/specs/closed/spc-2609120417486971-terminal-cast-import.md:841
- ac-8 — MET: internal/record is absent from the delivered diffstat entirely — no flag, no recorder, no subprocess, no signal path touched — so Ctrl+C finalises a session exactly as before, and TestUsageListsImport asserts the usage text offers no -terminal flag anywhere while naming the new hand-off verb.
  evidence: internal/cli/cli_test.go:895
  evidence: internal/record/record.go:1
  evidence: internal/cli/cli.go:36

Gap audit:
- honoured:
  - An import step in transcribe -audio's mould: a session directory plus an asciicast file, normalised into the session's interaction stream, with the raw .cast kept as an archival artefact alongside events.rrweb.jsonl
    evidence: internal/cast/cast.go:61
    evidence: internal/session/session.go:55
    evidence: docs/reference/session-directory.md:103
  - Both asciicast v2 and v3 accepted and distinguished by the header's version field; any other version refused by name
    evidence: internal/cast/scan.go:221
    evidence: internal/cast/scan.go:186
  - Clock anchoring from the header timestamp, an explicit -offset override, and a printed offset-provenance line carrying the whole-second quantisation caveat
    evidence: internal/cast/cast.go:137
    evidence: internal/cli/cli_test.go:797
  - Output events larger than a JSONL line split across consecutive records at safe boundaries, never truncated
    evidence: internal/cast/coalesce.go:178
    evidence: internal/cast/cast_test.go:800
  - Input (i) events dropped at import and never normalised into the interaction stream, with the drop counted and printed
    evidence: internal/cast/cast.go:122
    evidence: internal/cast/cast.go:319
    evidence: .github/workflows/ci.yml:187
  - Documenting the terminal path: the archival cast in the session-directory reference, a two-terminal how-to, and guidance-only asciinema install pointers
    evidence: docs/how-to/record-a-terminal-session.md:10
    evidence: docs/how-to/record-a-terminal-session.md:30
    evidence: docs/reference/session-directory.md:12
  - A privacy warning in the terminal how-to: review or redact the session before running analyze, and record with input capture off
    evidence: docs/how-to/record-a-terminal-session.md:81
    evidence: docs/how-to/record-a-terminal-session.md:65
  - merge, report, analyze and review unchanged — the timeline schema learns no new source type
    evidence: internal/cast/testdata/golden.interactions.jsonl:1
    evidence: internal/timeline/timeline.go:1
  - record wraps, spawns or supervises no terminal recorder — no -terminal flag, no pty, no change to how a session ends (the intent's fence)
    evidence: internal/record/record.go:1
    evidence: internal/cli/cli_test.go:895
  - 'One extra command afterwards' — the hand-off is a single peer verb the operator runs after the fact, with an idempotent re-run
    evidence: internal/cli/cli.go:317
    evidence: internal/cli/cli_test.go:841
    evidence: .github/workflows/ci.yml:190
- diverged:
  - 'Every byte preserved' on an oversized split: delivered as rune-exact preservation of the string encoding/json decoded, with byte-exactness held by the archived terminal.cast instead
    evidence: internal/cast/cast_test.go:816
    evidence: internal/cast/cast_test.go:653
    evidence: .abcd/development/specs/closed/spc-2609120417486971-terminal-cast-import.md:836
  - The unsupported-version refusal was specified as `asciicast version %d`; delivered as the JSON literal (`version "2"`), neutralised and clipped, so a non-integer version is named as written rather than as a decode failure
    evidence: internal/cast/scan.go:186
    evidence: .abcd/development/specs/closed/spc-2609120417486971-terminal-cast-import.md:239
  - A header `timestamp` field present but not an integer is refused with -offset guidance rather than treated as the absent case the intent's scope condition describes
    evidence: internal/cast/scan.go:192
- missing:
  - The live two-terminal validation the spec names as part of done for the implementing change — a real record + asciinema session on both the 2.x and 3.x CLI lines, against which the 250 ms gap and 1 s span constants were to be tuned — leaves no artefact in the delivered change (no note in DECISIONS.md, CONTEXT.md, or the CI smoke, which is hermetic and runs no recorder)
    evidence: .abcd/development/specs/closed/spc-2609120417486971-terminal-cast-import.md:1036
    evidence: .abcd/work/DECISIONS.md:1620

Scope-condition dispositions:
- cond-2609120432406223 — survived: The importer accepts exactly v2 and v3 as the header declares and refuses every other version by name before anything is written, so the claim covers the two formats in the wild and nothing else.
  evidence: internal/cast/scan.go:180
  evidence: internal/cast/testdata/bad-version.cast:1
- cond-2609120432405024 — narrowed: An absent (or null) header timestamp does default to offset 0 with the provenance printed, and -offset always wins — but a timestamp that is present and unusable is refused rather than defaulted, so the offset-0 fallback covers less ground than the condition states.
  narrowing: The 'placed at offset 0' fallback holds only when the header timestamp is absent or JSON null; a present but non-positive timestamp (internal/cast/cast.go:263) or a present non-integer one (internal/cast/scan.go:192) refuses the run with -offset guidance instead of assuming the recorder started at t0.
  evidence: internal/cast/cast.go:255
  evidence: internal/cast/cast.go:263
  evidence: internal/cast/testdata/v3-nots.cast:1
- cond-2609120432402714 — survived: The derived anchor is integer arithmetic over a whole-second header field, and the ±1 s bound is printed to the operator on every derived run rather than only documented.
  evidence: internal/cast/cast.go:274
  evidence: internal/cli/cli_test.go:797
- cond-2609120432402423 — survived: Run resolves t0 through session.Manifest.T0 before any other work and refuses an absent or negative anchor on every path including explicit -offset, with three table cases asserting the session is left byte-identical.
  evidence: internal/cast/cast.go:86
  evidence: internal/cast/cast_test.go:307
- cond-2609120432402550 — survived: Both streams are placed against the one manifest t0 with no per-source clock, which the interleaving test demonstrates end to end; nothing in the delivery contradicts the one-wall-clock-session assumption, and nothing tries to reconcile two unrelated recordings.
  evidence: internal/cast/cast_test.go:940
  evidence: internal/cast/coalesce.go:189
- cond-2609120432404128 — survived: Carriage returns are kept and are explicitly not a record boundary, so a progress line's redraw frames are preserved as they arrived rather than reconstructed into the final rendering — visible verbatim in the golden record.
  evidence: internal/cast/coalesce.go:107
  evidence: internal/cast/testdata/golden.interactions.jsonl:4
  evidence: docs/reference/session-directory.md:67
- cond-2609120432407170 — survived: A re-import drops every line whose kind is the reserved terminal_output and keeps every other line byte-for-byte, so a second, different cast's records replace the first's — asserted by the replaced count and the byte-identical re-import, and stated in both the reference and the how-to.
  evidence: internal/cast/write.go:129
  evidence: internal/cli/cli_test.go:841
  evidence: docs/how-to/record-a-terminal-session.md:117
- cond-2609120432408324 — survived: The how-to tells the operator to record output only and names both opt-in input flags with the suppressed-echo hazard, while the importer drops i events unconditionally and prints the count so an operator learns their recorder captured keystrokes.
  evidence: docs/how-to/record-a-terminal-session.md:65
  evidence: internal/cast/cast_test.go:749
  evidence: docs/reference/session-directory.md:107
- cond-2609120432400428 — untested: Whether an operator actually reviews or redacts the timeline before analyze is an assumption about human behaviour that the delivery neither exercises nor contradicts; it restates the instruction in docs/how-to/record-a-terminal-session.md:85 and docs/explanation/privacy.md, and adds no code-level check, so nothing in the delivered reality tests it.
## Grounds

- pursued: an operator-recorded asciicast (v2 or v3), anchored by its header timestamp to the session t0 and coalesced per displayed line, gives merge and report a terminal interaction stream good enough to sit a spoken stumble beside the command and output that caused it, with no change to record; what would show it wrong is real sessions where the whole-second header anchor or the line coalescing leaves output misaligned with speech beyond the join window
