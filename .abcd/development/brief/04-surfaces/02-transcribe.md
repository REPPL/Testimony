# `testimony transcribe`

Turns a session's voice recording into `transcript.jsonl` — time-aligned
utterances on the session clock — using a local ASR engine invoked as a
subprocess. Nothing touches the network.

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `-session` | (required) | session directory |
| `-audio` | (required) | voice recording (`.m4a`, `.mov`, or `.wav`) |
| `-engine` | `auto` | `auto` \| `whisperx` \| `whispercpp` — auto prefers WhisperX (word-level timestamps) over `whisper-cli` |
| `-model` | `large-v3-turbo` | Whisper model name, or (whispercpp) a ggml model file path |
| `-language` | `en` | spoken language code |
| `-device` | `auto` | (whisperx) `auto` \| `cpu` \| `cuda` |
| `-compute_type` | `auto` | (whisperx) `auto` \| `int8` \| `float16` \| … |
| `-vad` | `auto` | (whisperx) `auto` \| `silero` \| `pyannote` |
| `-offset` | derived | audio→session clock offset in seconds |

## Behaviour

- Converts the recording to canonical 16 kHz mono PCM `audio.wav` via
  ffmpeg; unsupported extensions and missing tools fail with guidance.
- Resolves the audio→session offset: an explicit `-offset` wins; otherwise
  it is derived from the recording's `creation_time` (ffprobe) minus the
  manifest's `t0_epoch_ms`; otherwise 0. The offset and its provenance are
  always printed.
- Engine subprocesses write machine-readable JSON output files, which are
  parsed — never their human-readable stdout. WhisperX yields word-level
  timestamps; whisper.cpp yields segment-level only.
- `auto` device never selects CUDA on macOS; on CPU the compute type
  defaults to `int8` (CTranslate2 rejects `float16` there). `auto` VAD picks
  silero, because pyannote's checkpoint trips newer torch's `weights_only`
  load and aborts the run.
- whisper.cpp model resolution: an existing file path is used as-is,
  otherwise common cache locations under `$HOME` are searched, and a miss
  carries download guidance.
- Utterances get sequential `utt-NNN` IDs, the offset applied, times rounded
  to two decimals, empty segments skipped, and speaker defaulting to `"P1"`
  when the engine supplies no diarisation label. Output schema:
  [`../05-internals/02-schemas.md`](../05-internals/02-schemas.md).
