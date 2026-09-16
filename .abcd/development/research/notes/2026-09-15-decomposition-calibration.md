# Decomposition calibration note

Hand-run itd-84 decompositions, graded against the maintainer's confirmation.
One entry per proposal. The corpus this note builds gates the automated rung.

## 2026-09-15: itd-3 codebase mapping (planning interview)

Initial routing, proposed before the interview:

| Part | Type | Home |
|---|---|---|
| Web-anchor mapping: emit over confirmed findings with selector, route, window, repository path; ingest validates each reference into a new record family; human accept or reject | intent | itd-3 |
| Issue draft rendered from a mapped finding | intent | itd-3, or split off |
| Terminal-anchor mapping over timeline text | intent | new draft, or a recorded deferral in itd-3 |
| `data-testid` anchoring convention for the app under test | docs | a how-to page, not a CLI capability |
| Third record family through the shared append primitive and review verb | ADR | refines adr-1 |

Typed links proposed: `builds_on` itd-2 and itd-9; `refines` adr-1; unblocked
by itd-11. No reversal flagged.

Verdict proposed: SPLIT.

Confirmed routing:

| Part | Type | Home | Survived? |
|---|---|---|---|
| Web-anchor mapping | intent | itd-3 | yes |
| Issue draft | intent | itd-3 (kept in; the maintainer chose the draft to render from any mapped finding, not only an accepted reference) | yes, with a scope change |
| Terminal-anchor mapping | intent | new draft itd-2609152113364815, filed in the same session | yes; the maintainer chose the spin-off over the deferral |
| `data-testid` convention | docs | already documented in `docs/how-to/instrument-your-own-app.md`; no new work | routing survived, but the part was already home |
| Third record family | ADR | refines adr-1; recorded in the spec rather than a new ADR, since adr-1 already names the pattern | yes |

Verdict adopted: SPLIT. The initial routing survived confirmation in full; the
two corrections were within a part (the issue draft's trigger) and a
discovery that one part needed no work (the convention doc already exists).
