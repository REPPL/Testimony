---
id: spc-2609120417480624
slug: regression-test-drafting
intent: itd-9
origin: researcher-authored
production_mode: hand-written
---
# regression-test-drafting

## Summary

`testimony draft-tests` adds the drafting step that turns a **confirmed**
finding into a proposed regression test case, and `testimony review -kind tests`
adds the human accept / edit / reject pass over each draft. The oracle stays
**host-delegated**, exactly as for `analyze`: the CLI never calls a model, holds
no keys, and adds no network dependency. So `draft-tests -session DIR` *emits* a
single self-contained drafting request — a versioned rubric, the session
context, and, for each confirmed finding, that finding's record plus its **event
window** from `timeline.jsonl` — on stdout or to `-out FILE`; any agent host or
human runs it and saves the JSON answer. `draft-tests -session DIR -ingest FILE`
is the **validation boundary**: it checks the answer field-by-field against the
draft schema — every draft must name a finding whose *current* status is
`confirmed`, carry that finding's `quote` byte-for-byte and its `severity`
unchanged, claim the session it actually sits in, and list at least one step —
and writes `tests.jsonl` with every draft forced to `status: proposed`.
`review -kind tests` then walks the proposed drafts (or takes one decision
non-interactively) and records `accepted | edited | rejected` as **appended,
non-destructive records**, so the drafted proposal and the human's decision both
survive, and `edited` carries the human's replacement fields without ever
rewriting the draft line. `draft-tests -session DIR -render` renders the
accepted drafts as Markdown test-case blocks a docs-as-code test plan can hold.

Eligibility is the whole point of the step: it sits *downstream* of verification,
so only evidence a human already vouched for can become a test. A session with
no confirmed finding is **staged loudly** — `draft-tests` refuses, names the
finding count by status, exits non-zero, and writes nothing.

Every test in this slice is fixture-based and hermetic; stdlib only.

## Design

### Eligibility — which findings are drafted from

A finding is eligible iff its **effective status is `confirmed`** and its `mode`
is not `B`. Effective status is not recomputed here: `draft-tests` calls
`analyze.Load(dir)` and `analyze.EffectiveStatus(findings, verdicts)`, the single
helper `review` and `report` already use. That is what makes "later verdicts
override earlier ones" true for free — verdict records apply in file order and
the last one for a finding wins, so a finding confirmed then later rejected is
**not** eligible, and one rejected then later confirmed **is**. `duplicate` and
`unverified` are never eligible, including a `duplicate` whose target is
confirmed: the canonical finding is the one that carries the evidence.

`mode: "B"` is excluded per the intent's out-of-scope bullet (reference-capture
findings are design preferences with nothing to regress against). Nothing
produces Mode B today, so the exclusion is a guard, not a live filter.

`type` is **not** filtered. AC1 says *a finding whose status is `confirmed`*
without qualification, so filtering out `preference`/`idea` would fail the
criterion as written. The request carries each finding's `type` so the model can
calibrate (a `preference` usually yields a weak "expected behaviour"), and the
human rejects a draft that has nothing to regress against — which is precisely
what the reject verb is for.

### CLI surface

```
testimony draft-tests -session DIR [-window 10] [-out FILE]    emit the drafting request (stdout default)
testimony draft-tests -session DIR -ingest FILE                validate answer JSON → tests.jsonl (FILE may be "-" for stdin)
testimony draft-tests -session DIR -render [-out FILE]         render accepted drafts as Markdown test cases (stdout default)

testimony review -session DIR -kind tests                                        interactive accept/edit/reject walk
testimony review -session DIR -kind tests -test T-NNN -decision accepted|rejected
testimony review -session DIR -kind tests -test T-NNN -decision edited -edit FILE
```

`draft-tests` runs in exactly one mode, mirroring `analyze`'s emit-or-ingest
rule and extending it by one: emit (neither `-ingest` nor `-render`), ingest
(`-ingest`), or render (`-render`). `-ingest` combines with neither `-out` nor
`-render`. `-out` pairs with emit or render. Every flag follows the CLI's
existing exit-2 gauntlet: `-session` required; an explicitly-empty `-out`,
`-ingest`, or `-kind` refused as a wrong invocation (the unset-shell-variable
case); a non-finite `-window` refused (the `report -window` precedent); no
positional arguments (`rejectArgs`).

| Flag | Default | Meaning |
|---|---|---|
| `-session` | *(required)* | session directory |
| `-window` | `10` | emit mode: the event-window half-width in seconds |
| `-out` | *(stdout)* | emit/render mode: write to `FILE` instead of stdout |
| `-ingest` | *(off)* | ingest mode: validate the answer JSON at `FILE` (or `-` for stdin) into `tests.jsonl` |
| `-render` | *(off)* | render mode: write Markdown test cases for the accepted drafts |

`review` gains three flags and one mode selector:

| Flag | Default | Meaning |
|---|---|---|
| `-kind` | `findings` | which record family to review: `findings` or `tests` |
| `-test` | *(interactive)* | non-interactive: the draft to decide (`T-NNN`), `-kind tests` only |
| `-decision` | *(interactive)* | non-interactive: `accepted`, `edited`, or `rejected`, `-kind tests` only |
| `-edit` | *(off)* | with `-decision edited`: the replacement fields as a JSON object at `FILE` (or `-` for stdin) |

`-finding`/`-verdict` are refused with `-kind tests`, and `-test`/`-decision`/
`-edit` with `-kind findings`, at exit 2 — a flag that belongs to the other
record family is a wrong invocation, not a silently ignored one. `-kind
findings` is byte-for-byte the behaviour `review` has today.

Reads by mode: emit reads `manifest.json`, `timeline.jsonl`, and
`findings.jsonl`; ingest reads `manifest.json` and `findings.jsonl` only (drafts
are validated against the *findings*, never re-derived from the timeline);
render reads `manifest.json`, `findings.jsonl`, and `tests.jsonl`. Emit hints to
run `merge` first when the timeline is missing (reusing `analyze`'s
`loadTimeline`, so a duplicated entry id or an unknown `src` is refused there
too); every mode hints to run `analyze -ingest` first when there is no
`findings.jsonl`, and ingest/render/`review -kind tests` hint to run
`draft-tests -ingest` first when there is no `tests.jsonl`.

### The event window

The reproduction steps are the expensive part, and the window is the only thing
the model is allowed to reconstruct them from. For finding `f`:

```go
// Window returns the timeline entries around f, in time order: every entry
// whose time falls in [lo, hi], where lo and hi span f's cited evidence
// entries widened by window on both sides.
func Window(entries []timeline.Entry, f analyze.Finding, window float64) []timeline.Entry
```

`lo = min(e.T) - window` and `hi = max(timeline.SpeechEnd(e)) + window` over the
entries `f.Evidence` resolves (ids matched in their `session.SafeText` form, the
form the request shows and `analyze` already validates against). A finding whose
evidence resolves to no entry — impossible after `analyze -ingest`, reachable via
a hand-edited `findings.jsonl` — falls back to `[f.T - window, f.T + window]`.
Speech and event entries are both included: the utterances around the moment are
what carry the *expected* behaviour, and the events are what carry the *steps*.

`-window` defaults to **10 seconds**, not `report`'s 2.5: `report`'s window
joins an event to the utterance it accompanies, whereas a repro needs the lead-up
and the aftermath. The bundled sample shows why. F-001 cites `utt-004` (22–28 s),
`ev-003` (19.2 s) and `ev-004` (24.1 s). At 2.5 s the window is [16.7, 30.5] and
excludes `utt-003` at 16.0 s — "Now I expect this save button to confirm
somehow", the one utterance in the session that states the expected behaviour. At
10 s the window is [9.2, 38.0] and holds the whole repro: click the display-name
field, type "Alice", click save, click save again, plus the utterances that frame
it. Negative values are legitimate (they narrow the window), matching `report`;
only finiteness is required.

The window is not separately capped: its size is bounded by `timeline.jsonl`,
which already carries the session's 16 MiB total-size limit, and emit writes
nothing to disk.

### `draft-tests` — emitting the request (host-delegated)

`EmitRequest(dir string, window float64) (string, error)` builds one
self-contained prompt so that an agent given **only** this text can answer.
Structure, in order, mirroring `analyze.EmitRequest`:

1. **Rubric header** — `Testimony regression-test drafting rubric:
   testimony-testdraft/v1`. The version is a package constant (`RubricVersion`),
   pinning the drafting scheme so drafts are comparable across sessions and a
   future revision is explicit.
2. **Stance** — every draft is a *proposal*, born `proposed`; a human accepts,
   edits, or rejects it. Only the confirmed findings below are eligible.
   Reconstruct steps only from the event window supplied with each finding —
   never invent a step, a selector, or a route that is not there. Never alter the
   quote, the severity, or the session.
3. **Instructions** — one or more drafts per confirmed finding, in finding-id
   order; `steps` in time order, each one imperative action a developer can
   follow, naming the selector or route where the window names it, ending at the
   moment the finding is anchored to; `expected` the behaviour the participant
   expected, grounded in their utterances; `observed` what the system actually
   did, grounded in the window's events and utterances; `title` one line naming
   the defect.
4. **Rubric body** — the field definitions and the hard constraints restated as
   the rules ingest enforces (quote copied byte-for-byte from the finding's
   `quote`; `severity` and `session` copied unchanged; `finding` naming the
   finding the draft came from; `steps` non-empty).
5. **Session context** — manifest `app`, `participant`, and the ordered `tasks`.
6. **Confirmed findings** — per eligible finding, in id order: a prose line
   naming its id, `type`, `severity` and clock, then the finding's own JSON line
   in a ```jsonl fence (so the `quote` bytes the model must copy are
   unambiguous), then its event window as a ```jsonl fence of timeline entries in
   time order.
7. **Required output shape + worked example** —

   > Answer with a single JSON document: `{"rubric":"testimony-testdraft/v1","tests":[ … ]}`.
   > A bare top-level array of drafts is also accepted. Output JSON only, no prose.

   ```json
   {"rubric":"testimony-testdraft/v1","tests":[
     {"id":"T-001","finding":"F-001","session":"sample-session",
      "title":"Saving gives no confirmation",
      "steps":["Open #general in the settings prototype.",
               "Change the display name to Alice.",
               "Click the Save button ([data-testid=save-btn])."],
      "expected":"The save is confirmed on screen — a toast, or the button briefly disabled.",
      "observed":"Nothing visibly changes, so there is no way to tell the save landed.",
      "rationale_quote":"I clicked save and nothing happened",
      "severity":3,"status":"proposed"}
   ]}
   ```

**Escaping.** The emitter honours exactly the rule the `[Unreleased]` fix
established for `analyze`: a value rendered as prose or a list item **outside**
any code fence goes through `session.SafeInline` (manifest `app`/`participant`/
`tasks` via the same `(none)`-fallback helper, and the finding id/type in each
per-finding header), so an attacker-authored manifest or findings file cannot
survive as an active link or an image beacon when a saved `request.md` is
previewed; a value rendered **inside** a fence goes through `session.SafeText`
only (each marshalled finding and timeline line), which strips terminal-control
and Trojan-Source bytes that `json.Marshal` passes through. There is one shared
home for the escape set and this adds no second one.

Nothing in the session directory is mutated by emit.

### Draft record — the `Draft` type

One draft per line of `tests.jsonl`. Draft lines carry no `kind` field; the
schema is closed (`DisallowUnknownFields`).

| Field | Type | Required | Validation at ingest |
|---|---|---|---|
| `id` | string | yes | matches `^T-\d{3}$`; unique within the answer and the file |
| `finding` | string | yes | names a finding in `findings.jsonl` whose **effective status is `confirmed`** and whose `mode` is not `B` |
| `session` | string | yes | equals the manifest's `session` value |
| `title` | string | yes | non-empty after `SafeText`+trim; at most 200 characters |
| `steps` | []string | yes | non-empty; at most 32 entries; every entry non-empty after `SafeText`+trim |
| `expected` | string | yes | non-empty after `SafeText`+trim |
| `observed` | string | yes | non-empty after `SafeText`+trim |
| `rationale_quote` | string | yes | **equals** the source finding's `quote` (compared in `SafeText` form) |
| `severity` | int | yes | **equals** the source finding's `severity` |
| `status` | string | no | **ignored on input and forced to `"proposed"`** on ingest, whatever the JSON says |

The field is `finding`, not `finding_id`: the verdict record in `findings.jsonl`
already names its referent `finding`, and the decision record below names its
referent `test`. One convention — *the field is named after the kind of record it
points at* — beats a second spelling of the same idea.

**Why `rationale_quote` must be equal, not merely verbatim-in-the-timeline.**
`analyze -ingest` already proved the finding's quote is a byte-for-byte substring
of a cited evidence utterance, and a human then confirmed *that* finding. Letting
the drafting model re-derive a quote from the same utterance would let it
substitute different words for the evidence the human vouched for. Requiring
equality makes the drafting step structurally incapable of introducing new
evidence: it can only carry forward what is already on the record. A mismatch is
also the cheapest available signal that the model linked the draft to the wrong
finding, which is why the field is required and validated rather than silently
filled in by the CLI.

**Why `severity` is copied and equality-checked.** This answers the intent's
third open question: yes, the severity carries through, and it is not the model's
to choose. Triage order is a human product — it came out of the finding's own
severity, which `analyze -ingest` bounded to `1..4` — so the draft restates it
and ingest refuses any restatement that disagrees.

### `draft-tests -ingest FILE` — the validation boundary

1. Load `manifest.json` (for `session`) and `findings.jsonl`; compute effective
   status; build the eligible set as `id → {quote, severity}`. If the eligible
   set is empty, refuse loudly (below) before reading a byte of the answer.
2. Read `FILE` (or stdin when `-`) bounded by `session.MaxAnswerBytes`. Accept a
   top-level object with a `tests` array, or a bare array. `rubric`, when
   present, must be a known version.
3. Decode each draft with **`DisallowUnknownFields`**, through a `rawDraft` whose
   `Severity` is a `*int` so an absent `severity` stays distinguishable from a
   present one (the `rawFinding.T` precedent): absent is reported as *missing*,
   not as a mismatch against a value the answer never gave.
4. Run every rule in the table. Validation is **transactional and exhaustive**:
   collect all errors across all drafts, each naming the draft (its id when
   well-formed, otherwise its position *in the answer*, via the
   `positioned`/`draftLabel` pattern), the field, and the offending value; if any
   error exists, write nothing and exit non-zero.
5. Force `status: "proposed"` on every draft, then pre-flight the serialised size
   against `session.MaxJSONLLine` per line and `session.MaxJSONLBytes` in total
   (`oversizedDrafts`, the sibling of `analyze.oversizedFindings`), so an
   oversized answer leaves the previous `tests.jsonl` untouched rather than
   writing a file no reader could scan back.
6. Commit through `session.CommitRecords` (below) with a guard that refuses to
   overwrite a `tests.jsonl` already holding any `kind:"decision"` line — the
   retained human record, protected exactly as `findings.jsonl`'s verdicts are.
7. Print `validated N test drafts → <path> (all proposed)`.

An answer with an empty `tests` array (a bare `[]`, `{"tests":[]}`, or a
truncated file) is refused rather than written, so it cannot erase a prior
`tests.jsonl`.

### The human pass — `review -kind tests`

**Decision record**, appended to `tests.jsonl`, never an in-place rewrite:

```json
{"kind":"decision","test":"T-001","decision":"accepted","at":"2026-09-12"}
{"kind":"decision","test":"T-002","decision":"rejected","at":"2026-09-12"}
{"kind":"decision","test":"T-003","decision":"edited","at":"2026-09-12","edit":{"title":"Saving a display name gives no confirmation","steps":["Open #general.","Click Save."]}}
```

| Field | Type | Validation |
|---|---|---|
| `kind` | string | literal `"decision"` — the only discriminator; draft lines carry no `kind` |
| `test` | string | an existing draft id in the file |
| `decision` | string | one of `accepted \| edited \| rejected` |
| `edit` | object | required iff `decision == "edited"`; a subset of `{title, steps, expected, observed}` with at least one member present, each held to the draft's own rule for that field |
| `at` | string | ISO date `YYYY-MM-DD` |

**How AC3's "the draft remains linked to its source finding and session" is
guaranteed.** Three mechanisms, each independently testable:

1. `tests.jsonl` is append-only. A decision is a new line; the draft line is
   never rewritten, so `finding`, `session`, `severity`, `rationale_quote` and
   `id` are unreachable by any later write. (Asserted byte-for-byte in the
   round-trip test.)
2. The `edit` object is a **closed subset** — `title`, `steps`, `expected`,
   `observed` — decoded with `DisallowUnknownFields`. An `edit` naming `finding`,
   `session`, `severity`, `rationale_quote` or `id` is a hard error, not a
   silently dropped key. There is therefore no path through which a human edit
   can re-point a draft at a different finding or session; the only way to change
   the link is to reject the draft and ingest a new one.
3. `session` on the draft is validated equal to the manifest's `session`, so the
   link survives the draft line being copied *out* of the session directory —
   which is what the render step exists to do.

**Effective status.** `EffectiveStatus(drafts, decisions)`: every draft starts
`proposed`; decision records apply in file order and the last one for an id wins;
a decision naming an unknown draft is ignored for display; a decision whose value
is outside the closed enum is ignored rather than applied (the `ParseRecords`
precedent — a draft in an unrenderable status would otherwise vanish from both
the walk and the render). An `edited` draft's rendered fields are the **last**
`edited` decision's `edit` applied over the draft, computed at render time; the
draft itself is untouched.

**Interactive** (`review -session DIR -kind tests`): load drafts and decisions,
walk the `proposed` ones in id order, print each and prompt
`[a]ccept [e]dit [r]eject [s]kip [q]uit`. Gated on stdin being a character
device, with the same one-line notice and exit 0 otherwise, so CI never blocks.
Printed block:

```
(1/3) T-001 — from F-001 (bug, severity 3), [00:22]
  Saving gives no confirmation
  steps:
    1. Open #general in the settings prototype.
    2. Change the display name to Alice.
    3. Click the Save button ([data-testid=save-btn]).
  expected: The save is confirmed on screen — a toast, or the button briefly disabled.
  observed: Nothing visibly changes, so there is no way to tell the save landed.
  “I clicked save and nothing happened”
[a]ccept [e]dit [r]eject [s]kip [q]uit:
```

Every field passes through `session.SafeText` with a placeholder fallback for one
that renders as nothing, matching `review.printFinding`. `e` prompts for each
editable field in turn showing the current value, blank keeping it, and for
`steps` reads lines until a blank one; if nothing changed it prints
`  no changes; recorded as accepted.` and records `accepted` — an `edited`
decision with an empty `edit` is not representable, so it is never written.
Recording echoes `  recorded: T-001 accepted (2026-09-12)`.

**Non-interactive** (`-test T-001 -decision accepted`): validate that the draft
exists and the decision parses; append one record; print
`recorded: T-001 accepted (2026-09-12)`. `-decision edited` requires
`-edit FILE` (or `-` for stdin), a JSON object holding the replacement fields,
read through `session.OpenFileNoFollowRead` and decoded with
`DisallowUnknownFields` and the same field rules as the interactive edit. Every
interactive path in this repo has a non-interactive twin; making `edited` the one
exception would put the only lossy decision out of reach of a script or an agent
host. A decision may be appended even when one already exists (append-only
correction; latest wins).

**Under-lock target check.** `AppendDecision` re-reads the current drafts under
its exclusive lock and refuses if the targeted id has vanished or now names a
different draft — the `review.verifyTarget` mechanism, via
`drafttests.SameIdentity` (equality in every field but `Status`). `review -kind
tests` snapshots the drafts once and then blocks on the operator, and a
concurrent `draft-tests -ingest` may truncate-and-rewrite in that gap (permitted
until the first decision exists), and draft ids restart at `T-001`, so without
the re-check a decision would silently attach to a different draft.

### The honest reuse — two shared primitives in `internal/session`

`review`'s verdict machinery is *not* generalised over two record families, and
`review -kind tests` does not reuse `review`'s findings walk. The walk's
vocabulary genuinely differs (`accepted|edited|rejected`, where `edited` carries
a payload no verdict ever does), and forcing both through a `Subject` interface
would buy less than the ~100 lines of prompt-shaped code it costs. What must
**not** be duplicated is the dangerous part — and there are two such operations,
each currently written once and each about to be written twice:

```go
// internal/session

// Append is one record appended to a session JSONL file.
type Append struct {
    Path   string                        // the file to append to
    Record []byte                        // the encoded record, without its newline
    Label  string                        // names the record in the line-limit error, e.g. "verdict for F-001"
    Kind   string                        // names the record kind in the file-limit error, e.g. "verdict"
    Verify func(current io.Reader) error // optional re-check of the file's contents, run under the lock
}

// AppendRecord appends a.Record as its own physical line: it opens Path under
// the no-follow guard (O_APPEND|O_RDWR), takes an exclusive advisory lock,
// pre-flights the record against MaxJSONLLine and the file against
// MaxJSONLBytes, runs a.Verify over the current contents, frames the record
// with a leading newline when the file does not already end in one, writes it,
// truncates back to the pre-write length on a short write, and returns the
// Close error so a record is never reported written when its bytes did not
// reach disk.
func AppendRecord(a Append) error

// Commit is a whole-file replacement of a session JSONL file.
type Commit struct {
    Path    string
    Records [][]byte                      // each encoded record, without its newline
    Guard   func(current io.Reader) error // refuse the replacement by returning an error
}

// CommitRecords replaces Path's contents with Records under the same no-follow
// guard and exclusive lock: it runs Guard over the current contents, encodes
// the whole set into one buffer before truncating, writes it as a single Write,
// rolls the file back to empty on a short write (an empty JSONL file is
// parseable and re-ingestable, so the failure state does not foreclose its own
// repair), and returns the Close error.
func CommitRecords(c Commit) error
```

Callers after the extraction:

- `review.AppendVerdict` → `session.AppendRecord` with
  `Label: "verdict for " + SafeText(v.Finding)`, `Kind: "verdict"`, and
  `Verify` wrapping the existing `verifyTarget` logic. `review.writeVerdict` and
  its `verdictFile` interface are deleted; their behaviour and their two size
  error messages move verbatim (the `Label`/`Kind` parameters and
  `filepath.Base(Path)` reproduce today's strings byte-for-byte, so review's
  existing message assertions keep passing unchanged).
- `analyze.commitFindings`/`writeFindings` → `session.CommitRecords` with
  `Guard: holdsVerdicts` and its existing refusal message. The
  `findingsFile` interface is deleted.
- `drafttests.AppendDecision` → `session.AppendRecord`.
- `drafttests.commitDrafts` → `session.CommitRecords` with `Guard: holdsDecisions`.

The per-record size **labelling** stays with the callers
(`oversizedFindings`/`oversizedDrafts`), because only they can name a record by
its own id or its position in an answer.

Rejected alternatives, for the record: copying the ~70 lines of lock/frame/
rollback logic into the new package (two copies of a subtle write path drifting
apart is exactly the hazard the existing comments argue against); a
`review -file FILE` flag (the operator would be naming a path where they mean a
record family, and the file name does not determine the vocabulary); doing the
extraction later (this is the one moment where the cost is one refactor instead
of two implementations plus a unification).

### `draft-tests -render` — the docs-as-code form

`Render(dir string) (string, error)` writes one Markdown test-case block per
draft whose effective status is `accepted` or `edited`, in id order, with the
last `edited` decision's fields applied. `proposed` and `rejected` drafts are
omitted: a proposal is not a test, and a rejected draft is retained in
`tests.jsonl` for the record, not for the plan. Output (stdout by default, or
`-out FILE`, which prints `wrote <path>`):

```markdown
# Regression tests — sample-session

Drafted from confirmed findings in session `sample-session` (app `testimony demo`,
participant `P1`). 2 of 3 drafts accepted.

## T-001 — Saving gives no confirmation

- **Source:** finding `F-001` (bug, severity 3) in session `sample-session`, at [00:22]
- **Decision:** accepted (2026-09-12)

**Steps**

1. Open #general in the settings prototype.
2. Change the display name to Alice.
3. Click the Save button ([data-testid=save-btn]).

**Expected:** The save is confirmed on screen — a toast, or the button briefly disabled.

**Observed:** Nothing visibly changes, so there is no way to tell the save landed.

**Rationale (participant, [00:22]):** “I clicked save and nothing happened”
```

Every inserted value goes through `session.SafeInline` — the one shared home for
the escape set that `report.md` and the emitted request already use — so an
attacker-authored draft cannot forge Markdown structure, an active link, or an
image beacon in a document the operator pastes into their own repository. The
clock is rendered `[MM:SS]` from the *finding's* `t`, with a leading `-` for a
negative time, matching `report`. Render writes nothing into the session
directory unless `-out` names a path there; the artefact is a hand-off copy, so
it defaults to stdout.

Why a mode on `draft-tests` rather than a section in `report.md`: `report.md` is
the session record, written at a fixed path, and would have to carry the section
whether or not anyone accepted a draft; the test plan is a different artefact
with a different audience and lives wherever the operator's docs-as-code plan
lives. `report` keeps its single mode and single output path.

### Loud staging

Both refusals write nothing and exit 1 (a well-formed invocation whose work
cannot be done), naming the counts so the operator can see *why* they are empty:

```
testimony: no confirmed findings to draft tests from (5 findings: 0 confirmed, 2 unverified, 1 duplicate, 1 rejected); confirm one with `testimony review -session sessions/x` first
testimony: no accepted test drafts to render (3 drafts: 0 accepted, 0 edited, 2 proposed, 1 rejected); accept one with `testimony review -session sessions/x -kind tests` first
```

The first applies to emit **and** ingest (with no eligible finding there is
nothing a draft could legally reference). The second keeps `-out FILE` from
truncating an existing test plan into an empty document, which is the same
reasoning behind `analyze -ingest`'s empty-answer refusal.

### Package layout & session constants

- **`internal/drafttests`** (new) — `Draft` and `Decision` types, `RubricVersion`,
  `Load`/`ParseRecords`, `EffectiveStatus`, `SameIdentity`, `Window`,
  `EmitRequest`, `Ingest`, `Render`, `Review` (the walk and the single-decision
  path), `AppendDecision`, and the unexported `validate`/`oversizedDrafts`/
  `holdsDecisions`. Imports `analyze`, `session`, `timeline`.
- **`internal/review`** — gains the `-kind` dispatch (`Options.Kind`); its
  findings path is unchanged except that `AppendVerdict` now calls
  `session.AppendRecord`.
- **`internal/analyze`** — `commitFindings` now calls `session.CommitRecords`;
  `maxAnswerBytes` moves to `session.MaxAnswerBytes` (16 MiB) beside
  `MaxJSONLLine`/`MaxJSONLBytes`, where the shared caps already live.
- **`internal/session`** — gains `TestsFile = "tests.jsonl"`,
  `MaxAnswerBytes`, `Append`/`AppendRecord`, and `Commit`/`CommitRecords`.
- **`internal/cli`** — the `draft-tests` case, `review`'s new flags, and the
  usage text.

The rendered Markdown gets **no** session constant: it is a hand-off artefact
whose destination the operator chooses, and stdout is the default.

### Sample session & the schema-move invariant

`tests.jsonl` is a new session artefact, so code, sample, tests and the brief's
schema page move in the same change. `examples/sample-session/` gains a
`tests.jsonl` whose three drafts all reference `F-001` — the one confirmed
finding in the bundled `findings.jsonl` — because a finding may legitimately
yield more than one test case, and three drafts let the sample exercise all three
decisions and the render filter: `T-001` **accepted**, `T-002` **edited**,
`T-003` **rejected**. Each draft's `rationale_quote` is F-001's quote verbatim
and each `severity` is `3`, so the bundled file is itself a fixture that passes
ingest. Render on the sample therefore produces two blocks, which CI can grep.

## Acceptance-criteria mapping

**AC1** — *Given a finding whose status is `confirmed`, when the drafting step
runs, then a test case draft is produced containing reproduction steps from the
event window, the expected and observed behaviour, and the participant's quote.*

- *Mechanism:* emit carries exactly the eligible findings and, for each, its
  event window (`Window`, `-window` default 10 s); the draft schema makes
  `steps` (non-empty), `expected`, `observed` and `rationale_quote` all
  **required**, and ingest refuses a draft missing any of them or whose quote is
  not the finding's quote byte-for-byte.
- *Tests:* `TestEmitCarriesConfirmedFindingsAndWindows`,
  `TestWindowSpansEvidenceWidenedByWindow`, `TestIngestRequiresSteps`,
  `TestIngestRequiresExpectedAndObserved`,
  `TestIngestRejectsQuoteThatIsNotTheFindingsQuote`, and the round-trip golden.
- **Flagged, as itd-2's AC3 was:** the CLI guarantees that a draft *contains*
  steps and that the request it came from carried *only* the event window. It
  cannot verify that a given step was in fact derived from the window — `steps`
  is free prose, and a validator that tried would either reject honest
  paraphrase or accept fabrication. AC1 is therefore met **at the request and
  schema level**, with the human accept/edit/reject pass as the check on
  content — which is the same second-coder stance the whole layer rests on. The
  10-second default window is the substantive part of the commitment, and the
  worked sample above is the evidence it is wide enough to reconstruct a repro.

**AC2** — *Given a finding whose status is `unverified` or `rejected`, when the
drafting step runs, then no test case is drafted for it.*

- *Mechanism:* enforced on both sides. Emit omits every non-`confirmed` finding
  from the request (so the model never sees it), and ingest independently
  refuses any draft whose `finding` is not currently `confirmed` — so a
  hand-written or stale answer cannot smuggle one in. Effective status comes
  from `analyze.EffectiveStatus`, so a later verdict overriding an earlier one is
  honoured.
- *Tests:* `TestEmitOmitsUnverifiedRejectedAndDuplicateFindings`,
  `TestIngestRejectsDraftOfUnverifiedFinding`,
  `TestIngestRejectsDraftOfRejectedFinding`,
  `TestIngestRejectsDraftOfDuplicateFinding`,
  `TestEligibilityHonoursLastVerdict` (confirmed-then-rejected excluded,
  rejected-then-confirmed included), `TestEmitRefusesWithNoConfirmedFindings`.

**AC3** — *Given a drafted test case, when a human accepts or rejects it, then
the decision is retained and the draft remains linked to its source finding and
session.*

- *Mechanism:* decisions are appended records (`kind:"decision"`), never in-place
  rewrites; the `edit` object is a closed four-field subset decoded with
  `DisallowUnknownFields`, so no decision can reach `finding`, `session`,
  `severity`, `rationale_quote` or `id`; `session` is validated equal to the
  manifest's, so the link travels with a line copied out of the directory; and
  `AppendDecision` re-checks the target under its lock.
- *Tests:* `TestDecisionIsAppendedAndDraftLinesUnchanged` (byte-for-byte),
  `TestEditCannotNameFindingOrSessionOrSeverityOrQuoteOrID`,
  `TestEffectiveStatusLastDecisionWins`,
  `TestIngestRejectsSessionMismatch`,
  `TestAppendDecisionRefusesWhenDraftChangedUnderTheLock`, and the interactive
  walk tests for `a`/`e`/`r`.

**Scope bullet 4** — *Emitting the draft in a form the docs-as-code manual test
records can hold.* Met by `draft-tests -render` (one Markdown test-case block per
accepted or edited draft, source finding and session named in each block). Tests:
`TestRenderGoldenFromSampleSession`, `TestRenderOmitsProposedAndRejected`,
`TestRenderAppliesLastEdit`, `TestRenderRefusesWithNoAcceptedDrafts`.

No acceptance criterion is unmeetable as written. The one reduction is AC1's
"steps *from the event window*", flagged above as a request-level rather than
validator-level guarantee.

## Decisions on open questions

**Open question 1 — does the event window alone yield reproduction steps a
developer can follow, or does a reliable repro need the keyframe channel as
well?** The window alone, at a 10-second default half-width, with `edited` in
the decision vocabulary as the recorded repair when it is not enough. The
sample's F-001 window holds the full click-type-click-click sequence plus the
utterance stating the expectation, which is a followable repro. Keyframes are
deferred for the same reason itd-2 deferred them: extraction needs local video
and a multimodal pass, neither of which fits CI or the local-only privacy
boundary, and Mode B (itd-4) is where that channel gets built. The design leaves
the seam open — `Window` already returns whole timeline entries, so a later
revision can attach a keyframe reference to an entry without changing the prompt
contract. Flagged for the maintainer rather than silently chosen.

**Open question 2 — where do accepted drafts live?** Both, with the boundary
drawn explicitly: the **record** lives alongside the session, as `tests.jsonl`,
append-only and linked to its finding; the **hand-off** is
`draft-tests -render`, whose output the operator places wherever their
docs-as-code test plan lives. Testimony never writes into the application's
repository. This keeps the evidence chain inside the session (which is the
exchange unit the whole tool is built around) and keeps Testimony out of the
business of knowing another repository's layout or test conventions — which the
intent's out-of-scope bullet already rules out for test *code*.

**Open question 3 — should a drafted test carry the finding's severity
through?** Yes, and it is copied rather than chosen: `severity` is required on
every draft and ingest refuses any value that disagrees with the source
finding's. Triage order is a human product and survives the hand-off unaltered.

**Further decisions taken.**

- **One verb per pipeline step, mirroring the existing shape.** `draft-tests` is
  the machine step (emit | ingest, plus render), and the human step is
  `review -kind tests` — one human-decision verb for the whole pipeline, which
  is what the intent's own wording ("the decision retained *as the verdicts
  already are*") asks for. A separate `accept-tests` verb would have split the
  human surface in two.
- **Two shared write primitives, not a generalised review.** `session.AppendRecord`
  and `session.CommitRecords` hold the lock/pre-flight/framing/rollback logic
  once; the walks and the vocabularies stay per-kind. Reasoning and rejected
  alternatives above.
- **`tests.jsonl`, and the draft field is `finding`.** The file name mirrors
  `findings.jsonl` (both hold a machine record plus appended human records) and
  the constant mirrors the others (`session.TestsFile`). The reference field is
  named after the kind of record it points at, as the verdict's `finding` and the
  decision's `test` are.
- **Quote equality, not re-derivation**, so the drafting step cannot introduce
  evidence; **`session` equality**, so the link survives the line being copied
  out; **`status` forced to `proposed`**, so a draft can never be born accepted —
  the same laundering `analyze -ingest` applies to `unverified`.
- **`-window` default 10 s**, not `report`'s 2.5 s, with the sample-session
  worked example as the justification.
- **No `type` filter**; `mode: "B"` excluded as a guard.
- **Loud staging** on zero confirmed findings and zero accepted drafts: exit 1,
  counts by status, nothing written.
- **`-decision edited` requires `-edit FILE`**, so every interactive decision has
  a non-interactive twin.
- **Validator strictness mirrors `analyze` exactly**: `DisallowUnknownFields`,
  transactional and exhaustive error collection, positional labels taken from the
  answer rather than from a filtered slice, and `SafeText`-form comparison for
  every value the model was shown in sanitised form.

## Test plan

Hermetic and fixture-based; CI-safe on ubuntu with no model, network, tool, or
TTY. Fixtures live in `internal/drafttests/testdata/`.

**Emit.** Substring and golden asserts on `EmitRequest`: the rubric version
header; the stance, instruction and rubric bodies; the manifest task list; the
output-shape example; the eligible finding's JSON line and its window entries
present; non-eligible findings (`F-002` unverified, `F-003` rejected, `F-005`
duplicate) absent; entries outside the window absent; `-window` widening and
narrowing the window set; determinism across runs. Escaping: a manifest `app` of
`[x](http://example.test/beacon.png)` renders escaped, and a timeline entry text
carrying `U+202E` renders stripped.

**The window.** Unit tests for `Window`: span over multiple evidence entries;
`SpeechEnd` used for the upper bound; an event-only evidence set; a negative
window narrowing; the `f.T ± window` fallback when evidence resolves to nothing;
inclusive boundaries.

**Eligibility.** `TestEligibilityHonoursLastVerdict` — confirmed-then-rejected
excluded, rejected-then-confirmed included, duplicate-of-confirmed excluded,
`mode: "B"` excluded.

**Ingest validation-failure fixtures** — one per rule, each proving a precise
message: bad id (`T-12`, `X-001`); duplicate id; unknown `finding`; `finding`
that is unverified / rejected / duplicate (three fixtures, AC2's ingest side);
`session` mismatch; empty and whitespace-only `title`; over-long `title`; absent,
empty, and whitespace-only-entry `steps`; over-long `steps`; empty `expected`;
empty `observed`; `rationale_quote` differing by one byte; absent `severity`;
mismatched `severity`; unknown field (`stpes`); a non-object element; empty
`tests` array; unknown `rubric`; an answer over `MaxAnswerBytes`; a draft whose
line exceeds `MaxJSONLLine`; a set exceeding `MaxJSONLBytes`. Plus
status-forcing (`status:"accepted"` in, `proposed` out), and the refusal to
overwrite a `tests.jsonl` holding decision records. Transactionality: an answer
with three bad drafts reports all three errors in one run and writes nothing;
positional labels point at the answer's own numbering when an earlier element
failed to decode.

**Decisions.** `-test T-001 -decision accepted` appends one record and leaves
every draft line byte-unchanged; `rejected`; `edited` with an `-edit` file and
with `-edit -` on stdin; an `edit` naming `finding`, `session`, `severity`,
`rationale_quote` or `id` rejected; an `edit` with no members rejected; an
unknown draft id rejected; last-decision-wins including accepted-then-rejected;
`EffectiveStatus` unit tests; `AppendDecision` refusing when the draft changed
under the lock.

**Interactive walk.** Scripted `In` with `IsTTY: true`: `a`, `e` (each field,
including blank-keeps and the all-blank → recorded-as-accepted path), `r`, `s`,
`q`, an unrecognised key re-prompting, end-of-input stopping cleanly, the
no-proposed-drafts notice, and the not-a-character-device notice exiting 0.

**Render.** Golden `tests.md` from the sample session; only accepted and edited
drafts rendered; the last `edit` applied over the draft; the counts line; clock
rendering including a negative time; `SafeInline` escaping of a draft field
carrying an inline-Markdown trigger; the zero-accepted refusal writing nothing.

**Shared primitives.** `internal/session` gains the tests moved from
`internal/review` and `internal/analyze`: `AppendRecord`'s newline framing over
an unterminated file, its line- and total-size pre-flights, its partial-write
rollback (via a fake satisfying the file interface), and its `Verify` refusal
path; `CommitRecords`' `Guard` refusal, truncate-then-write, and
rollback-to-empty. Both callers' existing error strings are asserted unchanged in
`internal/review` and `internal/analyze`, so the extraction is provably
behaviour-preserving.

**CLI.** Exit-2 table: missing `-session`; explicitly-empty `-out`/`-ingest`/
`-kind`/`-test`/`-decision`/`-edit`; `-out` with `-ingest`; `-render` with
`-ingest`; non-finite `-window`; a positional argument; an unknown `-kind`; a
bad `-test`; a bad `-decision`; `-decision edited` without `-edit`; `-edit`
without `-decision edited`; `-finding`/`-verdict` with `-kind tests` and
`-test`/`-decision`/`-edit` with `-kind findings`. Exit-1: both loud-staging
refusals, and the missing-`tests.jsonl` hint.

**Round-trip golden.** Sample session: `merge` → `draft-tests` (emit) → ingest a
known-good answer fixture → `review -kind tests` three decisions
(accepted / edited / rejected) → `draft-tests -render`, asserting the golden
Markdown, the append-only property, and that `findings.jsonl` is untouched
throughout.

**Sample smoke (CI).** `./testimony draft-tests -render -session
examples/sample-session` grepped for `T-001` — it needs no merged timeline, so it
runs before the existing `merge`/`report` smoke; and `./testimony draft-tests
-session examples/sample-session` after `merge`, asserting exit 0. Added to the
`AGENTS.md` command block and `.github/workflows/ci.yml`.

**Live verification (part of done, not CI).** Run the emitted request against the
maintainer's own host on a real session, ingest the answer, accept one draft and
edit another, render the plan, and read it; fix what it exposes before the PR.

## Docs plan

**User-facing (`docs/`, Diátaxis, present tense, British English in prose).**

- `docs/reference/cli.md` — a new `## testimony draft-tests` section after
  `analyze`: the three invocations, the flag table, the exactly-one-mode rule,
  what each mode reads, emit behaviour (request structure, the event window and
  the 10 s default, the escaping rule, nothing mutated), ingest behaviour (the
  schema rules by reference to the session-directory page, transactional
  validation, `status` forced to `proposed`, the empty-answer and
  decision-holding refusals, the printed line), render behaviour (which statuses
  render, the edit application, the zero-accepted refusal), and both
  loud-staging messages. `## testimony review` gains the `-kind`, `-test`,
  `-decision` and `-edit` rows, the cross-family flag refusals, and a
  tests-mode paragraph mirroring its findings paragraphs.
- `docs/reference/session-directory.md` — `tests.jsonl` added to the directory
  listing and to the 16 MiB total-size sentence; a new `## tests.jsonl` section
  with the Draft record and Decision record tables, an example of each, the
  closed-`edit` note, and the effective-status sentence.
- `docs/how-to/draft-regression-tests.md` — new, mirroring
  `analyse-a-session.md`'s numbered shape: prerequisite (a session with at least
  one confirmed finding), then 1 emit the drafting request, 2 run it with your
  assistant of choice, 3 ingest the answer, 4 review the drafts
  (`review -kind tests`, interactive and single-decision, including `-edit`),
  5 render the test plan and where to put it. Closes with pointers to the two
  reference pages.
- `docs/README.md` — the new how-to added to the How-to guides line.
- `README.md` — "Status and roadmap": `draft-tests` moved into "working today",
  and the regression-test bullet out of "Coming next".

**Durable record (`.abcd/development/`, not user-facing).**

- `brief/04-surfaces/08-draft-tests.md` — new surface page in the shape of
  `06-analyze.md`.
- `brief/04-surfaces/07-review.md` — the `-kind` dispatch and the tests-side
  flags.
- `brief/05-internals/01-packages.md` — `internal/drafttests`, and
  `internal/session`'s two new write primitives.
- `brief/05-internals/02-schemas.md` — the `tests.jsonl` tables and the
  directory listing (the schema-move invariant).
- `decisions/adrs/0001-one-append-primitive-and-one-review-verb.md` — MADR: the
  human-decision layer is one verb (`review -kind`) across record families, and
  the two dangerous session-file writes live once in `internal/session`. This is
  architecture-shaping and is the repository's first ADR.
- `.abcd/work/DECISIONS.md` — one dated line each for: the drafting step is
  host-delegated emit/ingest like `analyze`; drafts live in `tests.jsonl`
  alongside the session and render out as Markdown; decisions are appended and
  the `edit` object cannot reach a draft's link fields; severity is carried
  through and equality-checked.
- `.abcd/work/CONTEXT.md` — the pipeline sentence and the "next" pointer.

**Changelog.** One `### Added` entry under `[Unreleased]` naming
`testimony draft-tests` and `review -kind tests`, the new `tests.jsonl`
artefact, and the two extracted `internal/session` primitives (no behaviour
change for `analyze`/`review`).

**`AGENTS.md`.** "Current state" moves from seven pipeline commands to eight and
names the drafting layer; the build/test block gains the two new smoke lines.
