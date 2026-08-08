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

The evidence need is the one itd-6 identified, unchanged: the pipeline's model wants a structured, timestamped, text-searchable interaction stream for terminal targets, the codebase-mapping intent's acceptance criteria (itd-3) already assume a cast stream exists, and asciinema's asciicast v2 format already *is* that stream — no instrumentation of the tool under test. Without it, CLI sessions degrade to the weakest and most expensive evidence channel.

What this intent changes is the delivery shape. The wrap-the-shell design (spc-3) pulled the whole recorder lifecycle inside `record`: a pty hijack of the operator's interactive shell, a conditional end-of-session gesture — Ctrl-D on one flag, Ctrl+C otherwise — signal-handling reasoning about Ctrl+C mid-command, an acknowledged SIGTERM/SIGHUP forwarding gap, and a runtime binary dependency, all landing in the one command that must never lose a session. Yet the pipeline already has a normalised pattern for external capture it never manages: `demo`'s printed instructions tell the operator to start QuickTime themselves and hand the file to `transcribe -audio` afterwards, and `record`/`demo` never touch that recorder's lifecycle at all. Applying the same pattern to the terminal removes every item on that list — `record` gains no flag, no wrapped process, no second way to end.

The trade is honest but favourable. The cost is per-session ceremony: the operator starts and stops one more recorder and runs one more command, the same ceremony the external-audio path already asks of them. The wrap-the-shell design demanded operator ceremony too — knowing that one flag silently changes how a session ends — and paid for it again in standing failure modes. And the decoupled hand-off is *better* anchored than its audio precedent: an asciicast header carries an absolute Unix timestamp, so the offset derives exactly from data inside the artefact rather than from file creation time, with the explicit `-offset` override kept for the rare cast that lacks one.

Alternatives considered and set aside: plain `script(1)` is universally available where asciinema is not, but its timing capture splits across two files, carries no absolute timestamp to anchor against `t0`, and diverges between the BSD/macOS and util-linux implementations — buying availability at the cost of exactly the anchoring this evidence needs. Shell history with timestamps records commands but never output, which is half the evidence. A screen recording of the terminal (`record -video`, which works today) is not text-searchable and cannot serve as a mapping anchor. Asciicast v2, produced by a recorder the operator runs themselves, keeps the format's strengths without inheriting its process-management costs.

## What's In Scope

- An import step in `transcribe -audio`'s mould: given a session directory and an asciicast v2 file, normalise the cast's output events into the session's interaction stream on the shared clock, and keep the raw `.cast` in the session directory as an archival artefact alongside `events.rrweb.jsonl`.
- Clock anchoring from the cast header's absolute timestamp, an explicit `-offset` override, and a printed offset-provenance line matching `transcribe`'s existing pattern.
- Output events larger than a JSONL line split across consecutive records at safe boundaries, never truncated — evidence is not silently dropped.
- Output-only capture guidance: the recommended `asciinema rec` invocation records what the terminal displays (commands appear via shell echo), never raw keystrokes, so a password typed at a suppressed-echo prompt cannot land in the evidence.
- Documenting the terminal path: the archival cast in the session-directory reference, a how-to for the two-terminal session (`record` in one, `asciinema rec` in the one where the work happens), and guidance-only install pointers for asciinema as the suggested recorder.
- `merge`, `report`, `analyze`, and `review` unchanged — the timeline schema learns no new source type.

## What's Out of Scope

- `record` wrapping, spawning, or supervising any terminal recorder — no `-terminal` flag, no pty, no change to how a session ends. This is the fence that distinguishes this intent from itd-6's spec.
- Keystroke (`--stdin`) capture; the suppressed-echo password hazard identified in spc-3 carries forward unchanged.
- Accepting formats other than asciicast v2 — `script(1)` timing pairs, shell history — noted above as considered and set aside.
- Resolving cast-stream anchors to source locations; that is the codebase-mapping step (itd-3), which this intent unblocks rather than performs.
- TUI redraw handling beyond preserving the raw cast; line-oriented CLI sessions are the target.
- The installer behaviour change to `whisper.cpp` that spc-3 bundled — unrelated scope, deliberately not carried into this intent.
- Replaying a `.cast` as video; the stream is evidence and analysis input, not a playback surface.

## Acceptance Criteria

- **Given** a narrated session and an asciicast v2 file recorded alongside it, **when** the import step and then `merge` run, **then** `timeline.jsonl` interleaves the spoken utterances and the cast's commands and output on one session-relative clock derived from the same `t0`, with no separate clock for the terminal stream.
- **Given** an imported terminal session, **when** `report` runs, **then** each utterance renders with the commands and output that fall inside its join window, through the existing event rendering unchanged.
- **Given** the import step has run, **when** `merge`, `report`, or `analyze` execute, **then** they behave identically to today for the same inputs — no new source type, flag, or schema field is required of them.
- **Given** a cast whose header timestamp precedes the session's `t0` (the recorder was started early), **when** the timeline is built, **then** its early events carry negative session-relative times and render exactly as an early-started audio recording's utterances already do.
- **Given** a single output event larger than the readable JSONL line limit, **when** it is imported, **then** it is split across multiple records within the limit with every byte preserved, and no record is truncated.
- **Given** a `record` session with a terminal recording underway in another window, **when** the operator presses Ctrl+C in `record`'s terminal, **then** the session finalises exactly as an audio-only session does — nothing about terminal capture appears in `record`'s lifecycle.

## Open Questions

- Command surface: a new verb, or a flag on an existing command? The `transcribe -audio` analogy suggests a peer command; the name should not imply it transcribes speech.
- Re-run and mixing semantics: a second import of the same session should be idempotent like a re-run of `transcribe`, but a session that also holds browser interactions (a `-demo` session) shares the interaction stream — does import append, refuse, or replace only records it previously wrote?
- Should `install.sh` mention asciinema with the guidance-only pattern (explain and print the install command, never run it), or is the how-to page alone the right home?
- Does a spoken start marker remain worth recommending for the terminal path, or does the cast header's absolute timestamp make the cross-check redundant?

## Audit Notes

_Empty. Populated by intent-fidelity-reviewer when intent moves to shipped/._
