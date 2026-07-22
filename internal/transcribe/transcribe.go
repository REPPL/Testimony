// Package transcribe wraps a local ASR engine (WhisperX or whisper.cpp) to
// turn a session's voice recording into transcript.jsonl — time-aligned
// utterances on the session clock (docs/reference/session-directory.md).
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
	"os"
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
	SessionDir string  // session directory (docs/reference/session-directory.md)
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

	// -audio is optional. When omitted — or when it points at the session's own
	// audio.wav — the canonical 16 kHz mono capture already in the session is
	// used in place (record writes exactly this), so no conversion runs and
	// ffmpeg never rewrites a file onto itself. Otherwise the external recording
	// is converted into audio.wav.
	wav := filepath.Join(opts.SessionDir, session.AudioFile)
	external := opts.Audio != "" && !sameFile(opts.Audio, wav)
	if external {
		if err := convertAudio(opts.Audio, wav); err != nil {
			return 0, err
		}
	} else if err := checkSessionAudio(wav, opts.SessionDir); err != nil {
		return 0, err
	}

	offset, provenance, err := resolveOffset(opts, man, external)
	if err != nil {
		return 0, err
	}
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

// checkSessionAudio validates the in-place audio.wav before its path is handed
// to the ASR engine. The engine opens the path itself, so this read never passes
// through session.OpenFileNoFollow's regular-file guard, and mere existence is
// not enough to establish that reading it will terminate: in a session that was
// shared or downloaded rather than recorded here, a FIFO planted at audio.wav
// satisfies os.Stat and then blocks the engine's read for ever, hanging
// `testimony transcribe` on a session the operator merely received. A symlink is
// resolved by os.Stat and needs no refusal here — a symlink redirects writes,
// and this path is only ever read.
func checkSessionAudio(wav, sessionDir string) error {
	fi, err := os.Stat(wav)
	if err != nil {
		return fmt.Errorf("no %s in session %s and no -audio given: run `testimony record` first, or pass -audio FILE",
			session.AudioFile, sessionDir)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("refusing to read %s: it is not a regular file", wav)
	}
	return nil
}

// resolveOffset picks the audio→session offset: the explicit -offset flag
// wins; otherwise, for an external recording, the offset is derived from its
// creation time vs manifest t0 when ffprobe makes that cheap; otherwise 0.
// For an in-place session audio.wav there is no creation_time to derive from
// and none is needed — capture starts at t0, so 0 is correct by construction.
// A failed ffprobe derivation is never fatal (default 0), but an unusable t0 on
// the external path is: the manifest t0 is read through Manifest.T0 rather than
// the raw T0EpochMS field, because an absent (0) or negative anchor decodes to a
// value that deriveOffset would subtract from the recording's real epoch-second
// creation time, yielding an offset of roughly the whole Unix epoch (~1.78e9 s)
// that mapSegments then adds to every utterance — writing a transcript.jsonl
// with times about fifty-seven years into the session and returning success,
// silent corruption that reads as evidence. Refusing the run is the only honest
// outcome, so an unusable t0 is surfaced as an error rather than fabricated
// times. t0 is consulted only on this external-derivation path: the -offset flag
// and the in-place audio.wav (captured at t0, offset 0) neither derive from nor
// need an anchor, so a missing t0 must not fail them. The second return value is
// the provenance, for the mandatory stdout report.
func resolveOffset(opts Options, man session.Manifest, external bool) (float64, string, error) {
	if opts.OffsetSet {
		return opts.Offset, "from -offset flag", nil
	}
	if external {
		t0, err := man.T0()
		if err != nil {
			return 0, "", fmt.Errorf("deriving audio offset: %w", err)
		}
		if off, ok := deriveOffset(opts.Audio, t0); ok {
			return off, "derived: audio creation_time − manifest t0", nil
		}
		return 0, "default 0: audio creation time unavailable", nil
	}
	return 0, "default 0: session audio.wav captured at t0", nil
}

// mapSegments converts engine segments to the Utterance schema of
// docs/reference/session-directory.md: sequential utt-NNN IDs, offset applied, times
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

// sameFile reports whether a and b resolve to the same on-disk file, so a
// -audio flag pointing at the session's own audio.wav is treated as the
// in-place case rather than converting the file onto itself.
func sameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

// tail returns the trailing portion of subprocess output for error messages.
func tail(b []byte) string {
	s := strings.TrimSpace(string(b))
	const max = 800
	if len(s) > max {
		s = "…" + s[len(s)-max:]
	}
	return s
}
