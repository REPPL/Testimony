# `testimony analyze`

The first-pass analysis layer, in two host-delegated halves. The repository's
oracle is host-delegated: the CLI never calls a model, holds no keys, and adds
no network dependency. So `analyze` first *emits* a self-contained analysis
request — a versioned rubric plus the session's timeline — that any agent host
(or a human) runs to produce a JSON answer; then `analyze -ingest` validates
that answer field-by-field and writes `findings.jsonl`. Ingest is the sole
validation boundary: every finding lands `status: "unverified"` regardless of
what the answer claims. See the analysis design
([`../01-product/04-analysis.md`](../01-product/04-analysis.md)) and the schemas
page ([`../05-internals/02-schemas.md`](../05-internals/02-schemas.md)).

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `-session` | (required) | session directory |
| `-out` | *(stdout)* | write the emitted request to `FILE` instead of stdout (emit mode) |
| `-ingest` | *(off)* | validate the answer JSON at `FILE` (or `-` for stdin) into `findings.jsonl` (ingest mode) |

`analyze` runs in exactly one mode: emit (no `-ingest`) or ingest (`-ingest`).
`-out` and `-ingest` together is an error. Both modes read `manifest.json` and
`timeline.jsonl`, hinting to run `merge` first when the timeline is missing
(matching [`report`](04-report.md)).

## Behaviour — emit (default)

- Writes a single, self-contained prompt so that an agent given only this text
  can answer. In order: the rubric version header
  (`Testimony analysis rubric: testimony-analysis/v1`), the second-coder stance,
  the two-pass instructions (segment coding, then session synthesis), the rubric
  body (the five `type` definitions, the `1..4` severity scale, and the evidence
  hard-constraints), the session context (app, participant, ordered tasks), the
  timeline lines inline, and the required output shape with a worked example.
- The rubric version is a package constant; it pins the coding scheme so answers
  are comparable across sessions and future revisions are explicit.
- **Chunking (flagged divergence).** `timeline.jsonl` carries no task-boundary
  markers and the manifest task list has no timestamps, so v1 emits the *whole*
  timeline as one chunk with the task list as labelled context and asks the model
  to attribute findings to tasks in pass two. The emitter keeps a chunking seam
  for a future revision that splits at real task boundaries.
- Emit mutates nothing in the session directory. `-out FILE` writes the prompt to
  a file instead of stdout.

## Behaviour — ingest (`-ingest FILE`)

- Reads the answer from `FILE` (or stdin when `-`). Accepts a top-level object
  with a `findings` array (optionally carrying a `rubric`, which must be a known
  version) or a bare array of findings.
- Decodes each finding with unknown fields disallowed — a closed output shape, so
  a hallucinated or mistyped key is a hard error rather than silently dropped —
  and runs every schema rule
  ([`../05-internals/02-schemas.md`](../05-internals/02-schemas.md)): id format
  and uniqueness, `t` within the session, `type` and `severity` enums, non-empty
  `evidence` with every id real and at least one spoken `utt-*` anchor, a `quote`
  that is a verbatim substring of one *cited* evidence utterance's text, and any
  `ui` selector/route matching a real event.
- Validation is transactional and exhaustive: all errors across all findings are
  reported at once (each naming the finding, field, and offending value), and on
  any error nothing is written and the command exits non-zero.
- On success it forces `status: "unverified"` on every finding and writes
  `findings.jsonl`. Ingest never trusts the model — nothing lands `confirmed`.
- To protect the retained precision record, ingest refuses to overwrite a
  `findings.jsonl` that already holds verdict records; a fresh or verdict-free
  file is written cleanly. The guard counts any `kind:"verdict"` line, including
  one whose value is outside the closed enum (a hand-edited or shared file), so a
  foreign-valued human decision is never silently truncated by a re-ingest.
- An answer with no findings (a bare `[]`, `{"findings":[]}`, or a truncated
  file) is refused rather than written: the write truncates, so an empty answer
  would otherwise erase a prior good `findings.jsonl` and report success.

## Deferred

On-demand keyframe extraction (a video frame at an ambiguous utterance's
timestamp) is out of scope here: the emitted rubric lets the model flag a
verbal-only ambiguous referent, but frame extraction needs local video and a
multimodal pass and is deferred to a later intent
([`../01-product/04-analysis.md`](../01-product/04-analysis.md)).
