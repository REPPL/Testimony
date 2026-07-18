# `testimony review`

Records a human's verdict on each candidate finding. A finding is born
`unverified` ([`06-analyze.md`](06-analyze.md)); review is where a person
confirms, rejects, or marks it a duplicate. The retained human verdict is the
precision measure the method stands on, so a verdict is stored as a separate
*appended* record in `findings.jsonl`, never by rewriting the finding in place —
the birth state and the full decision history both survive.

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `-session` | (required) | session directory |
| `-finding` | *(interactive)* | non-interactive: the finding to judge (`F-NNN`) |
| `-verdict` | *(interactive)* | non-interactive: `confirmed` \| `rejected` \| `duplicate-of-F-NNN` |

## Behaviour

- Loads the findings and any existing verdicts (hinting to run
  `analyze -ingest` first when there is no `findings.jsonl`) and computes each
  finding's effective status: every finding starts `unverified`, verdict records
  apply in file order, and the last one for a finding wins.
- **Interactive** (`review -session DIR`): walks the `unverified` findings in id
  order. For each it prints the clock, type, severity, the quote, and the anchor
  (the `ui` selector/route, else the evidence ids), then prompts
  `[c]onfirm [r]eject [d]uplicate-of [s]kip [q]uit`. `d` asks for the canonical
  `F-NNN`. Each decision appends a verdict record stamped with today's date.
- **Interactive mode is TTY-gated**: when stdin is not a terminal it prints a
  one-line notice and exits 0 (mirroring [`record`](05-record.md)), so CI never
  blocks.
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
