# Command-line reference

```
testimony <command> [flags]
```

Running `testimony` with no command, or with an unknown command, prints the usage text and exits with status 2.

## Exit status

| Status | Meaning |
|---|---|
| 0 | success |
| 1 | runtime error — the message is printed to stderr as `testimony: <error>` |
| 2 | usage error, unknown command, or a stub command |

## `testimony demo`

Serves the instrumented demo app and captures a session.

```
testimony demo [-addr :8737] [-out sessions]
```

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `:8737` | listen address |
| `-out` | `sessions` | root directory for new session folders |

Behaviour: creates a new session directory named after the current time (`YYYY-MM-DD_HHMMSS`) under the `-out` root, writes `manifest.json` (participant `P1`, `t0_epoch_ms` set to now), serves the demo page at `/`, and appends captured events via two endpoints:

- `POST /api/interactions` — one JSON object per request, appended as one line of `interactions.jsonl`.
- `POST /api/events` — a JSON array per request, each element appended as one line of `events.rrweb.jsonl`.

Both accept POST only (405 otherwise), limit the body to 8 MiB, return 204 on success, and 400 on malformed bodies. The command blocks until interrupted (`Ctrl+C`).

## `testimony transcribe`

Transcribes a voice recording into `transcript.jsonl` using a local ASR engine.

```
testimony transcribe -session DIR -audio FILE
                    [-engine auto|whisperx|whispercpp] [-model large-v3-turbo]
                    [-language en] [-offset SECONDS]
                    [-device auto|cpu|cuda] [-compute_type auto|int8|float16]
                    [-vad auto|silero|pyannote]
```

| Flag | Default | Meaning |
|---|---|---|
| `-session` | *(required)* | session directory |
| `-audio` | *(required)* | voice recording (`.m4a`, `.mov`, or `.wav`) |
| `-engine` | `auto` | ASR engine: `auto`, `whisperx`, or `whispercpp`. `auto` prefers `whisperx` on PATH, then `whisper-cli` |
| `-model` | `large-v3-turbo` | Whisper model name, or (whispercpp) a ggml model file path. A whispercpp model name resolves to `ggml-<name>.bin` searched in `~/.cache/whisper.cpp`, `~/.cache/whisper`, `~/.local/share/whisper.cpp`, and `~/models` |
| `-language` | `en` | spoken language code |
| `-device` | `auto` | (whisperx) inference device: `auto`, `cpu`, or `cuda`. `auto` picks `cuda` only when an NVIDIA GPU is present, and never on macOS |
| `-compute_type` | `auto` | (whisperx) compute type: `auto`, `int8`, `float16`, … . `auto` follows the device: `float16` on CUDA, `int8` on CPU |
| `-vad` | `auto` | (whisperx) VAD method: `auto`, `silero`, or `pyannote`. `auto` picks `silero`; `pyannote` fails under newer torch versions |
| `-offset` | derived | audio-to-session clock offset in seconds. When not given, derived from the recording's creation time minus the manifest's `t0_epoch_ms`; 0 when derivation is impossible |

Behaviour: requires ffmpeg on PATH; converts the recording to 16 kHz mono `audio.wav` in the session directory, runs the engine, applies the offset, and writes `transcript.jsonl`. Always prints the offset it used and its provenance (`from -offset flag`, `derived: audio creation_time − manifest t0`, or `default 0: audio creation time unavailable`), then `transcribed N utterances → <path>`.

## `testimony merge`

Merges the transcript and interaction stream into `timeline.jsonl`.

```
testimony merge -session DIR
```

| Flag | Default | Meaning |
|---|---|---|
| `-session` | *(required)* | session directory |

Behaviour: reads `manifest.json`, `transcript.jsonl`, and `interactions.jsonl`; converts interaction epoch-millisecond times to session-relative seconds via `t0_epoch_ms`; writes the time-sorted `timeline.jsonl`; prints `merged N utterances + M events → <path>`.

## `testimony report`

Renders `timeline.jsonl` as a Markdown report.

```
testimony report -session DIR [-window 2.5]
```

| Flag | Default | Meaning |
|---|---|---|
| `-session` | *(required)* | session directory |
| `-window` | `2.5` | utterance-to-event join window, in seconds |

Behaviour: attaches each event to the first utterance whose span, widened by the window on both sides, contains it; events matched by no utterance appear as standalone lines. Writes `report.md` into the session directory and prints `wrote <path>`.

## `testimony record`

Stub. Managed screen and audio capture is not implemented yet; the command prints a notice and exits with status 2. Capture web sessions with `testimony demo` and record voice with QuickTime in the meantime.

## `testimony version`

Prints `testimony <version>` — the version stamped at release, or `dev`.

## `testimony help`

Prints the usage text (also `-h` or `--help`).
