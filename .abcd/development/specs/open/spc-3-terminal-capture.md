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
a new archival session artefact, `terminal.cast` (asciicast v2 **or** v3,
whichever the installed asciinema writes — see "Supported asciinema
versions" below). Unlike the
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
without input capture records what the terminal *displays* (typed commands
appear because the shell echoes them — arriving keystroke by keystroke, see
Normalisation below), not what is *typed*. Input capture is opt-in on both
supported asciinema lines — `--stdin` on 2.x, renamed `--capture-input`/`-I`
on 3.x with `--stdin` retained as an alias — and is explicitly out of scope
here: it would record raw keystrokes regardless of local echo state, so a
password typed at a suppressed-echo prompt (`sudo`, `ssh`) would land in the
evidence file verbatim. Splitting "command entered" from "output emitted" —
the intent's stated vocabulary — is deferred until a value proposition for
input capture justifies that risk; v1 ships a single generic interaction
`kind` carrying the displayed text, which already satisfies the intent's
actual acceptance criteria (interleaved on one clock, rendered in the join
window) without it.

## Design

### Supported asciinema versions and the format they write

Two asciinema CLI lines coexist and both must be supported, because the
operator's package manager decides which one they have:

- **3.x (Rust rewrite, 3.0.0 released 2025-09-15; 3.2.1 current).** What
  `brew install asciinema` builds today on macOS, the target platform.
  Writes **asciicast v3 by default**: the header carries
  `term: {cols, rows, ...}` and an optional integer `timestamp`; each event
  is `[interval, code, data]` where `interval` is the time in seconds
  **since the previous event**. A `--output-format` flag
  (`asciicast-v3` default, `asciicast-v2`, `raw`, `txt`) can force v2.
- **2.x (Python; 2.4.0, 2023).** Still what PyPI and Debian/Ubuntu ship.
  Writes **asciicast v2 only**: header carries `width`/`height` and an
  optional integer `timestamp`; each event is `[time, code, data]` where
  `time` is seconds **since recording start** (absolute). It has no
  `--output-format` flag and exits with a usage error if passed one.

Misreading one format with the other's semantics is silent timeline
corruption: a v3 cast read as v2 places every event after the first
progressively earlier than reality (interval mistaken for absolute), with no
parse error to catch it. The spec therefore makes two decisions:

**The spawn argv is version-independent.** `record -terminal` runs
`asciinema rec --quiet <dir>/terminal.cast` — both flags exist and mean the
same on both lines (`-q`/`--quiet` is 2.x's rec flag and 3.x's global flag).
It deliberately does **not** pass `--output-format asciicast-v2`: on a 2.x
binary that is a hard startup error killing the whole capture, so using it
would require probing `asciinema --version` and branching in the
session-critical path — and the parser below has to sniff versions anyway
for any cast not written by this exact spawn. One argv, no probe, no branch.

**The parser is version-sniffing and refuses the unknown.** `merge` reads
the header's `version` field first: `2` → event times are absolute seconds
since start; `3` → absolute times are reconstructed as the running sum of
intervals from the start; anything else (a v1 cast, a future v4) → `merge`
refuses with an error naming the file and the version found, and writes
nothing. The supported range is exactly asciicast v2 and v3; unsupported
versions fail loudly at merge time, never as a silently skewed clock. This
also keeps `record` itself entirely ignorant of asciinema's versioning — a
future 4.x that still accepts `rec --quiet FILE` and writes v2 or v3 keeps
working; one that writes v4 fails with a clear message at the first `merge`.

### New session artefact: `terminal.cast`

`session.TerminalCastFile = "terminal.cast"` — raw asciicast stream (v2 or
v3, as written by the installed recorder), archival, alongside `screen.mp4`
and `events.rrweb.jsonl`: `merge` reads it
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

**Recorder failure while the shell has the foreground.** Without
`-terminal`, `spc-1`'s failure classifier runs the moment a recorder child
exits before being asked to stop — a mic TCC denial is reported within
seconds. Blocking in `cmd.Run()` for the whole session must not turn that
into a silent blackout where the operator narrates for twenty minutes over a
recorder that died at second three. Each recorder child is therefore
`Wait`ed in its own goroutine from the start; on a pre-stop exit the
existing pure classifier runs immediately and its message (the same TCC-pane
or failed-start text `spc-1` specifies) is written to `record`'s stderr —
which, during the wrapped-shell period, is the operator's own terminal, so
the warning is seen mid-session (and lands in the cast as displayed output,
an accepted and even useful artefact: the evidence file records that capture
degraded, and when). The failure is also retained so the finalise path
reports it and exits non-zero, exactly as an unwrapped session would. The
session itself continues — a dead mic must not tear down the operator's
live shell — matching `spc-1`'s stance that a mid-session stop is reported,
never escalated into data loss elsewhere.

### Clock anchoring

Each event needs an absolute time before it can be made session-relative.
First, absolute-since-recording-start: in a v2 cast that is the event's
`time` field directly; in a v3 cast it is the running sum of `interval`
fields up to and including the event. Then the recording start itself is
anchored, in preference order:

1. **The spawn-time sidecar (managed path, preferred).** `record -terminal`
   captures `time.Now()` in epoch milliseconds immediately before spawning
   `asciinema` and persists it in a sidecar next to the cast, mirroring
   `transcribe`'s existing offset-sidecar pattern. `merge` prefers it when
   present. Precision is process-spawn latency — the same
   order-of-milliseconds error `spc-1` already accepts for the microphone
   recorder ("capture starts at `t0`... correct by construction").
2. **The cast header `timestamp` (fallback).** Used when the sidecar is
   absent — e.g. a cast the operator recorded by hand and copied into the
   session directory. The field is an optional **integer** in both v2 and
   v3: whole seconds, so the reconstructed clock can sit up to a second away
   from the manifest's millisecond `t0`. Against `report`'s 2.5-second
   default join window that is *not* negligible — it can move events across
   a window boundary — so `merge` says so: the offset-provenance line it
   prints for this path names the header timestamp as the anchor and states
   the ±1 s quantisation, and the spoken start marker remains the
   recommended calibration cross-check for any hand-recorded cast. A cast
   with neither sidecar nor header timestamp is refused with a message
   naming both remedies, never silently anchored to zero.

Either way `merge` computes
`anchor_epoch_seconds + absolute_event_time - manifest.T0EpochMS/1000`, the
same subtraction `timeline.Merge` already does for epoch-millisecond
interactions.

Per the intent's first open question: on the managed path the sidecar makes
the anchor exact to spawn latency, so no spoken-marker correction is needed
there — the marker recommendation survives only for the hand-recorded
fallback path above.

### Normalisation and output chunking

`merge` decodes `terminal.cast` line by line (header, then one JSON event
array per line: `[time-or-interval, code, data]`), keeping only `"o"`
(output) events — `"i"`, `"m"`, `"r"`, `"x"` and any unrecognised code are
dropped, which both formats' specs explicitly permit. The input-echo
distinction the intent's vocabulary wants isn't available without input
capture; kept events are emitted as timeline entries with `src: "event"`,
`payload.kind: "terminal"`, `payload.text` the event's `data`.

One raw event is **not** one timeline entry, because shell echo is
per-keystroke: as the operator types a command, each keystroke's echo
arrives as its own one-character `"o"` event, so the naive mapping would
shred a single typed command into dozens of near-empty entries interleaved
with prompt redraws — noise, not evidence, and a bloated `timeline.jsonl`.
`merge` therefore **coalesces** consecutive `"o"` events whose inter-event
gap is below a threshold (`terminalCoalesceGap`, a named constant, initial
value 500 ms, tuned by the live verification below) into one entry carrying
the *first* constituent event's time and the concatenated `data`. A gap at
or above the threshold — the pause between typing a command and reading its
output, or between commands — starts a new entry. This is a pure fold over
the decoded event list, unit-testable without a terminal, and it is why
"commands are visible because the shell echoes them" holds at the timeline
level and not only in the raw cast.

Answering the intent's second open question (verbatim vs truncated): a
single entry's `data` — one large `"o"` event or a coalesced run — can
exceed `session.MaxJSONLLine` (e.g., `cat` on a large file writes far more
than one line's worth in one `"o"` event). Truncating would
silently drop evidence — the one thing this codebase's size-cap hardening
(rounds 33/35/36 in `.abcd/work/DECISIONS.md`) has consistently refused to do
elsewhere. `merge` instead **splits** an oversized entry's `data` across
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

`docs/reference/session-directory.md` gains `terminal.cast` (and its
spawn-time sidecar) in the file layout table, alongside
`screen.mp4`/`events.rrweb.jsonl`, and the
`timeline.jsonl` payload table gains the `terminal` `kind` value.
`docs/reference/cli.md`'s `record` section gains the `-terminal` flag row and
a behaviour paragraph describing the Ctrl-D-to-end lifecycle change.
`.abcd/development/brief/05-internals/02-schemas.md` gains the matching
schema entries.

The `record` documentation also carries an explicit privacy warning the
browser path never needed: terminal output routinely contains usernames,
hostnames, absolute paths, environment values, and occasionally secrets a
tool prints, and `analyze` sends timeline text — terminal entries included —
to a model. The docs state this plainly and tell the operator to review the
session (the report is the natural reading surface) before running
`analyze` on a terminal session, and to prefer a scratch environment for
recorded work. Capture-side redaction is *not* attempted in v1 — a filter
that silently rewrites evidence is worse than an honest warning — but the
warning is a documented requirement of this spec, not an afterthought.

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

- **Support asciicast v2 and v3; sniff, never assume.** `brew install
  asciinema` ships the 3.x line (v3 by default, interval event times); PyPI
  and Debian still ship 2.x (v2 only, absolute event times). Reading one
  with the other's semantics silently corrupts the timeline, so the parser
  dispatches on the header's `version` field and refuses anything outside
  {2, 3} loudly. The spawn argv stays version-independent
  (`asciinema rec --quiet FILE` — no `--output-format`, which 2.x rejects as
  a hard error), so `record` needs no version probe.
- **Anchor from a spawn-time sidecar, header timestamp as fallback.** The
  header `timestamp` is an optional integer in both formats — whole-second
  precision, up to ~1 s of skew against a 2.5 s join window, which is not
  negligible. The managed path records its own epoch-ms spawn time in a
  sidecar (the `transcribe` offset-sidecar pattern); the header fallback
  prints its quantisation honestly and keeps the spoken-marker
  recommendation.
- **Coalesce per-keystroke echo; one entry per burst, not per event.**
  Shell echo arrives one character per `"o"` event; the naive one-event-one-
  entry mapping shreds typed commands into noise. Consecutive output events
  under a gap threshold merge into one timeline entry (pure fold, threshold
  a named constant tuned in live verification).
- **Recorder children are monitored during the wrapped-shell period.** A
  pre-stop recorder exit is classified and reported to the operator's
  terminal immediately (and retained for a non-zero exit at finalise), not
  discovered at session end — preserving `spc-1`'s TCC-detection behaviour
  under `-terminal` instead of regressing it to a session-long blackout.
- **Output-only capture (no input capture).** Raw keystroke capture would record
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
  that it is needed in practice; flagged rather than speculatively built.
- **Delivery-shape alternative on record.** itd-11 (terminal-cast-import)
  proposes the same evidence via a decoupled import step — the operator runs
  `asciinema rec` themselves and hands the `.cast` over afterwards, the
  `transcribe -audio` pattern — with no `record` lifecycle change at all.
  The version-landscape findings above weigh in its favour: the import shape
  needs only the version-sniffing parser (shared with this spec) and none of
  the wrapped-shell lifecycle, signal-forwarding, or mid-session-monitoring
  machinery. Which shape ships is the maintainer's call between itd-6/spc-3
  and itd-11; the parser, anchoring, coalescing, splitting, and privacy
  decisions in this spec apply verbatim to either.
- **`install.sh` stops auto-installing `whisper-cpp`.** A deliberate,
  maintainer-confirmed behaviour change bundled into this spec because it
  shares the exact "suggest, do not auto-install" pattern `asciinema` needs;
  not an incidental drive-by change.

## Test plan

Pure and unit-tested (hermetic, CI-safe, no `asciinema` binary needed):

- **asciicast parsing, both formats**: v2 (absolute times, `width`/`height`
  header) and v3 (interval times summed to absolutes, `term` header) decode
  to identical event lists for equivalent recordings; a header with
  `version` outside {2, 3}, or a missing/garbled header line, is refused
  with the file and version named — table tests over fixture casts of both
  versions.
- **Clock anchoring**: sidecar-preferred, header-fallback, neither-refused —
  table tests including a header `timestamp` before/at/after `t0`, the
  provenance strings for each path, and the quantisation wording on the
  header path.
- **Echo coalescing**: a per-keystroke echo burst (one-character events a
  few ms apart) folds into one entry at the first event's time; a gap at or
  above `terminalCoalesceGap` starts a new entry; boundary gap exactly at
  the threshold — pure-fold table tests.
- **Output chunking**: an entry whose `data` exceeds `MaxJSONLLine` splits
  into multiple same-time entries at valid UTF-8 boundaries, each within the
  cap; a normal small entry stays single; total bytes preserved exactly
  across the split.
- **Kind filtering**: `"i"`, `"m"`, `"r"`, `"x"`, and unknown event codes
  are dropped, never reach the timeline — in both format versions.
- **`record -terminal` argv construction**: the `asciinema rec` argv is built
  by a pure function, table-tested without spawning it.
- **`record -terminal` lifecycle**: extending the existing controller-over-a-
  `proc`-interface test harness (`spc-1`'s lifecycle state machine tests) with
  a fake wrapped-shell process, asserting the existing recorders are still
  started first and the existing stop-and-finalise path runs identically
  whether triggered by a signal or by the fake shell exiting; plus a fake
  recorder child dying mid-shell, asserting the classifier message is
  emitted immediately and the finalise path exits non-zero.
- **Spawn-time sidecar**: written before the recorder starts, round-trips,
  and is preferred over a header timestamp that disagrees with it.
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
`merge → report` and are checked for a legible terminal join. The
line-oriented run also tunes `terminalCoalesceGap` against real typing
cadence and, where a 2.x binary is obtainable alongside the brew-installed
3.x, exercises the parser against a genuinely recorded cast of each format
version (the CI fixtures are hand-built; this checks them against reality).

## How acceptance criteria are satisfied

- **`merge` interleaves spoken utterances and the cast stream on one
  clock** — `merge` reads `terminal.cast` as a third input, computing each
  event's session-relative time from the version-appropriate event timing
  (absolute for v2, summed intervals for v3), the recording-start anchor
  (spawn-time sidecar, else header `timestamp`), and the shared
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
