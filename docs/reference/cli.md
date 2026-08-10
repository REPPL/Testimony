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
| 2 | usage error — no command, an unknown command, a stray positional argument (no command takes one), an unparseable or invalid flag value, a missing required flag, or a flag combination that is not allowed |

## `testimony demo`

Serves the instrumented demo app and captures a session.

```
testimony demo [-addr :8737] [-out sessions]
```

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `:8737` | listen address (a bare `:port` binds loopback `127.0.0.1` only) |
| `-out` | `sessions` | root directory for new session folders |

Behaviour: creates a new session directory named after the current time (`YYYY-MM-DD_HHMMSS`) under the `-out` root, writes `manifest.json` (app `testimony demo`, participant `P1`, one seeded default task, `t0_epoch_ms` set to now), serves the demo page at `/`, and appends captured events via two endpoints:

- `POST /api/interactions` — one JSON object per request, appended as one line of `interactions.jsonl`.
- `POST /api/events` — a JSON array per request, each element appended as one line of `events.rrweb.jsonl`.

Both accept POST only (405 otherwise) and require a loopback remote peer, a loopback `Host`, and — when an `Origin` header is present — a loopback origin, and `Content-Type: application/json` (403 for any of the first three, 415 for the last). This guards the unauthenticated write endpoints against cross-site and DNS-rebinding forgery, and against a non-browser client on the same network simply forging a loopback `Host`. They return 204 on success and 400 on malformed bodies; `/api/interactions` also refuses with 400 any record `merge` would refuse — a body that is not a JSON object, or one missing the required `t` (a positive epoch-millisecond time on a plausible session clock) or `kind`. `POST /api/interactions` limits the body to 4 MiB — the readable JSONL line limit, since one request becomes one line — and also refuses with 413 a record that fits that limit alone but whose timeline entry would not once `merge` wraps it (a `src`/`id`/`payload` envelope on the session-relative clock, see [`timeline.jsonl`](session-directory.md#timelinejsonl)), one that would push `interactions.jsonl` past the session's 16 MiB total-size limit, or one whose own timeline entry would push the session's merged `timeline.jsonl` past that same limit even while `interactions.jsonl` itself stays under it (see [`session-directory.md`](session-directory.md)); `POST /api/events` limits the batch body to 8 MiB, refusing with 413 a batch over that cap or a batch element that would itself exceed the 4 MiB line limit — `events.rrweb.jsonl` is archival and carries no total-size cap. A body that passes every check but fails to append (a filesystem error) answers 500 rather than a false 204. Every refused capture write is logged to stderr, the operator's only signal, since the page posts via `sendBeacon`, which surfaces no status. The command blocks until interrupted (`Ctrl+C`).

The loopback remote-peer requirement means an explicit non-loopback `-addr` host (e.g. `0.0.0.0:8737`) serves the page to other devices but refuses their capture posts — even one that sends a loopback `Host` — so only the machine running `record`/`demo` itself can ever write evidence; the command warns at startup that such a bind serves the page only.

## `testimony transcribe`

Transcribes a voice recording into `transcript.jsonl` using a local ASR engine.

```
testimony transcribe -session DIR [-audio FILE]
                    [-engine auto|whisperx|whispercpp] [-model large-v3-turbo]
                    [-language en] [-offset SECONDS]
                    [-device auto|cpu|cuda] [-compute_type auto|int8|float16|…]
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
| `-offset` | derived | audio-to-session clock offset in seconds. When not given: with `-audio` naming a file other than the session's own `audio.wav`, derived from the recording's creation time minus the manifest's `t0_epoch_ms`, or 0 when derivation is impossible; without it (including `-audio audio.wav`), read back from `audio.offset.json` when the session has one, else 0. A non-finite value, or one beyond ±10⁹ seconds (the bound every derived or persisted offset already meets), is a usage error |

Behaviour: reads `manifest.json` (required). With `-audio`, requires ffmpeg on PATH and converts the recording to 16 kHz mono `audio.wav` in the session directory; without it (or when `-audio` points at the session's own `audio.wav`), it uses the existing `audio.wav` in place and skips the conversion. It then runs the engine, applies the offset, and writes `transcript.jsonl`. Always prints the offset it used and its provenance — one of:

- `from -offset flag` — the explicit flag, which always wins;
- `derived: audio creation_time − manifest t0` — derived for an external recording;
- `default 0: audio creation time unavailable` — an external recording whose creation time could not be read;
- `persisted: audio.wav converted from an external recording (+3.20s)` — read back from `audio.offset.json`, the printed value being the persisted offset;
- `default 0: session audio.wav captured at t0` — a session whose `audio.wav` was captured here and has no sidecar.

It then prints `transcribed N utterances → <path>`. With an `-audio` that names a file other than the session's own `audio.wav`, the offset in force is written to `audio.offset.json`; without it (including `-audio audio.wav`), the sidecar is rewritten only when an explicit `-offset` is given and the session already has one. A later bare run reuses the persisted value.

## `testimony merge`

Merges the transcript and interaction stream into `timeline.jsonl`.

```
testimony merge -session DIR
```

| Flag | Default | Meaning |
|---|---|---|
| `-session` | *(required)* | session directory |

Behaviour: reads `manifest.json` (required), `transcript.jsonl`, and `interactions.jsonl`; converts interaction epoch-millisecond times to session-relative seconds via `t0_epoch_ms`; writes the time-sorted `timeline.jsonl`; prints `merged N utterances + M events → <path>`. A missing `transcript.jsonl` or `interactions.jsonl` counts as zero records rather than an error, so a default audio-only `record` session (which never writes `interactions.jsonl`) still merges to a speech-only timeline. If the two sources together yield zero entries — missing, empty, or both — and a `timeline.jsonl` from an earlier merge already exists and is non-empty, merge refuses rather than truncate it to zero entries; a session with no timeline yet, or one already empty, still merges to an empty one. When interactions are present, `t0_epoch_ms` is required: without it their epoch-millisecond times cannot be placed on the session clock, so merge fails rather than write a corrupt timeline.

## `testimony report`

Renders `timeline.jsonl` as a Markdown report.

```
testimony report -session DIR [-window 2.5]
```

| Flag | Default | Meaning |
|---|---|---|
| `-session` | *(required)* | session directory |
| `-window` | `2.5` | utterance-to-event join window, in seconds |

Behaviour: reads `manifest.json` (required) and `timeline.jsonl`, plus `findings.jsonl` when present (the Findings section; without the file it is a short notice pointing at `analyze` and `review`; if the file exists but cannot be read, the section reports that instead, and the command still exits `0`). A timeline entry whose `src` is neither `speech` nor `event` is refused rather than silently omitted from the rendered record. Attaches each event to the first utterance whose span, widened by the window on both sides, contains it; events matched by no utterance appear as standalone lines. Writes `report.md` into the session directory and prints `wrote <path>`.

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
| `-task` | *(none)* | a task the participant will attempt; repeat the flag for several tasks. With `-demo` and no `-task`, the demo app's default task is seeded, matching a standalone `demo` session |
| `-video` | off | also capture the screen to `screen.mp4` (needs Screen Recording permission) |
| `-no-video` | — | explicitly disable screen capture; this is the default, and it wins when both `-video` and `-no-video` are given |
| `-demo` | off | also serve the instrumented demo app into the same session directory |
| `-addr` | `:8737` | demo server listen address (with `-demo`) |

Behaviour: creates a new session directory named after the current time (`YYYY-MM-DD_HHMMSS`) under the `-out` root and writes `manifest.json` (app, participant, tasks, commit, `t0_epoch_ms` set to now) through the same code path as `demo`, refusing the write if repeated `-task`/`-app` text would push it past its own 1 MiB size limit (see [`session-directory.md`](session-directory.md#manifestjson)). On macOS it captures the default microphone to `audio.wav` (16 kHz mono PCM, the canonical ASR input — no re-conversion needed downstream) and, with `-video`, the screen to `screen.mp4`; both recorders need ffmpeg on `PATH`. Audio-only is the default; `-video` opts in. With `-demo` it also serves the demo app so one command captures voice and clicks into the same directory.

The command blocks until interrupted (`Ctrl+C`). On SIGINT/SIGTERM — and on SIGHUP, so closing the terminal window mid-session finalises exactly like Ctrl+C — it sends each recorder an interrupt so it finalises its container, waits up to five seconds, and hard-kills only on timeout. It then validates each recorder's artefact — `audio.wav`, and `screen.mp4` with `-video` — and prints the exact next commands with the real session directory: with a usable `audio.wav` in place it offers `transcribe` → `merge` → `report` without `-audio`, because the recording is already present.

Any of the following leaves the command exiting with status 1, each diagnosed differently, and each appending the recorder's own output for diagnosis:

- A recorder that leaves no usable artefact at all — most often because its macOS permission was never granted, so it blocked on the prompt and captured nothing until it was stopped — names the missing file and points at the exact System Settings pane (Privacy & Security → Microphone, or → Screen Recording).
- A recorder that instead exits on its own before it is asked to stop is diagnosed by when: at start-up with ffmpeg's own permissions-failure signature, the same System Settings pane is named; an early exit without that signature, or an exit after the session was already underway, is reported without a pane pointer, since the cause is not provably a permission.
- A recorder that is still running when asked to stop but does not finalise its container within five seconds is force-killed and flagged as likely truncated or unplayable even when it left data — a microphone recording usually survives a kill intact and is still offered to `transcribe`, but the command still exits 1 to flag the risk.

When there is no usable `audio.wav`, the next-command block omits the bare `transcribe` line and instead keeps `merge` and `report` (interactions may still be captured) and explains how to get audio: on a platform with microphone capture, re-run `record` after granting the permission; either way, transcribe an external recording with `-audio FILE`.

On platforms other than macOS, audio and screen capture are unavailable, and the status output states what was skipped. Without `-demo`, the command exits 0 immediately: it still writes a valid manifest and session directory. With `-demo` it still serves the demo app and captures clicks — the demo server is not macOS-specific — so the command blocks until interrupted exactly as it does on macOS; the next-command block omits the bare `transcribe` line because there is no audio.

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

`analyze` runs in exactly one mode: emit (no `-ingest`) or ingest (`-ingest`). Combining `-out` and `-ingest` is an error. Emit reads `manifest.json` and `timeline.jsonl`; ingest reads `timeline.jsonl` only. Both hint to run `merge` first when the timeline is missing, and both refuse a timeline whose entries carry a `src` other than `speech` or `event`, or a duplicated entry id — findings cite evidence by id, so a reused one cannot be resolved unambiguously (a `merge`-produced timeline never carries either defect: `merge` refuses a transcript whose utterance ids repeat or collide with the `ev-NNN` event ids it synthesises).

Emit behaviour: writes a single self-contained prompt — the rubric version header (`testimony-analysis/v1`), the second-coder stance, two-pass instructions (segment coding, then session synthesis), the rubric body (five `type` definitions, the `1..4` severity scale, the evidence hard-constraints), the session context (app, participant, tasks), the timeline lines inline, and the required output shape with a worked example. Nothing in the session directory is mutated. The timeline is emitted whole (v1 does not chunk by task boundary; the manifest carries no task timestamps). With `-out FILE` the prompt goes to a file and the command prints `wrote <path>`; otherwise it prints to stdout.

Ingest behaviour: reads the answer from `FILE` (or stdin when `-`), accepting a top-level object with a `findings` array (optionally a `rubric`, which must be a known version) or a bare array. Ingest is the sole validation boundary and never trusts the model. Each finding is decoded with unknown fields disallowed, then checked against every schema rule (see [session directory reference](session-directory.md#findingsjsonl)): id format and uniqueness, `t` within the session, the `type`, `severity`, and `mode` enums, non-empty `evidence` of at most 64 ids with every id real and at least one spoken `utt-*` anchor, a `quote` that is a verbatim substring of one *cited* evidence utterance, and any `ui` selector/route matching a real event. Validation is transactional — all errors are reported at once and nothing is written on any failure. On success every finding is forced to `status: unverified`, `findings.jsonl` is written, and the command prints `validated N findings → <path> (all unverified)`. An answer with no findings (a bare `[]`, `{"findings":[]}`, or a truncated file) is refused rather than written, so it cannot erase a prior `findings.jsonl`; an answer whose findings would together push `findings.jsonl` past the session's 16 MiB total-size limit is refused the same way (see [`session-directory.md`](session-directory.md)). Ingest refuses to overwrite a `findings.jsonl` that already holds verdict records — counting any `kind:"verdict"` line, even one whose value is outside the closed enum.

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

Interactive (`review -session DIR`): walks the `unverified` findings in id order, printing each finding's id, type, severity, clock, quote, and anchor, then prompting `[c]onfirm [r]eject [d]uplicate-of [s]kip [q]uit`; `d` asks for the canonical `F-NNN`. Each decision appends a verdict record stamped with today's date. Interactive mode is gated on stdin being a character device — true for an interactive terminal, but also for `/dev/null`, so this is not simply "not a terminal". When stdin is a pipe or a redirected regular file, `review` prints a one-line notice and exits 0 instead of walking, so CI never blocks; redirected from `/dev/null` (`< /dev/null`) it still enters the walk and immediately reaches end of input, since redirection makes stdin the character device itself rather than a pipe reading from it.

Non-interactive (`-finding F-003 -verdict confirmed`, or `-verdict duplicate-of-F-002`): validates that the finding exists, the verdict parses, and any duplicate target exists and differs; appends one verdict record and prints a one-line confirmation. A verdict may be appended even when one already exists (append-only correction; the latest wins), unless appending it would push `findings.jsonl` past the session's 16 MiB total-size limit, which both interactive and non-interactive `review` refuse (see [`session-directory.md`](session-directory.md)). The stored verdict enum is exactly `confirmed | rejected | duplicate`; `duplicate-of-F-NNN` is stored as `verdict: "duplicate"` with `of: "F-NNN"`.

## `testimony version`

Prints `testimony <version>` — the version stamped at release, or `dev`.

## `testimony help`

Prints the usage text (also `-h` or `--help`).
