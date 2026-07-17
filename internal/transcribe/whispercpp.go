package transcribe

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// whispercppOutput mirrors the JSON file whisper-cli writes with -oj. Only
// the integer millisecond offsets are used for timing; the human-readable
// "HH:MM:SS,mmm" strings are ignored by design.
type whispercppOutput struct {
	Result struct {
		Language string `json:"language"`
	} `json:"result"`
	Transcription []struct {
		Offsets struct {
			From int64 `json:"from"` // ms
			To   int64 `json:"to"`   // ms
		} `json:"offsets"`
		Text string `json:"text"`
	} `json:"transcription"`
}

// runWhisperCpp transcribes wav with whisper-cli, asking it to write its
// JSON output file (-oj -of) into a temp dir under the session dir, then
// parses that file. whisper.cpp yields segment-level timestamps only.
func runWhisperCpp(bin, wav string, opts Options) ([]segment, error) {
	model, err := resolveModel(opts.Model)
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(opts.SessionDir, "whispercpp-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	outBase := filepath.Join(tmp, "transcript")
	cmd := exec.Command(bin,
		"-m", model,
		"-f", wav,
		"-oj",
		"-of", outBase,
		"--language", opts.Language)
	if raw, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("%s: %w\n%s", whisperCppBinary, err, tail(raw))
	}

	raw, err := os.ReadFile(outBase + ".json")
	if err != nil {
		return nil, fmt.Errorf("%s output: %w", whisperCppBinary, err)
	}
	return parseWhisperCpp(raw)
}

// parseWhisperCpp converts the whisper.cpp JSON into engine-neutral
// segments, using the millisecond offsets.
func parseWhisperCpp(raw []byte) ([]segment, error) {
	var out whispercppOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse %s JSON: %w", whisperCppBinary, err)
	}
	segs := make([]segment, 0, len(out.Transcription))
	for _, t := range out.Transcription {
		segs = append(segs, segment{
			start: float64(t.Offsets.From) / 1000.0,
			end:   float64(t.Offsets.To) / 1000.0,
			text:  t.Text,
		})
	}
	return segs, nil
}

// resolveModel turns the -model value into a ggml model file path: an
// existing file path is used as-is; otherwise common locations under $HOME
// are tried, and the miss carries download guidance.
func resolveModel(model string) (string, error) {
	if fi, err := os.Stat(model); err == nil && !fi.IsDir() {
		return model, nil
	}
	name := "ggml-" + model + ".bin"
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("whisper.cpp model %q: not a file, and no home directory to search for %s", model, name)
	}
	candidates := []string{
		filepath.Join(home, ".cache", "whisper.cpp", name),
		filepath.Join(home, ".cache", "whisper", name),
		filepath.Join(home, ".local", "share", "whisper.cpp", name),
		filepath.Join(home, "models", name),
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("whisper.cpp model %q not found: not a file path, and %s is absent from ~/.cache/whisper.cpp, ~/.cache/whisper, ~/.local/share/whisper.cpp, and ~/models — download it, e.g.\n  curl -L --create-dirs -o ~/.cache/whisper.cpp/%s https://huggingface.co/ggerganov/whisper.cpp/resolve/main/%s\nor pass an existing ggml file path via -model", model, name, name, name)
}
