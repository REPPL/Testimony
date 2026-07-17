# Analysis layer (planned — Phase 2+)

Not yet implemented: today [`report`](../04-surfaces/04-report.md) renders the
raw aligned record with a pending Findings section. This file states the
design; full detail is in the preserved note
([§7–§8](../../research/2026-07-17-architecture-note.md)).

## Findings

The timeline, chunked by task boundaries from the manifest, is analysed by an
LLM under a fixed, versioned rubric in two passes: segment-level coding, then
session-level synthesis (deduplication, severity, cross-task patterns).
Findings are structured (`findings.jsonl`):

```json
{"id":"F-012","t":129.0,"type":"bug",
 "severity":2,"mode":"A",
 "quote":"I expected this button to save immediately",
 "evidence":["utt-034","ev-482"],
 "ui":{"selector":"[data-testid=save-btn]","route":"/settings"},
 "code_refs":[{"file":"src/components/SettingsForm.tsx","symbol":"handleSave","confidence":"high"}],
 "status":"unverified"}
```

`type` is one of `bug | friction | inconsistency | preference | idea`. Every
finding starts `unverified`; a human verification pass flips it to
`confirmed | rejected | duplicate`, and the verdicts are retained.

When the transcript is ambiguous ("this thing here"), the analysis step may
request a **keyframe** — a video frame extracted at the utterance timestamp
(`ffmpeg -ss`) — and use a multimodal model to identify the referent.
Keyframes are the fallback evidence channel; transcript + events is cheaper,
more precise, and more auditable.

## Codebase mapping

A separate, deliberately last and separable agentic step resolves each
finding's UI anchor to code: `data-testid` selectors are grepped directly,
routes map through the router table, component names come from DOM structure.
Output is `code_refs` with a confidence level, plus optionally a drafted
issue. For CLIs the anchor is the command/flag/output text visible in the
event stream; for native apps, accessibility identifiers or the keyframe.

## Mode B: the pattern library

In Mode B the rubric shifts from defect-finding to preference elicitation —
`{pattern, app, liked|disliked, why, screenshot_ref, applicability}` —
accumulating into a pattern library of tagged patterns with keyframes.
Recordings of third-party apps are for private research/reference use;
keyframes stay in the private corpus, not in published material.
