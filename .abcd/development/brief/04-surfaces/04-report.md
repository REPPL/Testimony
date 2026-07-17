# `testimony report`

Renders `timeline.jsonl` as a human-readable Markdown session report — the
raw aligned record of what was said interleaved with what was done.
Structured findings arrive with the analysis layer
([`../01-product/04-analysis.md`](../01-product/04-analysis.md)); until then
the report's Findings section is an explicit "pending" notice.

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
