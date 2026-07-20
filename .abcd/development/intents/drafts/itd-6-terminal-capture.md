---
id: itd-6
slug: terminal-capture
spec_id: null
kind: null
suggested_kind: null
reclassification_history: []
builds_on: []
severity: major
---

# Think Aloud While You Work at the Terminal

## Press Release

> **Testimony captures think-aloud sessions against a command-line tool.** A terminal session recorded with asciinema arrives as a `.cast` stream — a timestamped record of every command typed and every line of output — which `merge` normalises onto the same session clock as the narration, exactly as it does a browser's interaction events. A tester's spoken "I have no idea what that error is telling me" lands next to the command that produced it and the text it printed, and the anchor greps straight back to the source that emitted it.
>
> "Half of what we build is command-line, and it was the half I could never study properly," said Bob, application developer. "I'd watch someone stumble over a flag and then have nothing to show for it but a memory. Now the stumble, the command, and their exact words all sit on one line of the timeline."

## Why This Matters

The pipeline's evidence model assumes an interaction stream that can be joined to speech and then resolved to code. For browser targets rrweb supplies it; for a terminal, asciinema's `.cast` v2 format already *is* one — a timestamped event stream needing no new instrumentation in the tool under test. Without it, CLI sessions degrade to the keyframe fallback, which is the weakest and most expensive evidence channel, for the target type that would grep back to source most reliably of all.

This is also a dependency hole in the recorded plan. The codebase-mapping intent's acceptance criteria already assume a cast stream exists — "**Given** a finding from a CLI session… the anchor is resolved from the command or output text in the cast stream" — while `record`'s intent explicitly fences asciinema wrappers out of scope. This intent is the capability that criterion rests on, and Testimony being a CLI itself makes it the first tool available to study.

## What's In Scope

- Capturing a terminal session as an asciinema `.cast` v2 stream alongside the audio narration, anchored to the session `t0`.
- Normalising `.cast` records into `interactions.jsonl` so `merge` and `report` consume them unchanged — the timeline schema does not learn a new source type.
- An interaction `kind` vocabulary for the terminal: at minimum the command entered and the output emitted, carrying the text that later serves as the mapping anchor.
- Documenting the terminal target in the session-directory reference alongside the existing browser path.

## What's Out of Scope

- Instrumenting the tool under test — asciinema records the terminal, not the program, and that is the point.
- Resolving cast-stream anchors to source locations; that is the codebase-mapping step (itd-3), which this intent unblocks rather than performs.
- Native GUI application capture (itd-7) and third-party website capture (itd-4).
- Replaying a `.cast` stream as video; the stream is evidence and analysis input, not a playback surface.

## Acceptance Criteria

- **Given** a terminal session recorded with asciinema alongside narration, **when** `merge` runs, **then** `timeline.jsonl` interleaves the spoken utterances and the cast stream's commands and output on one session-relative clock.
- **Given** a merged terminal session, **when** `report` runs, **then** each utterance renders with the commands and output that fall inside its join window.
- **Given** a cast record and a spoken utterance at the same instant, **when** the timeline is built, **then** both carry session-relative times derived from the same `t0`, with no separate clock for the terminal stream.

## Open Questions

- Does asciinema's own clock need the spoken-marker correction, or is starting it from `record` sufficient to anchor it to `t0`?
- Should output be captured verbatim or truncated per record? Full output makes a better mapping anchor but a much larger `interactions.jsonl`, and a single command can emit more than the JSONL line limit.
- Does a TUI (as opposed to a line-oriented CLI) produce a usable cast stream, or does redraw traffic swamp the signal?

## Audit Notes

_Empty. Populated by intent-fidelity-reviewer when intent moves to shipped/._
