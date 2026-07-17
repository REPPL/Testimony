# Session artefact schemas

The on-disk contract, as defined in `internal/session` and
`internal/timeline`. A session directory contains:

```
sessions/<timestamp>/
  manifest.json         # session metadata, including t0_epoch_ms
  audio.wav             # 16 kHz mono ASR input, written by transcribe (local only)
  events.rrweb.jsonl    # raw rrweb events (archival; web sessions only)
  interactions.jsonl    # normalised interaction events (epoch ms)
  transcript.jsonl      # word-aligned utterances (session-relative seconds)
  timeline.jsonl        # merged, session-relative timeline
  report.md             # human-readable session report
```

The bundled sample (`examples/sample-session/`) is the reference instance;
schema changes update code, sample, and tests together
([invariants](../02-constraints/03-invariants.md)).

## `manifest.json` (`session.Manifest`)

| Field | Type | Notes |
|---|---|---|
| `session` | string | directory basename |
| `app` | string | optional |
| `commit` | string | optional |
| `participant` | string | optional pseudonym (`P1`, …) |
| `t0_epoch_ms` | int64 | the clock anchor: `relative_seconds = (epoch_ms − t0_epoch_ms) / 1000` |
| `tasks` | []string | optional |
| `notes` | string | optional |

## `transcript.jsonl` — one `Utterance` per line

| Field | Type | Notes |
|---|---|---|
| `id` | string | `utt-NNN`, sequential |
| `t0`, `t1` | float64 | session-relative seconds, 2 dp |
| `speaker` | string | optional; defaults to `P1` without diarisation |
| `text` | string | whitespace-trimmed |
| `words` | []Word | optional; each `{"w": string, "t": float64}` — word start time, session-relative |

```json
{"id":"utt-034","t0":128.42,"t1":131.90,"speaker":"P1",
 "text":"I expected this button to save immediately",
 "words":[{"w":"I","t":128.42},{"w":"expected","t":128.61}]}
```

## `interactions.jsonl` — one `Interaction` per line

| Field | Type | Notes |
|---|---|---|
| `t` | int64 | **epoch milliseconds** (the only epoch-time artefact) |
| `kind` | string | e.g. `click`, `input` |
| `selector` | string | optional; `data-testid`-based where the convention holds |
| `text` | string | optional element text |
| `value` | string | optional input value |
| `route` | string | optional |

## `timeline.jsonl` — one `Entry` per line

| Field | Type | Notes |
|---|---|---|
| `t` | float64 | session-relative seconds |
| `src` | string | `"speech"` \| `"event"` |
| `id` | string | utterance ID, or `ev-NNN` assigned sequentially at merge |
| `payload` | object | speech: `t1`, `speaker`, `text`, optional `words`; event: `kind` plus whichever of `selector`/`text`/`value`/`route` are non-empty |

```json
{"t":128.42,"src":"speech","id":"utt-034","payload":{"t1":131.9,"speaker":"P1","text":"I expected this button to save immediately"}}
{"t":129.01,"src":"event","id":"ev-001","payload":{"kind":"click","selector":"[data-testid=save-btn]","text":"Save","route":"/settings"}}
```

`findings.jsonl` (the analysis layer's output) is designed but not yet
produced — schema in
[`../01-product/04-analysis.md`](../01-product/04-analysis.md).
