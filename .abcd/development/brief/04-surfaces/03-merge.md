# `testimony merge`

Merges a session's transcript and interaction stream into a single,
session-relative `timeline.jsonl` — the one artefact the analysis layer
consumes. It is small (kilobytes, not gigabytes), diffable, and archivable
alongside the manifest.

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `-session` | (required) | session directory |

## Behaviour

- Reads `manifest.json`, `transcript.jsonl`, and `interactions.jsonl`; a
  missing file is an error.
- Utterances enter as `src: "speech"` entries at their `t0` (already
  session-relative); interactions enter as `src: "event"` entries with epoch
  milliseconds converted via `t0_epoch_ms`, and are assigned sequential
  `ev-NNN` IDs at merge time.
- Entries are stably sorted by time and written one JSON object per line;
  schema in [`../05-internals/02-schemas.md`](../05-internals/02-schemas.md).
- Prints the count: `merged N utterances + M events → <dir>/timeline.jsonl`.
