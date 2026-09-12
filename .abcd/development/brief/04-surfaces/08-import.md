# `testimony import`

Normalises an operator-recorded terminal session — an asciinema recording, in
either asciicast format — into a session's `interactions.jsonl` on the shared
session clock, and keeps the raw `.cast` in the session as an archival
`terminal.cast`. It is `transcribe -audio`'s peer for the terminal: an artefact
the CLI never produced, an anchor read out of that artefact's own metadata, an
explicit `-offset` that always wins, a mandatory printed provenance line, an
idempotent re-run, and an all-or-nothing write. Nothing here calls a model,
spawns a process, allocates a pty, or touches the network.

`record` is deliberately untouched: no `-terminal` flag, no wrapped recorder,
no second way for a session to end. The operator runs their own recorder in the
window where the work happens, and hands the file over afterwards — the pattern
`demo` already uses for QuickTime and external audio.

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `-session` | (required) | session directory |
| `-cast` | (optional) | asciicast file to import; omit to re-import the session's own `terminal.cast` |
| `-offset` | derived | cast→session clock offset in seconds |

## Behaviour

- Accepts **asciicast v2 and v3**, told apart by the header's `version` field:
  v2 event times are absolute seconds since recording start, v3 event times are
  intervals since the previous event, reconstructed by a running sum. Any other
  version is refused by name. The difference is confined to one accumulator, so
  the same recording in either format yields byte-identical records.
- Resolves the cast→session offset in `transcribe`'s order: an explicit
  `-offset` wins; otherwise the offset is derived from the header's `timestamp`
  minus the manifest's `t0_epoch_ms`, in exact integer arithmetic; otherwise 0.
  The offset and its provenance are always printed, and the derived provenance
  carries the `(whole seconds, ±1s)` caveat, because the header field is an
  integer in both formats and the report's default join window is 2.5 s. No
  sidecar is persisted: the archived cast keeps its own header, so a re-import
  re-derives the identical offset from the identical bytes.
- Requires a usable `t0` on every path, unlike `transcribe`: the records are
  epoch-millisecond-timed and `-offset` is defined against the session clock,
  and `merge` already refuses a session with interactions and no anchor.
- Keeps only `o` (output) events. `i` (input), `r` (resize), `m` (marker), `x`
  (exit), and any unrecognised code are dropped and counted by code, with the
  counts printed. Dropping input is a privacy requirement, not a
  simplification: a cast recorded with input capture still cannot put
  keystrokes into the derived text. An unrecognised code is dropped rather than
  refused, so a future asciicast revision does not make its casts unimportable.
- Coalesces adjacent output into one record per line the terminal displayed,
  closed at the first of a newline, a 250 ms inter-event gap, a 1 s span cap
  measured from the record's first event, or the encoded JSONL line budget. A
  record's time is the instant its first rune arrived. Carriage returns are kept
  and are not a boundary, so a redrawn progress line is one record rather than
  one per frame. A record whose text renders empty is dropped and counted.
- Splits a single oversized output event across consecutive records at rune
  boundaries, budgeted against the **encoded** length of the timeline entry
  `merge` will wrap the record in — escaping is what consumes the budget, an
  escape byte costing six bytes — and every finished record is then measured for
  real, so a wrong assumption about the encoder costs a refusal rather than a
  session no command can read back.
- Writes `interactions.jsonl` whole and atomically: records from an earlier
  import (identified by `kind: "terminal_output"`, and nothing else) are
  dropped, every other line is kept byte-for-byte in file order, and the new
  records are appended. So a re-import is byte-identical, a `-demo` session's
  clicks survive untouched, and an import that would yield zero records refuses
  rather than erase. Before writing, the assembly is checked against the line
  and file limits `session.ReadJSONL` enforces, and the merged timeline the
  import implies is measured against the file limit too — the case only an
  offline importer can compute rather than estimate.
- Archives the cast in two phases: the copy is staged into a temp file beside
  `terminal.cast`, the records are written, and the staged copy is renamed into
  place last. A failure anywhere before that rename leaves the session exactly
  as it was; the one residual state is records with no archival copy, which is
  the less misleading of the two. With `-cast` omitted there is no copy phase.
- Keeps ANSI escape sequences raw in the record, because the record is
  evidence and a hand-written escape-sequence parser would corrupt it rather
  than merely litter it. `report`'s existing sink strips the escape byte, so no
  terminal control sequence reaches `report.md`; the printable residue is
  answered by recording guidance (`NO_COLOR=1`), not by code.
- Requires no change to `merge`, `report`, `analyze`, or `review`: the records
  carry only `t`, `kind`, and `text`, `kind` is an open set, and the timeline
  learns no new `src` value. Output schema:
  [`../05-internals/02-schemas.md`](../05-internals/02-schemas.md).
