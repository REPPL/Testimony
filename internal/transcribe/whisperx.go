package transcribe

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/REPPL/Testimony/internal/timeline"
)

// whisperxOutput mirrors the JSON file WhisperX writes with
// --output_format json.
type whisperxOutput struct {
	Segments []whisperxSegment `json:"segments"`
	Language string            `json:"language"`
}

type whisperxSegment struct {
	Start   float64        `json:"start"`
	End     float64        `json:"end"`
	Text    string         `json:"text"`
	Speaker string         `json:"speaker"` // diarisation label, when enabled
	Words   []whisperxWord `json:"words"`
}

type whisperxWord struct {
	Word    string   `json:"word"`
	Start   *float64 `json:"start"` // absent when alignment failed for the word
	End     *float64 `json:"end"`
	Score   *float64 `json:"score"`
	Speaker string   `json:"speaker"`
}

// runWhisperX transcribes wav by asking whisperx to write its JSON output
// file into a temp dir under the session dir, then parses that file.
func runWhisperX(bin, wav string, opts Options) ([]segment, error) {
	tmp, err := os.MkdirTemp(opts.SessionDir, "whisperx-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	device, compute := resolveCompute(opts.Device, opts.Compute, runtime.GOOS, cudaVisible())
	cmd := exec.Command(bin, wav,
		"--model", opts.Model,
		"--language", opts.Language,
		"--device", device,
		"--compute_type", compute,
		"--output_format", "json",
		"--output_dir", tmp)
	if raw, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("whisperx: %w\n%s", err, tail(raw))
	}

	base := strings.TrimSuffix(filepath.Base(wav), filepath.Ext(wav))
	raw, err := os.ReadFile(filepath.Join(tmp, base+".json"))
	if err != nil {
		return nil, fmt.Errorf("whisperx output: %w", err)
	}
	return parseWhisperX(raw)
}

// resolveCompute resolves the -device and -compute_type "auto" values to
// concrete whisperx arguments. whisperx's own CLI defaults (cuda + float16)
// abort at startup on machines without CUDA — notably macOS, the primary
// target (docs/architecture.md §5) — so they are never relied upon: "auto"
// picks cuda only when an NVIDIA GPU is plausibly present (never on darwin),
// and the compute type follows the device (float16 needs a GPU; CTranslate2
// rejects it on CPU, where int8 is the sensible default).
func resolveCompute(devicePref, computePref, goos string, hasCUDA bool) (device, compute string) {
	device = devicePref
	if device == "" || device == "auto" {
		if goos != "darwin" && hasCUDA {
			device = "cuda"
		} else {
			device = "cpu"
		}
	}
	compute = computePref
	if compute == "" || compute == "auto" {
		if device == "cuda" {
			compute = "float16"
		} else {
			compute = "int8"
		}
	}
	return device, compute
}

// cudaVisible reports whether an NVIDIA GPU is plausibly available, using
// nvidia-smi on PATH as the proxy (no bindings, per the zero-dependency
// rule; the driver always ships it).
func cudaVisible() bool {
	_, err := exec.LookPath("nvidia-smi")
	return err == nil
}

// parseWhisperX converts the WhisperX JSON into engine-neutral segments.
// Words without a start timestamp (alignment miss) are omitted.
func parseWhisperX(raw []byte) ([]segment, error) {
	var out whisperxOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse whisperx JSON: %w", err)
	}
	segs := make([]segment, 0, len(out.Segments))
	for _, s := range out.Segments {
		seg := segment{start: s.Start, end: s.End, text: s.Text, speaker: s.Speaker}
		for _, w := range s.Words {
			if w.Start == nil {
				continue
			}
			seg.words = append(seg.words, timeline.Word{W: w.Word, T: *w.Start})
		}
		segs = append(segs, seg)
	}
	return segs, nil
}
