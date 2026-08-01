# `testimony demo`

Serves a small instrumented settings app (embedded in the binary) so a
think-aloud session can be captured end-to-end before any real application is
wired up. It persists two streams into a fresh session directory: raw rrweb
events (archival) and normalised interactions (what `merge` consumes). The
app contains at least one intentional usability flaw, found by talking.

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `:8737` | listen address (a bare `:port` binds loopback `127.0.0.1` only) |
| `-out` | `sessions` | root directory for new session folders |

## Behaviour

- Creates `<out>/<timestamp>/` (format `2006-01-02_150405`) and writes a
  `manifest.json` with `t0_epoch_ms` set to launch time, app
  `"testimony demo"`, participant `"P1"`, and a one-line task.
- Serves the embedded single-page app at `/`; interactive elements carry
  `data-testid` attributes throughout.
- `POST /api/interactions` appends one normalised interaction (single JSON
  object) per line to `interactions.jsonl`, body size-limited to 4 MiB;
  `POST /api/events` appends a JSON array of raw rrweb events, one per line,
  to `events.rrweb.jsonl`, body size-limited to 8 MiB. Both are validated as
  JSON and re-encoded to a single line so an embedded newline cannot split
  one record across lines; writes are serialised under a mutex. A persisted
  batch answers `204 No Content`; if an append fails the endpoint answers
  `500` rather than a false `204`, so the client does not treat a dropped
  event as captured.
- The capture surface is loopback-only by default and the write endpoints are
  guarded against cross-origin forgery: a request must carry a loopback
  remote address, a loopback `Host`, a loopback `Origin` when present (any
  loopback host, whatever its port), and `Content-Type: application/json`.
  The remote-address check matters on a
  deliberately wide `-addr` bind: without it, a non-loopback client on the
  same network could still forge a loopback `Host` and pass. Together these
  close the CSRF, DNS-rebinding, and forged-`Host` paths by which a web page
  the participant merely has open, or another device on the network, could
  otherwise forge or corrupt evidence.
- Prints capture instructions on startup: start a voice recorder, say
  "session start" aloud, explore, then Ctrl+C and run
  [`transcribe`](02-transcribe.md) → [`merge`](03-merge.md) →
  [`report`](04-report.md).
- Blocks until interrupted.
