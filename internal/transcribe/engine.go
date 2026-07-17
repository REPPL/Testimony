package transcribe

import (
	"errors"
	"fmt"
	"os/exec"
)

// whisperCppBinary is whisper.cpp's CLI as installed by Homebrew.
const whisperCppBinary = "whisper-cli"

const installGuidance = `no ASR engine found on PATH; install one:
  whisperx (word-level timestamps, preferred):  uv tool install whisperx   (or: pipx install whisperx)
  whisper.cpp (segment-level):                  brew install whisper-cpp   (a ggml model file is also needed; see -model)`

// detectEngine resolves the -engine flag to a concrete engine and binary.
// "auto" prefers whisperx (word-level timestamps) over whisper-cli; an
// explicit engine checks only that engine.
func detectEngine(pref string) (engine, bin string, err error) {
	switch pref {
	case "", EngineAuto:
		if p, err := exec.LookPath("whisperx"); err == nil {
			return EngineWhisperX, p, nil
		}
		if p, err := exec.LookPath(whisperCppBinary); err == nil {
			return EngineWhisperCpp, p, nil
		}
		return "", "", errors.New(installGuidance)
	case EngineWhisperX:
		p, err := exec.LookPath("whisperx")
		if err != nil {
			return "", "", fmt.Errorf("whisperx not found on PATH: uv tool install whisperx (or: pipx install whisperx)")
		}
		return EngineWhisperX, p, nil
	case EngineWhisperCpp:
		p, err := exec.LookPath(whisperCppBinary)
		if err != nil {
			return "", "", fmt.Errorf("%s not found on PATH: brew install whisper-cpp (a ggml model file is also needed; see -model)", whisperCppBinary)
		}
		return EngineWhisperCpp, p, nil
	default:
		return "", "", fmt.Errorf("unknown engine %q: want auto, whisperx, or whispercpp", pref)
	}
}
