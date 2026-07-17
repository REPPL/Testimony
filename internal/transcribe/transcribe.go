// Package transcribe wraps a local ASR engine (WhisperX or whisper.cpp) to
// turn a session's voice recording into transcript.jsonl — time-aligned
// utterances on the session clock (docs/architecture.md §5).
//
// The pipeline: convert the recording to 16 kHz mono audio.wav (ffmpeg),
// run the engine so it writes a machine-readable JSON file, parse that file
// (never its human-readable stdout), shift times by the audio→session
// offset, and write the utterances via session.WriteJSONL. Everything runs
// locally; nothing here touches the network.
package transcribe

import (
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strings"

	"github.com/REPPL/Testimony/internal/session"
	"github.com/REPPL/Testimony/internal/timeline"
)

// Engine names accepted by Options.Engine.
const (
	EngineAuto       = "auto"
	EngineWhisperX   = "whisperx"
	EngineWhisperCpp = "whispercpp"
)

// Options configures one transcription run.
type Options struct {
	SessionDir string  // session directory (docs/architecture.md §11)
	Audio      string  // original voice recording (.m4a, .mov, or .wav)
	Engine     string  // auto | whisperx | whispercpp
	Model      string  // Whisper model name, or (whispercpp) a ggml file path
	Language   string  // spoken language code, e.g. "en"
	Device     string  // (whisperx) auto | cpu | cuda
	Compute    string  // (whisperx) auto | int8 | float16 | float32 | ...
	VAD        string  // (whisperx) auto | silero | pyannote
	Offset     float64 // audio→session clock offset in seconds
	OffsetSet  bool    // true when -offset was given explicitly
	Log        io.Writer
}

// segment is an engine-neutral transcription segment, times on the audio
// clock (seconds from the start of the recording).
type segment struct {
	start, end    float64
	text, speaker string
	words         []timeline.Word // word start times, audio clock
}

// Run performs the full pipeline and returns the number of utterances
// written to transcript.jsonl in the session directory.
func Run(opts Options) (int, error) {
	man, err := session.LoadManifest(opts.SessionDir)
	if err != nil {
		return 0, err
	}
	engine, bin, err := detectEngine(opts.Engine)
	if err != nil {
		return 0, err
	}

	wav := filepath.Join(opts.SessionDir, session.AudioFile)
	if err := convertAudio(opts.Audio, wav); err != nil {
		return 0, err
	}

	offset, provenance := resolveOffset(opts, man.T0EpochMS)
	fmt.Fprintf(opts.Log, "offset: %+.2fs (%s)\n", offset, provenance)

	var segs []segment
	switch engine {
	case EngineWhisperX:
		segs, err = runWhisperX(bin, wav, opts)
	case EngineWhisperCpp:
		segs, err = runWhisperCpp(bin, wav, opts)
	}
	if err != nil {
		return 0, err
	}

	utts := mapSegments(segs, offset)
	out := filepath.Join(opts.SessionDir, session.TranscriptFile)
	if err := session.WriteJSONL(out, utts); err != nil {
		return 0, fmt.Errorf("write transcript: %w", err)
	}
	return len(utts), nil
}

// resolveOffset picks the audio→session offset: the explicit -offset flag
// wins; otherwise the offset is derived from the original recording's
// creation time vs manifest t0 when ffprobe makes that cheap; otherwise 0.
// Derivation failure is never fatal. The second return value is the
// provenance, for the mandatory stdout report.
func resolveOffset(opts Options, t0EpochMS int64) (float64, string) {
	if opts.OffsetSet {
		return opts.Offset, "from -offset flag"
	}
	if off, ok := deriveOffset(opts.Audio, t0EpochMS); ok {
		return off, "derived: audio creation_time − manifest t0"
	}
	return 0, "default 0: audio creation time unavailable"
}

// mapSegments converts engine segments to the Utterance schema of
// docs/architecture.md §5: sequential utt-NNN IDs, offset applied, times
// rounded to 2 decimal places, whitespace trimmed, empty segments skipped,
// speaker defaulting to "P1" when the engine supplies no diarisation label.
func mapSegments(segs []segment, offset float64) []timeline.Utterance {
	var utts []timeline.Utterance
	for _, s := range segs {
		text := strings.TrimSpace(s.text)
		if text == "" {
			continue
		}
		speaker := s.speaker
		if speaker == "" {
			speaker = "P1"
		}
		u := timeline.Utterance{
			ID:      fmt.Sprintf("utt-%03d", len(utts)+1),
			T0:      round2(s.start + offset),
			T1:      round2(s.end + offset),
			Speaker: speaker,
			Text:    text,
		}
		for _, w := range s.words {
			word := strings.TrimSpace(w.W)
			if word == "" {
				continue
			}
			u.Words = append(u.Words, timeline.Word{W: word, T: round2(w.T + offset)})
		}
		utts = append(utts, u)
	}
	return utts
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }

// tail returns the trailing portion of subprocess output for error messages.
func tail(b []byte) string {
	s := strings.TrimSpace(string(b))
	const max = 800
	if len(s) > max {
		s = "…" + s[len(s)-max:]
	}
	return s
}
