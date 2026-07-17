---
id: spc-1
slug: record-command
intent: itd-1
---
# record-command

## Summary

`testimony record` is a managed capture launcher: one command creates the
session directory, writes `manifest.json` with the shared `t0_epoch_ms`
anchor, starts the microphone recorder (and, opt-in, the screen recorder) as
subprocesses, prints clear status, and runs until Ctrl+C. On SIGINT/SIGTERM it
stops the recorders cleanly so each capture file is a valid container,
finalises the session, and prints the exact next commands with the real
session path. It reuses `demo`'s session-and-manifest code path, and `-demo`
additionally serves the instrumented demo app into the same directory for a
one-command self-test rig. Audio-only is the default; `-video` opts in.
Downstream `transcribe → merge → report` consume the session with no
hand-noted clocks.

## Design

### Session creation (shared with `demo`)
`demo.Run`'s "make timestamped dir + build Manifest + `SaveManifest`" block is
extracted into `session.Create(outRoot string, now time.Time, m Manifest)
(dir string, err error)`: it derives the `2006-01-02_150405` directory name and
`t0_epoch_ms` from one `now`, `MkdirAll`s, sets `m.Session`, and writes the
manifest. Both `demo.Run` and `record` call it — the manifest is written once,
by the same path, so `t0` is a recorded fact, not a recollection. `record`
populates `app`, `participant`, `tasks` (and `commit` when supplied) from
flags.

### Recorders (macOS first, ffmpeg avfoundation)
Each recorder is an `*exec.Cmd` started in its own process group, argv built by
a **pure** function so it is unit-testable without a device:

- Microphone → `audio.wav` (always):
  `ffmpeg -f avfoundation -i ":<micIndex>" -ac 1 -ar 16000 -c:a pcm_s16le -y <dir>/audio.wav`
  — 16 kHz mono PCM straight into the session dir. These are exactly the
  parameters `transcribe.convertAudio` produces, so the file *is* canonical ASR
  input; no re-conversion is needed downstream (see contract below).
- Screen → `screen.mp4` (only with `-video`):
  `ffmpeg -f avfoundation -framerate 30 -capture_cursor 1 -i "<screenIndex>" -c:v libx264 -preset ultrafast -pix_fmt yuv420p -y <dir>/screen.mp4`
  — video only; the mic is captured by its own process so the ASR audio stays
  independent of the screen recording.

Device indices are resolved once at start-up by parsing
`ffmpeg -f avfoundation -list_devices true -i ""`: the screen input is the
video device whose name matches `Capture screen`, the microphone defaults to
audio index 0 (system default input). The pure argv builders take the resolved
indices as arguments; the impure probe is isolated and skips in CI. A new
`session.ScreenFile = "screen.mp4"` constant is added (with the layout doc,
schemas page, and tests, per the schema-move invariant).

### Transcribe composition (the contract)
`transcribe`'s `-audio` flag becomes **optional**. When omitted, `transcribe`
uses the session's existing `audio.wav` directly and skips `convertAudio`;
when a session already contains `audio.wav` (as every `record` session does),
`testimony transcribe -session DIR` just works. `-audio` retains its current
meaning (convert an external recording into `audio.wav`); if `-audio` points at
the session's own `audio.wav`, it is treated as the omitted case so ffmpeg
never converts a file onto itself. With no external recording there is no
`creation_time` tag, so the audio→session offset defaults to 0 — correct by
construction, because mic capture starts at `t0`; the spoken "session start"
marker remains the belt-and-braces manual calibration.

### Default: audio-only, `-video` opts in
Justified by brief hint (5): the downstream pipeline reads only `audio.wav` and
the interaction stream — `screen.mp4` is retained evidence, never consumed yet.
Audio-only needs only the **Microphone** TCC grant, not **Screen Recording**,
so the common, routine case has the fewest permission prompts and the lowest
barrier — which is the intent's whole value (cheap, repeatable small sessions).
Screen capture is the heavier, more failure-prone, more privacy-sensitive
stream; opt-in via `-video` is the honest default. `-no-video` is the explicit
symmetric off.

### Flags & lifecycle
`testimony record [-out sessions] [-app NAME] [-participant P1]
[-task ...repeatable] [-video|-no-video] [-demo]`. `-task` is a repeatable
`flag.Value` (a `stringSlice`). Lifecycle:
1. Parse flags → build Manifest → `session.Create`.
2. Resolve devices; build recorder argv; start subprocesses (each captures its
   own stderr).
3. With `-demo`, start the demo HTTP server into the **same** dir (see below).
4. Print status: session dir, what is recording, the "say 'session start'"
   prompt, "Press Ctrl+C to stop".
5. `signal.Notify` for SIGINT/SIGTERM; block. On signal: send **SIGINT** to
   each recorder child (ffmpeg finalises the container — writes the trailer/moov
   atom), `Wait` up to ~5 s each, escalate to SIGKILL only on timeout; shut the
   demo server down gracefully; print the next commands with the real dir; exit
   0.

Next commands printed verbatim (real DIR):
`testimony transcribe -session DIR` · `testimony merge -session DIR` ·
`testimony report -session DIR` — note **no** `-audio`, because `audio.wav`
is already present.

### `-demo` composition
`demo.Run` is split so the server is reusable: `demo.Serve(addr, dir string)
(*http.Server, error)` (non-blocking) plus the existing handlers.
`demo` keeps its current behaviour by calling `session.Create` then
`demo.Serve` and blocking. `record -demo` calls `session.Create` once, starts
the recorders, and starts `demo.Serve` into that same dir — so
`interactions.jsonl`/`events.rrweb.jsonl` land beside `audio.wav`/`screen.mp4`:
one command runs the full self-test rig. With `-demo`, `-app` defaults to
`testimony demo` and a demo task is seeded when none is given.

### Linux — honest degradation
No avfoundation and no assumed screen/mic tool. On `runtime.GOOS == "linux"`,
`record` still runs `session.Create` (valid manifest + dir), prints that screen
and microphone capture are unavailable on this platform and that the operator
should record audio externally and run `transcribe -session DIR -audio FILE`,
then exits 0. `-demo` still works (the demo server is cross-platform). What was
skipped is stated explicitly; nothing is silently faked.

### TCC permission-denial UX
TCC denial is the #1 failure mode. Detection: a recorder child that exits
**before** we asked it to stop is classified on two axes — *when* it exited and
*what* its captured stderr says — so the operator is never misdirected:

1. **Within the startup window (~5 s) with an avfoundation signature** in the
   stderr (`avfoundation`, `Input/output error`, `not authorized`, `Failed to`,
   `abort`) → most likely a TCC denial. Because an open failure cannot be proven
   to be TCC vs. a busy device, the message is phrased as "most likely a
   permissions issue" and names the **exact** pane. The failing stream selects it:
   - microphone recorder failed → "System Settings → Privacy & Security →
     **Microphone** — enable your terminal, then re-run."
   - screen recorder failed → "System Settings → Privacy & Security →
     **Screen Recording** — enable your terminal, then re-run."
2. **Within the startup window without an avfoundation signature** → reported as
   a failed start **without** claiming permissions; the ffmpeg tail carries the
   real cause (broken build, bad argv, full disk), so pointing at a TCC pane
   would misdirect.
3. **After the startup window** → the recorder ran for a while, so it cannot be
   a start-up denial. Reported as an unexpected mid-session stop (a device
   disconnect or the recorder dying), never mislabelled as permissions.

Every case appends the raw ffmpeg tail for diagnosis — never a stack trace — and
exits non-zero (1). The classifier is pure: the lifecycle passes it the failing
stream, the exit error, the stderr tail, and whether the exit fell inside the
startup window (derived from the child's elapsed run time).

## Decisions

- **Screen capture: ffmpeg avfoundation, not `screencapture -v`.** ffmpeg is
  already a hard dependency (microphone + `transcribe`), so this adds **zero**
  new dependency; its SIGINT→finalise-container behaviour is battle-tested and
  identical for audio and video, which is exactly the "stop cleanly, finalise
  files" the acceptance criteria demand; and one argv shape means one pure,
  uniformly testable builder. `screencapture -v` was considered — native
  ScreenCaptureKit, efficient, `-g` audio muxing, `-k` click overlay — but its
  signal/finalise semantics are undocumented for programmatic SIGTERM, it emits
  `.mov`, and its built-in audio muxing is irrelevant since the ASR-grade mic
  stream is captured separately regardless. It stays documented as a future
  quality-upgrade path.
- **Both machine devices confirmed present** by the probe (below), so
  avfoundation screen + mic capture is viable on the target Mac.
- **Microphone → transcribe:** `record` writes canonical 16 kHz mono PCM
  `audio.wav`; `transcribe -audio` becomes optional and reuses an existing
  `audio.wav` in place (no self-conversion); offset defaults to 0, correct
  because capture starts at `t0`.
- **Audio-only default, `-video` opt-in:** screen video is retained evidence,
  not yet consumed downstream; audio-only avoids the Screen Recording prompt.

## Test plan

Pure and unit-tested (hermetic, CI-safe on ubuntu with no ffmpeg/TTY):
- **argv construction**: `micArgs`/`screenArgs` → exact `[]string`, table tests.
- **flag logic**: repeatable `-task` (`stringSlice.Set`), `-video`/`-no-video`
  precedence, audio-only default.
- **manifest / session.Create**: flags + fixed `now` → correct Manifest and a
  dir named from `now`; round-trip via `LoadManifest`.
- **lifecycle state machine**: a controller over a `proc` interface
  (`Start`/`Signal`/`Wait`) driven by a fake process — assert children are sent
  SIGINT then reaped, and that a fake ignoring SIGINT is escalated to SIGKILL
  after the timeout; assert the next-commands string carries the real dir.
- **TCC classifier**: pure `(stream, exitErr, stderrTail) → message`, table
  tests over sample avfoundation stderr signatures naming the right pane.
- **platform plan**: pure `plan(GOOS, video) → recorders + skip messages`
  (empty recorder set on linux).

Skipped without tools/TTY (integration; `testing.Short`/OS/tool guards →
`t.Skip`): actually spawning ffmpeg to produce a real `audio.wav`/`screen.mp4`.
Live verification (part of done, not CI): a real `testimony record -demo`
session — speak, click, Ctrl+C — flows through `transcribe → merge → report`
and the report reads correctly; fixes land before the PR.

## How acceptance criteria are satisfied

- **Session dir + populated `t0_epoch_ms`, capture running** — `session.Create`
  writes `manifest.json` with `t0_epoch_ms` from the same `now` that names the
  dir; the mic recorder (and screen with `-video`) is started before the
  command blocks on signals, so capture is live while the session runs.
- **Clean stop leaves artefacts under expected names** — SIGINT/SIGTERM sends
  SIGINT to the ffmpeg children so each finalises its container (`audio.wav`,
  `screen.mp4`), with SIGKILL escalation only on timeout; the demo server (when
  present) is shut down gracefully, leaving `interactions.jsonl`/
  `events.rrweb.jsonl` intact.
- **Downstream consumes `t0` with no manual clock entry** — `transcribe` reads
  the manifest's `t0` and the in-place `audio.wav` (no `-audio`, no
  conversion), and `merge` uses the same `t0`; the printed next commands drive
  exactly this, no hand-noted clocks anywhere.
