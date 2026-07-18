---
id: spc-2
slug: analysis-findings
intent: itd-2
---
# analysis-findings

## Summary

`testimony analyze` and `testimony review` add the first-pass analysis layer.
The repository's oracle is **host-delegated**: the CLI never calls a model, holds
no keys, and adds no network dependency. So `analyze -session DIR` *emits* a
single self-contained analysis request — a versioned rubric plus the session's
timeline — on stdout (or to `-out FILE`); any agent host or human runs it and
saves the model's JSON answer. `analyze -session DIR -ingest FILE` is the
**validation boundary**: it checks that answer field-by-field against the
findings schema, rejecting fabricated evidence and quotes with precise errors,
and writes `findings.jsonl` with every finding forced to `status: unverified`.
`review -session DIR` walks the unverified findings (TTY-gated) — or takes one
verdict non-interactively — and records `confirmed | rejected | duplicate`
verdicts as **appended, non-destructive records**, so the original finding and
the full verdict history survive as the precision measure the method stands on.
`report` gains a Findings section that renders `findings.jsonl` grouped by
effective status. No LLM and no network anywhere in the CLI; every test is
fixture-based and hermetic.

## Design

### Findings schema — the `Finding` record

One finding per line of `findings.jsonl`, matching the documented schema
(brief `01-product/04-analysis.md`, note §7). Fields and the rules **ingest
enforces**:

| Field | Type | Required | Validation |
|---|---|---|---|
| `id` | string | yes | matches `^F-\d{3}$` (F-NNN, zero-padded); unique within the file |
| `t` | float64 | yes | `0 ≤ t ≤ sessionEnd`, where `sessionEnd` is the max entry time (speech `t1`) in `timeline.jsonl` |
| `type` | string | yes | one of `bug \| friction \| inconsistency \| preference \| idea` |
| `severity` | int | yes | integer `1..4` (Nielsen-style scale, below); non-integer or out-of-range rejected |
| `mode` | string | no | when present, `A \| B`; defaults to `A`. Only Mode A is produced in this slice (Mode B is itd-4) |
| `quote` | string | yes | non-empty; **verbatim substring of the `text` of at least one utterance listed in `evidence`** (see quote rule) |
| `evidence` | []string | yes | non-empty; every id exists in `timeline.jsonl`; ids are `utt-*` (speech) or `ev-*` (event); **at least one `utt-*`** |
| `ui` | object | no | `{selector?, route?}`; when `selector` present it must equal the `selector` of some event in the timeline; when `route` present it must equal the `route` of some event |
| `status` | string | no | **ignored on input and forced to `"unverified"`** on ingest, whatever the JSON says |

`severity` scale (Nielsen usability-severity ratings, dropping "0 = not a
problem" since a non-problem is not a finding): **1** cosmetic, **2** minor,
**3** major, **4** blocker. For `preference`/`idea` findings the same integer
expresses strength/priority rather than defect gravity.

Ingest decodes each finding with **`DisallowUnknownFields`**: the output shape is
closed, so a hallucinated or mistyped field (`servrity`, `code_refs`, a stray
key) is a hard error rather than silently dropped. `code_refs` is therefore
*not* part of this slice's schema — it is reserved for the codebase-mapping step
(itd-3), which will add the field to the struct and the rubric together.

**Quote rule (decision).** `quote` must be an exact, byte-for-byte substring of
the `text` of at least one utterance named in that finding's `evidence` — **not**
of the joined transcript. Joining utterance texts would let a "quote" span words
never spoken contiguously, manufacturing a sentence the participant never
uttered; per-utterance matching keeps a quote to a single spoken moment. Tying
the match to an *evidence* utterance (rather than any utterance in the session)
makes evidence load-bearing: the cited utterance must actually contain the
quoted words. There is no whitespace or case normalisation — "verbatim" means
verbatim, which also blocks paraphrase drift. (Merge already trims utterance
text, so there are no leading/trailing-space surprises.) Consequence, enforced
above: every finding cites at least one `utt-*` id, and one of those utterances
contains the quote.

**Timestamp.** `t` is validated for range only (`0 ≤ t ≤ sessionEnd`); it is not
forced to equal an evidence item's time, but the rubric instructs the model to
set it to the utterance's start. A `t` outside the session is a fabrication and
is rejected.

### Verdict record — append-only, non-destructive (decision)

A human verdict is stored as a **separate appended line** in `findings.jsonl`,
never by rewriting the finding in place:

```json
{"kind":"verdict","finding":"F-001","verdict":"confirmed","at":"2026-07-17"}
{"kind":"verdict","finding":"F-005","verdict":"duplicate","of":"F-001","at":"2026-07-17"}
{"kind":"verdict","finding":"F-003","verdict":"rejected","at":"2026-07-17"}
```

| Field | Type | Validation |
|---|---|---|
| `kind` | string | literal `"verdict"` — the only discriminator; finding lines carry no `kind`, so the documented finding schema is unchanged |
| `finding` | string | must reference an existing finding id in the file |
| `verdict` | string | one of `confirmed \| rejected \| duplicate` (the architecture's verdict set, §7) |
| `of` | string | required iff `verdict == "duplicate"`; an existing finding id, `≠ finding` |
| `at` | string | ISO date `YYYY-MM-DD` (the "verdict + date" the intent requires) |

**Why appended, not rewritten.** The retained human verdict *is* the precision
measure (note §2; itd-2 press release). Rewriting a `status` field on the
finding line would destroy the birth state and any prior verdict, so a
finding confirmed-then-later-rejected would leave no trace and the precision
proxy could not be computed. An appended verdict record keeps the original
`unverified` finding intact and preserves the full decision history in file
order; re-review simply appends another verdict and the **latest line wins** for
display. This is the append-only ethos the repository already applies to session
artefacts. The CLI flag value `duplicate-of-F-002` is parsed into
`verdict:"duplicate", of:"F-002"`, so the stored enum stays exactly the
architecture's three values.

**Effective status.** For a finding, scan verdict records in file order; the last
one for that id wins; absent any, the status is `unverified`. This is the single
helper `analyze.EffectiveStatus(findings, verdicts)` used by both `review`
(to pick the work queue) and `report` (to group).

### CLI surface

```
testimony analyze -session DIR [-out FILE]        emit the analysis request (stdout default)
testimony analyze -session DIR -ingest FILE       validate answer JSON → findings.jsonl (FILE may be "-" for stdin)
testimony review  -session DIR                     interactive verdict walk (TTY-gated)
testimony review  -session DIR -finding F-NNN -verdict confirmed|rejected|duplicate-of-F-NNN
```

`analyze` runs in exactly one mode: emit (no `-ingest`) or ingest (`-ingest`).
`-out` and `-ingest` together is an error. Both subcommands require `-session`
and read `manifest.json` + `timeline.jsonl`, hinting to run `merge` first when
the timeline is missing (matching `report`).

### `analyze` — emitting the request (host-delegated)

`analyze -session DIR` writes a single self-contained prompt so that an agent
given **only** this text can answer. Structure, in order:

1. **Rubric header** — `Testimony analysis rubric: testimony-analysis/v1`. The
   version is a package constant; it pins the coding scheme so answers are
   comparable across sessions and future rubric revisions are explicit.
2. **Stance** — think-aloud usability analysis, AI-as-second-coder: every
   finding is a *candidate* born `unverified`; a human confirms or rejects it.
   Analyse only the supplied text — never invent evidence.
3. **Two-pass instructions** (note §7):
   - *Pass 1 — segment coding.* Read the timeline in order; for each moment
     where the participant expresses a defect, friction, inconsistency,
     preference, or idea, draft a candidate finding with `type`, `severity`, a
     verbatim `quote`, `evidence` ids, and `ui` when an on-screen referent is
     clear.
   - *Pass 2 — session synthesis.* Deduplicate candidates that describe the same
     underlying issue (keep one, cite the strongest evidence), assign final
     `severity`, and note cross-task patterns. Attribute each finding to a task
     by the manifest task list.
4. **Rubric body** — the five `type` definitions; the `1..4` severity scale; the
   evidence rules restated as hard constraints (quote must be copied verbatim
   from one cited utterance's text; `evidence` ids must be real; `ui` selector/
   route must come from an event in the timeline); and the note that when a
   referent is verbal-only and ambiguous ("this thing here") the finding should
   still cite the utterance and set `ui` only if an event names the element
   (keyframe extraction is a later capability, below).
5. **Session context** — manifest `app`, `participant`, and the ordered `tasks`.
6. **The timeline** — the raw `timeline.jsonl` lines inline (this is the data the
   model codes; it is kilobytes and self-contained). **Chunking decision:**
   `timeline.jsonl` carries no task-boundary markers, so v1 emits the **whole
   timeline as one chunk** with the manifest task list as labelled context, and
   asks the model to attribute findings to tasks in pass 2. The emitter keeps a
   `chunk(entries, manifest)` seam that returns one chunk today; when an explicit
   task-boundary convention exists (a spoken marker or a `task` field on
   entries), it will split at those boundaries with no change to the prompt
   contract. *This is a flagged divergence from the note* — see below.
7. **Required output shape + worked example** — the exact finding schema and one
   example built from the supplied timeline, plus the container contract:

   > Answer with a single JSON document: `{"rubric":"testimony-analysis/v1","findings":[ … ]}`.
   > A bare top-level array of findings is also accepted. Output JSON only, no prose.

   ```json
   {"rubric":"testimony-analysis/v1","findings":[
     {"id":"F-001","t":22.0,"type":"bug","severity":3,"mode":"A",
      "quote":"I clicked save and nothing happened",
      "evidence":["utt-004","ev-003","ev-004"],
      "ui":{"selector":"[data-testid=save-btn]","route":"#general"},
      "status":"unverified"}
   ]}
   ```

`-out FILE` writes the prompt to a file instead of stdout; nothing in the session
directory is mutated by emit.

### `analyze -ingest FILE` — the validation boundary

1. Load `timeline.jsonl` and build three sets: the id set (`utt-*` + `ev-*`), the
   set of event `selector`s, the set of event `route`s, and `sessionEnd`.
2. Read `FILE` (or stdin when `-`). Accept a top-level object with a `findings`
   array, or a bare array. `rubric`, when present, must be a known version.
3. Decode each finding with `DisallowUnknownFields` and run every rule in the
   schema table. Validation is **transactional and exhaustive**: collect *all*
   errors across all findings (precise — naming the finding id or line, the
   field, and the offending value), and if any error exists, write nothing and
   exit non-zero. This lets the operator fix a batch in one pass.
4. On success, **force `status:"unverified"`** on every finding and write
   `findings.jsonl` (finding lines only). To protect the retained precision
   record, ingest **refuses to overwrite** a `findings.jsonl` that already
   contains verdict records; a fresh or verdict-free file is written cleanly.

Ingest never trusts the model: unknown evidence, fabricated quotes, bad enums,
out-of-range severity, phantom selectors, and stray fields are all rejected here,
and nothing lands `confirmed`.

### `review` — recording verdicts

- **Interactive** `review -session DIR`: load findings + verdicts, compute
  effective status, and walk the `unverified` findings in id order. For each,
  print the clock, `type`/`severity`, the quote, the anchor (`ui` selector/route
  or the evidence ids), and prompt: `[c]onfirm [r]eject [d]uplicate-of [s]kip
  [q]uit`. `d` asks for the canonical `F-NNN`. Each decision **appends** a
  verdict record with today's date. Interactive mode is **TTY-gated**: when
  stdin is not a terminal it prints a one-line notice and exits 0 (mirroring
  `record`), so CI never blocks.
- **Non-interactive** `review -session DIR -finding F-003 -verdict confirmed`
  (or `-verdict duplicate-of-F-002`): validate that `F-003` exists, the verdict
  parses to the `confirmed|rejected|duplicate` set, and any duplicate target
  exists and differs; append one verdict record; print a one-line confirmation.
  A verdict may be appended even if one already exists (append-only correction;
  latest wins).

### `report` — the Findings section

Replace the current "pending" placeholder in `internal/report/report.go`. When
`findings.jsonl` is absent, keep a short, non-fatal notice ("no findings yet —
run `analyze`/`review`"). When present, render findings **grouped by effective
status** in this order, each group headed with a count:

1. **Confirmed** — the actionable output.
2. **Unverified** — pending human judgement.
3. **Duplicate** — retained, showing "duplicate of F-NNN".
4. **Rejected** — retained for the record and the precision measure.

Each finding line renders its id, `type`, `severity`, clock `[MM:SS]`, the
quote, the anchor (`ui` selector in backticks + route, else the evidence ids),
and — where a verdict exists — the verdict and its date. Report continues to
read only derived text; it never touches media.

### Package layout & session constants

- `internal/analyze` — the `Finding` and `Verdict` types, `Validate` (the ingest
  rules), `EffectiveStatus`, the prompt emitter (`EmitRequest`), and `Ingest`.
- `internal/review` — the interactive walk and the verdict-append helper
  (`AppendVerdict`), calling `internal/analyze` for load/validate.
- `internal/report` imports `internal/analyze` for the types and status helper.
- `internal/session` gains `FindingsFile = "findings.jsonl"`; the schemas page
  (`05-internals/02-schemas.md`) gains the finding + verdict tables (the current
  "designed but not produced" note is replaced), per the schema-move invariant.

### Sample session & the schema-move invariant

`examples/sample-session/` gains a `findings.jsonl` whose quotes are verbatim
substrings of the bundled transcript (verified): a save-feedback **bug**
("I clicked save and nothing happened", evidence `utt-004`,`ev-003`,`ev-004`,
`ui` save-btn/#general), a dark-mode **preference** ("I like this dark mode
toggle"), a feedback **inconsistency** ("This is how the save button should
feel"), a toggle-label **friction** ("The label just says dark mode whether it's
on or off"), and a save-feedback **idea** ("A toast or a brief disabled state
would do"). Bundled verdicts exercise every group: the bug **confirmed**, the
idea **duplicate-of** the bug, the inconsistency **rejected**, the rest left
**unverified**. So CI renders all four status groups and the smoke test can grep
`report.md` for the confirmed save-feedback finding. Alice is the persona in
prose; the transcript speaker stays `P1`. Schema, sample, and tests move in the
same change.

### Flagged divergences from the note

- **Task-boundary chunking (note §7, brief `04-analysis.md`).** Both assume the
  timeline can be chunked "by task boundaries from the manifest", but
  `timeline.jsonl` carries no task markers and the manifest task list has no
  timestamps, so the mapping is not derivable from the data. v1 emits the whole
  timeline with the task list as context and defers real chunking behind a seam.
  Flagged for the maintainer rather than silently chosen.
- **Keyframes (itd-2 AC3, note §7).** On-demand keyframe extraction
  (`ffmpeg -ss` at an utterance timestamp) needs local video and a multimodal
  pass — outside CI and tied to Mode B/mapping. This slice satisfies AC3 at the
  **request** level: the rubric lets the model flag a verbal-only ambiguous
  referent, and frame *extraction* is deferred to a later intent. Flagged so the
  maintainer can confirm the reduced AC3 scope.

## Decisions

- **Quote match is per-utterance and evidence-anchored, not corpus-joined.** A
  quote must be an exact substring of one cited evidence utterance's text; no
  normalisation. This blocks manufactured cross-utterance sentences and makes
  evidence load-bearing.
- **`ui` selector/route validate against the timeline's events** (per the brief),
  not against a free string, so a fabricated selector is rejected.
- **Ingest uses `DisallowUnknownFields` and is transactional** — a closed output
  shape, all errors reported at once, nothing written on any failure, and
  `status` always forced to `unverified`.
- **`severity` is Nielsen-style `1..4`** (cosmetic/minor/major/blocker), dropping
  Nielsen's "0 = not a problem".
- **Verdicts are appended verdict records (`kind:"verdict"`), never in-place
  rewrites.** This preserves the birth state and full decision history — the
  precision measure — and keeps the documented finding schema unchanged; latest
  verdict wins for display; `duplicate-of-F-NNN` stores `verdict:"duplicate"` +
  `of:"F-NNN"`.
- **`analyze` is emit-or-ingest, host-delegated.** Emit prints a versioned,
  self-contained rubric + whole timeline (chunking seam kept); the CLI never
  calls a model or the network. Ingest is the sole validation boundary.
- **Ingest refuses to overwrite a `findings.jsonl` that holds verdicts**,
  protecting the retained precision record.

## Test plan

Hermetic, fixture-based, CI-safe on ubuntu with no LLM, network, tool, or TTY.

**Dedicated validation-failure fixtures** (each proves one precise error, JSON
in → expected message):
- bad id format (`F-12`, `X-001`);
- duplicate finding id;
- unknown evidence id (`utt-999`);
- evidence with no `utt-*` (no spoken anchor);
- fabricated quote (present in the session but not in an evidence utterance; and
  a quote absent entirely);
- wrong `type` enum; non-integer / out-of-range `severity`;
- `ui.selector` / `ui.route` not present on any event;
- `t` outside `[0, sessionEnd]`;
- unknown/stray field (`DisallowUnknownFields`);
- status-forcing: input `status:"confirmed"` still lands `unverified`.

**Prompt-emit test:** `EmitRequest` output contains the rubric version header,
all five `type` names, the severity scale, the output-shape example, the manifest
task list, and the timeline lines; deterministic (golden or substring asserts).

**Round-trip golden:** ingest a known-good fixture → `findings.jsonl`; append two
verdicts non-interactively (`confirmed` and `duplicate-of`) → verdict lines
present, originals intact; `report` → golden `report.md` with all status groups.
Assert the append-only property (finding lines byte-unchanged after review).

**Effective-status unit tests:** last-verdict-wins, including a
confirmed-then-rejected sequence.

**Sample smoke:** `merge → analyze -ingest (bundled) → report` on
`examples/sample-session`; grep `report.md` for the confirmed save-feedback
finding (extends the existing CI smoke test).

**Live verification (part of done, not CI):** run the emitted prompt against the
slice-1 captured session in the maintainer's own host, ingest the answer, review
two findings, and read the final report; fix what it exposes before the PR.

## Acceptance mapping

- **AC1** (merged timeline → `findings.jsonl`, every finding typed from the
  five-value set, with `t`, quote, evidence, and `status: unverified`) — the
  schema table + `Validate` require exactly these fields with those rules, and
  ingest forces `unverified`.
- **AC2** (Alice records a verdict → status becomes
  `confirmed|rejected|duplicate` and persists) — `review` (interactive and
  non-interactive) appends a verdict record from the architecture's three-value
  set; effective status reflects it; the record persists in `findings.jsonl`.
- **AC3** (ambiguous utterance → keyframe attached) — **partially, flagged**: the
  emitted request lets the model flag an ambiguous verbal-only referent;
  keyframe *extraction* is deferred to a later intent (needs local video, out of
  CI), and the reduced scope is flagged for the maintainer above.
