# `testimony demo`

Serves a small instrumented settings app (embedded in the binary) so a
think-aloud session can be captured end-to-end before any real application is
wired up. It persists two streams into a fresh session directory: raw rrweb
events (archival) and normalised interactions (what `merge` consumes). The
app contains at least one intentional usability flaw, found by talking.

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `:8737` | listen address |
| `-out` | `sessions` | root directory for new session folders |

## Behaviour

- Creates `<out>/<timestamp>/` (format `2006-01-02_150405`) and writes a
  `manifest.json` with `t0_epoch_ms` set to launch time, app
  `"testimony demo"`, participant `"P1"`, and a one-line task.
- Serves the embedded single-page app at `/`; interactive elements carry
  `data-testid` attributes throughout.
- `POST /api/interactions` appends one normalised interaction (single JSON
  object) per line to `interactions.jsonl`; `POST /api/events` appends a JSON
  array of raw rrweb events, one per line, to `events.rrweb.jsonl`. Request
  bodies are validated as JSON and size-limited (8 MiB); writes are
  serialised under a mutex.
- Prints capture instructions on startup: start a voice recorder, say
  "session start" aloud, explore, then Ctrl+C and run
  [`transcribe`](02-transcribe.md) → [`merge`](03-merge.md) →
  [`report`](04-report.md).
- Blocks until interrupted.
