# Brief

The current-state design brief for Testimony: what the project is, what
constrains it, what its surfaces do, how it is built and verified. It reflects
the present, not history — history lives in `git log`, and the original design
note is preserved as drafted in
[`../research/2026-07-17-architecture-note.md`](../research/2026-07-17-architecture-note.md).

## Contents

- [`00-meta.md`](00-meta.md) — naming and structure conventions of the brief itself.
- [`01-product/`](01-product/)
  - [`01-purpose.md`](01-product/01-purpose.md) — what Testimony is for; the two operating modes; method grounding.
  - [`02-scope.md`](01-product/02-scope.md) — what is in and out, per phase, with current status.
  - [`03-personas.md`](01-product/03-personas.md) — Alice, Bob, and Carol.
  - [`04-analysis.md`](01-product/04-analysis.md) — the planned analysis layer: findings schema, rubric, Mode B pattern library.
- [`02-constraints/`](02-constraints/)
  - [`01-platform.md`](02-constraints/01-platform.md) — macOS primary, Linux CLI-only, CI on Ubuntu.
  - [`02-dependencies.md`](02-constraints/02-dependencies.md) — zero Go dependencies; external capability means a subprocess.
  - [`03-invariants.md`](02-constraints/03-invariants.md) — the one-wall-clock rule, the privacy boundary, schema discipline.
  - [`04-ethics.md`](02-constraints/04-ethics.md) — validity limits and the privacy/ethics posture.
- [`04-surfaces/`](04-surfaces/) — one file per command: [`demo`](04-surfaces/01-demo.md), [`transcribe`](04-surfaces/02-transcribe.md), [`merge`](04-surfaces/03-merge.md), [`report`](04-surfaces/04-report.md), [`record`](04-surfaces/05-record.md) (planned).
- [`05-internals/`](05-internals/)
  - [`01-packages.md`](05-internals/01-packages.md) — the package map under `internal/`.
  - [`02-schemas.md`](05-internals/02-schemas.md) — the JSON schemas of the session artefacts.
- [`06-delivery/`](06-delivery/)
  - [`01-phases.md`](06-delivery/01-phases.md) — the phased plan and where it stands.
  - [`02-verification.md`](06-delivery/02-verification.md) — the gates and the live end-to-end procedure.
