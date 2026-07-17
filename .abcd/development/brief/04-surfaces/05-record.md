# `testimony record` (planned)

The remaining Phase 1 surface: a managed capture launcher that starts the
recorders and writes the manifest, making a session one command.

## Current behaviour

A deliberate, honest stub: running it prints that it is not implemented and
points at today's workaround — `demo` captures web sessions while QuickTime
records voice — and exits with code 2.

## Intended behaviour (from the design note)

- One launcher per session starts all recorders and writes the manifest with
  `t0_epoch_ms`, app under test, build/commit hash, participant pseudonym,
  and task list.
- A single wall clock is the synchronisation primitive
  ([`../02-constraints/03-invariants.md`](../02-constraints/03-invariants.md));
  the spoken "session start" marker stays as the fallback anchor.
- Per-platform capture (screen + microphone; interaction stream where the
  target supports instrumentation) as tabled in the preserved note
  ([§4](../../research/2026-07-17-architecture-note.md)).
