# Session directory reference

Every capture session lives in one directory (by default under `sessions/`):

```
sessions/<timestamp>/
  manifest.json        # session metadata, including t0_epoch_ms (written by demo and record)
  audio.wav            # 16 kHz mono ASR input (captured by record, or converted by transcribe -audio; local only)
  audio.offset.json    # audio→session offset for an external recording (written by transcribe; local only)
  screen.mp4           # screen capture, H.264, 30 fps with cursor (written by record -video; local only)
  events.rrweb.jsonl   # raw rrweb stream, archival (written by demo and record -demo)
  terminal.cast        # raw asciicast, archival (written by import; local only)
  interactions.jsonl   # normalised interaction events (written by demo and record -demo)
  transcript.jsonl     # time-aligned utterances (written by transcribe)
  timeline.jsonl       # merged, session-relative timeline (written by merge)
  findings.jsonl       # analysis findings + appended verdicts (written by analyze/review)
  tests.jsonl          # regression-test drafts + appended decisions (written by draft-tests/review)
  report.md            # human-readable aligned record (written by report)
```

All `.jsonl` files are JSON Lines: one JSON value per line, blank lines ignored. `timeline.jsonl`, `transcript.jsonl`, `interactions.jsonl`, `findings.jsonl`, and `tests.jsonl` each carry a 16 MiB total-size limit: a write that would push one over the cap is refused, and a load of one already over it is refused, so a session that reaches it needs a fresh session directory to continue in. `events.rrweb.jsonl` is archival and carries no such limit, and `terminal.cast` is not a JSON Lines file at all — it carries its own read bound, described below.

## `manifest.json`

A single JSON object describing the session. `t0_epoch_ms` anchors every session-relative time: `relative_seconds = (epoch_ms − t0_epoch_ms) / 1000`.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `session` | string | yes | session name (the directory's base name) |
| `app` | string | no | application under test |
| `commit` | string | no | build or commit identifier of the app |
| `participant` | string | no | participant pseudonym, e.g. `"P1"` |
| `t0_epoch_ms` | integer | yes | session start, epoch milliseconds — the shared clock anchor |
| `tasks` | array of strings | no | tasks given to the participant |
| `notes` | string | no | free-form notes |

`manifest.json` carries a 1 MiB size limit of its own, separate from the `.jsonl` limit above: a write that would push it over the cap is refused, and a load of one already over it is refused.

```json
{
  "session": "sample-session",
  "app": "testimony demo",
  "participant": "P1",
  "t0_epoch_ms": 1784300400000,
  "tasks": ["Change your display name and save it"]
}
```

## `interactions.jsonl`

One normalised interaction event per line, as posted by the instrumented app. Times are epoch milliseconds.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `t` | integer | yes | event time, epoch milliseconds; must be positive and within 1e9 seconds of `t0_epoch_ms` (`merge` and the demo write endpoint both refuse otherwise) |
| `kind` | string | yes | event kind, e.g. `"click"`, `"input"` |
| `selector` | string | no | element anchor, ideally `[data-testid=...]` |
| `text` | string | no | short element label (demo capture truncates to 40 characters) |
| `value` | string | no | new value for input events (demo capture truncates to 80 characters; checkboxes send `"true"`/`"false"`) |
| `route` | string | no | route or hash at the time of the event |

```json
{"t":1784300419200,"kind":"click","selector":"[data-testid=save-btn]","text":"Save","route":"#general"}
```

`kind` is an open set, with one exception: **`terminal_output` is reserved for `testimony import`**. Records carrying it are written only by `import`, and `import` identifies its own earlier records by that value alone — so a re-import replaces every `terminal_output` record in the file and leaves every other line byte-for-byte as it was. An instrumented app that posts `kind: "terminal_output"` to the demo capture endpoint therefore has its record replaced by a later import; use any other kind.

A `terminal_output` record carries `t`, `kind`, and `text` and nothing else. One record is one line the terminal displayed, so its `text` ends in a newline when the line was complete, may hold several carriage-return-separated frames of a progress line, and keeps any ANSI escape sequences the terminal received verbatim. A single output event too large for the JSONL line limit is split across consecutive records, each carrying the time of the event whose data it opens with, so concatenating their `text` reproduces the output exactly. `report` renders the records through the same event rendering as any other interaction, with control bytes — the ANSI escape byte included — stripped at that boundary.

```json
{"t":1784300398520,"kind":"terminal_output","text":"ls --color\r\n"}
```

## `transcript.jsonl`

One utterance per line. Times are session-relative seconds (audio time plus the transcription offset), rounded to two decimal places.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | yes | sequential utterance ID: `utt-001`, `utt-002`, …; unique within the file and outside the `ev-NNN` namespace `merge` synthesises for events (`merge` refuses a reused or colliding id, since findings cite evidence by id) |
| `t0` | number | yes | utterance start, session-relative seconds; must not exceed 1e9 seconds in magnitude (`merge` refuses otherwise) |
| `t1` | number | yes | utterance end, session-relative seconds; defaults to `t0` when absent or earlier than `t0`, and otherwise must not exceed 1e9 seconds in magnitude (`merge` refuses) |
| `speaker` | string | no | speaker label; `"P1"` when the engine supplies no diarisation |
| `text` | string | yes | utterance text, whitespace-trimmed (segments that are empty, whitespace-only, or invisible-only Unicode are dropped) |
| `words` | array | no | word-level alignment (WhisperX only); each element is `{"w": <word>, "t": <start seconds>}` — a word is omitted if the aligner could not time it, if its time is implausible (non-finite, or beyond ±1e9 seconds) either as engine-reported (before the session offset is added) or after adding the offset, or if its text is empty, whitespace-only, or invisible-only Unicode |

```json
{"id":"utt-003","t0":16,"t1":21,"speaker":"P1","text":"Typing feels fine. Now I expect this save button to confirm somehow.","words":[{"w":"Typing","t":16},{"w":"feels","t":16.42}]}
```

## `audio.offset.json`

Written by `transcribe` only when the audio came from an external recording (a `-audio FILE` that is not the session's own `audio.wav`), which is not captured at `t0`. It records the audio→session offset so a later bare `transcribe` (for example, a re-run with a different model that reuses `audio.wav`) recovers the same offset instead of assuming `0`. A session recorded with `testimony record` captures `audio.wav` at `t0` and has no sidecar; its offset is `0`, and an explicit `-offset` on such a session applies to that run alone rather than creating a sidecar. On a session that already has one, an explicit `-offset` rewrites it, so the correction carries into later bare runs. If the sidecar is present but unreadable or malformed, `transcribe` refuses rather than guess, and asks for an explicit `-audio` or `-offset`. The sidecar must be a regular file: `transcribe` refuses to read — or rewrite — one that is a symlink.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `offset_seconds` | number | yes | seconds added to every audio-clock time to place it on the session clock; must not exceed 1e9 seconds in magnitude (`transcribe` refuses otherwise) |
| `provenance` | string | no | how the offset was obtained, for the operator |

## `events.rrweb.jsonl`

One raw [rrweb](https://github.com/rrweb-io/rrweb) event per line, exactly as emitted by the recorder (DOM snapshots, incremental mutations, pointer movement). Archival only: nothing downstream reads it; it exists so full session replay stays possible later. The demo page loads the rrweb recorder from a public CDN, so on a machine without network access (or with the CDN blocked) the file is created but stays empty — the session still captures `interactions.jsonl`, which carries the evidence the pipeline consumes.

## `terminal.cast`

One [asciicast](https://docs.asciinema.org/manual/asciicast/v2/) recording, exactly as the operator's asciinema wrote it — asciicast v2 or v3, as its header's `version` field declares. Archival only: nothing downstream reads it, and it exists so the byte-exact record of the terminal survives alongside the derived records, as `events.rrweb.jsonl` does for a web session.

`import` writes it by copying the file named with `-cast`, and reads it back when `-cast` is omitted, which is what makes re-importing with a corrected `-offset` a one-line command. A cast is read within two bounds: 16 MiB for a single line, and 64 MiB for the whole file; a file past either is refused rather than read in part.

The file is local only, and it holds more than the derived records do: every event the recorder captured, including any keystrokes a recorder run with input capture recorded, which `import` drops rather than normalising. Treat it with the same care as `audio.wav`.

## `timeline.jsonl`

The merged record — one entry per line, speech and interface events on the shared session-relative clock, stably sorted by `t`. This is the single artefact the report (and any later analysis) consumes.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `t` | number | yes | entry time, session-relative seconds; must not exceed 1e9 seconds in magnitude (`report` and `analyze` refuse otherwise; `merge` never writes past this bound) |
| `src` | string | yes | `"speech"` or `"event"` (a closed set: `report` and `analyze` refuse any other value); entry ids must be unique for `analyze`, which resolves cited evidence by id |
| `id` | string | yes | `utt-NNN` (from the transcript) or `ev-NNN` (assigned at merge, in input order) |
| `payload` | object | yes | source-dependent, see below |

Speech payload (`src: "speech"`; `t` is the utterance's `t0`): `t1` (also bounded to ±1e9 seconds in magnitude), `speaker`, `text`, and `words` when present in the transcript.

Event payload (`src: "event"`): `kind`, plus `selector`, `text`, `value`, and `route` — each only when non-empty in the interaction.

```json
{"t":16,"src":"speech","id":"utt-003","payload":{"speaker":"P1","t1":21,"text":"Typing feels fine. Now I expect this save button to confirm somehow.","words":[{"w":"Typing","t":16},{"w":"feels","t":16.42}]}}
{"t":19.2,"src":"event","id":"ev-003","payload":{"kind":"click","route":"#general","selector":"[data-testid=save-btn]","text":"Save"}}
```

## `findings.jsonl`

The analysis layer's output, written by `testimony analyze -ingest` and appended to by `testimony review`. Two record kinds share the file, one per line: a **finding** line (no `kind` field) and a **verdict** line (`kind: "verdict"`). Verdicts are appended, never written in place, so a finding's original state and the full verdict history are retained. Blank lines are ignored.

Ingest validates every finding against the merged timeline and is the sole validation boundary — it never trusts the model. Unknown fields are rejected (the shape is closed), and `status` is forced to `"unverified"` on ingest regardless of the answer JSON.

**Finding record**

| Field | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | yes | `F-NNN`, zero-padded (`^F-\d{3}$`); unique within the file |
| `t` | number | yes | finding time, session-relative seconds; within `[sessionStart, sessionEnd]` (`sessionStart` is `0` unless the timeline holds a negative-time entry from a recording predating `t0`; `sessionEnd` is the latest moment on the timeline — the maximum over all entries, taking an utterance's end (`t1`) and an event's time) |
| `type` | string | yes | one of `bug`, `friction`, `inconsistency`, `preference`, `idea` |
| `severity` | integer | yes | usability-severity scale `1..4`: cosmetic, minor, major, blocker |
| `mode` | string | no | `A` (testing your own application) or `B` (reference capture of a third-party app — see "Coming next" in [`README.md`](../../README.md#status-and-roadmap)), default `A`; only Mode A is produced today |
| `quote` | string | yes | a verbatim substring of the `text` of one *cited* evidence utterance — no normalisation, never joined across utterances |
| `evidence` | array of strings | yes | non-empty, at most 64 ids; every id exists in `timeline.jsonl`; at least one `utt-*` (a spoken anchor) |
| `ui` | object | no | `{selector?, route?}`; when present, each must match a real timeline event's `selector`/`route` |
| `status` | string | yes | always `"unverified"` on ingest |

```json
{"id":"F-001","t":22,"type":"bug","severity":3,"mode":"A","quote":"I clicked save and nothing happened","evidence":["utt-004","ev-003","ev-004"],"ui":{"selector":"[data-testid=save-btn]","route":"#general"},"status":"unverified"}
```

**Verdict record**

| Field | Type | Required | Meaning |
|---|---|---|---|
| `kind` | string | yes | literal `"verdict"` (the discriminator) |
| `finding` | string | yes | an existing finding id in the file |
| `verdict` | string | yes | one of `confirmed`, `rejected`, `duplicate` |
| `of` | string | when `duplicate` | an existing finding id, different from `finding` |
| `at` | string | yes | verdict date, ISO `YYYY-MM-DD` |

```json
{"kind":"verdict","finding":"F-001","verdict":"confirmed","at":"2026-07-17"}
{"kind":"verdict","finding":"F-005","verdict":"duplicate","of":"F-001","at":"2026-07-17"}
```

A finding's effective status starts `unverified`; verdict records apply in file order and the last one for that finding wins.

## `tests.jsonl`

The regression-test drafting layer's output, written by `testimony draft-tests -ingest` and appended to by `testimony review -kind tests`. Two record kinds share the file, one per line: a **draft** line (no `kind` field) and a **decision** line (`kind: "decision"`). Decisions are appended, never written in place, so a draft's original state and the full decision history are retained. Blank lines are ignored.

Ingest validates every draft against `findings.jsonl` and is the sole validation boundary — it never trusts the model. Each draft object is closed: an unknown field in one is rejected rather than dropped, as is an unknown field in a decision's `edit` object. The top-level answer container is not closed — a key beside `rubric` and `tests` is tolerated, mirroring `analyze` — so strictness lands on the records themselves. `status` is forced to `"proposed"` on ingest regardless of the answer JSON, so a draft can never be born accepted. A draft may only ever reference a finding whose effective status is `confirmed`.

**Draft record**

| Field | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | yes | `T-NNN`, zero-padded (`^T-\d{3}$`); unique within the file |
| `finding` | string | yes | an existing finding id in `findings.jsonl` whose effective status is `confirmed` and whose `mode` is not `B` |
| `session` | string | yes | equal to the manifest's `session`, so the link survives the line being copied out of the session directory |
| `title` | string | yes | one line naming the defect; non-empty, at most 200 characters |
| `steps` | array of strings | yes | the reproduction in time order; non-empty, at most 32 entries, every entry non-empty |
| `expected` | string | yes | the behaviour the participant expected; non-empty |
| `observed` | string | yes | what the system did; non-empty |
| `rationale_quote` | string | yes | **equal** to the source finding's `quote`, byte for byte — the drafting step carries evidence forward and never introduces any |
| `severity` | integer | yes | **equal** to the source finding's `severity`, so triage order survives the hand-off unaltered |
| `status` | string | yes | always `"proposed"` on ingest, whatever the answer claims (the answer may omit it; the written record always carries it) |

```json
{"id":"T-001","finding":"F-001","session":"sample-session","title":"Saving gives no confirmation","steps":["Open #general in the settings prototype.","Change the display name to Alice.","Click the Save button ([data-testid=save-btn])."],"expected":"The save is confirmed on screen — a toast, or the button briefly disabled.","observed":"Nothing visibly changes, so there is no way to tell the save landed.","rationale_quote":"I clicked save and nothing happened","severity":3,"status":"proposed"}
```

**Decision record**

| Field | Type | Required | Meaning |
|---|---|---|---|
| `kind` | string | yes | literal `"decision"` (the discriminator) |
| `test` | string | yes | an existing draft id in the file |
| `decision` | string | yes | one of `accepted`, `edited`, `rejected` |
| `at` | string | yes | decision date, ISO `YYYY-MM-DD` |
| `edit` | object | when `edited` | the replacement fields: a subset of `{title, steps, expected, observed}` with at least one member, each held to the draft's own rule for that field |

```json
{"kind":"decision","test":"T-001","decision":"accepted","at":"2026-09-12"}
{"kind":"decision","test":"T-003","decision":"edited","at":"2026-09-12","edit":{"title":"Saving a display name gives no confirmation","steps":["Open #general.","Click Save."]}}
```

The `edit` object is a **closed** four-field subset: an `edit` naming `id`, `finding`, `session`, `severity`, or `rationale_quote` is a hard error, not a silently dropped key. A human edit therefore cannot re-point a draft at a different finding or session; the only way to change the link is to reject the draft and ingest a new one.

A draft's effective status starts `proposed`; decision records apply in file order and the last one for that draft wins. An `edited` draft's rendered fields are the last `edited` decision's `edit` applied over the draft, computed when the plan is rendered — the draft line itself is never rewritten. `testimony draft-tests -render` renders the drafts whose effective status is `accepted` or `edited`.

## `report.md`

Human-readable Markdown rendered from the timeline and findings:

- a header with session name, app, participant, duration (`MM:SS`, the latest moment on the timeline — the maximum over all entries, taking an utterance's end `t1` and an event's time), and utterance/event counts, plus the task list;
- a **Timeline** section: each utterance as `**[MM:SS] <speaker>:** “<text>”` (curly quotes), with the events joined to it — the first utterance (in time) whose span, widened by the report's join window, contains the event — as indented bullets ``[MM:SS] <kind> `<selector>` "<text>" value="…" (<route>)`` (straight quotes, selector in its own code span); events matched by no utterance appear as standalone bullets in time order; every `MM:SS` in `report.md` (including the header's duration) carries a leading `-` for a negative time — one preceding `t0` (a recording predating it, see `t`'s note above) — except a time that rounds to zero, which renders `00:00` unsigned;
- a **Findings** section rendering `findings.jsonl` grouped by effective status (Confirmed, Unverified, Duplicate, Rejected), each group headed with a count and each finding line carrying its id, type, severity, clock, quote, anchor, and any verdict and date. When there is no `findings.jsonl` the section is a short notice pointing at `analyze` and `review`; when the file exists but cannot be read, the section instead reports that `findings.jsonl` could not be read, without the underlying error (`report` still exits `0`).
