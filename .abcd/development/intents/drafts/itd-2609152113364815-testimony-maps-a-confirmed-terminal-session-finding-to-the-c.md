---
id: itd-2609152113364815
slug: testimony-maps-a-confirmed-terminal-session-finding-to-the-c
spec_id: null
kind: null
suggested_kind: null
reclassification_history: []
builds_on: [itd-3, itd-11]
severity: minor
impact: additive
origin: researcher-authored
production_mode: hand-written
---

# Testimony maps a confirmed terminal-session finding to the command that produced it

## Press Release

> _Seeded from a quoted-text intent capture. Expand into the full press-release narrative before planning._

## Why This Matters

Split from itd-3 (codebase mapping) during its planning interview on 2026-09-15. Web findings carry a selector or route anchor; a terminal finding carries neither, so the mapping step for terminal sessions is a different problem and is recorded on its own rather than deferred silently inside itd-3.

## Mechanism

> _Prompted (the claim-recording gradient): why the authors expect this to work, as a falsifiable "we expect X because Y" — not the outcome restated. Replace this line with the claim, or with the exact token `None stated.` alone on its line to record the claim as considered and declined._

## Scope Conditions

> _Required (the claim-recording gradient): the population, platform, scale, or assumptions this claim holds under, one per top-level bullet — `abcd intent plan` stamps each with a persistent identity. Replace this line with those bullets, or with the exact token `None stated.` alone on its line._

## Acceptance Criteria

> _Required (the itd-1 discipline): add at least one Given-When-Then bullet describing the verifiable bar for "shipped" before this draft can be planned._

## Open Questions

- A terminal finding has no `ui` anchor field: `terminal_output` timeline records carry only `t`, `kind`, and `text`. The only anchor is the finding's quote and its cited evidence ids, so what the request hands the host, and what ingest can verify, both need deciding.
- Timeline text keeps its escape sequences verbatim and is stripped only at `report`'s render boundary. Anything that searches the text for a command string must strip control bytes first, and nothing does so today.
- `terminal.cast` is archival: nothing downstream reads it. Anchoring must work from the timeline, not the cast.

## Audit Notes

_Empty. Populated by intent-auditor when intent moves to shipped/._
