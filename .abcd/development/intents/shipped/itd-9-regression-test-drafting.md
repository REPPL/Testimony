---
id: itd-9
slug: regression-test-drafting
spec_id: spc-2609120417480624
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
---

# A Confirmed Bug Arrives With Its Test Already Written

## Press Release

> **Testimony drafts a regression test case from every confirmed finding.** Once a human has flipped a finding to `confirmed`, the drafting step turns it into a proposed test case: the steps reconstructed from the event window, the behaviour the participant expected, the behaviour they got, and their own words as the rationale. The draft goes to a person to accept, edit, or reject — nothing is written into a suite automatically. A session stops being a report that ages and becomes a test that keeps the bug from coming back.
>
> "A finding tells me something broke once; a test tells me it stays fixed," said Alice, the maintainer. "Getting the steps and the expected behaviour handed to me in the participant's own framing is most of the work of writing the test, and it's the part I always got subtly wrong from memory."

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

## Scope Conditions

- The session holds a `findings.jsonl` with at least one finding whose current <!-- cond: cond-2609120430207432 -->
  verdict is `confirmed`; a session with none is staged loudly rather than
  drafted from.
- `timeline.jsonl` is present, so each confirmed finding's event window <!-- cond: cond-2609120430209331 -->
  resolves — the reproduction steps are reconstructed from it.
- The host that answers the drafting request is the operator's chosen model, as <!-- cond: cond-2609120430205995 -->
  for `analyze`: the CLI emits the request, never calls a model, holds no keys,
  and adds no network dependency.

## Acceptance Criteria

- **Given** a finding whose status is `confirmed`, **when** the drafting step runs, **then** a test case draft is produced containing reproduction steps from the event window, the expected and observed behaviour, and the participant's quote.
- **Given** a finding whose status is `unverified` or `rejected`, **when** the drafting step runs, **then** no test case is drafted for it.
- **Given** a drafted test case, **when** a human accepts or rejects it, **then** the decision is retained and the draft remains linked to its source finding and session.

## Open Questions

- Does the event window alone yield reproduction steps a developer can follow, or does a reliable repro need the keyframe channel as well?
- Where do accepted drafts live — the application's own repository, the docs-as-code test plan, or alongside the session? The architecture note leaves this open.
- Should a drafted test carry the finding's severity through, so triage order survives the hand-off?

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-f14572859148 -->
Fidelity review — receipt rcp-f14572859148 (verifier abcd:intent-auditor claude-opus-5[1m]).

Provenance: abcd:intent-auditor@claude-opus-5[1m] · rubric_hash sha256:6e1ec6201a0891f1863b71cc9b5703ab8681f3f56252128f641c5ea6f81d3fd6 · prompt_hash sha256:462ac0436fbe2acebbb5155ba514eb06880b7ea1edb7a524953461d40e8436a5
Input attestations: diff:origin/main..working tree@sha256:b6447e482347548dcdf5331618a74bf4b48dc29605cdffd69438d7c9878aa924;

Acceptance rollup: MET 2 · MET_WITH_CONCERNS 1 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET_WITH_CONCERNS: A confirmed finding reaches the drafting step with its event window attached (emit.go builds the request from Window over timeline.jsonl) and the ingested draft carries steps, expected, observed and the finding's quote byte-for-byte (validate enforces quote and severity equality); smoke run over examples/sample-session produced exactly such a draft. Concern: the drafting oracle is host-delegated, so the CLI emits a request and validates an answer rather than drafting in-process, and the boundary enforces only non-emptiness of steps — that the steps actually derive from the event window is instructed in the rubric, never verified.
  evidence: internal/drafttests/emit.go:152
  evidence: internal/drafttests/window.go:37
  evidence: internal/drafttests/drafttests.go:60
  evidence: internal/drafttests/ingest.go:413
  evidence: internal/drafttests/ingest.go:387
  evidence: examples/sample-session/tests.jsonl:1
- ac-2 — MET: Eligibility is enforced twice over the same effective-status computation: eligible() admits only findings whose current verdict is confirmed (so a rejected, unverified or duplicate finding is omitted from the emitted request) and validate refuses any ingested draft naming a finding not in that set; a run over the sample session emitted only F-001 and refused drafts of F-002 (unverified), F-003 (rejected) and F-005 (duplicate), writing no tests.jsonl.
  evidence: internal/drafttests/drafttests.go:327
  evidence: internal/drafttests/emit.go:48
  evidence: internal/drafttests/ingest.go:371
  evidence: internal/drafttests/ingest_test.go:1
- ac-3 — MET: A decision is an appended, non-destructive record written through session.AppendRecord — the draft line is never rewritten — and the draft's link fields are unreachable by any later write: Edit is a closed {title,steps,expected,observed} subset decoded with DisallowUnknownFields, and commitDrafts refuses to overwrite a tests.jsonl that already holds decisions. Smoke run: two decisions on T-001 appended as lines 2 and 3 with the draft line byte-identical, an edit naming "finding" refused, and a re-ingest refused.
  evidence: internal/drafttests/review.go:390
  evidence: internal/drafttests/drafttests.go:86
  evidence: internal/drafttests/review.go:146
  evidence: internal/drafttests/ingest.go:157
  evidence: internal/session/records.go:31
  evidence: examples/sample-session/tests.jsonl:4

Gap audit:
- honoured:
  - Drafting a test case from a confirmed finding: reproduction steps derived from the finding's event window, expected versus observed behaviour, and the participant quote as rationale.
    evidence: internal/drafttests/window.go:37
    evidence: internal/drafttests/emit.go:152
    evidence: internal/drafttests/drafttests.go:60
  - A human accept / edit / reject pass over each drafted test, with the decision retained as the verdicts already are.
    evidence: internal/drafttests/review.go:37
    evidence: internal/drafttests/review.go:390
    evidence: internal/session/records.go:31
  - Linking the drafted test back to its source finding and session, so a failing test leads to the evidence that motivated it.
    evidence: internal/drafttests/ingest.go:376
    evidence: internal/drafttests/drafttests.go:86
    evidence: internal/drafttests/render.go:11
  - Emitting the draft in a form the docs-as-code manual test records can hold.
    evidence: internal/drafttests/render.go:23
    evidence: internal/drafttests/testdata/tests.md:1
    evidence: docs/how-to/draft-regression-tests.md:1
  - Nothing is written into a suite automatically: every draft is born a proposal and the model's claimed status is laundered away.
    evidence: internal/drafttests/ingest.go:121
    evidence: internal/drafttests/render.go:15
  - The CLI never calls a model, holds no keys, and adds no network dependency.
    evidence: go.mod:1
    evidence: internal/drafttests/drafttests.go:18
    evidence: internal/drafttests/emit.go:36
- diverged:
  - "the drafting step turns it into a proposed test case" — delivered as a two-phase host-delegated exchange (emit a request, then ingest and validate an answer) rather than one in-process step; the operator must run a model between the two halves.
    evidence: internal/drafttests/emit.go:36
    evidence: internal/drafttests/ingest.go:41
    evidence: internal/cli/cli.go:435
  - "the steps reconstructed from the event window" — the window is supplied and the rubric forbids inventing steps, but the validation boundary checks only that steps is non-empty and within bounds; step provenance from the window is never verified, so only quote and severity are evidence-locked.
    evidence: internal/drafttests/emit.go:73
    evidence: internal/drafttests/ingest.go:387
    evidence: internal/drafttests/ingest.go:34
  - "Reference-capture findings (itd-4), which are design preferences rather than defects" out of scope — implemented as a mode != B guard that the code itself records as dead today, while a confirmed mode-A finding of type preference or idea stays eligible because type is deliberately not filtered.
    evidence: internal/drafttests/drafttests.go:322
    evidence: internal/drafttests/drafttests.go:331
- missing: (none)

Scope-condition dispositions:
- cond-2609120430207432 — survived: Both write paths check eligibility before doing anything else and a session with no confirmed finding is refused loudly with the by-status tally and the review hint, writing nothing; a run with the sample's verdicts stripped exited 1 on emit and on ingest with "5 findings: 0 confirmed, 5 unverified, 0 duplicate, 0 rejected".
  evidence: internal/drafttests/drafttests.go:397
  evidence: internal/drafttests/drafttests.go:405
  evidence: internal/drafttests/emit.go:49
  evidence: internal/drafttests/ingest.go:55
- cond-2609120430209331 — narrowed: Only the emit path reads timeline.jsonl (and refuses with a "run merge first" hint when it is absent); ingest, review and render need no timeline at all, and a confirmed finding whose cited evidence ids match no entry does not fail — Window falls back to a time-centred span around f.T, which can be empty.
  narrowing: Holds for `draft-tests` emit only — ingest/review/render ran successfully with timeline.jsonl deleted — and "the event window resolves" means a window is always produced, not that the finding's cited evidence actually resolved: unresolvable evidence silently degrades to [f.T-window, f.T+window].
  evidence: internal/drafttests/emit.go:56
  evidence: internal/drafttests/window.go:63
  evidence: internal/drafttests/ingest.go:35
  evidence: .github/workflows/ci.yml:124
- cond-2609120430205995 — survived: The drafting layer is stdlib-plus-internal only and the module still declares no dependency; EmitRequest returns the request as text for the operator's chosen host and Ingest reads the answer from a file or stdin, so no model is called and no key is held on this path.
  evidence: go.mod:1
  evidence: internal/drafttests/drafttests.go:1
  evidence: internal/drafttests/emit.go:3
  evidence: internal/cli/cli.go:482
## Grounds

- pursued: a confirmed finding's event window plus the participant's own words is enough for a host model to draft a followable regression test, and a human accept/edit/reject pass keeps every draft a proposal; what would show it wrong is drafts whose steps a developer cannot follow from the window alone, which the edited-decision rate on real sessions would reveal
