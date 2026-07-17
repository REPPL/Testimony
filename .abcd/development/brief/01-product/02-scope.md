# Scope

## In scope, by phase

The phased plan (status detail in
[`../06-delivery/01-phases.md`](../06-delivery/01-phases.md)):

1. **Phase 0 — manual pilot.** One self-session, tooling run by hand, to
   validate sync quality and the join window. Done.
2. **Phase 1 — the CLI.** `record` (launcher + manifest + t0), `transcribe`
   (wraps a local Whisper engine), `merge`, `report`; single static binary.
3. **Phase 2 — analysis layer.** Rubric, finding schema, chunking, report
   generation with findings; the human verification pass.
4. **Phase 3 — codebase mapping.** `data-testid` convention in our own apps;
   an agentic mapping step; issue drafting.
5. **Phase 4 — Mode B.** Keyframe extraction + multimodal identification;
   the pattern-library format; optional rrweb browser extension.
6. **Phase 5 — participant-ready.** Consent templates, pseudonymisation,
   retention automation; moderated-session support (diarisation on).

Phases 0–2 deliver standalone value (searchable, analysed session records)
even if 3–5 never happen.

## Current status

Shipped and working end-to-end: `demo`, `transcribe`, `merge`, `report` —
capture a browser session with narration, transcribe it locally, merge the
streams, and read the aligned report. `record` (managed screen/audio capture)
is the remaining Phase 1 stub and the next surface to build; until then,
voice is recorded with QuickTime and `demo` captures the interaction stream.

`demo` — a small instrumented settings app serving as the capture testbed —
is an addition beyond the original Phase 1 command list: it exists so the
pipeline can be exercised end-to-end before any real application is wired up.

## Out of scope (for now)

- Performance or timing measurement of users (think-aloud alters timing;
  see [`../02-constraints/04-ethics.md`](../02-constraints/04-ethics.md)).
- Cloud ASR of any kind — transcription is local by design.
- The macOS app layer that will eventually wrap the CLI core; Linux stays
  CLI-only either way.
- Quantitative claims from sessions: findings are qualitative signals, not
  statistics.
