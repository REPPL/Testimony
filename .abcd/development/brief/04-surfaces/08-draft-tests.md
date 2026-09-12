# `testimony draft-tests`

The regression-test drafting layer, in the same host-delegated halves as
[`analyze`](06-analyze.md) plus a render mode. The repository's oracle is
host-delegated: the CLI never calls a model, holds no keys, and adds no network
dependency. So `draft-tests` first *emits* a self-contained drafting request — a
versioned rubric, the session context, and, for each **confirmed** finding, that
finding's record plus its event window from `timeline.jsonl` — that any agent host
(or a human) runs to produce a JSON answer; then `draft-tests -ingest` validates
that answer field-by-field and writes `tests.jsonl`. Ingest is the sole validation
boundary: every draft lands `status: "proposed"` regardless of what the answer
claims. `draft-tests -render` renders the accepted drafts as Markdown test-case
blocks a docs-as-code test plan can hold.

Eligibility is the whole point of the step: it sits *downstream* of verification,
so only evidence a human already vouched for can become a test. The human
accept / edit / reject pass is [`review -kind tests`](07-review.md) — one
human-decision verb for the whole pipeline
([ADR 0001](../../decisions/adrs/0001-one-append-primitive-and-one-review-verb.md)).
See the schemas page ([`../05-internals/02-schemas.md`](../05-internals/02-schemas.md)).

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `-session` | (required) | session directory |
| `-window` | `10` | emit mode: the event-window half-width in seconds |
| `-out` | *(stdout)* | emit/render mode: write to `FILE` instead of stdout |
| `-ingest` | *(off)* | ingest mode: validate the answer JSON at `FILE` (or `-` for stdin) into `tests.jsonl` |
| `-render` | *(off)* | render mode: write Markdown test cases for the accepted drafts |

`draft-tests` runs in exactly one mode, extending `analyze`'s emit-or-ingest rule
by one: emit (neither `-ingest` nor `-render`), ingest (`-ingest`), or render
(`-render`). `-ingest` combines with neither `-out` nor `-render`, and `-window` is refused
outside emit mode (`-window applies to the emit mode only`) rather than silently
ignored. Emit reads
`manifest.json`, `findings.jsonl`, and `timeline.jsonl`; ingest reads
`manifest.json` and `findings.jsonl` only (drafts are validated against the
*findings*, never re-derived from the timeline); render reads `manifest.json`,
`findings.jsonl`, and `tests.jsonl`. Emit hints to run `merge` first when the
timeline is missing (reusing `analyze.LoadTimeline`, so a duplicated entry id or an
unknown `src` is refused there too); every mode hints to run `analyze -ingest`
first when there is no `findings.jsonl`, and ingest, render and
`review -kind tests` hint to run `draft-tests -ingest` first when there is no
`tests.jsonl`.

## Eligibility

A finding is eligible iff its **effective status is `confirmed`** and its `mode`
is not `B`. Effective status is not recomputed here: `drafttests` calls
`analyze.Load` and `analyze.EffectiveStatus`, the single helper `review` and
`report` already use, so "later verdicts override earlier ones" holds for free —
a finding confirmed then later rejected is not eligible, and one rejected then
later confirmed is. `duplicate` and `unverified` are never eligible, including a
`duplicate` whose target is confirmed: the canonical finding carries the
evidence. `mode: "B"` (reference capture, itd-4) is excluded because a design
preference has nothing to regress against; nothing produces Mode B today, so the
exclusion is a guard rather than a live filter. `type` is deliberately **not**
filtered — the acceptance criterion names a confirmed finding without
qualification, the request carries each finding's `type` so the model can
calibrate, and a draft with nothing to regress against is what the reject verb is
for.

## Behaviour — emit (default)

- Writes a single, self-contained prompt so that an agent given only this text can
  answer. In order: the rubric version header
  (`Testimony regression-test drafting rubric: testimony-testdraft/v1`), the
  proposal stance, the per-field instructions, the rubric body (the field
  definitions and the hard constraints ingest enforces), the session context
  (session, app, participant, ordered tasks), each eligible finding's own JSON
  line followed by its event window, and the required output shape with a worked
  example.
- The rubric version is a package constant; it pins the drafting scheme so drafts
  are comparable across sessions and future revisions are explicit.
- **The event window** is the only material the model may reconstruct steps from.
  `Window(entries, f, window)` returns every entry whose time falls in `[lo, hi]`,
  where `lo` is the earliest cited evidence entry's start minus `window` and `hi`
  the latest cited entry's end (`timeline.SpeechEnd`) plus `window`. Speech and
  event entries are both included: the utterances carry the *expected* behaviour
  and the events carry the *steps*. Evidence ids are matched in their
  `session.SafeText` form, the form the request shows and `analyze` validates
  against. A finding whose evidence resolves to no entry — reachable only via a
  hand-edited `findings.jsonl` — falls back to `[f.T - window, f.T + window]`.
- **`-window` defaults to 10 seconds**, not `report`'s 2.5: `report`'s window
  joins an event to the utterance it accompanies, whereas a repro needs the
  lead-up and the aftermath. On the bundled sample, F-001's evidence spans
  `ev-003` (19.2 s), `utt-004` (22–28 s) and `ev-004` (24.1 s); at 2.5 s the
  window is [16.7, 30.5] and excludes `utt-003` at 16.0 s — the one utterance
  stating the expected behaviour — while at 10 s it is [9.2, 38.0] and holds the
  whole click-type-click-click repro. Negative values are legitimate (they
  narrow the window), matching `report`; only finiteness is required.
- **Escaping** follows the rule `analyze`'s emitter established: a value rendered
  as prose or a list item *outside* any code fence goes through
  `session.SafeInline` (the manifest fields, and each per-finding header's id and
  type), so an attacker-authored manifest or findings file cannot survive as an
  active link or an image beacon when a saved `request.md` is previewed; a value
  rendered *inside* a fence goes through `session.SafeText` only, which strips the
  terminal-control and Trojan-Source bytes `json.Marshal` passes through. There is
  one shared home for the escape set and this adds no second one.
- Emit mutates nothing in the session directory. `-out FILE` writes the prompt to
  a file instead of stdout.

## Behaviour — ingest (`-ingest FILE`)

- Loads `manifest.json` and `findings.jsonl`, computes effective status, and
  builds the eligible set. If it is empty, the refusal comes **before** a byte of
  the answer is read: every draft would fail the same rule, and the operator
  should read the one fact that explains them.
- Reads the answer from `FILE` (or stdin when `-`), bounded by
  `session.MaxAnswerBytes`. Accepts a top-level object with a `tests` array
  (optionally carrying a `rubric`, which must be a known version) or a bare array.
- Decodes each draft with unknown fields disallowed — a closed output shape — and
  runs every schema rule
  ([`../05-internals/02-schemas.md`](../05-internals/02-schemas.md)): id format
  and uniqueness; a `finding` whose effective status is `confirmed` and whose mode
  is not `B`; a `session` equal to the manifest's; a non-empty `title` of at most
  200 characters; non-empty `steps` of at most 32 non-empty entries; non-empty
  `expected` and `observed`; a `rationale_quote` **equal** to the source finding's
  `quote`; and a `severity` **equal** to the source finding's.
- `severity` decodes through a `*int` so an absent value is reported as *missing*
  rather than as a mismatch against a number the answer never gave (the
  `rawFinding.T` precedent).
- Validation is transactional and exhaustive: all errors across all drafts are
  reported at once, each naming the draft (its id when well-formed, otherwise its
  position *in the answer*), the field, and the offending value; on any error
  nothing is written and the command exits non-zero.
- On success it forces `status: "proposed"` on every draft, pre-flights the
  serialised set against `session.MaxJSONLLine` per line and
  `session.MaxJSONLBytes` in total, and commits through `session.CommitRecords`.
  Nothing is ever born accepted.
- To protect the retained human record, ingest refuses to overwrite a
  `tests.jsonl` that already holds decision records. The guard counts any
  `kind:"decision"` line, including one whose value is outside the closed enum, so
  a foreign-valued human decision is never silently truncated.
- An answer with no drafts (a bare `[]`, `{"tests":[]}`, or a truncated file) is
  refused rather than written: the commit replaces the file whole, so an empty
  answer would otherwise erase a prior good `tests.jsonl` and report success.

### Why quote and severity are equality-checked

`analyze -ingest` already proved the finding's quote is a byte-for-byte substring
of a cited evidence utterance, and a human then confirmed *that* finding. Letting
the drafting model re-derive a quote from the same utterance would let it
substitute different words for the evidence the human vouched for. Requiring
equality makes the drafting step structurally incapable of introducing new
evidence: it can only carry forward what is already on the record. A mismatch is
also the cheapest available signal that the model linked the draft to the wrong
finding. Severity is the same argument for triage order: it is a human product,
bounded to `1..4` at analysis time, so the draft restates it and ingest refuses
any restatement that disagrees.

## Behaviour — render (`-render`)

- Writes one Markdown test-case block per draft whose effective status is
  `accepted` or `edited`, in id order, with the last `edited` decision's `edit`
  applied over the draft. `proposed` and `rejected` drafts are omitted: a proposal
  is not a test, and a rejected draft is retained in `tests.jsonl` for the record,
  not for the plan.
- Each block names the source finding (id, type, severity, clock) and the session,
  so a failing test leads back to the evidence that motivated it. The clock is
  rendered `[MM:SS]` from the *finding's* `t`, with a leading `-` for a negative
  time, matching `report`.
- Every inserted value is escaped through the shared home for the escape set —
  `session.SafeInline` in prose, and the backtick-stripping code-span form inside
  a code span, where a backslash escape does not apply — so an attacker-authored
  draft cannot forge Markdown structure, an active link, or an image beacon in a
  document the operator pastes into their own repository.
- Output goes to stdout by default; `-out FILE` writes it and prints
  `wrote <path>`. The rendered plan gets **no** session constant: it is a hand-off
  artefact whose destination the operator chooses, and Testimony never writes into
  the application's repository.

Why a mode on `draft-tests` rather than a section in `report.md`: `report.md` is
the session record, written at a fixed path, and would have to carry the section
whether or not anyone accepted a draft; the test plan is a different artefact with
a different audience and lives wherever the operator's docs-as-code plan lives.
`report` keeps its single mode and single output path.

## Loud staging

Both refusals write nothing and exit 1 (a well-formed invocation whose work
cannot be done), naming the counts so the operator can see *why* they are empty:

```
testimony: no confirmed findings to draft tests from (5 findings: 0 confirmed, 2 unverified, 1 duplicate, 2 rejected); confirm one with `testimony review -session sessions/x` first
testimony: no accepted test drafts to render (3 drafts: 0 accepted, 0 edited, 2 proposed, 1 rejected); accept one with `testimony review -session sessions/x -kind tests` first
```

The first applies to emit **and** ingest: with no eligible finding there is
nothing a draft could legally reference. The second keeps `-out FILE` from
truncating an existing test plan into an empty document, which is the same
reasoning behind `analyze -ingest`'s empty-answer refusal. Both wrap a package
sentinel (`drafttests.ErrNoConfirmedFindings`, `drafttests.ErrNoAcceptedDrafts`)
so a caller can tell a staged-empty session from a genuine failure.

## Deferred

Keyframe extraction stays out of scope, for the reason
[`analyze`](06-analyze.md) defers it: it needs local video and a multimodal pass,
neither of which fits CI or the local-only privacy boundary, and Mode B (itd-4) is
where that channel gets built. The seam is left open — `Window` returns whole
timeline entries, so a later revision can attach a keyframe reference to an entry
without changing the prompt contract. The open question the intent raised — whether
the window alone yields a followable repro — is answered by the 10-second default
with `edited` in the decision vocabulary as the recorded repair when it is not
enough; the edited-decision rate on real sessions is what would show it wrong.
