# Session directory reference

Every capture session lives in one directory (by default under `sessions/`):

```
sessions/<timestamp>/
  manifest.json        # session metadata, including t0_epoch_ms (written by demo)
  audio.wav            # 16 kHz mono ASR input (written by transcribe; local only)
  events.rrweb.jsonl   # raw rrweb stream, archival (written by demo)
  interactions.jsonl   # normalised interaction events (written by demo)
  transcript.jsonl     # time-aligned utterances (written by transcribe)
  timeline.jsonl       # merged, session-relative timeline (written by merge)
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

## `report.md`

Human-readable Markdown rendered from the timeline:

- a header with session name, app, participant, duration (`MM:SS`, from the last entry), and utterance/event counts, plus the task list;
- a **Timeline** section: each utterance as `**[MM:SS] <speaker>:** "<text>"`, with the events joined to it (within the report's join window) as indented bullets `[MM:SS] <kind> <selector> "<text>" value="…" (<route>)`; events matched by no utterance appear as standalone bullets in time order;
- a **Findings** section, currently a placeholder for the analysis layer.
