---
id: itd-3
slug: codebase-mapping
spec_id: spc-2609152218094570
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: [itd-2, itd-9]
severity: major
---

# Findings Land in the Code, Not Just the Report

## Press Release

> **Testimony maps confirmed findings to the code that owns them.** Once a human has confirmed a finding, the mapping step hands its selector, route, and event window, together with the path to the application's repository, to the operator's chosen model. The model answers with the source locations it believes own that anchor. Testimony checks each answer against the repository, the file exists and the line is in range, and records it as a proposed reference. From a mapped finding, Testimony renders an issue draft: a title, reproduction steps from the event window, the participant's own words as evidence, and the suspected file. A person accepts or rejects each reference, and the decision is kept beside it. Nothing is filed anywhere, and nothing is written into the application's repository.
>
> "The gap was always between 'users struggled here' and 'this file, this handler'," said Alice, the maintainer. "Now a finding comes to me with the selector, the route, the suspect file, and the person's own words. I start fixing instead of reconstructing."

## Why This Matters

Session findings that stop at a report require a second act of translation before anyone can act on them, and that translation is where evidence goes stale. Anchoring interactive elements with stable `data-testid` attributes at build time makes selectors near-deterministic grep targets, so the translation can be delegated to a model and checked by the CLI, turning a qualitative observation into a workable, evidenced issue while the session is still fresh.

The step sits downstream of verification, as regression-test drafting (itd-9) does: only a finding a human has confirmed is mapped, every reference is born a proposal, and the human decision on it is retained beside the machine record. The mapping is the last step of the pipeline, kept separable so it can iterate without destabilising capture or analysis.

## What's In Scope

- Emitting a mapping request over each confirmed finding that carries a selector or route anchor: the anchor, the participant's quote, the event window, and the path to the application's repository.
- Ingesting the answer into a new record family, `refs.jsonl`, beside `findings.jsonl` and `tests.jsonl`: each reference names a confirmed finding and a repo-relative path that exists under the repository, with any line within the file's length, and is born `proposed`.
- A human accept / reject pass over each reference, appended non-destructively as verdicts and test decisions are, so the reference stays linked to its finding and session.
- Rendering an issue draft from a mapped finding: a title, reproduction steps from the event window, the participant's quote, and the suspected file.
- Reading the application's repository only to verify that a returned path exists and a line is in range.

## What's Out of Scope

- Automatic filing of issues into a tracker without human review.
- Anchoring a finding from a terminal session on the command or output text in the timeline; a terminal finding carries no selector or route, and the timeline text keeps its escape sequences, so that is its own draft (itd-2609152113364815).
- A confidence level on a reference. Ingest cannot validate a model-asserted word, and the human decision is the only quality signal.
- Grepping, router-table resolution, or any other reasoning about the application's source inside the CLI; resolution is the host's job.
- Writing anything into the application's repository.
- Native-app accessibility-identifier instrumentation beyond using identifiers already present.
- Mapping for third-party apps; Mode B has no codebase access by definition (itd-4).
- The `data-testid` anchoring convention itself, which the how-to for instrumenting your own app documents.

## Scope Conditions

- The session is a web session and at least one confirmed finding carries a selector or route anchor. A session with none is refused loudly, not mapped. <!-- cond: cond-2609152218105354 -->
- The application's repository is available locally and passed by path. Testimony reads it only to verify that a returned path exists and a line is in range, and never writes into it. <!-- cond: cond-2609152218108484 -->
- The application anchors interactive elements with stable `data-testid` attributes. Without them, resolution rests on selectors that may not survive a rebuild. <!-- cond: cond-2609152218105360 -->
- The host that answers the mapping request is the operator's chosen model. The CLI emits the request, never calls a model, holds no keys, and adds no network dependency. <!-- cond: cond-2609152218106626 -->
- `timeline.jsonl` is present, so each mapped finding's event window resolves for the issue draft. <!-- cond: cond-2609152218107939 -->

## Mechanism

We expect a `data-testid` selector or a route, handed to a model with the repository path, to resolve to the owning component because stable test ids are near-deterministic grep targets in the application's own source. It would be shown wrong by a high reject rate on references, or by references that land on the test suite instead of the component.

## Acceptance Criteria

- **Given** a session with a confirmed finding that carries a selector or route anchor, and a path to the application's repository, **when** the mapping request is emitted, **then** it carries that finding's anchor, quote, event window, and the repository path, and no finding that is unverified, rejected, or duplicate.
- **Given** a mapping answer from the host, **when** it is ingested, **then** each reference that names a confirmed finding and a repo-relative path that exists under the repository, with any line within the file's length, is written to `refs.jsonl` with status `proposed`, and an answer in which any reference fails a check is refused as a whole with every error reported.
- **Given** a mapped finding, **when** an issue draft is produced, **then** it contains a title, reproduction steps from the event window, the participant quote, and the suspected file.
- **Given** a proposed reference, **when** a human accepts or rejects it, **then** the decision is appended to `refs.jsonl` without rewriting the reference line, and the reference stays linked to its finding and session.
- **Given** a session with no confirmed finding that carries a selector or route, **when** the mapping step runs, **then** it refuses with the finding count by status and writes nothing.

## Open Questions

- Does a route anchor resolve as reliably as a test id? A route lives in a router table the model must read and interpret, where a test id is a literal string to search for.
- Should an accepted reference feed the regression-test draft (itd-9), so a test arrives with the file it exercises already named?

## Grounds

- pursued: itd-9 proved the emit, ingest, review, render shape on drafts, and mapping is the last pipeline step that turns a finding into something a developer opens in an editor; it would be shown wrong if reviewers reject most references, or if the issue drafts are not what a maintainer actually files
