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
| 2 | usage error or unknown command |

## `testimony demo`

Serves the instrumented demo app and captures a session.

```
testimony demo [-addr :8737] [-out sessions]
```

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `:8737` | listen address (a bare `:port` binds loopback `127.0.0.1` only) |
| `-out` | `sessions` | root directory for new session folders |

Behaviour: creates a new session directory named after the current time (`YYYY-MM-DD_HHMMSS`) under the `-out` root, writes `manifest.json` (participant `P1`, `t0_epoch_ms` set to now), serves the demo page at `/`, and appends captured events via two endpoints:

- `POST /api/interactions` — one JSON object per request, appended as one line of `interactions.jsonl`.
- `POST /api/events` — a JSON array per request, each element appended as one line of `events.rrweb.jsonl`.

Both accept POST only (405 otherwise), limit the body to 8 MiB, return 204 on success, and 400 on malformed bodies. The command blocks until interrupted (`Ctrl+C`).

## `testimony transcribe`

Transcribes a voice recording into `transcript.jsonl` using a local ASR engine.

```
testimony transcribe -session DIR [-audio FILE]
                    [-engine auto|whisperx|whispercpp] [-model large-v3-turbo]
                    [-language en] [-offset SECONDS]
                    [-device auto|cpu|cuda] [-compute_type auto|int8|float16]
                    [-vad auto|silero|pyannote]
```

| Flag | Default | Meaning |
|---|---|---|
| `-session` | *(required)* | session directory |
| `-audio` | *(optional)* | voice recording (`.m4a`, `.mov`, or `.wav`) to convert into the session's `audio.wav`. Omit to reuse an `audio.wav` already in the session (as a `testimony record` session has); required only when the session has none |
| `-engine` | `auto` | ASR engine: `auto`, `whisperx`, or `whispercpp`. `auto` prefers `whisperx` on PATH, then `whisper-cli` |
| `-model` | `large-v3-turbo` | Whisper model name, or (whispercpp) a ggml model file path. A whispercpp model name resolves to `ggml-<name>.bin` searched in `~/.cache/whisper.cpp`, `~/.cache/whisper`, `~/.local/share/whisper.cpp`, and `~/models` |
| `-language` | `en` | spoken language code |
| `-device` | `auto` | (whisperx) inference device: `auto`, `cpu`, or `cuda`. `auto` picks `cuda` only when an NVIDIA GPU is present, and never on macOS |
| `-compute_type` | `auto` | (whisperx) compute type: `auto`, `int8`, `float16`, … . `auto` follows the device: `float16` on CUDA, `int8` on CPU |
| `-vad` | `auto` | (whisperx) VAD method: `auto`, `silero`, or `pyannote`. `auto` picks `silero`; `pyannote` fails under newer torch versions |
| `-offset` | derived | audio-to-session clock offset in seconds. When not given, derived from the recording's creation time minus the manifest's `t0_epoch_ms`; 0 when derivation is impossible |

Behaviour: with `-audio`, requires ffmpeg on PATH and converts the recording to 16 kHz mono `audio.wav` in the session directory; without it (or when `-audio` points at the session's own `audio.wav`), it uses the existing `audio.wav` in place and skips the conversion. It then runs the engine, applies the offset, and writes `transcript.jsonl`. Always prints the offset it used and its provenance (`from -offset flag`, `derived: audio creation_time − manifest t0`, or `default 0: audio creation time unavailable`), then `transcribed N utterances → <path>`.

## `testimony merge`

Merges the transcript and interaction stream into `timeline.jsonl`.

```
testimony merge -session DIR
```

| Flag | Default | Meaning |
|---|---|---|
| `-session` | *(required)* | session directory |

Behaviour: reads `manifest.json` (required), `transcript.jsonl`, and `interactions.jsonl`; converts interaction epoch-millisecond times to session-relative seconds via `t0_epoch_ms`; writes the time-sorted `timeline.jsonl`; prints `merged N utterances + M events → <path>`. A missing `transcript.jsonl` or `interactions.jsonl` counts as zero records rather than an error, so a default audio-only `record` session (which never writes `interactions.jsonl`) still merges to a speech-only timeline. When interactions are present, `t0_epoch_ms` is required: without it their epoch-millisecond times cannot be placed on the session clock, so merge fails rather than write a corrupt timeline.

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

Managed capture: creates the session directory and manifest, starts the recorders, and runs until interrupted.

```
testimony record [-out sessions] [-app NAME] [-participant P1] [-commit HASH]
                 [-task ...] [-video|-no-video] [-demo [-addr :8737]]
```

| Flag | Default | Meaning |
|---|---|---|
| `-out` | `sessions` | root directory for new session folders |
| `-app` | *(empty)* | application under test; with `-demo`, defaults to the demo app |
| `-participant` | `P1` | participant pseudonym |
| `-commit` | *(empty)* | build/commit hash under test |
| `-task` | *(none)* | a task the participant will attempt; repeat the flag for several tasks |
| `-video` | off | also capture the screen to `screen.mp4` (needs Screen Recording permission) |
| `-no-video` | — | explicitly disable screen capture; this is the default, and it wins when both `-video` and `-no-video` are given |
| `-demo` | off | also serve the instrumented demo app into the same session directory |
| `-addr` | `:8737` | demo server listen address (with `-demo`) |

Behaviour: creates a new session directory named after the current time (`YYYY-MM-DD_HHMMSS`) under the `-out` root and writes `manifest.json` (app, participant, tasks, commit, `t0_epoch_ms` set to now) through the same code path as `demo`. On macOS it captures the default microphone to `audio.wav` (16 kHz mono PCM, the canonical ASR input — no re-conversion needed downstream) and, with `-video`, the screen to `screen.mp4`. Audio-only is the default; `-video` opts in. With `-demo` it also serves the demo app so one command captures voice and clicks into the same directory.

The command blocks until interrupted (`Ctrl+C`). On SIGINT/SIGTERM it sends each recorder an interrupt so it finalises its container, waits up to five seconds, and hard-kills only on timeout. It then validates each recorder's artefact — `audio.wav`, and `screen.mp4` with `-video` — and prints the exact next commands with the real session directory: with a usable `audio.wav` in place it offers `transcribe` → `merge` → `report` without `-audio`, because the recording is already present.

If a recorder leaves no usable artefact — most often because its macOS permission was never granted, so it blocked on the prompt and captured nothing — the command names the missing file, points at the exact System Settings pane (Privacy & Security → Microphone, or → Screen Recording), appends the recorder's output, and exits with status 1. When there is no `audio.wav`, the next-command block omits the bare `transcribe` line and instead keeps `merge` and `report` (interactions may still be captured) and explains how to get audio: re-run `record` after granting the permission, or transcribe an external recording with `-audio FILE`. A recorder that instead exits on its own before it is asked to stop is reported the same way, distinguishing a start-up permissions denial from an unexpected mid-session stop. On platforms other than macOS, capture is unavailable — the command still writes a valid manifest and session directory, states what was skipped, and exits 0.

## `testimony analyze`

The first-pass analysis layer. `analyze` never calls a model, holds no keys, and adds no network dependency: it *emits* a self-contained analysis request that any assistant (or a human) runs, then *ingests* and validates the JSON answer into `findings.jsonl`.

```
testimony analyze -session DIR [-out FILE]        # emit the request
testimony analyze -session DIR -ingest FILE       # validate the answer → findings.jsonl
```

| Flag | Default | Meaning |
|---|---|---|
| `-session` | *(required)* | session directory |
| `-out` | *(stdout)* | emit mode: write the request to `FILE` instead of stdout |
| `-ingest` | *(off)* | ingest mode: validate the answer JSON at `FILE` (or `-` for stdin) into `findings.jsonl` |

`analyze` runs in exactly one mode: emit (no `-ingest`) or ingest (`-ingest`). Combining `-out` and `-ingest` is an error. Both modes read `manifest.json` and `timeline.jsonl`, hinting to run `merge` first when the timeline is missing.

Emit behaviour: writes a single self-contained prompt — the rubric version header (`testimony-analysis/v1`), the second-coder stance, two-pass instructions (segment coding, then session synthesis), the rubric body (five `type` definitions, the `1..4` severity scale, the evidence hard-constraints), the session context (app, participant, tasks), the timeline lines inline, and the required output shape with a worked example. Nothing in the session directory is mutated. The timeline is emitted whole (v1 does not chunk by task boundary; the manifest carries no task timestamps). With `-out FILE` the prompt goes to a file and the command prints `wrote <path>`; otherwise it prints to stdout.

Ingest behaviour: reads the answer from `FILE` (or stdin when `-`), accepting a top-level object with a `findings` array (optionally a `rubric`, which must be a known version) or a bare array. Ingest is the sole validation boundary and never trusts the model. Each finding is decoded with unknown fields disallowed, then checked against every schema rule (see [session directory reference](session-directory.md#findingsjsonl)): id format and uniqueness, `t` within the session, the `type` and `severity` enums, non-empty `evidence` with every id real and at least one spoken `utt-*` anchor, a `quote` that is a verbatim substring of one *cited* evidence utterance, and any `ui` selector/route matching a real event. Validation is transactional — all errors are reported at once and nothing is written on any failure. On success every finding is forced to `status: unverified`, `findings.jsonl` is written, and the command prints `validated N findings → <path> (all unverified)`. An answer with no findings (a bare `[]`, `{"findings":[]}`, or a truncated file) is refused rather than written, so it cannot erase a prior `findings.jsonl`. Ingest refuses to overwrite a `findings.jsonl` that already holds verdict records — counting any `kind:"verdict"` line, even one whose value is outside the closed enum.

## `testimony review`

Records a human verdict on each candidate finding, appended to `findings.jsonl` without ever rewriting a finding in place — the finding's birth state and the full verdict history are retained as the precision measure.

```
testimony review -session DIR
testimony review -session DIR -finding F-NNN -verdict confirmed|rejected|duplicate-of-F-NNN
```

| Flag | Default | Meaning |
|---|---|---|
| `-session` | *(required)* | session directory |
| `-finding` | *(interactive)* | non-interactive: the finding to judge (`F-NNN`) |
| `-verdict` | *(interactive)* | non-interactive: `confirmed`, `rejected`, or `duplicate-of-F-NNN` |

Behaviour: loads findings and existing verdicts (hinting to run `analyze -ingest` first when there is no `findings.jsonl`) and computes each finding's effective status (every finding starts `unverified`; the last verdict for a finding wins).

Interactive (`review -session DIR`): walks the `unverified` findings in id order, printing each finding's clock, type, severity, quote, and anchor, then prompting `[c]onfirm [r]eject [d]uplicate-of [s]kip [q]uit`; `d` asks for the canonical `F-NNN`. Each decision appends a verdict record stamped with today's date. Interactive mode is TTY-gated: when stdin is not a terminal it prints a one-line notice and exits 0, so CI never blocks.

Non-interactive (`-finding F-003 -verdict confirmed`, or `-verdict duplicate-of-F-002`): validates that the finding exists, the verdict parses, and any duplicate target exists and differs; appends one verdict record and prints a one-line confirmation. A verdict may be appended even when one already exists (append-only correction; the latest wins). The stored verdict enum is exactly `confirmed | rejected | duplicate`; `duplicate-of-F-NNN` is stored as `verdict: "duplicate"` with `of: "F-NNN"`.

## `testimony version`

Prints `testimony <version>` — the version stamped at release, or `dev`.

## `testimony help`

Prints the usage text (also `-h` or `--help`).
