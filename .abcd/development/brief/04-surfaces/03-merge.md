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

- Reads `manifest.json` (required), `transcript.jsonl`, and
  `interactions.jsonl`. A missing `transcript.jsonl` or `interactions.jsonl` is
  treated as zero records, not an error, so a default audio-only `record`
  session (which never writes `interactions.jsonl`) still merges to a
  speech-only timeline, and a session merged before transcription still merges
  to an event-only one. A malformed line in either file is still an error.
- Utterances enter as `src: "speech"` entries at their `t0` (already
  session-relative); interactions enter as `src: "event"` entries with epoch
  milliseconds converted via `t0_epoch_ms`, and are assigned sequential
  `ev-NNN` IDs at merge time.
- When interactions are present, `t0_epoch_ms` is required: without it the
  epoch-millisecond interaction times cannot be placed on the session clock, so
  merge rejects the session rather than emit a silently corrupt timeline. A
  transcript-only session (no interactions) is already session-relative and is
  unaffected.
- Entries are stably sorted by time and written one JSON object per line;
  schema in [`../05-internals/02-schemas.md`](../05-internals/02-schemas.md).
- Prints the count: `merged N utterances + M events → <dir>/timeline.jsonl`.
