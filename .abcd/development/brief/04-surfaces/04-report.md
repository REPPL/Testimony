# `testimony report`

Renders `timeline.jsonl` as a human-readable Markdown session report — the
raw aligned record of what was said interleaved with what was done, followed
by a Findings section rendering `findings.jsonl` from the analysis layer
([`../01-product/04-analysis.md`](../01-product/04-analysis.md),
[`06-analyze.md`](06-analyze.md), [`07-review.md`](07-review.md)).

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `-session` | (required) | session directory |
| `-window` | `2.5` | utterance↔event join window, seconds |

## Behaviour

- Reads `manifest.json` and `timeline.jsonl` (hinting to run `merge` first
  if the timeline is missing) and writes `report.md` into the session
  directory.
- Join rule: an event attaches to the first utterance whose span
  `[t0 − window, t1 + window]` contains it; each event attaches at most
  once. Unattached events appear as standalone timeline lines in time order.
- Header carries app, participant, duration (`MM:SS`), utterance and event
  counts, and the manifest's task list.
- Utterance lines are quoted with speaker and clock; attached events render
  kind, selector (in backticks), text, value, and route.
- A **Findings** section renders `findings.jsonl` grouped by effective status —
  Confirmed, Unverified, Duplicate, Rejected — each group headed with a count.
  Each finding line carries its id, type, severity, clock, quote, anchor (the
  `ui` selector in backticks and route, else the evidence ids), and, where a
  verdict exists, the verdict and its date. When there is no `findings.jsonl`
  the section is a short, non-fatal notice pointing at `analyze` and `review`.
  Report reads only derived text; it never touches media.
