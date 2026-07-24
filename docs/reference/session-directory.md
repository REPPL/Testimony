# Session directory reference

Every capture session lives in one directory (by default under `sessions/`):

```
sessions/<timestamp>/
  manifest.json        # session metadata, including t0_epoch_ms (written by demo)
  audio.wav            # 16 kHz mono ASR input (written by transcribe; local only)
  audio.offset.json    # audio→session offset for an external recording (written by transcribe; local only)
  events.rrweb.jsonl   # raw rrweb stream, archival (written by demo)
  interactions.jsonl   # normalised interaction events (written by demo)
  transcript.jsonl     # time-aligned utterances (written by transcribe)
  timeline.jsonl       # merged, session-relative timeline (written by merge)
  findings.jsonl       # analysis findings + appended verdicts (written by analyze/review)
  report.md            # human-readable aligned record (written by report)
```

All `.jsonl` files are JSON Lines: one JSON value per line, blank lines ignored.

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
| `t` | integer | yes | event time, epoch milliseconds |
| `kind` | string | yes | event kind, e.g. `"click"`, `"input"` |
| `selector` | string | no | element anchor, ideally `[data-testid=...]` |
| `text` | string | no | short element label (demo capture truncates to 40 characters) |
| `value` | string | no | new value for input events (demo capture truncates to 80 characters; checkboxes send `"true"`/`"false"`) |
| `route` | string | no | route or hash at the time of the event |

```json
{"t":1784300419200,"kind":"click","selector":"[data-testid=save-btn]","text":"Save","route":"#general"}
```

## `transcript.jsonl`

One utterance per line. Times are session-relative seconds (audio time plus the transcription offset), rounded to two decimal places.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | yes | sequential utterance ID: `utt-001`, `utt-002`, … |
| `t0` | number | yes | utterance start, session-relative seconds |
| `t1` | number | yes | utterance end, session-relative seconds |
| `speaker` | string | no | speaker label; `"P1"` when the engine supplies no diarisation |
| `text` | string | yes | utterance text, whitespace-trimmed (empty segments are dropped) |
| `words` | array | no | word-level alignment (WhisperX only); each element is `{"w": <word>, "t": <start seconds>}` — words the aligner could not time are omitted |

```json
{"id":"utt-003","t0":16.0,"t1":21.0,"speaker":"P1","text":"Now I expect this save button to confirm somehow.","words":[{"w":"Now","t":17.6},{"w":"I","t":17.92}]}
```

## `audio.offset.json`

Written by `transcribe` only when the audio came from an external recording (a `-audio FILE` that is not the session's own `audio.wav`), which is not captured at `t0`. It records the audio→session offset so a later bare `transcribe` (for example, a re-run with a different model that reuses `audio.wav`) recovers the same offset instead of assuming `0`. A session recorded with `testimony record` captures `audio.wav` at `t0` and has no sidecar; its offset is `0`. If the sidecar is present but unreadable or malformed, `transcribe` refuses rather than guess, and asks for an explicit `-audio` or `-offset`.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `offset_seconds` | number | yes | seconds added to every audio-clock time to place it on the session clock |
| `provenance` | string | no | how the offset was obtained, for the operator |

## `events.rrweb.jsonl`

One raw [rrweb](https://github.com/rrweb-io/rrweb) event per line, exactly as emitted by the recorder (DOM snapshots, incremental mutations, pointer movement). Archival only: nothing downstream reads it; it exists so full session replay stays possible later.

## `timeline.jsonl`

The merged record — one entry per line, speech and interface events on the shared session-relative clock, stably sorted by `t`. This is the single artefact the report (and any later analysis) consumes.

| Field | Type | Meaning |
|---|---|---|
| `t` | number | entry time, session-relative seconds |
| `src` | string | `"speech"` or `"event"` |
| `id` | string | `utt-NNN` (from the transcript) or `ev-NNN` (assigned at merge, in input order) |
| `payload` | object | source-dependent, see below |

Speech payload (`src: "speech"`; `t` is the utterance's `t0`): `t1`, `speaker`, `text`, and `words` when present in the transcript.

Event payload (`src: "event"`): `kind`, plus `selector`, `text`, `value`, and `route` — each only when non-empty in the interaction.

```json
{"t":19.2,"src":"event","id":"ev-003","payload":{"kind":"click","route":"#general","selector":"[data-testid=save-btn]","text":"Save"}}
{"t":16,"src":"speech","id":"utt-003","payload":{"speaker":"P1","t1":21,"text":"Now I expect this save button to confirm somehow."}}
```

## `findings.jsonl`

The analysis layer's output, written by `testimony analyze -ingest` and appended to by `testimony review`. Two record kinds share the file, one per line: a **finding** line (no `kind` field) and a **verdict** line (`kind: "verdict"`). Verdicts are appended, never written in place, so a finding's original state and the full verdict history are retained. Blank lines are ignored.

Ingest validates every finding against the merged timeline and is the sole validation boundary — it never trusts the model. Unknown fields are rejected (the shape is closed), and `status` is forced to `"unverified"` on ingest regardless of the answer JSON.

**Finding record**

| Field | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | yes | `F-NNN`, zero-padded (`^F-\d{3}$`); unique within the file |
| `t` | number | yes | finding time, session-relative seconds; within `[sessionStart, sessionEnd]` (the earliest and latest timeline entry times; `sessionStart` is `0` unless the timeline holds negative-time utterances from a recording predating `t0`) |
| `type` | string | yes | one of `bug`, `friction`, `inconsistency`, `preference`, `idea` |
| `severity` | integer | yes | usability-severity scale `1..4`: cosmetic, minor, major, blocker |
| `mode` | string | no | `A` or `B`, default `A`; only Mode A is produced today |
| `quote` | string | yes | a verbatim substring of the `text` of one *cited* evidence utterance — no normalisation, never joined across utterances |
| `evidence` | array of strings | yes | non-empty; every id exists in `timeline.jsonl`; at least one `utt-*` (a spoken anchor) |
| `ui` | object | no | `{selector?, route?}`; when present, each must match a real timeline event's `selector`/`route` |
| `status` | string | yes | always `"unverified"` on ingest |

```json
{"id":"F-001","t":22.0,"type":"bug","severity":3,"mode":"A","quote":"I clicked save and nothing happened","evidence":["utt-004","ev-003","ev-004"],"ui":{"selector":"[data-testid=save-btn]","route":"#general"},"status":"unverified"}
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

## `report.md`

Human-readable Markdown rendered from the timeline and findings:

- a header with session name, app, participant, duration (`MM:SS`, from the last entry), and utterance/event counts, plus the task list;
- a **Timeline** section: each utterance as `**[MM:SS] <speaker>:** "<text>"`, with the events joined to it (within the report's join window) as indented bullets `[MM:SS] <kind> <selector> "<text>" value="…" (<route>)`; events matched by no utterance appear as standalone bullets in time order;
- a **Findings** section rendering `findings.jsonl` grouped by effective status (Confirmed, Unverified, Duplicate, Rejected), each group headed with a count and each finding line carrying its id, type, severity, clock, quote, anchor, and any verdict and date. When there is no `findings.jsonl` the section is a short notice pointing at `analyze` and `review`.
