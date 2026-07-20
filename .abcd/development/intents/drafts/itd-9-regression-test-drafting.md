---
id: itd-9
slug: regression-test-drafting
spec_id: null
kind: null
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
---

# A Confirmed Bug Arrives With Its Test Already Written

## Press Release

> **Testimony drafts a regression test case from every confirmed finding.** Once a human has flipped a finding to `confirmed`, the drafting step turns it into a proposed test case: the steps reconstructed from the event window, the behaviour the participant expected, the behaviour they got, and their own words as the rationale. The draft goes to a person to accept, edit, or reject — nothing is written into a suite automatically. A session stops being a report that ages and becomes a test that keeps the bug from coming back.
>
> "A finding tells me something broke once; a test tells me it stays fixed," said Bob, application developer. "Getting the steps and the expected behaviour handed to me in the participant's own framing is most of the work of writing the test, and it's the part I always got subtly wrong from memory."

## Why This Matters

The pipeline's value decays at the end: a confirmed finding becomes an issue, the issue gets fixed, and the evidence that motivated it is never exercised again. Meanwhile the reproduction steps — which are the expensive, error-prone part of writing a regression test — are already sitting in the timeline as a precise event window with a spoken account of what the person expected. Drafting the test while that evidence is intact converts a one-off observation into a standing guarantee.

Keeping the human in the loop mirrors the stance the rest of the pipeline takes. Findings are unverified until a person confirms them; a test drafted from a finding is likewise a proposal, not a commit. The step is deliberately downstream of verification, so only evidence a human already vouched for can become a test.

## What's In Scope

- Drafting a test case from a `confirmed` finding: reproduction steps derived from the finding's event window, expected versus observed behaviour, and the participant quote as rationale.
- A human accept / edit / reject pass over each drafted test, with the decision retained as the verdicts already are.
- Linking the drafted test back to its source finding and session, so a failing test leads to the evidence that motivated it.
- Emitting the draft in a form the docs-as-code manual test records can hold, so a session becomes evidence attached to a test run.

## What's Out of Scope

- Generating executable test code for the application under test; that needs its test framework and conventions, and is a further step.
- Running, filing, or committing tests automatically — the output is a draft for review.
- Drafting from unverified or rejected findings; only human-confirmed findings are eligible.
- Reference-capture findings (itd-4), which are design preferences rather than defects and have nothing to regress against.

## Acceptance Criteria

- **Given** a finding whose status is `confirmed`, **when** the drafting step runs, **then** a test case draft is produced containing reproduction steps from the event window, the expected and observed behaviour, and the participant's quote.
- **Given** a finding whose status is `unverified` or `rejected`, **when** the drafting step runs, **then** no test case is drafted for it.
- **Given** a drafted test case, **when** a human accepts or rejects it, **then** the decision is retained and the draft remains linked to its source finding and session.

## Open Questions

- Does the event window alone yield reproduction steps a developer can follow, or does a reliable repro need the keyframe channel as well?
- Where do accepted drafts live — the application's own repository, the docs-as-code test plan, or alongside the session? The architecture note leaves this open.
- Should a drafted test carry the finding's severity through, so triage order survives the hand-off?

## Audit Notes

_Empty. Populated by intent-fidelity-reviewer when intent moves to shipped/._
