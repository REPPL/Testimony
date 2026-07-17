# Phases

| Phase | Deliverable | Status |
|---|---|---|
| 0 — manual pilot | One self-recorded session; sync quality and join window validated by hand before tooling | Done |
| 1 — the CLI | `record`, `transcribe`, `merge`, `report` as a single static binary; sessions become one command | **Mostly done**: `transcribe`, `merge`, `report` shipped, plus `demo` (capture testbed, an addition to the original list); `record` is the remaining stub |
| 2 — analysis layer | Rubric, finding schema, chunking, report generation; verification-pass workflow | Designed ([`../01-product/04-analysis.md`](../01-product/04-analysis.md)), not started |
| 3 — codebase mapping | `data-testid` convention in our own apps; agentic mapping step; issue drafting | Convention adopted in the demo app; mapping not started |
| 4 — Mode B | Keyframe extraction + multimodal identification; pattern-library format; optional rrweb browser extension | Not started |
| 5 — participant-ready | Consent templates, pseudonymisation, retention automation; moderated-session support (diarisation on) | Not started |

Phases 0–2 deliver standalone value even if 3–5 never happen. The macOS app
layer wrapping the CLI core sits beyond this table; Linux remains CLI-only
throughout.
