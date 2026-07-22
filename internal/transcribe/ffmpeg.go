package transcribe

import (
	"encoding/json"
	"errors"
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
	// os.Stat resolves a symlink, so an operator pointing -audio at a symlinked
	// recording is fine; what must be refused is a non-regular target, because a
	// FIFO handed to ffmpeg as its input blocks the subprocess's open(2) for ever,
	// waiting for a writer that never arrives.
	if fi, err := os.Stat(in); err != nil {
		return fmt.Errorf("audio file: %w", err)
	} else if !fi.Mode().IsRegular() {
		return fmt.Errorf("refusing to read %s: it is not a regular file", in)
	}
	if err := checkPlainOutput(out); err != nil {
		return err
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("ffmpeg not found on PATH (needed to produce the 16 kHz mono %s): brew install ffmpeg", session.AudioFile)
	}
	// Convert into a temp file beside out, then rename over out only on success (see
	// atomicConvert), so an interrupted or crashed ffmpeg (Ctrl+C, SIGKILL, ENOSPC)
	// never leaves a partial audio.wav that a later bare `transcribe` would silently
	// treat as the whole recording.
	return atomicConvert(out, func(tmpPath string) error {
		cmd := exec.Command(ffmpeg, "-y", "-i", in, "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", tmpPath)
		if raw, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("ffmpeg: %w\n%s", err, tail(raw))
		}
		return nil
	})
}

// atomicConvert runs a producer that writes the converted audio to a temp file beside
// out, then renames it over out only if the producer succeeded. If the producer
// returns an error — including one raised after it has already written a partial temp,
// as a signalled or ENOSPC-hit ffmpeg does — out is left untouched and the temp is
// removed, so a failed conversion never leaves a truncated file that a later run would
// mistake for the whole recording. The temp shares out's directory so the rename stays
// on one filesystem and is atomic. The producer receives the temp path.
func atomicConvert(out string, produce func(tmpPath string) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(out), ".audio-*.wav")
	if err != nil {
		return fmt.Errorf("audio convert: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	tmp.Close() // the producer reopens by path; we only needed the reserved name
	defer os.Remove(tmpPath)
	if err := produce(tmpPath); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, out); err != nil {
		return fmt.Errorf("audio convert: finalise %s: %w", out, err)
	}
	return nil
}

// checkPlainOutput refuses an ffmpeg output path that already exists as anything
// other than a regular file. ffmpeg is handed the path as a string and told to
// overwrite it with -y, so this write cannot go through
// session.OpenFileNoFollow, and ffmpeg opens without either O_NOFOLLOW or
// O_NONBLOCK. A session directory is an exchange unit — a shared or downloaded
// session may be attacker-authored — and both non-regular cases matter there. A
// symlink pre-planted at audio.wav would silently redirect the whole conversion
// outside the session, overwriting an arbitrary file the operator never named. A
// FIFO planted at the same path is worse than useless: ffmpeg's open(2) blocks
// for ever waiting for a reader that never arrives, so `testimony transcribe`
// hangs rather than failing, on a session the operator merely received. os.Lstat
// does not resolve the link, so a symlink is reported with ModeSymlink set even
// when its target is missing; it is named separately from the general refusal
// because a redirected write and a stuck one call for different remedies. An
// absent path is fine — that is the ordinary case, and ffmpeg creates it.
func checkPlainOutput(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write %s: it is a symlink", path)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("refusing to write %s: it is not a regular file", path)
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
