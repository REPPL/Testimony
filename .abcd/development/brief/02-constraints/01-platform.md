# Platform

- **macOS is the primary target.** Capture depends on the Mac's recording
  affordances (QuickTime for voice today; ScreenCaptureKit later), and the
  code accounts for it explicitly — e.g. `transcribe`'s device auto-detection
  never selects CUDA on Darwin. A macOS app layer is planned to wrap the CLI
  core.
- **Linux is CLI-only**, by design, and stays that way.
- **CI runs on `ubuntu-latest`** (`.github/workflows/ci.yml`), which keeps
  the core honest: everything except live capture must build, test, and run
  the sample pipeline on plain Linux with no ASR engine installed.
- Go version as pinned in `go.mod` (currently 1.22); a single static binary
  built from `cmd/testimony`.
