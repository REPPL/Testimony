# Analysis layer

The first-pass analysis layer is shipped: [`analyze`](../04-surfaces/06-analyze.md)
turns a merged timeline into structured findings and [`review`](../04-surfaces/07-review.md)
records the human verdicts, with [`report`](../04-surfaces/04-report.md) rendering
both. This file states the design; full detail is in the preserved note
([§7–§8](../../research/2026-07-17-architecture-note.md)) and the surface pages.
Codebase mapping, keyframes, and Mode B remain planned (below).

## Findings

The timeline is analysed by a model under a fixed, versioned rubric
(`testimony-analysis/v1`) in two passes: segment-level coding, then session-level
synthesis (deduplication, severity, cross-task patterns). The oracle is
**host-delegated** — the CLI emits a self-contained request and never calls a
model itself; a host runs it and the CLI validates the JSON answer. Findings are
structured (`findings.jsonl`):

```json
{"id":"F-001","t":22.0,"type":"bug","severity":3,"mode":"A",
 "quote":"I clicked save and nothing happened",
 "evidence":["utt-004","ev-003","ev-004"],
 "ui":{"selector":"[data-testid=save-btn]","route":"#general"},
 "status":"unverified"}
```

`type` is one of `bug | friction | inconsistency | preference | idea`;
`severity` is a Nielsen-style integer `1..4` (cosmetic, minor, major, blocker).
Every finding starts `unverified`; a human verification pass records
`confirmed | rejected | duplicate`, and the verdicts are retained. The full field
rules and the append-only verdict record are on the schemas page
([`../05-internals/02-schemas.md`](../05-internals/02-schemas.md)); the request
and validation flow is [`06-analyze.md`](../04-surfaces/06-analyze.md), the
verdict flow [`07-review.md`](../04-surfaces/07-review.md).

Ingest is the sole validation boundary: it never trusts the model. Fabricated
evidence, quotes not spoken verbatim in a cited utterance, bad enums,
out-of-range severity, phantom selectors, and stray fields are all rejected with
a precise error, and every finding lands `unverified` whatever the JSON claims.

**Chunking (flagged divergence).** The note and this page assumed the timeline
could be chunked "by task boundaries from the manifest", but `timeline.jsonl`
carries no task markers and the manifest task list has no timestamps, so the
mapping is not derivable from the data. v1 emits the whole timeline with the task
list as context and defers real chunking behind a seam.

## Keyframes (planned)

When the transcript is ambiguous ("this thing here"), the analysis step may
request a **keyframe** — a video frame extracted at the utterance timestamp
(`ffmpeg -ss`) — and use a multimodal model to identify the referent. In the
shipped slice the rubric lets the model *flag* a verbal-only ambiguous referent,
but frame extraction (which needs local video and a multimodal pass) is deferred
to a later intent. Keyframes are the fallback evidence channel; transcript +
events is cheaper, more precise, and more auditable.

## Codebase mapping (planned)

A separate, deliberately last and separable agentic step resolves each finding's
UI anchor to code: `data-testid` selectors are grepped directly, routes map
through the router table, component names come from DOM structure. Output is
`code_refs` with a confidence level, plus optionally a drafted issue. (`code_refs`
is not part of the shipped findings schema — the closed schema rejects it — and
arrives with this step.) For CLIs the anchor is the command/flag/output text
visible in the event stream; for native apps, accessibility identifiers or the
keyframe.

## Mode B: the pattern library (planned)

In Mode B the rubric shifts from defect-finding to preference elicitation —
`{pattern, app, liked|disliked, why, screenshot_ref, applicability}` —
accumulating into a pattern library of tagged patterns with keyframes.
Recordings of third-party apps are for private research/reference use;
keyframes stay in the private corpus, not in published material.
