---
id: itd-2
slug: analysis-findings
spec_id: spc-2
severity: major
kind: standalone
---

# From Timeline to Findings, With a Human Holding the Verdict

## Press Release

> **Testimony turns a merged session timeline into structured findings.** An analysis pass reads `timeline.jsonl` chunked by task boundaries and codes it under a fixed, versioned rubric, producing `findings.jsonl` — each finding typed as `bug | friction | inconsistency | preference | idea`, anchored to a timestamp, quoting the participant's own words, and citing its evidence utterances and events. Every AI-generated finding is born `status: unverified`; a verification pass lets a human flip each one to `confirmed | rejected | duplicate`, and the verdicts are retained so precision can be tracked over time.
>
> "The transcription and first-pass coding used to be the week of work that meant I only ever analysed one session in five," said Alice, usability researcher. "Now the machine does the first pass overnight, and my job is the judgement call — which is the part I actually wanted to keep."

## Why This Matters

The literature on LLM analysis of think-aloud data is consistent: machines are effective first-pass coders that both miss and occasionally invent problems. The economic win is real — transcription and coding were historically the expensive part — but only if the human retains final judgement. Building unverified-by-default into the data model, rather than into a workflow document, makes the second-coder stance structural: no finding can be mistaken for a confirmed one, and the accumulated verdicts become an ongoing measure of the analyser's precision.

## What's In Scope

- The analysis pass over `timeline.jsonl`: segment-level coding then session-level synthesis (deduplication, severity, cross-task patterns), under a versioned rubric.
- The findings schema: id, timestamp, type (`bug | friction | inconsistency | preference | idea`), severity, mode, participant quote, evidence references (utterance and event IDs), UI anchor, `status`.
- `status: unverified` as the birth state of every machine-generated finding.
- A human verification pass (report-driven or simple TUI) recording `confirmed | rejected | duplicate` verdicts, retained in `findings.jsonl`.
- On-demand keyframe requests when the transcript alone is ambiguous ("this thing here").

## What's Out of Scope

- Mapping findings to source code and drafting issues (itd-3).
- Mode B preference-elicitation rubric and the pattern library (itd-4).
- Any claim of statistical significance; findings are qualitative signals.
- Automated re-analysis or self-tuning of the rubric from verdict history.

## Acceptance Criteria

- **Given** a session with a merged `timeline.jsonl`, **when** the analysis pass runs, **then** `findings.jsonl` exists and every finding carries a type from the five-value set, a timestamp, a participant quote, evidence references, and `status: unverified`.
- **Given** an unverified finding, **when** Alice records a verdict in the verification pass, **then** the finding's status becomes `confirmed`, `rejected`, or `duplicate` and the verdict persists in the session artefacts.
- **Given** an ambiguous utterance, **when** the analysis pass requests a keyframe, **then** the frame extracted at the utterance timestamp is attached as evidence rather than raw video being consumed wholesale.
