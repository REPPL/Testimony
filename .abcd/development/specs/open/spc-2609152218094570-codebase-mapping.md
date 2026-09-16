---
id: spc-2609152218094570
slug: codebase-mapping
intent: itd-3
origin: researcher-authored
production_mode: hand-written
---
# codebase-mapping

## Summary

`testimony map` adds the mapping step that turns a **confirmed** web-session
finding into proposed references to the application's source, and
`testimony review -kind refs` adds the human accept / reject pass over each
reference. The resolution stays **host-delegated**, exactly as for `analyze` and
`draft-tests`: the CLI never calls a model, holds no keys, and adds no network
dependency. `map -session DIR -repo DIR` *emits* one self-contained mapping
request: a versioned rubric, the session context, the repository path, and, for
each confirmed finding that carries a selector or route anchor, that finding's
record plus its event window from `timeline.jsonl`. Any agent host or human
resolves the anchors against the repository and saves the JSON answer.
`map -session DIR -repo DIR -ingest FILE` is the **validation boundary**: every
reference must name a finding whose current status is `confirmed` and whose
`ui` carries a selector or route, a repo-relative path that exists as a regular
file under `-repo`, and, when a line is given, a line within that file's
length. Ingest writes `refs.jsonl`, a third record family beside
`findings.jsonl` and `tests.jsonl`, with every reference forced to
`status: proposed`. `review -kind refs` walks the proposed references (or takes
one decision non-interactively) and records `accepted | rejected` as appended,
non-destructive records. `map -session DIR -render` renders an issue draft per
mapped finding to stdout: a title, reproduction steps from the event window,
the participant's quote, and the suspected files.

Nothing is filed anywhere, and the CLI never writes into the application's
repository: `-repo` is opened read-only, for existence and line-count checks.

A session with no confirmed finding carrying a selector or route is **staged
loudly**: `map` refuses, prints the finding count by status, exits non-zero,
and writes nothing.

Every test in this slice is fixture-based and hermetic; stdlib only. The
package is `internal/coderefs`.

## Design

### Decisions carried in from the planning interview

- **Web anchors only.** A finding is mappable iff its effective status is
  `confirmed`, its `mode` is not `B`, and its `ui` carries a non-empty
  `selector` or `route`. Terminal findings carry no `ui` and are the subject of
  their own draft (itd-2609152113364815); this slice does not read
  `terminal_output` text.
- **A separate `refs.jsonl`.** The finding schema is closed and `analyze
  -ingest` refuses to rewrite a verdict-bearing `findings.jsonl`, so nothing is
  written onto a finding. The reference is its own record family through the
  ADR 0001 primitives (`session.CommitRecords` for the machine record,
  `session.AppendRecord` for the human decision) and its own `review -kind`.
- **No confidence field.** A model-asserted `high | medium | low` cannot be
  validated at the boundary. The reference carries what ingest verified and the
  human decision is the only quality signal. An unknown field, `confidence`
  included, is refused by `DisallowUnknownFields`.
- **The issue draft renders from a mapped finding**, not only from an accepted
  reference. The render lists every reference for the finding with its current
  status, so the reader sees which paths a person has already vouched for;
  review does not gate the render.
- **Resolution is the host's job.** The CLI hands over the anchor and the
  repository path; it never greps, never reads a router table, and never
  interprets the source. Its only reads of the repository are the existence
  and line-count checks at ingest.

### Eligibility

`map` calls `analyze.Load(dir)` and `analyze.EffectiveStatus(findings,
verdicts)`, the single helper `review`, `report`, and `draft-tests` already use,
so a finding confirmed then later rejected is not eligible and one rejected then
later confirmed is. `duplicate` and `unverified` are never eligible. On top of
the `draft-tests` rule, a finding whose `ui` is absent, or whose `selector` and
`route` are both empty, is not eligible: there is no anchor to hand over.

The refusal names both counts, so an operator can tell a session with no
confirmed finding from one whose confirmed findings carry no anchor:

```
map: no mappable finding in 5 findings: 1 confirmed (0 with a selector or route), 3 unverified, 0 duplicate, 1 rejected
```

### CLI surface

```
testimony map -session DIR -repo DIR [-window 10] [-out FILE]   emit the mapping request (stdout default)
testimony map -session DIR -repo DIR -ingest FILE               validate answer JSON → refs.jsonl (FILE may be "-" for stdin)
testimony map -session DIR -render [-out FILE]                  render an issue draft per mapped finding (stdout default)

testimony review -session DIR -kind refs                                       interactive accept/reject walk
testimony review -session DIR -kind refs -ref R-NNN -decision accepted|rejected
```

`map` runs in exactly one mode, following `draft-tests`' rule: emit (neither
`-ingest` nor `-render`), ingest (`-ingest`), or render (`-render`). `-ingest`
combines with neither `-out` nor `-render`; `-window` belongs to emit alone and
is refused with `-ingest` or `-render` rather than silently ignored. `-repo` is
required for emit and ingest and refused with `-render`, which reads only what is
on disk in the session. `-repo` must name an existing directory; a path that is
not a directory, or an explicitly empty value, is refused at exit 2. Every flag
follows the existing gauntlet: `-session` required or inferred from the current
directory; empty explicit values refused; a non-finite `-window` refused; no
positional arguments.

| Flag | Default | Meaning |
|---|---|---|
| `-session` | *(required, or inferred)* | session directory |
| `-repo` | *(required for emit and ingest)* | the application's repository root; read only |
| `-window` | `10` | emit mode: the event-window half-width in seconds, as `draft-tests` |
| `-out` | *(stdout)* | emit/render mode: write to `FILE` instead of stdout |
| `-ingest` | *(off)* | ingest mode: validate the answer JSON at `FILE` (or `-`) into `refs.jsonl` |
| `-render` | *(off)* | render mode: write the issue drafts as Markdown |

`review` gains `refs` as a third value of `-kind` and two flags:

| Flag | Default | Meaning |
|---|---|---|
| `-kind` | `findings` | `findings`, `tests`, or `refs` |
| `-ref` | *(interactive)* | non-interactive: the reference to decide (`R-NNN`), `-kind refs` only |
| `-decision` | *(interactive)* | non-interactive: `accepted` or `rejected`; `edited` is refused with `-kind refs` |

Flags belonging to another record family (`-finding`, `-verdict`, `-test`,
`-edit`) are refused with `-kind refs` at exit 2, checked in `internal/cli` and
again in `review.Run`, as the `tests` kind already is. `-kind findings` and
`-kind tests` are byte-for-byte unchanged.

Reads by mode: emit reads `manifest.json`, `timeline.jsonl`, and
`findings.jsonl`; ingest reads `manifest.json`, `findings.jsonl`, and the
repository (existence and line counts only); render reads `manifest.json`,
`timeline.jsonl`, `findings.jsonl`, and `refs.jsonl`. Emit hints to run `merge`
first when the timeline is missing; every mode hints to run `analyze -ingest`
first when there is no `findings.jsonl`; render and `review -kind refs` hint to
run `map -ingest` first when there is no `refs.jsonl`.

### The event window

Reused, not reimplemented: `drafttests.Window(entries, f, window)` is the one
helper that turns a finding's cited evidence into a time-ordered slice of the
timeline, and the issue draft's reproduction steps need exactly the window the
test draft needs. The default half-width is 10 seconds for the same reason
`draft-tests` chose it: a repro needs the lead-up and the aftermath.

### `map` — emitting the request (host-delegated)

`EmitRequest(dir, repo string, window float64) (string, error)` builds one
self-contained prompt, mirroring `drafttests.EmitRequest`:

1. **Rubric header**: `Testimony code-mapping rubric: testimony-coderefs/v1`,
   with `RubricVersion` a package constant.
2. **Stance**: every reference is a proposal, born `proposed`; a human accepts
   or rejects it. Only the confirmed findings below are eligible. Resolve each
   anchor by reading the repository at the path given; never invent a path,
   never guess a line, and omit a finding rather than answer it with a path
   that does not exist. Never alter the quote.
3. **Instructions**: zero or more references per finding, in finding-id order;
   each names the finding, a repo-relative path (forward slashes, no leading
   `/`, no `..` segment), an optional 1-based line, and one `role` from the
   closed set `owner | handler | route | test` (the file that renders the
   element, the code that handles its interaction, the router entry for the
   route, or a test that exercises it). The role is the model's description of
   *why* the path is relevant, which the human reads; it is validated as a
   member of the set, never for truth.
4. **Rubric body**: field definitions, the hard constraints restated as the
   rules ingest enforces, and the `status` explanation `draft-tests` already
   carries (the record is shown verbatim, so its `status` reads `unverified`;
   the confirming verdict is named in the header).
5. **Repository**: the absolute path passed as `-repo`, as the host will open
   it, plus a note that the CLI verifies existence and line counts at ingest.
   This is the one place an absolute local path appears in an emitted request;
   it is the operator's own machine's path, written to a file the operator
   chooses, and it is never written into the session directory.
6. **Session context**: manifest `app`, `participant`, and ordered `tasks`.
7. **Eligible findings**: per finding, in id order: a prose header naming its
   id, `type`, `severity`, clock, the selector and route, and the date of the
   confirming verdict; the finding's own JSON line in a ```jsonl fence; its
   event window as a ```jsonl fence.
8. **Required output shape and worked example**:

   > Answer with a single JSON document: `{"rubric":"testimony-coderefs/v1","refs":[ … ]}`. A bare top-level array is also accepted. Output JSON only.

   ```json
   {"rubric":"testimony-coderefs/v1","refs":[
     {"id":"R-001","finding":"F-001","session":"sample-session",
      "path":"src/settings/ProfileForm.tsx","line":48,"role":"owner",
      "status":"proposed"},
     {"id":"R-002","finding":"F-001","session":"sample-session",
      "path":"src/settings/saveProfile.ts","line":12,"role":"handler",
      "status":"proposed"}
   ]}
   ```

**Escaping** follows the established rule: prose and list-item values go
through `session.SafeInline`; fenced values through `session.SafeText`. The
repository path goes through `SafeInline` too.

Nothing in the session directory is mutated by emit.

### Reference record — the `Ref` type

One reference per line of `refs.jsonl`. Reference lines carry no `kind` field;
the schema is closed (`DisallowUnknownFields`).

| Field | Type | Required | Validation at ingest |
|---|---|---|---|
| `id` | string | yes | matches `^R-\d{3}$`; unique within the answer and the file |
| `finding` | string | yes | names a finding in `findings.jsonl` whose effective status is `confirmed`, whose `mode` is not `B`, and whose `ui` carries a selector or route |
| `session` | string | yes | equals the manifest's `session` |
| `path` | string | yes | repo-relative: non-empty after `SafeText`+trim, at most 512 bytes, forward slashes, no leading `/`, no `.` or `..` segment, no NUL; after `filepath.Join(repo, path)` and `filepath.Rel` back, still inside `repo`; names an existing **regular file** (symlinks are not followed: `os.Lstat`) |
| `line` | int | no | when present, `1 <= line <= lines(file)`, where `lines` counts `\n`-terminated lines plus an unterminated last line, reading at most `session.MaxJSONLBytes`; a file larger than that bound is refused for a line check rather than counted |
| `role` | string | yes | one of `owner \| handler \| route \| test` |
| `status` | string | no on input, always present on disk | ignored on input and forced to `"proposed"` |

The field is `finding`, following the convention the verdict and decision
records set: the field is named after the kind of record it points at. The
decision record below names its referent `ref`.

**Why the path check is a containment check and not a string check.** A model
answer is untrusted input naming a filesystem path the CLI will open. The
`..`-segment rule catches the obvious escape; the `Rel`-back-inside-`repo` rule
catches the ones a string rule misses (a path through a symlinked directory,
an absolute path on a platform where `Join` does not neutralise it). `Lstat`
rather than `Stat` means a symlink inside the repository pointing outside it is
refused as "not a regular file" rather than followed. The CLI opens the file
only when `line` is present, only to count lines, and only up to the shared
byte bound.

**Why there is no `quote` on the reference.** The test draft carries
`rationale_quote` because a test is a document a person reads on its own. A
reference is a pointer; its finding carries the quote, and the render joins
them. Copying the quote onto every reference would be a second place for the
evidence to drift.

### `map -ingest FILE` — the validation boundary

1. Load `manifest.json` and `findings.jsonl`; compute effective status; build
   the eligible set `id → finding`. If empty, refuse loudly before reading a
   byte of the answer.
2. Read `FILE` (or stdin) bounded by `session.MaxAnswerBytes`. Accept an object
   with a `refs` array or a bare array. `rubric`, when present, must be known.
3. Decode each reference with `DisallowUnknownFields` through a `rawRef` whose
   `Line` is `*int`, so an absent line is distinguishable from `0`.
4. Run every rule in the table. Validation is **transactional and exhaustive**:
   all errors across all references are collected, each naming the reference by
   id or answer position, the field, and the offending value; if any exists,
   nothing is written and the exit is non-zero.
5. Force `status: "proposed"`; pre-flight the serialised size against
   `session.MaxJSONLLine` per line and `session.MaxJSONLBytes` in total.
6. Commit through `session.CommitRecords` with a guard that refuses to
   overwrite a `refs.jsonl` already holding any `kind:"decision"` line, exactly
   as `commitDrafts` protects `tests.jsonl`.
7. Print `validated N references → <path> (all proposed)`.

An empty `refs` array is refused rather than written, so a bad answer cannot
erase a prior `refs.jsonl`. A model that found nothing for a finding omits it;
an operator who wants that recorded has the refusal message and the request.

### The human pass — `review -kind refs`

**Decision record**, appended to `refs.jsonl` through `session.AppendRecord`,
never an in-place rewrite:

```json
{"kind":"decision","ref":"R-001","decision":"accepted","at":"2026-09-15"}
{"kind":"decision","ref":"R-002","decision":"rejected","at":"2026-09-15"}
```

| Field | Type | Validation |
|---|---|---|
| `kind` | string | literal `"decision"` |
| `ref` | string | an existing reference id in the file |
| `decision` | string | `accepted \| rejected`; there is no `edited`, because a wrong path is rejected and a corrected one is ingested, never patched |
| `at` | string | ISO date `YYYY-MM-DD` |

The interactive walk shows, per proposed reference, the finding's quote and
anchor, the path, line, and role, and the first lines around `line` from the
repository when `-repo` is given (read-only, bounded), so the reviewer can
judge without leaving the terminal. Without `-repo` the walk shows the record
alone.

`EffectiveStatus(refs, decisions)`: every reference starts `proposed`; decisions
apply in file order and the last one wins; a decision naming an unknown
reference or carrying a value outside the enum is ignored for display.

### `map -render` — the issue draft

One Markdown block per mapped finding, in finding-id order, to stdout or
`-out`:

```markdown
## F-001: Saving gives no confirmation

**Severity** 3 · **Anchor** `[data-testid=save-btn]` on `/settings/profile`

> "I clicked save and nothing happened"
> Alice, 00:22

### Steps to reproduce
1. Open /settings/profile.
2. Click the display-name field ([data-testid=display-name]).
3. Type "Alice".
4. Click Save ([data-testid=save-btn]).

### Suspected files
- `src/settings/ProfileForm.tsx:48` (owner) — accepted 2026-09-15
- `src/settings/saveProfile.ts:12` (handler) — proposed

Session `sample-session` · finding F-001 · references R-001, R-002
```

The title is derived by the CLI, not the model: the finding's `type` and the
first clause of its quote, capped at 80 characters, because a title is
presentation and the render must work from the records alone. The steps are
derived from the window the way `draft-tests`' rubric instructs the model to,
but here by the CLI: one orientation line from the first event's route, then
one imperative line per event in the window at or before the finding's `t`,
naming the selector where the event carries one. Steps the CLI derives are
mechanical and may read flatly; that is the trade for a render that needs no
second model round-trip and cannot invent a step. Every reference for the
finding is listed with its current status, so a draft rendered before review is
visibly unreviewed.

Nothing is written into the application's repository, and the render does not
open it.

### Docs and record

- `docs/reference/cli.md`: `map` and the `review -kind refs` additions.
- `docs/reference/session-directory.md`: `refs.jsonl`.
- `docs/how-to/map-findings-to-code.md`: the four-step flow.
- `examples/sample-session`: a `refs.jsonl` with one accepted and one proposed
  reference, and a `request.md` fixture; the sample references point into a
  small fixture tree under `internal/coderefs/testdata/repo/` rather than into
  Testimony's own source, so the smoke run in CI does not depend on the layout
  of this repository.
- `CHANGELOG.md` `[Unreleased]`: one Added entry.
- The change that lands this closes the spec: `abcd spec close
  spc-2609152218094570 --impact additive`.

## How the acceptance criteria are satisfied

1. **The emitted request carries the anchor, quote, window, and repository
   path, and no ineligible finding.** `EmitRequest` renders only the eligible
   set computed from `EffectiveStatus` plus the anchor rule; each finding's
   header names selector and route, its JSON line carries the quote, its window
   follows, and the repository section carries `-repo`. Tested over the sample
   session with F-001 confirmed and anchored, F-002 unverified, F-003 rejected,
   F-005 duplicate, and a confirmed finding with no `ui`: exactly F-001 appears.
2. **Ingest verifies finding, path, and line, and refuses the whole answer on
   any failure.** The `Ref` table is enforced field by field; the path check is
   the containment-plus-`Lstat` rule; the line check counts the file. Tests
   cover: an unconfirmed finding, an anchorless finding, a `..` path, a symlink
   out of the repository, a directory, a missing file, a line past the end, an
   unknown field (`confidence`), and a second good reference in the same answer
   that is not written because the first failed.
3. **An issue draft from a mapped finding carries title, steps, quote, and
   suspected file.** `Render` derives the title, steps from `Window`, the
   quote from the finding, and lists every reference. Tested against a golden
   file from the sample session, and against a session whose `refs.jsonl` holds
   a proposed-only reference to show the render does not wait for review.
4. **A decision is appended without rewriting the reference line, and the link
   survives.** `AppendDecision` goes through `session.AppendRecord`; the
   round-trip test asserts the reference line is byte-identical after two
   decisions; there is no edit path, so `finding`, `session`, and `path` are
   unreachable by any later write.
5. **A session with no mappable finding is refused with the tally and writes
   nothing.** The eligibility check runs first on every write path; the test
   strips the sample's verdicts and asserts exit 1, the by-status line with the
   anchored count, and no `refs.jsonl`.

## Open questions carried on the intent

- Route anchors: the first slice hands the route over and lets the human
  decide; the accept rate on `role: route` references against `role: owner`
  ones is the measurement, and it is readable from `refs.jsonl` alone.
- Feeding accepted references into `draft-tests`: not built here. When it is,
  the reference id is the link, and the render above already shows the join.
