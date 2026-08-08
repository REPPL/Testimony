---
id: spc-3
slug: terminal-capture
intent: itd-6
---
# terminal-capture

## Summary

`testimony record -terminal` adds a third recorder alongside the existing
microphone (and opt-in screen) capture: an `asciinema` session that records
every line the operator's shell displays — commands and their output — into
a new archival session artefact, `terminal.cast` (asciicast v2). Unlike the
ffmpeg recorders, `asciinema rec` does not run quietly in the background; it
wraps the operator's own shell in a pty, so `-terminal` changes what `record`
waits on to end the session from a signal (Ctrl+C) to the wrapped shell
exiting (Ctrl-D / `exit`) — the operator works at the terminal exactly as
normal, inside the recording, and ending that shell is what stops the
session. `merge` reads `terminal.cast` as a third, optional input alongside
`transcript.jsonl` and `interactions.jsonl`, normalising each output chunk
into a `kind: "terminal"` timeline entry on the same session clock, so a
spoken "I have no idea what that error means" lands next to the command and
output that were on screen at that moment — with no new source type learned
by `timeline.jsonl` or `report`, and no change to either's code.

v1 deliberately captures **output only**, never keystrokes: `asciinema rec`
without `--stdin` records what the terminal *displays* (commands are visible
because the shell echoes them), not what is *typed*. `--stdin` capture is
explicitly out of scope — it would record raw keystrokes regardless of local
echo state, so a password typed at a suppressed-echo prompt (`sudo`, `ssh`)
would land in the evidence file verbatim. Splitting "command entered" from
"output emitted" — the intent's stated vocabulary — is deferred until a
value proposition for `--stdin` justifies that risk; v1 ships a single
generic interaction `kind` carrying the raw displayed text, which already
satisfies the intent's actual acceptance criteria (interleaved on one clock,
rendered in the join window) without it.

## Design

### New session artefact: `terminal.cast`

`session.TerminalCastFile = "terminal.cast"` — raw asciicast v2 stream,
archival, alongside `screen.mp4` and `events.rrweb.jsonl`: `merge` reads it
(to build the timeline) but never rewrites it, matching `events.rrweb.jsonl`'s
"raw source, not itself consumed as the timeline input" role rather than
`interactions.jsonl`'s "normalised, merge's direct input" role. This side-steps
a real correctness hazard: if normalised terminal records were instead
appended into `interactions.jsonl` (the intent's literal wording), a second
`merge` run over the same session would re-normalise and re-append them,
duplicating every terminal interaction. Reading `terminal.cast` fresh on every
`merge` run and folding its entries directly into the timeline build (the same
way `interactions.jsonl` entries already are) keeps `merge` idempotent, which
none of its other inputs currently risk breaking.

### `record -terminal`: capture lifecycle

New boolean flag, mirroring `-video`. Composes with the existing flags:

1. `session.Create` as today (manifest, `t0_epoch_ms`).
2. Start the microphone recorder (and screen, with `-video`) as background
   subprocesses, exactly as today.
3. With `-demo`, start the demo HTTP server into the same dir, as today.
4. Print status, including a `-terminal`-specific line explaining that the
   session ends when the operator exits the wrapped shell (not Ctrl+C).
5. **Without `-terminal`:** block on `signal.NotifyContext` as today.
   **With `-terminal`:** run
   `asciinema rec --quiet <dir>/terminal.cast` with `Stdin`/`Stdout`/`Stderr`
   wired to `record`'s own (inheriting the real TTY) via `cmd.Run()`, blocking
   until that shell exits. The operator's whole terminal session — every
   command they run — happens inside this call.
6. Either way, once unblocked (signal received, or the wrapped shell exited),
   run the existing stop-recorders-and-finalise path unchanged: SIGINT to the
   ffmpeg children, graceful demo shutdown, print next commands.

`-terminal` and `-demo` compose (demo's server runs in the background exactly
as it does today; the operator's foreground shell is independent of it).
`-terminal` and `-video`/audio-only compose without change, since the
terminal recorder is a peer to the existing recorders, not a replacement for
either.

**Signal handling inside the wrapped shell.** A Ctrl+C typed while
`asciinema rec` has the foreground TTY is delivered to the foreground process
*group*, which is `asciinema` and whatever command it is currently running —
ordinary shell Ctrl+C semantics (interrupt the current command), not
`record`'s own SIGINT handler, because `record` itself is not the foreground
process at that point. This is the correct, expected behaviour (an operator
pressing Ctrl+C mid-command inside the recorded shell should interrupt that
command, not end the whole session) and requires no new code: `cmd.Run()`
already blocks until the child's `Wait()` returns, and `record`'s own
`signal.Notify` registration is irrelevant while it is not in the foreground.
SIGTERM/SIGHUP sent to `record`'s own process (e.g., a supervisor stopping
it) are **not** forwarded into the wrapped shell in v1 — `asciinema rec`
receiving neither signal will keep running until the operator exits it
manually, an accepted gap flagged in Decisions below rather than solved
speculatively.

### Clock anchoring

asciicast v2's header carries a Unix `timestamp` (seconds); each event's
`time` field is seconds elapsed since that timestamp. `merge` computes each
event's session-relative time as
`(header.timestamp + event.time) - manifest.T0EpochMS/1000`, the same
subtraction `timeline.Merge` already does for epoch-millisecond interactions,
adapted for asciicast's float-seconds header.

Per the intent's first open question: starting `asciinema rec` from `record`,
immediately after the other recorders, is sufficient to anchor it to `t0` —
no separate spoken-marker correction is needed. This matches `spc-1`'s
identical, already-shipped assumption for the microphone recorder ("mic
capture starts at `t0`... correct by construction"); the few milliseconds of
process-start latency before `asciinema rec` begins recording is the same
order of negligible error already accepted there.

### Normalisation and output chunking

`merge` decodes `terminal.cast` line by line (header, then one JSON event
array per line: `[time, "o"|"i"|..., data]`), keeping only `"o"` (output)
events — the input-echo distinction the intent's vocabulary wants isn't
available without `--stdin`, so every kept event is emitted as one timeline
entry, `src: "event"`, `payload.kind: "terminal"`, `payload.text` the
event's `data`.

Answering the intent's second open question (verbatim vs truncated): a single
`data` chunk can exceed `session.MaxJSONLLine` (e.g., `cat` on a large file
writes far more than one line's worth in one `"o"` event). Truncating would
silently drop evidence — the one thing this codebase's size-cap hardening
(rounds 33/35/36 in `.abcd/work/DECISIONS.md`) has consistently refused to do
elsewhere. `merge` instead **splits** an oversized event's `data` across
consecutive timeline entries at safe UTF-8 boundaries, each within the
per-line cap, all carrying the same source time — preserving every byte the
terminal displayed rather than either truncating it or refusing the whole
run over one large command's output. The existing total-size guard
(`session.MaxJSONLBytes`) applies to `timeline.jsonl` exactly as it already
does; an oversized `terminal.cast` still cannot brick the session beyond that
existing, already-tested boundary.

Answering the third open question (TUI redraw noise) with a plan rather than
a guess: this is a live-verification item, not a blocking design question — a
line-oriented CLI session is the intent's actual target, and even a noisy TUI
capture stays useful as the raw archival `terminal.cast`, just a weaker
timeline signal. Verified before the PR merges (see Test plan), not decided
here.

### Documentation

`docs/reference/session-directory.md` gains `terminal.cast` in the file
layout table, alongside `screen.mp4`/`events.rrweb.jsonl`, and the
`timeline.jsonl` payload table gains the `terminal` `kind` value.
`docs/reference/cli.md`'s `record` section gains the `-terminal` flag row and
a behaviour paragraph describing the Ctrl-D-to-end lifecycle change.
`.abcd/development/brief/05-internals/02-schemas.md` gains the matching
schema entry.

### `install.sh`: asciinema and whisper.cpp, guidance not auto-install

New optional dependency, `asciinema`, needed only for `-terminal`. Per
explicit maintainer instruction, this is **not** auto-installed: `install.sh`
gains `dep_terminal()`, called after `dep_asr`, which — when `asciinema` is
not already on PATH — unconditionally prints why it exists (`record
-terminal`'s narrated, timestamped terminal capture) and the manual install
command (`brew install asciinema` where Homebrew is present, else a link to
`https://asciinema.org/docs/installation`). No `ask`/`choose` prompt, no
install action taken; this mirrors the existing ggml-model guidance block
that already stops short of running the `curl` for the operator.

Per the same instruction, `dep_asr`'s `whisper.cpp` branch changes from
auto-running `brew install whisper-cpp` to the identical guidance-only
treatment: explain why (segment-level ASR, and — per the earlier live
investigation this intent grew out of — genuine Apple Silicon acceleration
via Homebrew's Metal-enabled build, unlike whisperx's CPU-only path on
macOS) and print the manual `brew install whisper-cpp` command alongside the
existing ggml-model download guidance, rather than running it. **This is a
behaviour change to an already-shipped installer path**, confirmed explicitly
by the maintainer in the conversation that produced this spec, not inferred —
flagged here for the adversarial reviewers to scrutinise on its own terms.

## Decisions

- **Output-only capture (no `--stdin`).** Raw keystroke capture would record
  a password typed at a suppressed-echo prompt regardless of what the
  terminal displays; the intent's own vocabulary ("command entered and
  output emitted") is satisfied closely enough by output-only capture (shell
  echo shows commands) that the extra risk buys little. Revisit only if a
  concrete need for the input/output split emerges.
- **`terminal.cast` is a raw archival artefact merge reads, not a durable
  normalisation into `interactions.jsonl`.** Diverges from the intent's
  literal "normalising `.cast` records into `interactions.jsonl`" wording; a
  materialised, re-appended-on-every-run write would make `merge`
  non-idempotent, a property none of its current inputs risk. `report`'s and
  `timeline.jsonl`'s schemas are unaffected either way, which is the
  intent's actual load-bearing requirement.
- **Oversized events are split, never truncated.** Consistent with every
  other size-cap decision in this codebase; see Design above.
- **SIGTERM/SIGHUP are not forwarded into the wrapped shell in v1.** A
  supervisor stopping `record -terminal` leaves the operator's shell running
  until they exit it manually. Solving this needs process-group signal
  relaying that has no precedent elsewhere in `record` and no evidence yet
  that it is needed in practice; flagged rather than spculatively built.
- **`install.sh` stops auto-installing `whisper-cpp`.** A deliberate,
  maintainer-confirmed behaviour change bundled into this spec because it
  shares the exact "suggest, do not auto-install" pattern `asciinema` needs;
  not an incidental drive-by change.

## Test plan

Pure and unit-tested (hermetic, CI-safe, no `asciinema` binary needed):

- **asciicast parsing and clock anchoring**: header + event-array decoding,
  `(header.timestamp + event.time) - t0` arithmetic, table tests including a
  header `timestamp` before/at/after `t0`.
- **Output chunking**: an event whose `data` exceeds `MaxJSONLLine` splits
  into multiple same-time entries at valid UTF-8 boundaries, each within the
  cap; a normal small event stays a single entry; total bytes preserved
  exactly across the split.
- **Kind filtering**: `"i"` (and any other non-`"o"`) event types are
  dropped, never reach the timeline.
- **`record -terminal` argv construction**: the `asciinema rec` argv is built
  by a pure function, table-tested without spawning it.
- **`record -terminal` lifecycle**: extending the existing controller-over-a-
  `proc`-interface test harness (`spc-1`'s lifecycle state machine tests) with
  a fake wrapped-shell process, asserting the existing recorders are still
  started first and the existing stop-and-finalise path runs identically
  whether triggered by a signal or by the fake shell exiting.
- **`dep_terminal`/`dep_asr` guidance text**: install.sh's existing
  flag-handling test pattern (`--help`, `--dir`, etc.) extended to assert the
  new guidance strings print and that no `brew install` invocation is
  attempted for either dependency.

Skipped without tools/TTY (integration; `t.Skip` guarded): actually spawning
`asciinema rec`.

Live verification (part of done, not CI, before the PR merges): a real
`testimony record -terminal` session against a line-oriented CLI (this
repository's own `testimony` binary makes a convenient, safe subject) and,
separately, against a TUI (`htop` or similar) to observe and document actual
redraw-traffic volume, resolving the intent's third open question with a
real measurement rather than a guess. Both flow through
`merge → report` and are checked for a legible terminal join.

## How acceptance criteria are satisfied

- **`merge` interleaves spoken utterances and the cast stream on one
  clock** — `merge` reads `terminal.cast` as a third input, computing each
  event's session-relative time from the header `timestamp` and the shared
  `t0`, and folds the resulting `kind: "terminal"` entries into the same
  timeline build that already interleaves `transcript.jsonl` and
  `interactions.jsonl` — no new join logic, the existing time-ordered merge
  already does this once terminal entries are in the same entry stream.
- **`report` renders commands and output inside an utterance's join
  window** — terminal entries are ordinary `src: "event"` timeline entries;
  `report`'s existing event-in-window rendering, unchanged, already covers
  any `kind` value, `"terminal"` included.
- **Cast records and speech share one `t0`-derived clock, no separate
  clock** — both `timeline.Merge`'s existing epoch-ms interaction handling
  and the new asciicast handling above subtract the same manifest
  `T0EpochMS`; there is exactly one anchor in the session, as today.
