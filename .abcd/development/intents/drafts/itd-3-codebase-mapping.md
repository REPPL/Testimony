---
id: itd-3
slug: codebase-mapping
spec_id: null
severity: major
---

# Findings Land in the Code, Not Just the Report

## Press Release

> **Testimony maps confirmed findings back to the codebase.** An agentic mapping step resolves each finding's UI anchor — a `data-testid` attribute, a CSS selector, a route, or the command text visible in a terminal cast — to source locations, attaching `code_refs` with a confidence level. Where the evidence supports it, the step drafts an issue: title, reproduction steps reconstructed from the event window, the participant's quote as user evidence, and the suspected file. A tester's spoken "I expected this button to save immediately" arrives in the tracker pointing at the component that owns the button.
>
> "The gap was always between 'users struggled here' and 'this file, this handler'," said Bob, application developer. "Now a finding comes to me with the selector, the route, the suspect file, and the person's own words. I start fixing instead of reconstructing."

## Why This Matters

Session findings that stop at a report require a second act of translation before anyone can act on them, and that translation is where evidence goes stale. Anchoring interactive elements with stable `data-testid` attributes at build time makes selectors near-deterministic grep targets, so the translation can be automated — turning a qualitative observation into a workable, evidenced issue while the session is still fresh. This is the deliberately last and most novel step of the pipeline, kept separable so it can iterate without destabilising capture or analysis.

## What's In Scope

- The `data-testid` build-time convention for our own web apps, documented as the anchoring contract.
- The agentic mapping step: selectors and test IDs grepped directly; routes resolved through the router table; CLI findings anchored on command/flag/output text from the cast stream.
- `code_refs` written back onto findings with `high | medium | low` confidence.
- Drafted issues (title, repro from the event window, quote as evidence, suspected file) for findings that warrant them.

## What's Out of Scope

- Automatic filing of issues into a tracker without human review.
- Native-app accessibility-identifier instrumentation beyond using identifiers already present.
- Mapping for third-party apps — Mode B has no codebase access by definition (itd-4).
- Seeding regression tests from confirmed findings; noted as an open question, not built here.

## Acceptance Criteria

- **Given** a confirmed finding with a `data-testid` selector anchor, **when** the mapping step runs against the app's repository, **then** the finding gains at least one `code_refs` entry with a file path and confidence level.
- **Given** a finding from a CLI session, **when** the mapping step runs, **then** the anchor is resolved from the command or output text in the cast stream to the owning source location.
- **Given** a mapped finding, **when** an issue draft is produced, **then** it contains a title, reproduction steps from the event window, the participant quote, and the suspected file.
