# Session artefact schemas

The on-disk contract, as defined in `internal/session` and
`internal/timeline`. A session directory contains:

```
sessions/<timestamp>/
  manifest.json         # session metadata, including t0_epoch_ms
  audio.wav             # 16 kHz mono ASR input (captured by record, or converted by transcribe -audio; local only)
  audio.offset.json     # audio→session offset for an external recording (written by transcribe; local only)
  screen.mp4            # screen capture (written by record -video; local only)
  events.rrweb.jsonl    # raw rrweb events (archival; web sessions only)
  interactions.jsonl    # normalised interaction events (epoch ms)
  transcript.jsonl      # word-aligned utterances (session-relative seconds)
  timeline.jsonl        # merged, session-relative timeline
  findings.jsonl        # analysis findings + appended verdicts (written by analyze/review)
  tests.jsonl           # regression-test drafts + appended decisions (written by draft-tests/review)
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
{"id":"utt-034","t0":128.42,"t1":131.9,"speaker":"P1",
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

## `findings.jsonl` — findings plus appended verdicts

The analysis layer's output, written by [`analyze -ingest`](../04-surfaces/06-analyze.md)
and [`review`](../04-surfaces/07-review.md). Two record kinds share the file, one
per line. A finding line carries no `kind`; a verdict line is discriminated by
`kind: "verdict"`. Verdicts are **appended, never in-place rewrites**, so the
finding's birth state and full decision history survive as the precision measure
([note §2](../../research/2026-07-17-architecture-note.md)). Ingest decodes each
finding with unknown fields disallowed — the shape is closed — and is the sole
validation boundary; every field below is checked, and `status` is forced to
`"unverified"` on ingest whatever the answer JSON claims.

**Finding record** (`analyze.Finding`):

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string | yes | `^F-\d{3}$` (F-NNN, zero-padded); unique within the file |
| `t` | float64 | yes | finding time, session-relative seconds; `sessionStart ≤ t ≤ sessionEnd`, where `sessionStart` is `0` unless the timeline holds a negative-time entry (a recording predating `t0`) and `sessionEnd` is the latest moment on the timeline — the maximum over all entries, taking an utterance's end (`t1`) rather than its start, and an event's time |
| `type` | string | yes | one of `bug \| friction \| inconsistency \| preference \| idea` |
| `severity` | int | yes | Nielsen-style `1..4` (cosmetic, minor, major, blocker) |
| `mode` | string | no | `A \| B`, default `A`; only Mode A is produced in this slice |
| `quote` | string | yes | non-empty; a **verbatim** substring of the `text` of one *cited* evidence utterance (no normalisation, no joining across utterances) |
| `evidence` | []string | yes | non-empty, at most 64 ids; every id exists in `timeline.jsonl`; at least one `utt-*` (a spoken anchor) |
| `ui` | object | no | `{selector?, route?}`; when present, each must match a real timeline event's `selector`/`route` |
| `status` | string | yes | always `"unverified"` on ingest (the model is never trusted) |

```json
{"id":"F-001","t":22,"type":"bug","severity":3,"mode":"A",
 "quote":"I clicked save and nothing happened","evidence":["utt-004","ev-003","ev-004"],
 "ui":{"selector":"[data-testid=save-btn]","route":"#general"},"status":"unverified"}
```

**Verdict record** (`analyze.Verdict`):

| Field | Type | Required | Notes |
|---|---|---|---|
| `kind` | string | yes | literal `"verdict"` — the discriminator |
| `finding` | string | yes | an existing finding id in the file |
| `verdict` | string | yes | one of `confirmed \| rejected \| duplicate` |
| `of` | string | when duplicate | an existing finding id, `≠ finding` |
| `at` | string | yes | ISO date `YYYY-MM-DD` |

```json
{"kind":"verdict","finding":"F-001","verdict":"confirmed","at":"2026-07-17"}
{"kind":"verdict","finding":"F-005","verdict":"duplicate","of":"F-001","at":"2026-07-17"}
```

Effective status: every finding starts `unverified`; verdict records apply in
file order and the last one for a finding wins. A verdict whose `verdict` value
is outside the closed enum (a typo, an empty string, or a foreign value in a
shared session) is ignored, so its finding keeps `unverified` and still appears
in the report and review queue rather than vanishing into an unrendered group.
Design and rationale:
[`../01-product/04-analysis.md`](../01-product/04-analysis.md).

## `tests.jsonl` — test drafts plus appended decisions

The drafting layer's output, written by
[`draft-tests -ingest`](../04-surfaces/08-draft-tests.md) and
[`review -kind tests`](../04-surfaces/07-review.md). Two record kinds share the
file, one per line. A draft line carries no `kind`; a decision line is
discriminated by `kind: "decision"`. Decisions are **appended, never in-place
rewrites**, so the drafted proposal and the human's decision both survive and a
draft's link to its source finding and session is unreachable by any later write.
Ingest decodes each draft with unknown fields disallowed — the shape is closed —
and is the sole validation boundary; `status` is forced to `"proposed"` on ingest
whatever the answer JSON claims.

**Draft record** (`drafttests.Draft`):

| Field | Type | Required | Notes |
|---|---|---|---|
| `id` | string | yes | `^T-\d{3}$` (T-NNN, zero-padded); unique within the file |
| `finding` | string | yes | an existing finding id whose **effective status is `confirmed`** and whose `mode` is not `B` |
| `session` | string | yes | equal to the manifest's `session`, so the link survives the line being copied out of the directory |
| `title` | string | yes | non-empty, at most 200 characters |
| `steps` | []string | yes | non-empty, at most 32 entries, every entry non-empty; in time order |
| `expected` | string | yes | non-empty; the behaviour the participant expected |
| `observed` | string | yes | non-empty; what the system did |
| `rationale_quote` | string | yes | **equal** to the source finding's `quote` (compared in `SafeText` form) |
| `severity` | int | yes | **equal** to the source finding's `severity` |
| `status` | string | yes | always `"proposed"` on ingest (the model is never trusted) |

```json
{"id":"T-001","finding":"F-001","session":"sample-session",
 "title":"Saving gives no confirmation",
 "steps":["Open #general in the settings prototype.","Click the Save button ([data-testid=save-btn])."],
 "expected":"The save is confirmed on screen.","observed":"Nothing visibly changes.",
 "rationale_quote":"I clicked save and nothing happened","severity":3,"status":"proposed"}
```

The reference field is named after the kind of record it points at — as the
verdict's `finding` and the decision's `test` are — so one convention covers all
three rather than a second spelling of the same idea.

**Decision record** (`drafttests.Decision`):

| Field | Type | Required | Notes |
|---|---|---|---|
| `kind` | string | yes | literal `"decision"` — the discriminator |
| `test` | string | yes | an existing draft id in the file |
| `decision` | string | yes | one of `accepted \| edited \| rejected` |
| `at` | string | yes | ISO date `YYYY-MM-DD` |
| `edit` | object | when edited | a **closed** subset of `{title, steps, expected, observed}` with at least one member, each held to the draft's own rule for that field |

```json
{"kind":"decision","test":"T-001","decision":"accepted","at":"2026-09-12"}
{"kind":"decision","test":"T-003","decision":"edited","at":"2026-09-12","edit":{"title":"Saving a display name gives no confirmation","steps":["Open #general.","Click Save."]}}
```

`edit` is decoded with `DisallowUnknownFields`, so one naming `id`, `finding`,
`session`, `severity`, or `rationale_quote` is a hard error rather than a silently
dropped key: there is no path through which a human edit can re-point a draft at
a different finding or session. The only way to change the link is to reject the
draft and ingest a new one.

Effective status: every draft starts `proposed`; decision records apply in file
order and the last one for a draft wins. A decision naming an unknown draft, or
carrying a value outside the closed enum, is ignored rather than applied — a draft
in an unrenderable status would otherwise vanish from both the review walk and the
render. An `edited` draft's rendered fields are the last `edited` decision's
`edit` applied over the draft, computed at render time. Design and rationale:
[`../04-surfaces/08-draft-tests.md`](../04-surfaces/08-draft-tests.md).
