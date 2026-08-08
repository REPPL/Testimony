---
id: itd-11
slug: terminal-cast-import
spec_id: null
kind: null
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

## Open Questions

- Command surface: a new verb, or a flag on an existing command? The `transcribe -audio` analogy suggests a peer command; the name should not imply it transcribes speech.
- Re-run and mixing semantics: a second import of the same session should be idempotent like a re-run of `transcribe`, but a session that also holds browser interactions (a `-demo` session) shares the interaction stream — does import append, refuse, or replace only records it previously wrote?
- Should `install.sh` mention asciinema with the guidance-only pattern (explain and print the install command, never run it), or is the how-to page alone the right home?
- Chunking granularity: a shell echoes typed commands back one keystroke at a time, so the raw `o` stream around a command is a run of one-character events interleaved with prompt redraws — one timeline entry per raw event would fragment a single typed command across dozens of near-empty records. Does the importer coalesce adjacent output events below an inter-event gap threshold into one record (the natural fix, tuned against a real session), and is the coalesced record still a faithful "what the terminal displayed" claim? This is the spec-level question the "commands appear via shell echo" guidance rests on.

## Audit Notes

_Empty. Populated by intent-fidelity-reviewer when intent moves to shipped/._
