# Dependencies

- **Zero Go dependencies.** `go.mod` declares no requirements; the CLI is
  standard library only. New dependencies need explicit sign-off first.
- **External capability = subprocess.** Anything the standard library cannot
  do is an external tool invoked as a subprocess whose *machine-readable
  output file* is the contract — never its human-readable stdout:
  - `ffmpeg` — converts recordings to the canonical 16 kHz mono `audio.wav`;
    `ffprobe` reads the recording's `creation_time` for offset derivation
    (best-effort, never fatal).
  - `whisperx` — preferred ASR engine (word-level timestamps via forced
    alignment); parsed from its `--output_format json` file.
  - `whisper-cli` (whisper.cpp) — fallback ASR engine (segment-level
    timestamps); parsed from its `-oj` JSON file.
  - GPU detection uses `nvidia-smi` on PATH as a proxy — no bindings.
- **ASR is local-only.** Transcription never touches the network; missing
  tools fail with install guidance, not degraded cloud fallbacks. See
  [`03-invariants.md`](03-invariants.md) for the privacy boundary this
  serves.
