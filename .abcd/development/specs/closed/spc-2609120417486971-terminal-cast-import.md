---
id: spc-2609120417486971
slug: terminal-cast-import
intent: itd-11
origin: researcher-authored
production_mode: hand-written
---
# terminal-cast-import

## Summary

`testimony import` is the terminal path's hand-off step: one command that takes a
session directory and an asciicast file the operator recorded themselves, and
normalises the cast's **output** events into that session's ordinary
`interactions.jsonl` on the shared clock. It is `transcribe -audio`'s peer in
every respect that matters — an artefact the CLI never produced, an anchor read
out of that artefact's own metadata, an explicit `-offset` that always wins, a
mandatory printed provenance line, an idempotent re-run, and an all-or-nothing
write — and it is nothing like `record`: no flag on `record`, no pty, no
subprocess, no change to how a session ends.

Both asciicast formats are accepted and distinguished by the header's `version`
field: v2 event times are absolute seconds since recording start, v3 event times
are intervals since the previous event, reconstructed by a running sum. Every
other version is refused by name. Output events are coalesced into one record per
line the terminal displayed — cut at a newline, at a 250 ms inter-event gap, at a
one-second span cap, and at the JSONL line limit, whichever comes first — so a
command echoed back one keystroke at a time becomes one record rather than
dozens, and a single oversized output event becomes several records with every
rune preserved. Input (`i`) events are dropped and counted, never normalised.

Downstream is untouched: the records carry only the fields
`docs/reference/session-directory.md` already documents for an interaction, so
`merge` reads them through `checkedInteractions`/`BuildEntries` with no change,
`report` renders them through `eventLine` with no change, and `analyze` and
`review` see one more `event`-source entry each. The timeline schema learns no new
source type. The raw `.cast` is kept in the session as `terminal.cast`, the
archival counterpart of `events.rrweb.jsonl`.

New package `internal/cast` — one exported entry point, `cast.Run`, plus the
`cast.OutputKind` constant the reserved kind is named by — one new `session`
file-name constant (`TerminalCastFile`), and one new `cli` verb. Standard library
only; no new dependency, no network call, no subprocess.

## Design

### Command surface

```
testimony import -session DIR [-cast FILE] [-offset SECONDS]
```

| Flag | Default | Meaning |
|---|---|---|
| `-session` | *(required)* | session directory |
| `-cast` | *(optional)* | asciicast file to import; omit to re-import the session's own `terminal.cast` |
| `-offset` | derived | cast→session clock offset in seconds |

The verb is `import`: a peer command, not a flag on `transcribe` (which is
speech-only and would have to grow a second, mutually-exclusive input mode) and
not a flag on `record` (the fence this intent exists to hold). The name says what
the step does — it imports an artefact the operator already holds — and says
nothing about speech.

Flag names follow `transcribe` exactly. `-session` is the same required flag every
pipeline command takes. `-cast` is `-audio`'s twin: the operator-named external
artefact, optional in the same way and for the same reason — `transcribe` reuses
the session's own `audio.wav` when `-audio` is omitted, so `import` reuses the
session's own `terminal.cast`, which makes "re-run with a corrected `-offset`" a
one-liner (`testimony import -session DIR -offset -12.4`) instead of an
instruction to keep the original file findable. A `-cast` that resolves to the
session's own `terminal.cast` (`os.SameFile`, the `transcribe.sameFile` precedent)
is treated as the omitted case, so the archival copy is never copied onto itself.
`-offset` keeps `transcribe`'s meaning verbatim — seconds added to every
cast-clock time to place it on the session clock — and reuses
`transcribe.CheckOffset` for validation, so the rule that an offset must be finite
and within ±10⁹ seconds keeps one home.

`-cast` carries **no** extension check, unlike `-audio`'s closed `.m4a/.mov/.wav`
set. That set exists because ffmpeg accepts only those containers; here the
header's `version` field is the authority on whether a file is importable, and a
name rule would refuse a legitimately-named cast (`asciinema rec` writes whatever
name the operator gives, and a redirected recording may carry none).

`-session` obeys the shared inference rule itd-12 established for every pipeline
command: it is resolved through `cli.resolveSession(fs, dir)` — the explicit
flag wins; otherwise the current directory when it holds a Testimony session
`manifest.json`; a usage error otherwise — and the resolution is the **last** of
the command's invocation checks, so a run refused for any other flag never first
announces a session it did not use. `import` is a pipeline command like the
rest, and two rules for one flag would be the defect; the synopsis is therefore
`[-session DIR]`, as on its five siblings.

CLI-layer refusals, all exit 2 (the `usageErr` path), all before any work starts:
`-session` missing with no session manifest in the current directory;
`-session` or `-cast` explicitly empty (the
`transcribe: -audio must not be empty` precedent — an unset shell variable spliced
into the flag, which would otherwise silently select the in-place branch); a
stray positional (`rejectArgs`); an `-offset` that fails
`transcribe.CheckOffset`. Everything else is a runtime failure at exit 1.

The exact strings, which `internal/cli`'s tables pin:

```
import: -session is required (no -session flag, and the current directory holds no regular manifest.json file)
import: -session must not be empty
import: -cast must not be empty
import: unexpected argument "junk" (the command takes no positional arguments)
import: -offset must be a finite number of seconds, got NaN
import: -offset 1e+10 exceeds 1e+09 seconds in magnitude; no recording→session offset is that large
```

Reusing `transcribe.CheckOffset` generalises its magnitude message from
"no audio→session offset is that large" to "no recording→session offset", so the
one home for the rule reads correctly for both callers rather than gaining a
second copy of the bound.

The usage text gains one block, placed after `transcribe` (its analogue) and
before `merge` (its consumer), with the continuation line `transcribe`'s own
block already uses:

```
  testimony import      [-session DIR] [-cast FILE]     import an asciinema recording's output into interactions.jsonl (reuses the session's terminal.cast when -cast is omitted)
                        [-offset SECONDS]
```

and the footer that lists the inferring commands gains `import`.

### Package layout

`internal/cast`, entered through one exported function shaped like
`transcribe.Run`:

```go
// Package cast reads an asciinema recording (asciicast v2 or v3) and
// normalises its terminal-output events into a session's interaction stream.
// The package is named for the artefact it parses rather than for the verb it
// implements, because `import` is a Go keyword.
package cast

// OutputKind is the interaction kind every record this package writes carries.
// It is the importer's own marker: a re-run replaces exactly the records
// carrying it and leaves every other interaction untouched.
const OutputKind = "terminal_output"

type Options struct {
    SessionDir string    // session directory
    Cast       string    // asciicast file; "" reuses the session's terminal.cast
    Offset     float64   // cast→session clock offset in seconds
    OffsetSet  bool      // true when -offset was given explicitly
    Log        io.Writer // status sink; defaults to os.Stderr when nil
}

// Run performs the import and returns the number of records written to
// interactions.jsonl.
func Run(opts Options) (int, error)
```

Unexported internals, each independently testable and all but two of them pure:

- `scanCast(r io.Reader, name string, onHeader func(castHeader) error, fn func(castEvent) error) (castHeader, error)`
  — the streaming reader: header, then one callback per event line. `onHeader`
  runs after the header is decoded and **before the first event**, which is what
  lets the caller resolve the offset — the thing the header's timestamp anchors,
  and the thing every record's `t` needs — without buffering the events or
  reading the file twice. A header-first callback is the whole reason the
  signature carries two functions rather than one.
- `castHeader{Version *int; Timestamp *int64}` — a pointer version and timestamp
  so an absent field stays distinguishable from a genuine `0`, the
  `timeline.rawInteraction.T` / `transcribe.offsetSidecar.OffsetSeconds`
  precedent. Every other header field (`width`, `height`, `term`, `env`, `theme`,
  `command`, `title`, `duration`, `idle_time_limit`) is ignored: the importer
  needs the version and the anchor and nothing else, and unknown fields must not
  be rejected, since both formats are extensible.
- `castEvent{Line int; US int64; Code, Data string}` — `US` is always **absolute
  microseconds since recording start**, so the v2/v3 difference is resolved
  inside `scanCast` and nothing downstream of it knows which format was read.
  This is the single seam behind the "identical timeline from either format"
  criterion, and the grain is what makes that criterion literally true rather
  than approximately so (see *Reading the cast*).
- `resolveOffset(name string, opts Options, man session.Manifest, hdr castHeader) (offsetMS int64, provenance string, err error)`
  — pure given its arguments; the `transcribe.resolveOffset` twin. `name` is the
  cast's display name, which the implausible-timestamp refusal below names.
- `coalescer` — the record builder (`add(castEvent) error`, `flush() error`),
  pure over its inputs, holding one pending record's runes at a time.
- `rewriteInteractions(dir, castName string, t0 int64, records []timeline.Interaction) (replaced int, err error)`
  — the all-or-nothing write. `castName` is named by the size refusal, and `t0`
  anchors the merged-timeline pre-flight.
- `stageCast(dir, name string, src *os.File) (tmpPath string, err error)` and
  `commitCast(tmpPath string) error` — the two-phase archival copy, staged from
  the descriptor the scan already holds.

`Run`'s order of operations is `transcribe.Run`'s, for `transcribe.Run`'s stated
reason — **every refusal fires before anything on disk changes**, so a refused
import leaves the session byte-for-byte as it found it:

1. Load `manifest.json`; resolve `t0` through `session.Manifest.T0`.
2. Resolve the cast input: `-cast` (stat-and-regular-file guard) or the session's
   `terminal.cast` (no-follow guard). Refuse if neither is usable.
3. Scan the cast, building records as events arrive. Refuse on any malformed
   line, unsupported version, or implausible time, naming the line.
4. Validate the record set: each record through `timeline.CheckInteraction`, and
   the assembled `interactions.jsonl` (plus the merged `timeline.jsonl` it
   implies) against `session.MaxJSONLBytes`. Refuse the run, not the record.
   Each wrapped timeline entry is measured against `session.MaxJSONLLine` as its
   record is **closed** (see *Splitting an oversized event*) rather than in a
   separate pass: the check exists to catch a wrong encoded-length table, so it
   belongs beside the budget it verifies. Both fire before step 5, so the
   nothing-written guarantee is the same either way.
5. Stage the archival cast copy into a temp file beside `terminal.cast`.
6. Rewrite `interactions.jsonl` atomically.
7. Rename the staged copy over `terminal.cast`.

### Reading the cast (both formats)

`scanCast` reads with a `bufio.Scanner` bounded exactly as `session.ReadJSONL`
bounds a JSONL file, since a cast is the same kind of input — line-oriented,
operator-supplied, and possibly received rather than recorded here:

- per line: `sc.Buffer(make([]byte, 0, 64*1024), maxCastLine)` with
  `maxCastLine = 16 << 20` (16 MiB). Rationale: one `o` event can legitimately be
  a single large write (a `cat` of a file), and JSON-escaping inflates it — an
  ESC byte encodes to six bytes (verified against `encoding/json`) — so the
  4 MiB `session.MaxJSONLLine` would refuse casts whose *records* this importer
  can split and persist perfectly well. 16 MiB matches
  `session.MaxJSONLBytes`'s scale, and a line at the bound still splits into
  records that fit. `bufio.ErrTooLong` is reported as
  `%s:%d: line exceeds %d bytes; refusing to read`, with the line number the
  scanner reached.
- per file: `maxCastBytes = 64 << 20` (64 MiB), counted as lines are scanned
  (including the newline, and counted before any blank-line skip, exactly as
  `ReadJSONL` counts). A cast large enough to matter cannot become records that
  fit `interactions.jsonl`'s own 16 MiB cap anyway, so the file cap exists only
  to bound the scan itself: `%s: exceeds %d bytes across %d lines; refusing to
  read`.

Line 1 is the header, decoded into `castHeader`:

- not a JSON object (including an event line, so a cast with no header is caught
  here) → `%s:1: not an asciicast header (expected a JSON object with a "version" field)`;
- no `version` → `%s:1: asciicast header carries no "version" field`;
- `version` other than 2 or 3 → `%s: asciicast version %d is not supported (import reads version 2 and 3)`. Naming the file
  and the version found is the criterion's exact wording, and because this fires
  in step 3 nothing has been written.

Subsequent lines are events, `[time, code, data]`, decoded into
`[]json.RawMessage` and then element-wise (`float64`, `string`, `string`). Blank
lines are skipped, matching every other line reader in the repository. Any of
these is a refusal naming the line, with nothing written:

- not a 3-element JSON array, or an element of the wrong type →
  `%s:%d: malformed asciicast event (expected [time, code, data])`. An
  out-of-range numeric literal (`1e400`) lands here too: `encoding/json` refuses
  it into `float64` rather than yielding `+Inf`.
- v2, time decreasing →
  `%s:%d: event time %g precedes the previous event's %g; asciicast v2 times must not decrease`.
- v3, negative interval →
  `%s:%d: event interval %g is negative; asciicast v3 intervals must not be negative`.
- either format, absolute time beyond `maxCastSeconds = 1e9` →
  `%s:%d: event time %gs exceeds %g seconds; that is no recording clock`. The
  bound mirrors `timeline.maxUtteranceSeconds` and `transcribe.maxOffsetSeconds`,
  so a time this importer accepts is a time `merge` accepts.

The version difference is confined to one accumulator, and that accumulator runs
on an **exact integer grain**: microseconds (`castTimeGrain = 1e6`). Each event's
time is rounded onto the grain as it is parsed — `us := int64(math.Round(t*1e6))`
— and from there v2 sets the clock to it (refusing a decrease) while v3 adds it
(refusing a negative). The rounding from the grain to the millisecond a record
records happens once more, downstream, in one place (`microsToMillis`).

The grain is what makes "identical records from either format" true rather than
nearly true. A `float64`-seconds clock does not give it: v2 rounds a **stated**
absolute time while v3 rounds a **running sum**, and at a half-millisecond tie
the two land on different milliseconds. Seven 0.0015 s intervals sum to
0.010499999999999999 and round to 10 ms, where v2's stated 0.0105 rounds to 11 —
and one millisecond is enough to put a following event on either side of the
250 ms coalescing gap, so the same recording becomes one record read as v2 and
two read as v3. On the grain both formats reach 10500 µs and the question does
not arise. A microsecond is exact for every time either format writes (both cap a
time at six decimal places), and 1e9 seconds on the grain is 1e15, three orders
of magnitude inside `int64`.

The bound is applied **before** the conversion as well as after it, because
`int64(math.Round(x))` has no defined answer for a float past the integer range:
a 1e300 time or interval is refused where it is read rather than converted.

Event codes: only `o` (output) becomes records. `i` (input), `r` (resize), `m`
(marker), `x` (exit), and any code this importer does not recognise are dropped
and **counted by code**, with the counts printed (see *Printed output*) — the
intent's "evidence is not silently dropped" applied to whole event classes, not
only to oversized ones. Dropping `i` is a privacy requirement, not a
simplification (below). Dropping `r`/`m`/`x` is scope: a resize and an exit
status are terminal-session facts with no place in an interaction stream whose
schema is `kind`/`selector`/`text`/`value`/`route`, and a marker is a navigation
aid for a player, not an observed action. An unrecognised code is dropped rather
than refused so a future asciicast revision that adds one does not turn every
cast it writes into an unimportable file; the printed count is what keeps that
tolerance honest.

The tally is bounded, because a code is a single character in both formats but
nothing in the file format enforces that: a crafted cast can carry a different
code on every line, and an unbounded map would grow — along with the line it
prints — in step with the file. At most `maxDropCodes = 16` distinct codes are
named individually; everything past that is counted together and reported as
`%d under further codes`. Each named code is passed through `session.SafeText`
and clipped to 8 runes, the same rule the header literals above obey, and the
summary is ordered **by code** rather than by its formatted string, so a
three-digit count cannot sort a code above one that precedes it.

Two details of the bound are load-bearing:

- **`i` is exempt from it.** The input count is the one tally entry that is a
  privacy disclosure rather than a scoping note — it is how an operator learns
  their recorder captured keystrokes — and a cast carrying sixteen junk codes
  before its first `i` would otherwise swallow that line into the anonymous
  overflow and never print it. Exempting one fixed key cannot unbound the tally:
  the map holds at most `maxDropCodes+1` entries.
- **The overflow is a counter, not a map entry under a reserved key.** `""` is a
  legitimate event code a cast can carry, so a sentinel key would report a real
  empty-coded event as overflow and overflow as a real event.

Rune fidelity: `encoding/json` replaces invalid UTF-8 in a JSON string with
U+FFFD when it decodes, so what reaches `castEvent.Data` is already a valid Go
string. The preservation guarantee this spec makes is therefore stated at rune
granularity over the decoded string, and the byte-exact record is the archived
`terminal.cast` — the one place a byte-for-byte claim can honestly be made. See
the acceptance-criteria mapping, where this is called out as a refinement of the
criterion's wording.

### Clock anchoring

An interaction's `t` is epoch milliseconds, so the record's time is computed in
integers:

```
t = t0 + offsetMS + round(eventSeconds * 1000)
```

`offsetMS` is the cast→session offset in milliseconds, resolved by
`resolveOffset` — which reads `t0` through `session.Manifest.T0` itself, so it
stays a pure function of its arguments and its table test can drive the
usable/absent-anchor axis directly — in the order `transcribe.resolveOffset`
uses:

| Condition | `offsetMS` | Printed provenance |
|---|---|---|
| `-offset` given | `round(opts.Offset * 1000)` | `from -offset flag` |
| header `timestamp` present and positive | `*hdr.Timestamp*1000 − t0` | `derived: cast header timestamp − manifest t0 (whole seconds, ±1s)` |
| header carries no `timestamp` | `0` | `default 0: cast header carries no timestamp` |

The derived case is exact integer arithmetic — no float enters it — because both
operands are integers: the header's `timestamp` is whole Unix seconds in both
formats, and `t0_epoch_ms` is whole milliseconds. Only an explicit `-offset`
introduces a rounding step, to the nearest millisecond.

Two refusals, both the `transcribe` twin:

- `t0` is obtained through `session.Manifest.T0`, never the raw field, so an
  absent (`0`) or negative anchor refuses the run:
  `anchoring the terminal cast: %w`. Unlike `transcribe`, `import` needs `t0`
  even on the explicit-`-offset` path, because the artefact it writes is
  epoch-millisecond-timed and `-offset` is defined relative to the session clock.
  This is not a gap: `merge` already refuses a session whose `interactions.jsonl`
  is non-empty and whose manifest carries no usable `t0`, so importing into such
  a session would persist records no command could ever read back — the
  write-before-read invariant `session.WriteJSONL` and `SaveManifest` both
  enforce.
- a present-but-non-positive header `timestamp` is refused rather than defaulted:
  `%s: header timestamp %d is not a recording instant; pass -offset SECONDS to anchor the cast explicitly`.
  `Manifest.T0`'s reasoning applies unchanged — no recorder produces a capture
  instant at or before 1 January 1970 — and `transcribe` likewise refuses a
  *present but implausible* creation time (its derived-offset magnitude bound)
  while defaulting only when the metadata is *absent*.
- a derived offset beyond `1e9` seconds in magnitude is refused in
  `transcribe.resolveOffset`'s words:
  `derived cast offset %+.2fs exceeds %g in magnitude; the cast's header timestamp or the manifest t0 is implausible — pass -offset SECONDS to state it explicitly`.

**Absent-timestamp policy: default 0, mirroring `transcribe` exactly.** When
`transcribe` cannot read an external recording's `creation_time` it prints
`default 0: audio creation time unavailable` and continues; `import` prints
`default 0: cast header carries no timestamp` and continues. Refusing instead
would make the terminal path stricter than the audio path for the same class of
missing metadata, and the remedy is identical in both: the spoken "session start"
marker as the cross-check, then `-offset` to correct. The provenance line is
printed on **every** run, so "offset 0 because the header said nothing" is never
a silent assumption.

**No offset sidecar.** `transcribe` persists `audio.offset.json` because
converting an external recording into `audio.wav` destroys the `creation_time` it
derived from, leaving the session unable to tell external audio from
record-origin audio. Nothing analogous happens here: the archived
`terminal.cast` keeps its own header, so a re-import re-derives the identical
offset from the identical bytes. An explicit `-offset` must be repeated on a
re-import, and `-cast` being optional is what makes repeating it cheap.

**The quantisation caveat is in the printed line, not only in the docs.** The
derived provenance string carries `(whole seconds, ±1s)` because the header field
is an integer in both formats: the reconstructed clock can sit up to a second
adrift of `t0`'s millisecond precision, which is material against `report`'s
2.5-second default join window. The operator reads the bound at the moment they
read the offset.

### From output events to interaction records

The problem the intent names: a shell echoes a typed command back roughly one
keystroke at a time, so the raw `o` stream around a command is a run of
one-character events. One record per raw event fragments a single typed command
across dozens of near-empty records. The fix is a single accumulator with four
boundaries.

`coalescer.add(ev)` appends `ev.Data`'s runes to the pending record and closes
that record at the first of:

1. **a newline.** The `\n` is included in the record, and the next rune starts a
   new record. This is the primary boundary and the one that makes the stream
   legible: one record per line the terminal displayed. It also solves the
   keystroke-echo problem outright — the echo of a typed command carries no
   newline until Enter, so the whole command arrives as one record.
2. **an inter-event gap of `coalesceGap = 250 ms` or more** to the next `o`
   event. This closes a record whose output never ends in a newline: a bare
   prompt (`$ `), a `read` prompt, a progress line. 250 ms sits above a human's
   typical inter-keystroke interval (~100–200 ms), so a typed command still
   coalesces; three orders of magnitude above the gaps inside a program's output
   burst, so it never merges across a genuine pause; and an order of magnitude
   below `report`'s 2.5 s join window, so coalescing alone can never pull content
   across a window boundary. It is a named constant, not a flag: an operator has
   no way to know what value to pass, and the value interacts with `-window`,
   which is already a flag on the command that needs it.
3. **a span cap of `maxCoalesceSpan = 1 s`** measured from the record's first
   event. A record states one time — its first event's — so unbounded coalescing
   would attribute minutes of output to a single early instant. One second keeps
   the attribution error well inside `report`'s 2.5 s default window, so a record
   still joins to the utterance spoken over it.
4. **the JSONL line budget** (below).

`coalescer.flush()` closes the pending record at end of stream. A record's `t` is
always the time of the event that contributed its **first** rune — never
fabricated, never averaged — so a record cut by any of the four boundaries is
honestly timed, and continuation records after a split carry the time of the
event whose data they open with.

A record whose text renders empty — `strings.TrimSpace(session.SafeText(text)) == ""`,
the `transcribe.mapSegments` test, so a blank line or a lone carriage return does
not become a timeline bullet showing only the word `terminal_output` — is
dropped and counted, and the count is printed. The archived `terminal.cast` holds
those bytes verbatim, which is what makes the drop a rendering decision rather
than a loss of evidence.

Carriage returns inside a record are kept, and `\r` is **not** a boundary: a
progress bar emits many `\r`-separated frames for one displayed line, and one
record per frame would flood the stream. A record holding several frames renders
as their concatenation (`report`'s `SafeText` strips the `\r` itself), which is
the acknowledged cost of the intent's "TUI redraw handling beyond preserving the
raw cast is out of scope".

**Splitting an oversized event.** The binding limit is not the record's own line
length but the size of the **timeline entry `merge` wraps it in** — the
`transcribe.checkEntriesFit` and `demo.tooLongOnceWrapped` invariant. The budget
is computed once **per record**, at the moment the record opens, from that
record's own time:

```
probe    := timeline.Interaction{T: t0 + offsetMS + castMS, Kind: OutputKind, Text: "x"}
envelope := session.EncodedLen(timeline.EventEntry(probe, t0)) - 1
budget   := session.MaxJSONLLine - envelope - castEntryIDMargin
```

Two details of that expression are load-bearing. The probe carries a one-rune
text (whose single byte the `- 1` removes) because `timeline.BuildEntries` omits
an empty `text` from the payload entirely, so an envelope measured without one
under-counts by the whole `,"text":""` scaffolding. And the budget is per record
rather than per run because the entry's session-relative `t` varies in encoded
length across a session — `0` is one byte, `-1000000000.123` is fifteen, and
float64 division of an integer millisecond count produces the decimal form
either way — so a single run-wide envelope would have to guess at the widest
case. A record's `t` is known the instant the record opens, which makes
measuring it exact and costs one small struct encode per record.

`castEntryIDMargin = 32` is `demo.eventIDGrowthMargin`'s twin, for the same
reason: `timeline.EventEntry` stamps the placeholder id `ev-001`, while the real
ordinal depends on the record's position among every interaction in the session,
so the measured entry is a lower bound and the margin covers the ordinal's
growth. (`demo`'s constant is unexported, so the value is restated here with the
citation rather than reached across the package boundary.)

The accumulator tracks the pending text's **JSON-encoded** length, not its raw
length, because escaping is what actually consumes the budget: an ESC byte costs
six bytes encoded, and ANSI-coloured output is dense in them. Per rune
(verified against `encoding/json` with `SetEscapeHTML(false)`, which is how
`session.WriteJSONL` encodes):

| rune | encoded bytes |
|---|---|
| `"`, `\` | 2 |
| `\n`, `\r`, `\t` | 2 |
| any other C0 control (ESC 0x1b included) | 6 |
| U+2028, U+2029 | 6 |
| everything else, DEL and `<`/`>`/`&` included | `utf8.RuneLen(r)` |

The table is an **upper bound**, not an equality: `encoding/json` has written
backspace and form feed as a six-byte u-escape in some Go versions and as a
two-byte short escape in others, and the table takes the larger for both. That
is the safe direction — an over-count spends a few of a 4 MiB budget's bytes,
while an under-count over-fills the budget and costs a false refusal on the
measured check below. The property test asserts the bound (never under, equal
everywhere the two agree) rather than exact equality.

A rune is appended only if it keeps the running total within `budget`; otherwise
the record closes and the rune opens the next one. A rune is split off only from
a **non-empty** pending record, so a rune that could not fit even an empty
record's budget is appended anyway — and caught by the measured check — rather
than closing and opening records for ever on one it can never hold. Splitting therefore never
occurs inside a rune, and concatenating a split's records reproduces the event's
decoded data exactly. Because the table is an assumption about another package's
encoder, every finished record is additionally measured for real —
`session.EncodedLen(timeline.EventEntry(rec, t0)) + castEntryIDMargin <= session.MaxJSONLLine`
— and a failure is a refusal, not a truncation:
`record %d encodes to a %d-byte timeline entry, over the %d-byte JSONL line limit; this is an importer bug — please report it with the cast that triggered it`.
The check is unreachable if the table is right, and it is the difference between a
wrong table costing a refused run and a wrong table costing a session no command
can read back.

No continuation marker is written on a split record. A field outside the
documented interaction schema would be dropped by `timeline.rawInteraction` at
merge and so could never reach the report anyway, and overloading `value` or
`route` to carry one would pollute a documented schema for a cosmetic gain.
Consecutive records with identical or adjacent `t` values, rendered as
consecutive bullets, are what a split looks like — and the whole-line boundary
means splits are rare in practice.

### The interaction record shape

Exactly the fields `docs/reference/session-directory.md` already documents, and
no others:

```json
{"t":1784300424100,"kind":"terminal_output","text":"$ ls -la\n"}
```

| Field | Value |
|---|---|
| `t` | epoch milliseconds, computed as above |
| `kind` | `"terminal_output"` — the constant `cast.OutputKind` |
| `text` | the record's accumulated output, runes exactly as the cast's JSON decoded them |
| `selector`, `value`, `route` | never set (omitted by `omitempty`) |

**`kind` is the source marker.** A re-run identifies its own prior records by
`kind == cast.OutputKind` and nothing else. This is a deliberate deviation from
adding a `source: "cast"` field: an extra field would be dropped by every reader
in the repository (`timeline.rawInteraction` decodes six fields and ignores the
rest), so it would be dead weight in an exchanged artefact while still needing a
row in a schema table whose readers ignore it — whereas `kind` is already the
documented discriminator for *what happened*, is already the field `report`
renders first, and already survives into `timeline.jsonl` where it tells a reader
that this entry came from a terminal. The cost is a collision hazard: a
third-party instrumented app posting `kind:"terminal_output"` to
`POST /api/interactions` would have its record replaced by a later import. It is
closed by documentation — `docs/reference/session-directory.md` names
`terminal_output` a reserved kind — rather than by a new refusal in `demo`'s
endpoint, which would change an accept set for a hazard with no realistic
attacker payoff (the "attack" substitutes genuine terminal evidence for a forged
record) and is outside this intent's fence.

`report` reads `kind` (`mdOrDash`) and `text` (`mdInline`, in straight quotes)
from an event payload, and nothing else this record carries, so a terminal record
renders as `- [01:23] terminal_output "$ ls -la"` through `eventLine`
**unchanged**. Two consequences of that existing sink are worth stating plainly
rather than discovering later:

- `session.SafeText` strips `\n`, so a record's trailing newline is invisible in
  `report.md` — which is why the record is one displayed line: were records
  multi-line, the report would run their lines together.
- `SafeText` strips ESC but not the rest of a CSI sequence, so coloured output
  renders with residue (`[0;34m`). See *ANSI escape sequences*.

### Writing: all-or-nothing, idempotent re-run

`rewriteInteractions` replaces `interactions.jsonl` whole:

1. Read the existing file, if any, through `session.OpenFileNoFollowRead` (a
   FIFO or symlink planted at the name in a received session is refused, not
   followed or blocked on), bounded by `session.MaxJSONLLine` per line and
   `session.MaxJSONLBytes` for the file — the same pair `ReadJSONL` enforces. A
   missing file is zero lines, not an error. The read is whole rather than
   scanned, and the split is on `\n` by hand: `bufio.Scanner` strips a trailing
   carriage return along with the newline, which would silently rewrite a CRLF
   line the step below promises to keep byte-for-byte. The replacement is
   assembled in memory anyway for the atomic write, so reading the 16 MiB-capped
   original whole costs nothing extra.
2. Classify each line by decoding a probe struct (`{Kind string}` only): a line
   whose `kind` equals `cast.OutputKind` is a record from an earlier import and
   is **dropped**; every other line — a `demo` click, an `input`, a line this
   importer cannot decode at all, a blank line — is **kept byte-for-byte**.
   Lines are never re-encoded, which is why this write cannot go through
   `session.WriteJSONL[T]` (it encodes values, and would silently rewrite a
   foreign record's field order, number formatting, or an unknown field it cannot
   model).
3. Assemble kept lines, in file order, followed by the new records encoded with
   `session.EncodedLen`'s encoder settings (`SetEscapeHTML(false)`, so a
   measured size and a written line cannot disagree). One byte is added and only
   one: a final kept line carrying no terminating newline gets one, because
   without it the first imported record would be appended onto that line and
   neither would survive a read back.
4. Pre-flight the assembly with the checks `WriteJSONL` would have applied, since
   step 2 is why they cannot be delegated: every line within
   `session.MaxJSONLLine`, and the total within `session.MaxJSONLBytes` —
   `importing %s would take %s past its %d-byte limit (%d bytes across %d lines); record shorter terminal sessions, or start a fresh session`.
5. Pre-flight the **merged timeline** the assembly implies, which `demo`'s
   endpoint can only estimate and an offline importer can measure: the sum of
   `session.EncodedLen(timeline.EventEntry(rec, t0))` over every decodable
   interaction line plus, when `transcript.jsonl` is present,
   `session.EncodedLen(timeline.SpeechEntry(u))` over every utterance, against
   `session.MaxJSONLBytes`. Each event entry is charged its **id growth** —
   `demo.idGrowth`'s arithmetic, restated as `castIDGrowth` — because
   `timeline.EventEntry` sizes every entry with the placeholder id `ev-001`
   while `merge` assigns `ev-%03d` by position, so past the thousandth
   interaction the real id is longer than the measured one. Without the charge a
   session sitting just under the cap with a few thousand interactions passes
   this pre-flight and is then refused by `merge` — the exact "import succeeds,
   merge can never read it back" state the pre-flight exists to prevent. The
   ordinal is the position across the prior records and then the new ones, which
   is the order they are written in and so the order `merge` numbers them in.
   The message is —
   `the imported records would take the merged %s past its %d-byte limit; record shorter terminal sessions, or start a fresh session`.
   An interaction line that does not decode is not sized: `merge` will refuse
   that session for its own, pre-existing reason, and `import` must not be
   blamed for it. Like `demo`'s estimate, the guarantee is "as of import time" —
   a `transcribe` run afterwards adds speech entries this pass could not see.
6. Write with `session.WriteFileAtomicNoFollow` (temp file plus rename, symlink
   refused up front, an existing file's mode preserved exactly). A failure at any
   point leaves the prior `interactions.jsonl` untouched.

Consequences, all intended:

- **Idempotent.** Importing the same cast twice yields a byte-identical
  `interactions.jsonl`: the second run drops exactly what the first wrote and
  writes exactly the same records back.
- **Mixed sessions are safe.** A `record -demo` session's clicks and inputs are
  preserved byte-for-byte, in their original order, ahead of the terminal
  records. The file need not be time-sorted — `merge` sorts, and `report` sorts
  again — so appending is correct without reordering anything.
- **One terminal recording per session.** Importing a *different* cast into a
  session replaces the first cast's records (and its `terminal.cast`), because
  both are identified by the same reserved kind. Two concurrent terminals in one
  session are out of scope; the reference says so, and the remedy is one terminal
  per session.
- **Zero records is a refusal, not an erasure.** A cast holding no importable
  output would otherwise silently delete a prior import's records — the hazard
  `transcribe`'s zero-utterance guard and `merge`'s zero-entry guard both refuse.
  The two ways to reach it are named apart, because they call for different
  remedies: a cast with no output events at all is the wrong file (or one
  recorded with input capture only) —
  `%s holds no output events; refusing to rewrite %s` — while a cast whose output
  all rendered empty is a real recording of a terminal that displayed nothing
  legible —
  `%s holds no importable output (%d record(s) rendered empty); refusing to rewrite %s`.

### The archival copy

The raw cast is kept in the session directory as `terminal.cast`, a new
`session.TerminalCastFile` constant beside `AudioFile`, `ScreenFile`, and
`RawEventsFile`, and documented in the `session` package doc comment and the
session-directory reference in the same pass (the schema-move invariant spc-1
states). It is the archival counterpart of `events.rrweb.jsonl`: nothing
downstream reads it, and it exists so the byte-exact record survives and so a
re-import needs no external file.

`import` **copies** the operator's file rather than requiring it to be in place
already. This mirrors `transcribe -audio`, which normalises an external recording
into the session as `audio.wav`: the hand-off pattern's whole point is that the
operator hands over a file and the session becomes self-contained. Requiring the
operator to place the file themselves would add a step whose only failure mode is
a wrong name.

The copy is two-phase, so the ordering question ("which artefact is left behind
if the other write fails?") has a defensible answer rather than a rollback:

- `stageCast(dir, name string, src *os.File)` streams the source into
  `.terminal.cast.tmp-*` beside the target with `os.CreateTemp` and `io.Copy`
  over an `io.LimitReader(maxCastBytes+1)`. `src` is the descriptor the scan
  already read, **rewound** rather than re-opened by path: the archive must hold
  the bytes the records were derived from, and between two opens of a path the
  operator's file can be replaced or rewritten, which would leave the session
  asserting a byte-for-byte archive of something else. The copy is refused if it
  reaches the limit reader's extra byte — the bound is read one byte past so a
  file that grew is refused rather than archived truncated, which is the one
  place this design makes a byte-for-byte claim. Otherwise the
  `transcribe.atomicConvert` shape, including
  the prior-mode preservation rule (an existing `terminal.cast`'s own mode is reapplied; a new file takes
  `0o644 &^ umask`, so a privacy-conscious operator's umask is honoured).
  A non-regular or symlinked `terminal.cast` is refused before the temp is
  created, `transcribe.checkPlainOutput`'s rule.
- `rewriteInteractions` runs next.
- `commitCast` renames the temp into place last, and the temp is removed by a
  `defer` on every failure path.

So a failure anywhere before the final rename leaves the session exactly as it
was, and the only residual window is a same-directory rename failing after the
records landed — leaving records with no archival copy, which is the less
misleading of the two possible residual states: the records are the evidence, the cast
is the archive. With `-cast` omitted (the in-place re-import), there is no copy
phase at all.

### Input events and the privacy boundary

`i` events are dropped in `scanCast` and never reach a record, under any flag.
The recommended invocation (`asciinema rec session.cast`, on either CLI line)
does not capture input at all; input capture is opt-in (`--stdin` on 2.x,
`--capture-input`/`-I` on 3.x), and the how-to tells the operator to leave it off,
because a password typed at a suppressed-echo prompt is exactly the thing that
would land in the evidence. The importer's unconditional drop is the second
layer: a cast that *was* recorded with input capture — by an operator who did
not read the guidance, or handed over by someone else — still cannot put
keystrokes into `interactions.jsonl`. The printed count says how many were
dropped, so the operator learns their recorder captured input.

Note the honest boundary: the dropped keystrokes remain in the archived
`terminal.cast`, a local-only file exactly like `audio.wav`. That belongs in the
privacy documentation, not in a silent deletion — the importer does not rewrite
the operator's evidence.

### ANSI escape sequences

Kept raw in `text`. The record is evidence, and stripping escape sequences would
mean the importer applying a lossy transform to the one copy every downstream
reader consumes — with a hand-written ANSI parser as the new surface, whose bugs
would corrupt evidence rather than merely litter it.

`report` renders them through `eventLine` → `mdInline` → `session.SafeText`,
which strips the ESC byte (and every other C0 control) but leaves the printable
tail of a CSI sequence, so a coloured `ls` renders as
`- [01:23] terminal_output "[0;34mdocs[0m"`. That is deliberate hardening in the
render sink — a raw ANSI sequence must never reach `report.md` or the `review`
terminal — and this spec does not change it: there is no code fence, no raw
passthrough, and `report` stays untouched. The remedy is guidance, not code: the
how-to tells the operator to record with colour disabled (`NO_COLOR=1`, or
`TERM=dumb` for tools that ignore it), which also makes the records more
searchable and cheaper to hand to an analysis model. Stripping CSI/OSC sequences
at import is recorded here as a deliberate follow-up option, with the archived
`terminal.cast` as the safety net that would make it reversible.

### Printed output

Everything `import` says about its own run goes to `opts.Log`, which the CLI
wires to **stderr** — beside `resolveSession`'s inference line, and for the same
reason. `stdout` carries exactly one line, the summary the CLI itself prints, so
a script reads one line rather than parsing diagnostics out of a stream. (This
is a deliberate split from `transcribe`, which puts its own progress on stdout:
`transcribe` predates the inference line, and `import` has more to say.)

In order, and all but the first printed only when they are non-zero:

```
offset: %+.2fs (%s)                                      the provenance table above; every run
dropped %d input (i) event(s): keystrokes are never imported
dropped %d other event(s): 1 marker (m), 1 resize (r), 1 exit (x)
dropped %d record(s) that render empty
replaced %d terminal_output record(s) from an earlier import
```

then, from the CLI:

```
imported %d records → <session>/interactions.jsonl
```

The offset line is `transcribe`'s format verbatim, and it prints on **every**
run, so "offset 0 because the header said nothing" is never a silent
assumption. Input is named on its own line because its drop is a privacy
guarantee rather than a scoping decision, and the count is how an operator
learns their recorder captured keystrokes — and the count is exempt from the
tally's bound, so a cast full of junk codes cannot suppress the disclosure. The
other codes are listed together, ordered by code so the line is deterministic
whatever order the tally iterates in, with any overflow last.

The offset and drop lines print before the zero-records refusal, so an operator
whose cast held nothing importable still learns what was in it.

### Downstream: merge, report, analyze unchanged

Confirmed by reading, not assumed:

- `timeline.rawInteraction` decodes `t`, `kind`, `selector`, `text`, `value`,
  `route`. A terminal record sets `t`, `kind`, `text`, all three of which it
  already handles.
- `timeline.checkInteraction` requires `t` non-nil, `t > 0`, `|rel| ≤ 1e9`, and a
  non-empty `kind`. Every record satisfies all four by construction, and each one
  is checked through the exported `timeline.CheckInteraction` before it is
  written — the same guard `demo`'s endpoint applies, so `import` cannot persist a
  record `merge` would refuse.
- `kind` is an **open** set: nothing in `timeline`, `report`, or `analyze`
  validates it against an enum (`docs/reference/session-directory.md` says
  `e.g. "click", "input"`). `terminal_output` therefore adds no schema field and
  no new `src` value — `BuildEntries` emits `src:"event"`, which `CheckSrc`,
  `report`'s bucket switch, and `analyze`'s indexer all already accept.
- `BuildEntries` assigns `ev-%03d` by position; more events widen the ordinal
  (`ev-1234`) and stay unique, which `merge`'s duplicate-id scan confirms.
- `report`'s join, `end()`, `clock()`, and `eventLine` need nothing new; negative
  session-relative times already render with a leading `-`.
- `analyze` indexes event ids and emits the timeline inline; a terminal record is
  citable evidence like any other event.

**No change to `merge`, `report`, `analyze`, or `review` is required.** One
non-behavioural consequence is worth naming: `analyze` emits the whole timeline
inline, so a verbose terminal session makes a much larger analysis request. That
is a documentation matter (keep terminal sessions short; review before
analysing), not a code change, and it is where the privacy warning lands too.

### Failure modes and exit statuses

| Situation | Status | Message shape |
|---|---|---|
| `-session` missing with no session manifest in the current directory, empty `-session`/`-cast`, stray positional, bad `-offset` | 2 | the existing `usageErr` shapes |
| no `manifest.json`, or no usable `t0` | 1 | `anchoring the terminal cast: %w` |
| neither `-cast` nor `terminal.cast` | 1 | `no %s in session %s and no -cast given: record a terminal with asciinema, then pass -cast FILE` |
| `-cast` names a missing or non-regular file | 1 | `cast file: %w` / `refusing to read %s: it is not a regular file` |
| unsupported version, malformed header or event, over-long line or file | 1 | the `%s:%d:`-prefixed shapes above |
| implausible header timestamp or derived offset | 1 | the `-offset SECONDS` guidance shapes above |
| no importable output | 1 | `%s holds no output events…`, or `%s holds no importable output (%d record(s) rendered empty)…` |
| a size limit reached | 1 | the limit shapes above |
| a write failure | 1 | wrapped `session`/`os` error |

Every exit-1 path above fires before any file in the session changes, except a
write failure, which is left all-or-nothing by the atomic writers.

## Acceptance-criteria mapping

Each bullet is the intent's criterion, then the mechanism, then the test.

1. **v2 cast + narrated session → one interleaved clock from the same `t0`.**
   Mechanism: `scanCast` yields absolute seconds; `resolveOffset` derives
   `header_ts*1000 − t0`; records carry epoch-ms `t`; `merge` rebases them
   through the same `t0` it rebases nothing else with — there is no second clock
   anywhere in the design, and no per-source offset in `timeline.jsonl`.
   Test: `TestImportThenMergeInterleaves` — a `t.TempDir()` session with
   `manifest.json`, a two-utterance `transcript.jsonl`, and a v2 fixture; run
   `cast.Run` then `timeline.Merge`; assert the entry order and each entry's `t`
   against golden values.
2. **The same session as v3 yields an identical timeline.**
   Mechanism: the v2/v3 difference is confined to `scanCast`'s accumulator, and
   `castEvent.T` is absolute in both cases, so every stage after it is
   format-blind.
   Test: `TestV2AndV3Agree` — a table over two fixture pairs, each describing one
   recording in both formats (identical header timestamp, event codes, and
   absolute times; v3's intervals are the differences): the ordinary recording,
   and the half-millisecond tie pair above, which is the case a `float64` clock
   splits apart. Assert the two runs produce byte-identical `interactions.jsonl`,
   and the expected record count, so the pair cannot pass by both formats being
   wrong the same way; `TestTiesCoalesceIntoOneRecord` states that count
   independently. Two more pin the reconstruction itself:
   `TestScanCastV3RunningSum` over a table of interval sequences (compared
   exactly, on the grain, ties included) and `TestMicrosToMillis` over the one
   rounding step from the grain to a record's millisecond.
3. **A version other than 2 or 3 is refused, naming file and version, writing
   nothing.**
   Mechanism: the header switch refuses before step 4, and every write is in
   steps 5–7.
   Test: `TestUnsupportedVersionRefuses` — `version` 1, 4, `"2"`, and absent;
   assert the message contains the file name and the version found, and that a
   hash of every file in the session directory is unchanged (a shared
   `assertSessionUnchanged` helper reused by every refusal test).
4. **`report` renders utterances with the terminal output in their window,
   through the existing event rendering unchanged.**
   Mechanism: records carry only `kind` and `text`, which `eventLine` already
   renders; one record per displayed line is what keeps that rendering legible
   under `SafeText`'s newline stripping.
   Test: `TestReportRendersTerminalEvents` — import into a session with one
   utterance, `timeline.Merge`, `report.Render`; assert the utterance line is
   followed by indented `- [MM:SS] terminal_output "…"` bullets, and that
   `internal/report` carries no change (no new test in that package).
5. **`merge`, `report`, `analyze` behave identically for the same inputs.**
   Mechanism: no new `src`, no new schema field, `kind` is an open set.
   Test: the existing `internal/timeline`, `internal/report`, and
   `internal/analyze` suites stay green with no edits — the evidence is the empty
   diff in those packages, asserted by the review, plus
   `TestImportedRecordsPassCheckInteraction`, which runs every record every
   fixture produces through `timeline.CheckInteraction`.
6. **A header timestamp preceding `t0` yields negative session-relative times
   that render as an early-started audio recording's do.**
   Mechanism: `offsetMS` is negative, `t` stays a positive epoch-ms value
   (`t0 + offsetMS` is still ~1.7e12), so `CheckInteraction`'s positivity rule is
   met and `merge` produces a negative `rel`; `report.clock` already signs it.
   Test: `TestEarlyStartedCastGoesNegative` — header timestamp 30 s before `t0`;
   assert entry times around `-30`, and that `report.Render`'s output contains
   `[-00:30]`.
7. **A single output event larger than the line limit is split, within the limit,
   nothing truncated.**
   Mechanism: the encoded-length budget, the per-rune append test, and the
   measured `EncodedLen` assertion per record.
   Test: `TestOversizedEventSplits` — one `o` event of mixed ASCII,
   multi-byte runes, and ESC bytes, sized as the test plan explains (~6 MiB at
   the package boundary, the full ~12 MiB in the `coalescer` unit, which writes
   nothing); assert (a) more than one record, (b) every
   wrapped entry within `session.MaxJSONLLine`, (c) concatenating the records'
   `text` reproduces the event's decoded data exactly, (d) no record boundary
   falls inside a rune (implied by (c), asserted directly with `utf8.ValidString`
   on each record). **Refinement of the criterion's wording:** the criterion says
   "every byte preserved"; what is preserved at rune granularity is the string
   `encoding/json` decodes, since the decoder itself replaces invalid UTF-8 with
   U+FFFD before this package sees a byte. The byte-exact artefact is
   `terminal.cast`, asserted byte-identical to the input by
   `TestCastArchivedVerbatim`. This is the one place the spec narrows a criterion,
   and it narrows it to something true rather than leaving a claim no
   JSON-reading importer can honour.
8. **Ctrl+C in `record`'s terminal finalises exactly as an audio-only session
   does; nothing about terminal capture appears in `record`'s lifecycle.**
   Mechanism: `internal/record` is not touched — no flag, no recorder, no
   subprocess, no signal path.
   Test: the existing `internal/record` suite stays green with no edits; the
   evidence is the empty diff in that package. `TestUsageListsImport` asserts the
   new verb appears in the usage text (so the surface change is visible), that
   the inferring-commands footer names it, and that no `-terminal` flag is
   offered anywhere in that text — the fence, asserted rather than assumed.

Additional criteria the intent states in scope rather than as a Given/When/Then,
each with its test: input events dropped (`TestInputEventsDropped`, asserting no
record and a non-zero printed count); the printed provenance line and its
quantisation caveat (`TestOffsetProvenance`, a table over the three cases);
idempotent re-run and preservation of `demo` records
(`TestReimportIsIdempotent`, `TestForeignRecordsPreserved`).

## Decisions on open questions

The intent's four open questions, and the two this spec had to add.

1. **Command surface: a new verb.** `testimony import -session DIR [-cast FILE]
   [-offset SECONDS]`, a peer of `transcribe` with `transcribe`'s flag names and
   `transcribe`'s optional-input rule. A flag on `transcribe` would give one
   command two unrelated input modes; a flag on `record` is the fence this intent
   exists to hold. `import` names the step without implying speech.
2. **Re-run and mixing: replace only what this importer wrote, identified by
   `kind: "terminal_output"`.** The write is a whole-file atomic rewrite that
   drops prior terminal records, keeps every other line byte-for-byte, and
   appends the new records — so a re-import is byte-identical, a `-demo`
   session's clicks are untouched, and an import that would yield zero records
   refuses rather than erase. The marker is the `kind` value rather than a new
   `source` field because every reader in the repository drops fields outside the
   documented six, so a `source` field would be dead weight that still needed
   documenting; `kind` is already the discriminator, already rendered, and
   already carried into the timeline.
3. **`install.sh`: no. The how-to alone.** The installer's guidance-only pattern
   covers dependencies the CLI itself executes — ffmpeg (`record`'s capture,
   `transcribe`'s conversion) and an ASR engine (`transcribe`'s engine). The CLI
   never executes asciinema: `import` reads a file, and reads it just as happily
   from any other producer of asciicast v2/v3. Listing it among the installer's
   dependencies would assert a runtime dependency that does not exist, and would
   put a third-party recorder's install path on the critical path of installing
   a binary that does not need it. The how-to prints the install line where the
   operator is already deciding to record a terminal.
4. **Chunking granularity: coalesce, and cut at the line the terminal displayed.**
   Adjacent `o` events accumulate into one record, closed by a newline, a 250 ms
   gap, a 1 s span cap, or the encoded line budget — values justified in the
   design above, all named constants, none exposed as flags. Is the coalesced
   record still a faithful "what the terminal displayed" claim? Yes, with the
   boundaries stated: the record holds the runes the terminal received, in order,
   with nothing inserted; its `t` is the instant its first rune arrived, and the
   span cap bounds how stale that instant can be (≤ 1 s, inside `report`'s
   window). The two places the claim is weaker than "what was displayed" are
   named rather than hidden — a `\r`-redrawn line records every frame rather than
   the final rendering, and an ANSI sequence is data in the record that a
   terminal would have consumed as a command. Both are the intent's out-of-scope
   TUI boundary, and both are exactly why the raw cast is archived.
5. **Added: the absent-anchor policy is `transcribe`'s, verbatim.** No header
   `timestamp` → offset 0 with the provenance printed; a present but
   non-positive timestamp, or a derived offset beyond ±10⁹ s → refuse with
   `-offset` guidance. Mirroring the audio path keeps one rule for one class of
   missing metadata, and the always-printed provenance line is what makes the
   default non-silent.
6. **Added: ANSI sequences stay raw in the record.** The report sink already
   neutralises them (`SafeText` strips ESC), the archived cast keeps the bytes,
   and the remedy for the printable residue is recording guidance
   (`NO_COLOR=1`) rather than an ANSI parser in the importer. Stripping at
   import is recorded as a reversible follow-up.
7. **Settled: `-session` follows itd-12's shared inference rule.** The open
   question in the command-surface section is closed by itd-12 having landed:
   `import` resolves `-session` through `cli.resolveSession`, last among its
   invocation checks, and carries no required-flag refusal of its own. One flag,
   one rule, on all six pipeline commands.
8. **Settled: the budget is measured per record, and `cast` reuses
   `transcribe.CheckOffset`.** Two numeric rules that could each have been
   copied are not: the encoded-length budget is measured from the record's own
   time through `session.EncodedLen` (rather than assumed once per run), and the
   offset's finiteness and ±10⁹ s bound stay in `transcribe.CheckOffset`, called
   by both the CLI (for exit 2) and `cast.Run` (so a direct caller cannot pass a
   non-finite offset into an integer conversion). `cast` importing `transcribe`
   for one predicate is the cheaper of the two costs.

## Test plan

Everything below is hermetic: `t.TempDir()` session directories, fixture casts
under `internal/cast/testdata/`, no network, no subprocess, no ffmpeg, no TTY. CI
runs it via the existing `go test -race ./...` gate, with `gofmt -l .` and
`go vet ./...` as before.

**Fixtures** (`internal/cast/testdata/`): `v2.cast` and `v3.cast` (the same
recording in both formats, with a header timestamp, output, input, resize,
marker, and exit events); `v2-ties.cast` and `v3-ties.cast` (the same recording
again, built so the clock's rounding is the thing under test: seven 1.5 ms
keystrokes put the seventh at exactly 10.5 ms — a half-millisecond tie that
rounds one way from a stated absolute time and the other from a running sum —
with the eighth event 249.5 ms later, so a one-millisecond disagreement falls on
either side of the 250 ms gap and the recording becomes one record or two — on a
`float64` clock this pair yields 1 record read as v2 and 2 read as v3, and on the
microsecond grain 1 either way); `v3-nots.cast` (no header `timestamp`);
`bad-version.cast`, `bad-header.cast`, `bad-event.cast`, `decreasing.cast`,
`negative-interval.cast`; `golden.interactions.jsonl` (the expected records for
`v2.cast`, the `whisperx.golden.jsonl` precedent). Oversized inputs are generated
in-test rather than committed.

**Pure units**
- `scanCast`: v2 absolute times; v3 running sum (table of interval sequences,
  including `0` intervals and a long tail); every code delivered to the callback
  in order; blank-line skipping (with the line numbers still counting the blanks,
  so a refusal names a line the operator can find); every malformed-line refusal,
  each asserting the line number in the message; the per-line and per-file bounds
  (the file bound driven by a repeating reader rather than a 64 MiB fixture);
  both callbacks' errors propagating unchanged. The per-code drop counts are
  asserted at `Run`, where the classification lives, along with the tally's
  16-code bound and the clipping of an over-long code.
- `resolveOffset`: table over `-offset` set/unset × header timestamp
  present/absent/non-positive × `t0` usable/absent → `(offsetMS, provenance,
  error)`, asserting the three provenance strings verbatim (they are a documented
  contract) and the two refusal messages.
- `coalescer`: table of synthetic event sequences → expected record boundaries
  and times, one case per boundary (newline, 250 ms gap, 1 s span, budget), plus
  the interaction of two boundaries falling together, plus the
  renders-empty drop and its count.
- the per-rune encoded-length table: property test asserting the table is never
  *under* `session.EncodedLen`'s real cost for every rune in a sampled set (all
  of ASCII, a handful of multi-byte runes, U+2028/9, U+FFFD, the invisible Cf
  runes), and equal wherever the two agree — so a stdlib change that invalidates
  the table fails here rather than as a refused import, while the deliberate
  over-count on backspace and form feed stays legal.
- the measured entry-size refusal, which is unreachable while the table holds:
  driven white-box, by falsifying a `coalescer`'s budget so an over-long record
  reaches `close`, and asserting the importer-bug message and that no record is
  kept.

**Package-level `Run`**
- happy path: records written, return value, printed lines (a `bytes.Buffer`
  `Log`), `terminal.cast` archived byte-identically.
- idempotence: two runs → byte-identical `interactions.jsonl`.
- mixing: a pre-existing `interactions.jsonl` of `demo` records plus a prior
  import's records → foreign lines byte-for-byte preserved in order, terminal
  records replaced; a foreign line that does not decode at all is preserved too.
- in-place re-import (`-cast` omitted) and the `os.SameFile` case (`-cast`
  pointing at the session's own `terminal.cast`): no copy, same result.
- refusals, each with `assertSessionUnchanged`: unsupported version; malformed
  header/event; over-long line and file; missing manifest; unusable `t0`;
  non-positive header timestamp; implausible derived offset; zero output events;
  neither `-cast` nor `terminal.cast`; `-cast` naming a directory or a FIFO; a
  symlink or FIFO at `terminal.cast` or at `interactions.jsonl`.
- size limits: an assembled `interactions.jsonl` over 16 MiB; a merged-timeline
  total over 16 MiB with `interactions.jsonl` itself under it (the case only an
  offline importer can measure).
- oversized single event: the four assertions in criterion 7, plus a `Merge` over
  the result, since fitting the line limit is only worth anything if merge then
  accepts it. The event is ~6 MiB of mixed ASCII, multi-byte runes, and ESC
  bytes rather than the ~12 MiB the criterion's prose suggests: escaping inflates
  those records by about a third, so a 12 MiB event is refused for
  `interactions.jsonl`'s own 16 MiB file cap before the split can be observed.
  6 MiB is comfortably past the 4 MiB line limit, which is all the split needs.
  The `coalescer` unit test, which writes nothing, uses the full 12 MiB.
- the file-mode rules: an existing `interactions.jsonl`'s mode preserved across
  the rewrite, and an existing `terminal.cast`'s preserved across the archive.

**Integration (still hermetic)**
- `import` → `timeline.Merge` → `report.Render` for the v2 and the v3 fixture:
  interleaving, negative times, the rendered bullets, and golden `report.md`
  fragments.
- `TestImportedRecordsPassCheckInteraction` over every fixture.

**`internal/cli`** — `import` joins the four shared tables that already state
these contracts for its siblings, rather than growing a table of its own:
`TestStrayPositionalIsAUsageError`, `TestInvalidFlagValuesExitTwo` (empty
`-cast`, non-finite and out-of-bound `-offset`), `TestMissingSessionIsAUsageError`
and `TestEmptySessionIsAUsageErrorNotInference` (the two `-session` refusals),
and `TestRefusedInvocationAnnouncesNoSession` (a run refused for `-offset`
announces no inferred session). Six cases are its own:
- `TestImportWritesTerminalRecords` — a well-formed run over a cast fixture
  written in-test: exit 0, the summary line on stdout, the records and their
  times in `interactions.jsonl`, no keystroke among them, and the cast archived
  byte-identically.
- `TestImportDiagnosticsStayOffStdout` — the offset provenance and the drop
  counts on stderr, and stdout carrying nothing but the one summary line.
- `TestImportInfersSessionAndReImportsInPlace` — the offset-correction recipe
  end to end: import with `-cast`, then a bare `import -offset -12.4` from inside
  the session, asserting the inference line, the explicit-offset provenance, the
  replaced-record count, and that the correction replaced the derived offset
  rather than compounding it.
- `TestImportRefusesUnreadableCastAtRuntime` — an absent `-cast` is exit 1, not
  exit 2, so a script can tell a mistyped flag from a missing file.
- `TestUsageListsImport` — the usage block, the inferring-commands footer, and
  the absence of any `-terminal` flag.

**CI** — one hermetic smoke step (`Terminal import smoke test`) builds a
throwaway session around `internal/cast/testdata/v2.cast` in a temp directory and
asserts what the unit tests cannot reach through the CLI: the stdout/stderr
split, the archived cast being byte-identical (`cmp`), a bare re-import leaving
`interactions.jsonl` byte-for-byte unchanged (`cmp`), and the records merging and
rendering with a signed clock. `examples/sample-session/` is deliberately
untouched, so the quickstart's golden report is unaffected.

**Live verification** (not a CI gate, and stated here for exactly what it does
and does not cover).

*Verified against a real recorder.* An asciinema **2.4.0** recording — the PyPI
line, so asciicast **v2** — of a shell session running `printf hello`, `ls /`, a
three-frame `\r` progress line, and three ticks 300 ms apart, imported into a
scratch session whose `t0` sits 1.5 s before the cast header's timestamp. The
derived offset is `+1.50s`, matching the constructed skew; the run produces 8
records; each `ls` line is its own record; the three `\r` frames coalesce into
one record; the three ticks stay separate, which is the 250 ms gap behaving on
real shell timing rather than on fixture timing. `merge` and `report` then render
them under the utterance as designed. The one thing the live run showed that the
fixtures only asserted is the cost this spec already accepts: a coloured `ls`
renders as CSI residue in `report.md` (open ledger issue
`iss-2609120520334220`), which is what the `NO_COLOR=1` guidance is for.

*Not verified against a real recorder.* The **3.x (v3)** line is not installed on
the machine the verification ran on, so asciicast v3 is exercised by fixtures
only — including the tie pair, which is the case the two formats could diverge
on. The two-terminal procedure itself (`testimony record` narrating in one window
while `asciinema rec` records in another) was not run end to end with live audio,
so the spoken-marker cross-check for the whole-second anchor remains a documented
procedure rather than a measured one. Both gaps are named here rather than in a
claim the tests do not carry.

## Docs plan

`docs/` is user-facing, one Diátaxis type per page, present tense, British
English in prose.

- **`docs/reference/cli.md`** — a new `## testimony import` section between
  `## testimony transcribe` and `## testimony merge`: the synopsis, the
  three-row flag table, the behaviour (what is read, both formats and how they
  differ, which event codes are kept and which dropped, the coalescing
  boundaries and their values, the split rule, the archival copy, the re-run
  semantics, the one-cast-per-session limit), the three provenance strings
  verbatim with the quantisation caveat, the printed lines, and the refusals that
  exit 2 versus 1. The `transcribe` section gains nothing; the `merge` section
  gains one sentence noting that terminal records merge like any other
  interaction.
- **`docs/reference/session-directory.md`** — `terminal.cast` in the layout
  block (archival, written by `import`, local only) and its own
  `## terminal.cast` section modelled on `## events.rrweb.jsonl`: what it is,
  that nothing downstream reads it, that `import` re-reads it when `-cast` is
  omitted, and the 64 MiB read bound. The `## interactions.jsonl` section gains:
  `terminal_output` as a **reserved** kind written only by `import` (with the
  collision consequence stated), that its `text` may hold multi-line output and
  ANSI bytes, and that one record is one displayed line.
- **`docs/how-to/record-a-terminal-session.md`** (new) — the two-terminal
  procedure: `testimony record -app …` in window one, `asciinema rec session.cast`
  in window two (the window where the work happens), say "session start" aloud,
  work, end the cast the way asciinema always ends it, `Ctrl+C` in window one,
  then `testimony import -session sessions/<dir> -cast session.cast`,
  `transcribe`, `merge`, `report`. Plus: the install pointer
  (`brew install asciinema`, or `pipx install asciinema`) with the note that
  either version works and no `--output-format` flag is needed; the
  **do not capture input** warning naming `--stdin` and `--capture-input`/`-I`
  and the suppressed-echo password hazard; the colour guidance (`NO_COLOR=1`);
  the **privacy warning** — terminal output routinely carries usernames,
  hostnames, absolute paths, environment values, and occasionally secrets printed
  by tools, and `analyze` sends timeline text to a model, so review or redact
  `timeline.jsonl` before running `analyze`, and keep terminal sessions short
  because the whole timeline goes into the request; and the offset-correction
  recipe, cross-referencing
  [fix a wrong clock offset](transcribe-a-recording.md) rather than restating it
  (`testimony import -session DIR -offset -12.4`, no `-cast` needed).
- **`docs/README.md`** — the new how-to in the how-to list.
- **`docs/explanation/privacy.md`** — one short paragraph in *The privacy
  boundary*: a terminal recording shows more of the machine than a demo app
  does; keystrokes are never imported into the derived text; `terminal.cast` is
  raw local evidence of the same class as `audio.wav`.
- **`README.md`** — `import` in the *Status and roadmap* working-today list, and
  `terminal.cast` in the session-directory block. The pipeline diagram gains one
  row (`terminal ──► asciinema ──► terminal.cast ──► interactions.jsonl`).
- **`AGENTS.md`** (`CLAUDE.md` is a symlink to it, so one edit serves both) —
  the *Current state* paragraph's command inventory goes from seven pipeline
  commands to eight, pairing `import` with `transcribe` as the two hand-off
  commands for a recording the CLI never made; and the *Build, test, and checks*
  block gains the import smoke line beside the `merge`/`report` one.
- **`.github/workflows/ci.yml`** — one hermetic `Terminal import smoke test`
  step (see the test plan) and the workflow header comment that enumerates what
  it runs.
- **`CHANGELOG.md`** — one entry under the unreleased heading, per the
  changelog-driven release gate.
- **`install.sh`** — **no change**, for the reason recorded under decision 3: the
  CLI never executes asciinema, so it is not a dependency the installer's
  guidance-only pattern is for.
- **`examples/sample-session/`** — no change. Adding terminal records to the
  bundled sample would change the quickstart's golden report for every reader to
  demonstrate a path the how-to already walks; a fixture in
  `internal/cast/testdata/` carries the same weight for tests without touching
  the published example.
