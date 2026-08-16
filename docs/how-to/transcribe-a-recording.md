# Transcribe a recording

This guide covers the common `testimony transcribe` tasks: transcribing the session's own recording or an external one, choosing an engine, transcribing other languages, tuning WhisperX, and correcting a wrong clock offset. All transcription runs locally; the recording never leaves your machine.

## Transcribe the session's own recording

A session captured with `testimony record` already holds its voice recording as `audio.wav`, so name the session and nothing else:

```sh
testimony transcribe -session sessions/<dir>
```

The command reads that `audio.wav` in place: no conversion runs, and ffmpeg is not needed.

## Transcribe an external recording

When the voice was recorded separately — a `testimony demo` session with QuickTime Player alongside it, say — pass the file with `-audio`:

```sh
testimony transcribe -session sessions/<dir> -audio recording.m4a
```

This variant needs ffmpeg on your PATH (`brew install ffmpeg`). It accepts `.m4a`, `.mov`, and `.wav` recordings, converts the file into a 16 kHz mono `audio.wav` in the session directory, and persists the clock offset it works out beside it in `audio.offset.json`, so a later bare re-run keeps the same alignment.

The recipes below use the bare form; add `-audio FILE` to any of them to transcribe an external recording instead.

## Choose an engine

By default (`-engine auto`) Testimony prefers WhisperX and falls back to whisper.cpp, depending on what is installed.

### WhisperX (preferred: word-level timestamps)

```sh
uv tool install whisperx        # or: pipx install whisperx

testimony transcribe -session sessions/<dir> -engine whisperx
```

WhisperX produces word-level timestamps, which make the utterance-to-event join in reports precise. `-model` names a Whisper model; the default is `large-v3-turbo`.

### whisper.cpp (segment-level timestamps)

```sh
brew install whisper-cpp

testimony transcribe -session sessions/<dir> -engine whispercpp
```

whisper.cpp needs a ggml model file. `-model` accepts either:

- **A model name** (default `large-v3-turbo`): Testimony looks for `ggml-<name>.bin` in `~/.cache/whisper.cpp`, `~/.cache/whisper`, `~/.local/share/whisper.cpp`, and `~/models`. Download one there if missing:

  ```sh
  curl -fL --create-dirs -o ~/.cache/whisper.cpp/ggml-large-v3-turbo.bin \
    https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-turbo.bin
  ```

- **A file path** to an existing ggml model, used as-is:

  ```sh
  testimony transcribe -session sessions/<dir> \
    -engine whispercpp -model ~/models/ggml-base.en.bin
  ```

## Transcribe a language other than English

Pass the spoken language code:

```sh
testimony transcribe -session sessions/<dir> -language de
```

The default is `en`.

## Tune WhisperX: device, precision, VAD

These three flags apply to WhisperX only.

- `-device auto|cpu|cuda` — `auto` picks `cuda` only when an NVIDIA GPU is present (never on macOS), otherwise `cpu`.
- `-compute_type auto|int8|float16|...` — `auto` follows the device: `float16` on CUDA, `int8` on CPU (CPU inference rejects `float16`).
- `-vad auto|silero|pyannote` — voice-activity detection. `auto` picks `silero`; `pyannote` fails to load under newer torch versions, so select it only in environments where you know it works.

Example, forcing CPU with int8:

```sh
testimony transcribe -session sessions/<dir> \
  -device cpu -compute_type int8
```

## Fix a wrong clock offset

`transcribe` shifts audio times onto the session clock and prints the offset it uses, for example:

```
offset: +3.20s (derived: audio creation_time − manifest t0)
```

If the report shows speech clearly misaligned with events, correct the offset using the spoken marker:

1. Find the "session start" utterance in `transcript.jsonl` and note its `t0` — call it `m`.
2. If you said the marker at the moment the session began, it belongs at roughly 0 seconds, so the corrected offset is the printed offset minus `m`. Example: printed offset `+0.00`, marker at `t0: 12.40` → corrected offset `-12.4`.
3. Re-run with the explicit value (an explicit `-offset` always wins over derivation). The bare form works whichever way the audio arrived, because the session already holds `audio.wav`:

   ```sh
   testimony transcribe -session sessions/<dir> -offset -12.4
   ```

   On a session whose audio came from an external recording, this rewrites the offset in `audio.offset.json`, so later bare re-runs keep the correction. On a `record` session there is no sidecar and the value applies to this run. Naming the recording again — `-audio recording.m4a -offset -12.4` — works too, at the cost of converting it a second time.

4. Re-run `testimony merge` and `testimony report` to rebuild the timeline and report.

See [how alignment works](../explanation/how-alignment-works.md) for why the offset exists.
