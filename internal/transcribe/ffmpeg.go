package transcribe

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/REPPL/Testimony/internal/session"
)

// audioExts are the accepted voice-recording containers: QuickTime outputs
// and plain WAV. Everything is normalised through ffmpeg regardless.
var audioExts = map[string]bool{".m4a": true, ".mov": true, ".wav": true}

// convertAudio produces the canonical ASR input — 16 kHz mono PCM WAV — from
// the original recording via an ffmpeg subprocess.
func convertAudio(in, out string) error {
	ext := strings.ToLower(filepath.Ext(in))
	if !audioExts[ext] {
		return fmt.Errorf("unsupported audio format %q: expected .m4a, .mov, or .wav", ext)
	}
	if _, err := os.Stat(in); err != nil {
		return fmt.Errorf("audio file: %w", err)
	}
	// ffmpeg -y follows a symlink at the output path. In an untrusted (shared or
	// downloaded) session directory a pre-planted audio.wav symlink would
	// redirect the write outside the session, so refuse to overwrite through one.
	if fi, err := os.Lstat(out); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write %s: it is a symlink", out)
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("ffmpeg not found on PATH (needed to produce the 16 kHz mono %s): brew install ffmpeg", session.AudioFile)
	}
	cmd := exec.Command(ffmpeg, "-y", "-i", in, "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", out)
	if raw, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %w\n%s", err, tail(raw))
	}
	return nil
}

// deriveOffset reads the original recording's creation time via ffprobe and
// returns creation_epoch_seconds − t0_epoch_seconds. The boolean is false
// whenever ffprobe or the creation_time tag is unavailable — derivation is
// best-effort and never fatal.
func deriveOffset(audio string, t0EpochMS int64) (float64, bool) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return 0, false
	}
	cmd := exec.Command(ffprobe, "-v", "quiet", "-print_format", "json", "-show_format", audio)
	raw, err := cmd.Output()
	if err != nil {
		return 0, false
	}
	var probe struct {
		Format struct {
			Tags struct {
				CreationTime string `json:"creation_time"`
			} `json:"tags"`
		} `json:"format"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return 0, false
	}
	created, ok := parseCreationTime(probe.Format.Tags.CreationTime)
	if !ok {
		return 0, false
	}
	return float64(created.UnixMilli())/1000.0 - float64(t0EpochMS)/1000.0, true
}

// parseCreationTime accepts the RFC3339-ish stamps QuickTime/ffmpeg write,
// e.g. "2026-07-17T15:30:00.000000Z"; a zoneless variant is read as UTC.
func parseCreationTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
