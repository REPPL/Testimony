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

// whisperxSegment is how one segment of the engine's output is decoded before
// it is trusted. Start and End are pointers so that an absent time stays
// distinguishable from a genuine 0: a segment of speech at the very start of
// the recording legitimately carries start 0, so a value-typed field cannot
// tell the two apart. With one, a segment whose start whisperx omitted decodes
// to 0 and mapSegments files the utterance at session time 0 — speech planted
// at the head of the evidence record, minutes from where it was actually said,
// with nothing on the record to say the engine never placed it. The word-level
// fields below have carried pointers for exactly this reason since they were
// written; the segment-level ones were the omission. This mirrors the guard
// timeline.rawInteraction and analyze.rawFinding apply to their own untrusted
// times.
type whisperxSegment struct {
	Start   *float64       `json:"start"`
	End     *float64       `json:"end"`
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
		"--vad_method", resolveVAD(opts.VAD),
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
// target — so they are never relied upon: "auto" picks cuda only when an
// NVIDIA GPU is plausibly present (never on darwin),
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

// resolveVAD resolves the -vad "auto" value. whisperx's default VAD (pyannote)
// loads its checkpoint through torch.load, which newer torch versions refuse
// under the weights_only default (omegaconf globals in the pickle) — the run
// aborts before transcribing. silero avoids that path entirely, so "auto"
// picks it; pyannote remains selectable for environments where it works.
func resolveVAD(pref string) string {
	if pref == "" || pref == "auto" {
		return "silero"
	}
	return pref
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
//
// A segment missing either of its own times is a hard error rather than a
// silent default. An unaligned word is a routine, expected outcome the engine
// reports for individual tokens, so dropping it costs only word-level detail;
// a segment without times is a malformed engine response, and the two
// alternatives to refusing it both put a fabrication in the evidence record.
// Defaulting start to 0 relocates the speech. Defaulting end to start yields a
// zero-length utterance, and t1 is not decorative: timeline.EventsNear joins
// interactions to speech over [t0−window, t1+window], so a fabricated end
// quietly shrinks the window and drops the very interactions the utterance was
// about. Refusing the run tells the operator their transcript is incomplete,
// which is the one outcome that leaves nothing false on the record.
func parseWhisperX(raw []byte) ([]segment, error) {
	var out whisperxOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse whisperx JSON: %w", err)
	}
	segs := make([]segment, 0, len(out.Segments))
	for i, s := range out.Segments {
		if s.Start == nil {
			return nil, fmt.Errorf("whisperx segment %d is missing start; cannot place it on the audio clock", i+1)
		}
		if s.End == nil {
			return nil, fmt.Errorf("whisperx segment %d is missing end; cannot say when the speech stopped", i+1)
		}
		seg := segment{start: *s.Start, end: *s.End, text: s.Text, speaker: s.Speaker}
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
