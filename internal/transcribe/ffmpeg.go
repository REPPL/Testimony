package transcribe

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/REPPL/Testimony/internal/session"
)

// audioExts are the accepted voice-recording containers: QuickTime outputs
// and plain WAV. Everything is normalised through ffmpeg regardless.
var audioExts = map[string]bool{".m4a": true, ".mov": true, ".wav": true}

// CheckAudioExt validates an explicit -audio value's extension without
// touching the filesystem, so the CLI can refuse it at exit 2 — the
// CheckEngine/CheckDevice/CheckVAD pattern — before detectEngine and offset
// resolution spend any work on a path whisperx/whisper.cpp were never going
// to accept. checkExternalAudio calls this too (and adds the stat/regular-file
// check, which does need the filesystem), so the extension rule keeps one home.
func CheckAudioExt(in string) error {
	ext := strings.ToLower(filepath.Ext(in))
	if !audioExts[ext] {
		return fmt.Errorf("unsupported audio format %q: expected .m4a, .mov, or .wav", ext)
	}
	return nil
}

// checkExternalAudio validates the operator-named recording before anything
// opens it. It lives apart from convertAudio because the conversion is no longer
// the first thing to touch the path: transcribe.Run resolves the audio→session
// offset first (so a refused run cannot destroy the session's audio.wav), and
// that derivation hands the same path to an ffprobe subprocess. Both subprocesses
// open without O_NONBLOCK, so a FIFO at -audio blocks their open(2) for ever and
// hangs the command; and ffprobe is a media parser, so pointing it at a file
// whose container the pipeline does not even accept widens its exposure for
// nothing. Run calls this on the external branch and convertAudio calls it again,
// so the guard keeps one home and neither entry point is unguarded.
//
// os.Stat resolves a symlink, so an operator pointing -audio at a symlinked
// recording is fine; what must be refused is a non-regular target.
func checkExternalAudio(in string) error {
	if err := CheckAudioExt(in); err != nil {
		return err
	}
	if fi, err := os.Stat(in); err != nil {
		return fmt.Errorf("audio file: %w", err)
	} else if !fi.Mode().IsRegular() {
		return fmt.Errorf("refusing to read %s: it is not a regular file", in)
	}
	return nil
}

// convertAudio produces the canonical ASR input — 16 kHz mono PCM WAV — from
// the original recording via an ffmpeg subprocess. beforeFinalise, when
// non-nil, runs after the conversion succeeded but before the rename replaces
// out, so a caller can insert its own last refusal (the offset sidecar write)
// while the session is still byte-for-byte as it was found.
func convertAudio(in, out string, beforeFinalise func() error) error {
	if err := checkExternalAudio(in); err != nil {
		return err
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
		return convertRunner(ffmpeg, in, tmpPath)
	}, beforeFinalise)
}

// convertRunner runs the actual ffmpeg conversion into the temp path. A var
// only so a hermetic test can stand in for ffmpeg and pin the atomicConvert
// wiring at the call site — a real ffmpeg failure on a bad input aborts before
// it ever creates the output, so no integration case can observe whether the
// producer was pointed at the temp or at out itself. Production never
// reassigns it.
var convertRunner = func(ffmpeg, in, tmpPath string) error {
	cmd := exec.Command(ffmpeg, "-y", "-i", in, "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", tmpPath)
	if raw, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %w\n%s", err, tail(raw))
	}
	return nil
}

// atomicConvert runs a producer that writes the converted audio to a temp file beside
// out, then renames it over out only if the producer succeeded. If the producer
// returns an error — including one raised after it has already written a partial temp,
// as a signalled or ENOSPC-hit ffmpeg does — out is left untouched and the temp is
// removed, so a failed conversion never leaves a truncated file that a later run would
// mistake for the whole recording. The temp shares out's directory so the rename stays
// on one filesystem and is atomic. The producer receives the temp path.
// beforeFinalise, when non-nil, is the caller's last refusal: it runs
// immediately before the rename, and its error aborts with out untouched.
func atomicConvert(out string, produce func(tmpPath string) error, beforeFinalise func() error) error {
	// A rename over an EXISTING out (checkPlainOutput already refused a symlink
	// or other non-regular target, so a Lstat hit here is a regular file) must
	// preserve that file's own mode: a direct ffmpeg write reopens an existing
	// path with O_TRUNC, which open(2) honours by leaving the current mode
	// alone (the mode argument is only consulted on O_CREAT), so the rename
	// path has to match it rather than reapply the umask-masked default below.
	// Without this, re-running `transcribe -audio` over a session whose
	// audio.wav an operator had deliberately chmod 600'd — or one copied from
	// a machine with a different umask — silently widens (or narrows) that
	// mode instead of leaving it exactly as found, unlike every sibling
	// atomic writer (session.WriteFileAtomicNoFollow's identical guarantee).
	priorPerm, havePrior := os.FileMode(0), false
	if fi, err := os.Lstat(out); err == nil && fi.Mode().IsRegular() {
		priorPerm, havePrior = fi.Mode().Perm(), true
	}
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
	finalPerm := priorPerm
	if !havePrior {
		// os.CreateTemp reserves the name at 0600; a direct ffmpeg write onto a
		// path with no prior file would instead have created audio.wav honouring
		// the operator's umask — 0644 for the default 022, but 0600 under a
		// restrictive umask a privacy-conscious operator sets so the microphone
		// recording is not group/world-readable. Restore that umask-masked mode,
		// matching the record-side audio.wav and every sibling artefact (all
		// created through umask-masked opens); a flat 0644 would silently widen
		// this one file past the umask. The brief syscall.Umask(0) probe is safe
		// here — transcribe creates no other file concurrently.
		um := syscall.Umask(0)
		syscall.Umask(um)
		finalPerm = 0o644 &^ os.FileMode(um)
	}
	if err := os.Chmod(tmpPath, finalPerm); err != nil {
		return fmt.Errorf("audio convert: finalise %s: %w", out, err)
	}
	if beforeFinalise != nil {
		if err := beforeFinalise(); err != nil {
			return err
		}
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

// deriveOffsetFn is the seam resolveOffset calls for offset derivation. A var
// only so a test can stub the ffprobe-gated derivation (the deviceListTimeout
// precedent in record) and exercise resolveOffset's derived-offset magnitude
// bound hermetically; production never reassigns it.
var deriveOffsetFn = deriveOffset

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
