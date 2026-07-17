# `testimony record`

A managed capture launcher: one command creates the session directory, writes
the manifest with the shared `t0_epoch_ms` anchor, starts the recorders as
subprocesses, prints status, and runs until Ctrl+C. On interrupt it stops each
recorder cleanly, finalises the capture files, and prints the exact downstream
commands with the real session path — so a session flows into
[`transcribe`](02-transcribe.md) → [`merge`](03-merge.md) →
[`report`](04-report.md) with no hand-noted clocks. Audio-only is the default;
screen video is opt-in retained evidence, not yet consumed downstream.

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `-out` | `sessions` | root directory for new session folders |
| `-app` | *(empty)* | application under test (with `-demo`, defaults to the demo app) |
| `-participant` | `P1` | participant pseudonym |
| `-commit` | *(empty)* | build/commit hash under test |
| `-task` | *(none)* | a task the participant will attempt (repeatable) |
| `-video` | off | also capture the screen to `screen.mp4` (needs Screen Recording permission) |
| `-no-video` | — | explicitly disable screen capture (the default; wins when both are given) |
| `-demo` | off | also serve the instrumented demo app into the session |
| `-addr` | `:8737` | demo server listen address (with `-demo`) |

## Behaviour

- Creates `<out>/<timestamp>/` (format `2006-01-02_150405`) and writes
  `manifest.json` through the same code path as [`demo`](01-demo.md), so
  `t0_epoch_ms` is set once, from the same instant that names the directory. The
  manifest carries the app under test, participant pseudonym, task list, and the
  commit hash when supplied.
- On macOS, captures the default microphone to `audio.wav` — canonical 16 kHz
  mono PCM, the exact input the ASR step expects, so no re-conversion is needed
  downstream. With `-video`, it also captures the screen to `screen.mp4` (H.264).
  Each stream is an independent subprocess, so the ASR audio stays clean of the
  screen recording.
- Device indices are resolved once at start-up by probing the platform's
  audio/video device listing: the microphone is the system default input and the
  screen is the "Capture screen" device.
- `-demo` serves the instrumented demo app into the *same* session directory, so
  the captured interactions land beside the audio — one command runs the full
  self-test rig. It defaults `-app` to the demo app and seeds a demo task when
  none is given.
- Runs until interrupted. The interrupt handler is installed before any recorder
  starts, and each recorder runs in its own process group, so Ctrl+C is handled
  by `record` alone — no child is ever orphaned still recording.
- On SIGINT/SIGTERM, sends each recorder an interrupt so it finalises its
  container, waits up to five seconds, and escalates to a hard kill only on
  timeout; a running demo server is shut down gracefully. It then validates each
  recorder's artefact — `audio.wav`, and `screen.mp4` with `-video` — and prints
  the next commands carrying the real directory. With a usable `audio.wav` in
  place it offers `transcribe`, `merge`, `report` with **no** `-audio`, because
  the recording is already present. The audio→session offset defaults to 0,
  correct because capture starts at `t0`; the spoken "session start" marker
  remains the manual calibration fallback.
- A recorder that leaves no usable artefact — an absent or empty file, most often
  because it blocked on its macOS permission prompt for the whole session and was
  reaped only at stop — is reported actionably rather than silently swallowed by
  the exit code: the command names the missing file, states that the most likely
  cause is the corresponding permission (Microphone, or Screen Recording for the
  screen stream) never being granted for the launching terminal, appends the raw
  recorder tail, and exits non-zero. When there is no `audio.wav`, the
  next-command block omits the bare `transcribe` line and instead keeps `merge`
  and `report` (interactions may still be captured) plus a line explaining that
  transcribe needs audio: re-run `record` after granting the permission, or
  transcribe an external recording with `-audio FILE`. An empty
  `events.rrweb.jsonl` at finalise is fine — the browser may simply not batch any
  rrweb events — and is not warned about.
- A recorder that instead exits before it is asked to stop is reported the same
  way, never as a stack trace: within the start-up window with an open-failure
  signature it is most likely a permissions denial and names the exact System
  Settings pane; within the window without that signature it is a failed start
  whose cause is in the recorder output; after the window it is an unexpected
  mid-session stop (for example a disconnected device). The raw recorder tail is
  always appended, and the command exits non-zero.
- On platforms other than macOS, capture is unavailable: `record` still writes a
  valid manifest and session directory, states that microphone and screen capture
  were skipped, advises recording audio externally and running
  `transcribe -session DIR -audio FILE`, and exits cleanly. `-demo` still serves
  the demo app, since it is cross-platform.
