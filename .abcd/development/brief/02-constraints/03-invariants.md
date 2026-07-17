# Invariants

- **One wall clock.** `t0_epoch_ms` in `manifest.json` is the single
  synchronisation anchor: interaction events carry epoch milliseconds and are
  converted to session-relative seconds as `(epoch_ms − t0_epoch_ms) / 1000`;
  the transcript is anchored to the same clock via the audio→session offset.
  As a belt-and-braces fallback, every session begins with a spoken "session
  start" marker usable for manual offset calibration.
- **The privacy boundary.** Raw audio and video never leave the machine; ASR
  is local. Only derived text — transcript, serialised events, and any
  keyframes an analyst explicitly releases — may reach a cloud LLM. See
  [`04-ethics.md`](04-ethics.md).
- **Schema changes move together.** A change to a session artefact schema
  updates the code (`internal/timeline`, `internal/session`), the bundled
  sample session (`examples/sample-session/`), and the tests in the same
  change — the brief's [schema page](../05-internals/02-schemas.md) follows.
- **The `data-testid` convention.** Interactive elements in our own web apps
  carry stable `data-testid` attributes, making captured selectors
  near-deterministic anchors for codebase mapping instead of fragile
  auto-generated class chains. The demo app follows it throughout; it must be
  adopted in an app under test before its first participant session.
