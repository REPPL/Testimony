---
id: itd-8
slug: local-analysis
spec_id: null
kind: null
suggested_kind: null
reclassification_history: []
builds_on: []
severity: major
---

# Analysis That Never Leaves the Machine

## Press Release

> **Testimony can run its analysis pass entirely on local hardware.** A flag on the analysis surface points the same versioned rubric at a locally hosted model instead of a cloud one, producing the same `findings.jsonl` — same schema, same typing, same `status: unverified` birth state — with no session text leaving the machine. Voice and screen were already local; with this, so is every derived artefact, and the privacy boundary moves from "only derived text leaves" to "nothing leaves".
>
> "The ethics application asks where the data goes, and 'nowhere' is a much shorter answer than a paragraph about derived text," said Carol, session moderator. "Being able to say the whole pipeline runs on one machine in a locked office is what got the protocol through."

## Why This Matters

The local-processing boundary is the pipeline's strongest privacy property and, as the ethics constraints record, the strongest card in a research-ethics application. Today that boundary sits between raw media and derived text: audio and video stay put, but transcript and events reach a cloud model. For most sessions that is a defensible line. For a protocol that will not permit it — or a participant population where it cannot be justified — the whole pipeline becomes unusable at the last step, after all the local capture and transcription work is already done.

Making this a flag on the existing surface rather than a separate mode is deliberate. One analysis code path, one rubric, one findings schema means a local-analysed session is comparable with a cloud-analysed one, and the retained human verdicts measure both against the same bar. The operator chooses per session; the artefacts do not fork.

## What's In Scope

- A flag on the analysis surface selecting a locally hosted model backend, with the cloud path remaining the default.
- Identical output contract: the same rubric version, the same findings schema, and `status: unverified` on every machine-generated finding regardless of backend.
- Recording which backend and model produced a session's findings, so the provenance of a verdict is auditable and the two backends' precision can be compared over time.
- A stated, documented quality floor below which a local backend is not fit for the second-coder role.

## What's Out of Scope

- Local transcription, which is already local by design and is not part of this choice.
- Shipping, vendoring, or managing a local model — the flag points at a backend the operator has already stood up.
- Guaranteeing parity of finding quality between backends; the retained verdicts measure the gap rather than hiding it.
- A local backend for the codebase-mapping step (itd-3), which is a separate agentic surface.

## Acceptance Criteria

- **Given** a merged timeline and a local backend configured, **when** the analysis pass runs with the local flag, **then** `findings.jsonl` is produced under the same rubric and schema as a cloud run, with no session text sent off the machine.
- **Given** a findings file produced by either backend, **when** it is read, **then** the backend and model that produced it are recorded alongside the findings.
- **Given** the local flag is not passed, **when** the analysis pass runs, **then** behaviour is unchanged from today.

## Open Questions

- What is the acceptable quality floor for a local model in the second-coder role, and how is it measured — retained verdict precision against a cloud-analysed control set?
- Can a local model sustain the two-pass structure (segment coding then session-level synthesis), or does synthesis degrade first and need a smaller chunk size?
- Does the local path need its own rubric phrasing to survive a smaller context window, and if so, does that break comparability with cloud runs?

## Audit Notes

_Empty. Populated by intent-fidelity-reviewer when intent moves to shipped/._
