# `testimony review`

The pipeline's one human-decision verb, across both record families. `-kind
findings` (the default) records a verdict on each candidate finding: a finding is
born `unverified` ([`06-analyze.md`](06-analyze.md)), and review is where a person
confirms, rejects, or marks it a duplicate. `-kind tests` records an accept / edit
/ reject decision on each drafted regression test
([`08-draft-tests.md`](08-draft-tests.md)). One verb rather than two is an
architecture-shaping choice
([ADR 0001](../../decisions/adrs/0001-one-append-primitive-and-one-review-verb.md)).

The retained human decision is the precision measure the method stands on, so
both families store it as a separate *appended* record — in `findings.jsonl` and
`tests.jsonl` respectively — never by rewriting the machine record in place: the
birth state and the full decision history both survive. Both append through the
one shared primitive, `session.AppendRecord`; the walks and the vocabularies stay
per-kind, because `accepted | edited | rejected` (where `edited` carries a payload
no verdict ever does) is genuinely a different vocabulary from
`confirmed | rejected | duplicate`.

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `-session` | (required) | session directory |
| `-kind` | `findings` | which record family to review: `findings` or `tests` |
| `-finding` | *(interactive)* | non-interactive: the finding to judge (`F-NNN`), `-kind findings` only |
| `-verdict` | *(interactive)* | non-interactive: `confirmed` \| `rejected` \| `duplicate-of-F-NNN`, `-kind findings` only |
| `-test` | *(interactive)* | non-interactive: the draft to decide (`T-NNN`), `-kind tests` only |
| `-decision` | *(interactive)* | non-interactive: `accepted` \| `edited` \| `rejected`, `-kind tests` only |
| `-edit` | *(off)* | with `-decision edited`: the replacement fields as a JSON object at `FILE` (or `-` for stdin) |

`-finding`/`-verdict` are refused with `-kind tests`, and
`-test`/`-decision`/`-edit` with `-kind findings`, at exit 2 — a flag that belongs
to the other record family is a wrong invocation, not a silently ignored one.
`review.Run` refuses the same pairings itself, so the rule is a property of the
API rather than of one caller's invariants: a run that recorded nothing while
exiting 0 would let a script believe the decision landed.
`-kind findings` is byte-for-byte the findings-side behaviour described below.

## Behaviour — `-kind findings` (default)

- Loads the findings and any existing verdicts (hinting to run
  `analyze -ingest` first when there is no `findings.jsonl`) and computes each
  finding's effective status: every finding starts `unverified`, verdict records
  apply in file order, and the last one for a finding wins.
- **Interactive** (`review -session DIR`): walks the `unverified` findings in id
  order. For each it prints the id, type, severity, the clock, the quote, and
  the anchor (the `ui` selector/route, else the evidence ids), then prompts
  `[c]onfirm [r]eject [d]uplicate-of [s]kip [q]uit`. `d` asks for the canonical
  `F-NNN`. Each decision appends a verdict record stamped with today's date.
- **Interactive mode is gated on stdin being a character device** — true for
  an interactive terminal, but also for `/dev/null`, so this is not simply
  "not a terminal". When stdin is a pipe or a redirected regular file,
  `review` prints a one-line notice and exits 0 instead of walking, so CI
  never blocks; redirected from `/dev/null` (`< /dev/null`) it still enters
  the walk and immediately reaches end of input, since redirection makes
  stdin the character device itself rather than a pipe reading from it.
- **Non-interactive** (`review -session DIR -finding F-003 -verdict confirmed`,
  or `-verdict duplicate-of-F-002`): validates that the finding exists, the
  verdict parses to the `confirmed | rejected | duplicate` set, and any duplicate
  target exists and differs; appends one verdict record and prints a one-line
  confirmation. A verdict may be appended even when one already exists
  (append-only correction; the latest wins).
- The stored verdict enum is exactly `confirmed | rejected | duplicate`: the CLI
  value `duplicate-of-F-NNN` is parsed into `verdict: "duplicate"` with
  `of: "F-NNN"`. No existing line is ever touched.

The rendered verdicts appear in [`report`](04-report.md)'s Findings section,
grouped by effective status.

## Behaviour — `-kind tests`

- Loads the drafts and any existing decisions (hinting to run
  `draft-tests -ingest` first when there is no `tests.jsonl`) and computes each
  draft's effective status: every draft starts `proposed`, decision records apply
  in file order, and the last one for a draft wins. A decision naming an unknown
  draft is ignored for display, and one whose value is outside the closed enum is
  ignored rather than applied — a draft in an unrenderable status would otherwise
  vanish from both the walk and the render.
- **Interactive** (`review -session DIR -kind tests`): walks the `proposed` drafts
  in id order. For each it prints the id, the source finding with its type,
  severity and clock, the title, the numbered steps, the expected and observed
  behaviour, and the participant's quote, then prompts
  `[a]ccept [e]dit [r]eject [s]kip [q]uit`. `e` asks for each editable field in
  turn showing the current value, a blank answer keeping it, and reads `steps` one
  per line until a blank line. A pass that changes nothing prints
  `no changes; recorded as accepted.` and records `accepted` — an `edited`
  decision with an empty `edit` records a change that did not happen and is not
  representable. Each decision appends a record stamped with today's date. The
  source finding supplies only the type and the clock: if `findings.jsonl` is
  absent or unreadable those degrade to placeholders rather than blocking a
  decision, because the draft itself carries the substance.
- **Interactive mode is gated on stdin being a character device**, exactly as the
  findings walk is, so CI never blocks.
- **Non-interactive** (`review -session DIR -kind tests -test T-001 -decision
  accepted`): validates that the draft exists and the decision parses, appends one
  record, and prints a one-line confirmation. `-decision edited` requires
  `-edit FILE` (or `-` for stdin), a JSON object holding the replacement fields,
  read through `session.OpenFileNoFollowRead` and decoded with
  `DisallowUnknownFields` and the same field rules as the interactive edit. Every
  interactive path in this repository has a non-interactive twin; making `edited`
  the one exception would put the only lossy decision out of reach of a script or
  an agent host.
- **The `edit` object is a closed four-field subset** (`title`, `steps`,
  `expected`, `observed`). An `edit` naming `id`, `finding`, `session`,
  `severity`, or `rationale_quote` is a hard error, not a silently dropped key, so
  no human edit can re-point a draft at a different finding or session; the only
  way to change the link is to reject the draft and ingest a new one. An `edited`
  draft's rendered fields are the last `edited` decision's `edit` applied over the
  draft, computed at render time — the draft line is never rewritten.
- **Under-lock target check.** `AppendDecision` re-reads the current drafts under
  its exclusive lock and refuses if the targeted id has vanished or now names a
  different draft — the mechanism `AppendVerdict` uses, via
  `drafttests.SameIdentity`. The walk snapshots the drafts once and then blocks on
  the operator, a concurrent `draft-tests -ingest` may truncate-and-rewrite in
  that gap (permitted until the first decision exists), and draft ids restart at
  `T-001`, so without the re-check a decision would silently attach to a different
  draft.
- A decision may be appended even when one already exists (append-only
  correction; the latest wins). No existing line is ever touched.

The accepted and edited drafts are what
[`draft-tests -render`](08-draft-tests.md) puts in the test plan.
