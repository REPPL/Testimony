---
status: accepted
date: 2026-09-12
decision-makers: the maintainer
---

# One append primitive, and one review verb, across record families

## Context and Problem Statement

Testimony's pipeline produces two session artefacts that hold a machine record
plus appended human records: `findings.jsonl` (candidate findings plus verdicts)
and, with the regression-test drafting layer (itd-9), `tests.jsonl` (test drafts
plus decisions). Both are written the same dangerous way — a no-follow open, an
exclusive advisory lock, two size pre-flights against the read-side invariants, a
newline framing over a possibly unterminated last line, a partial-write rollback,
and a returned `Close` error — and both are guarded against a re-ingest
destroying the human record the file exists to hold.

Two questions arrived together. Where does that write logic live once the second
artefact exists? And does the human decision on a drafted test belong to
`testimony review`, or to a verb of its own?

The retained human decision is the precision measure the whole method stands on.
Losing one to a subtly divergent second copy of a lock-and-rollback sequence, or
to a decision attached to the wrong record, is the failure mode that matters most
here — not a missing feature.

## Decision Drivers

- The human-decision record must never be silently lost, truncated, or
  misattributed; every write path that can do so is the same hazard.
- Two copies of a subtle write path drift apart. The comments in the original
  single copy already argue this at length for each of its guards.
- The two record families have genuinely different *vocabularies*
  (`confirmed | rejected | duplicate` against
  `accepted | edited | rejected`, where `edited` carries a replacement payload no
  verdict ever does) and genuinely the same *mechanics*.
- The operator learns one pipeline, and the pipeline's shape is already one verb
  per step.
- There is exactly one moment when the extraction costs one refactor rather than
  two implementations plus a later unification: the moment the second
  implementation is written.

## Considered Options

1. **Extract two write primitives into `internal/session`; keep one review verb
   with a `-kind` selector.**
2. **Copy the write logic into the new package; add a separate `accept-tests`
   verb.**
3. **Generalise the whole review machinery over a `Subject` interface, so one
   walk serves both families.**
4. **Add a `review -file FILE` flag, letting the path select the record family.**
5. **Defer the extraction: ship the duplicate now, unify later.**

## Decision Outcome

Chosen option: **option 1**.

`internal/session` gains `AppendRecord` (one appended record, with an optional
under-lock `Verify` hook the caller supplies) and `CommitRecords` (a guarded
whole-file replacement). Both are written once and shared: `review.AppendVerdict`
and `drafttests.AppendDecision` append through the first;
`analyze.commitFindings` and `drafttests.commitDrafts` replace through the
second. The per-record *labelling* stays with the callers, because only they can
name a record by its own id or by its position in an answer, and the guard and
verify closures stay with the callers, because only they know what makes their
file's human record worth protecting.

The human decision stays one verb: `testimony review -kind findings|tests`. The
walks and the vocabularies stay per-kind — `-kind tests` does not reuse the
findings walk — because the prompt-shaped code is about a hundred lines and the
vocabulary difference is real. A flag that belongs to the other record family is
refused as a wrong invocation rather than silently ignored.

### Consequences

- Good: the lock, the size pre-flights, the framing, the rollback, and the
  returned `Close` error exist in one place, so a fix or a hardening reaches both
  artefacts at once. The extraction is behaviour-preserving: both callers' error
  strings are unchanged, and their existing assertions pass untouched.
- Good: the regression tests that pinned the two write paths move to
  `internal/session` with the code they exercise, so the primitive is tested where
  it lives rather than twice through its callers.
- Good: one human-decision verb keeps the surface the intent's own wording asks
  for — the decision retained *as the verdicts already are*.
- Bad: `internal/session` grows beyond a pure layout package; it holds two
  behaviours, not only a schema. The alternative is worse, and the two behaviours
  are precisely the ones every session-file writer needs.
- Bad: `review` gains a mode selector, so its flag surface is larger and the
  cross-family refusals have to be stated and tested.
- Neutral: the shared primitives do not fix a caller's own pre-flight duty. Each
  caller still holds its records to the line and total caps before committing,
  because only it can attribute an oversized record to something the operator can
  count to.

### Confirmation

The extraction is confirmed by the existing assertions in `internal/review` and
`internal/analyze` passing with their expected strings unmodified, and by the
moved tests in `internal/session` covering the newline framing over an
unterminated file, both size pre-flights, the partial-write rollback on each side,
the `Verify` refusal, and the `Guard` refusal. The one-verb decision is confirmed
by the CLI's exit-2 table refusing each flag against the wrong `-kind`.

## Pros and Cons of the Options

### Option 2 — copy the write logic; add `accept-tests`

- Good: each package reads standalone, with no shared abstraction to understand.
- Bad: two copies of a lock-frame-rollback sequence. Every guard in it exists
  because of a specific failure already reasoned about once; a copy inherits the
  reasoning only until someone edits one side.
- Bad: a separate verb splits the human surface in two, so the operator learns
  where a decision lives per record family rather than learning one verb.

### Option 3 — generalise review over a `Subject` interface

- Good: one walk, one prompt loop, one place to improve the interaction.
- Bad: the vocabularies do not unify. `edited` carries a payload, prompts for four
  fields, and can collapse to `accepted`; no verdict does any of that. The
  interface would buy less than the prompt-shaped code it costs, and it would
  couple two surfaces that are free to diverge.

### Option 4 — `review -file FILE`

- Good: no new enum; the file name carries the choice.
- Bad: the operator names a path where they mean a record family, and the file
  name does not determine the vocabulary — a renamed or copied file would silently
  select the wrong walk.

### Option 5 — defer the extraction

- Good: the drafting layer ships sooner.
- Bad: the cost rises rather than falls. Deferring buys two implementations plus a
  unification, against one refactor taken while both call sites are in hand and
  both test suites are green.

## More Information

- Surfaces: [`../../brief/04-surfaces/07-review.md`](../../brief/04-surfaces/07-review.md),
  [`../../brief/04-surfaces/08-draft-tests.md`](../../brief/04-surfaces/08-draft-tests.md).
- Packages and schemas:
  [`../../brief/05-internals/01-packages.md`](../../brief/05-internals/01-packages.md),
  [`../../brief/05-internals/02-schemas.md`](../../brief/05-internals/02-schemas.md).
- Intent and spec: `itd-9` (regression-test drafting) and
  `spc-2609120417480624`.
