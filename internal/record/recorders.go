package record

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// deviceListTimeout bounds the avfoundation device enumeration. It is a var, not a
// const, only so a test can shrink it (TestProbeDevicesTimesOut does); production
// never reassigns it. Enumeration normally returns in well under a second, but a
// wedged CoreAudio/camera driver or a stuck TCC daemon can block the AVFoundation
// query indefinitely, and probeDevices runs before the interrupt handler can stop
// anything — so without a deadline a single bad device hangs `testimony record`
// forever, with the session dir already created and nothing recording.
var deviceListTimeout = 15 * time.Second

// probeKillGrace bounds how long probeDevices waits for the enumeration child to be
// reaped after the deadline SIGKILL. A var only for the same test reason as
// deviceListTimeout. If even SIGKILL does not produce an exit within this grace, the
// child is pinned in an uninterruptible kernel wait (a wedged IOKit driver call
// defers signal delivery until the kernel call returns — possibly never), and no
// amount of waiting will reap it; probeDevices abandons it instead of hanging.
var probeKillGrace = 2 * time.Second

// micArgs builds the ffmpeg argv that captures the system default microphone to
// a canonical 16 kHz mono PCM WAV — exactly the parameters
// transcribe.convertAudio produces, so the file is canonical ASR input needing
// no re-conversion. It captures the avfoundation ":default" audio device (the
// actual system default input macOS resolves at capture time) rather than a
// fixed index: a virtual audio driver (BlackHole, Loopback, a conferencing
// tool's device) can enumerate at index 0 and would then be recorded to silence
// in the real microphone's place. startRecorders logs the detected input roster
// so a surprising default is still visible. Pure and unit-testable.
func micArgs(outPath string) []string {
	return []string{
		"-f", "avfoundation",
		"-i", ":default",
		"-ac", "1",
		"-ar", "16000",
		"-c:a", "pcm_s16le",
		"-y", outPath,
	}
}

// screenArgs builds the ffmpeg argv that captures the screen to H.264 MP4.
// Video only — the microphone is captured by its own process so the ASR audio
// stays independent of the screen recording. Pure, like micArgs.
func screenArgs(screenIndex int, outPath string) []string {
	return []string{
		"-f", "avfoundation",
		"-framerate", "30",
		"-capture_cursor", "1",
		"-i", strconv.Itoa(screenIndex),
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-pix_fmt", "yuv420p",
		"-y", outPath,
	}
}

// avDevice is one entry from ffmpeg's avfoundation device listing.
type avDevice struct {
	index int
	name  string
}

// deviceLine matches an avfoundation listing row, e.g.
// "[AVFoundation indev @ 0x...] [1] Capture screen 0".
var deviceLine = regexp.MustCompile(`\[AVFoundation[^\]]*\]\s+\[(\d+)\]\s+(.*)`)

// parseAVDevices splits ffmpeg's `-list_devices true` stderr into its video and
// audio device lists. Pure and table-testable against captured stderr.
func parseAVDevices(stderr string) (video, audio []avDevice) {
	section := 0 // 0 none, 1 video, 2 audio
	for _, line := range strings.Split(stderr, "\n") {
		switch {
		case strings.Contains(line, "AVFoundation video devices:"):
			section = 1
			continue
		case strings.Contains(line, "AVFoundation audio devices:"):
			section = 2
			continue
		}
		m := deviceLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		idx, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		dev := avDevice{index: idx, name: strings.TrimSpace(m[2])}
		switch section {
		case 1:
			video = append(video, dev)
		case 2:
			audio = append(audio, dev)
		}
	}
	return video, audio
}

// selectDevices validates that at least one avfoundation audio input exists and,
// when video is requested, resolves the screen index (the video device whose
// name contains "Capture screen"). It returns the detected audio-input names for
// the caller to log: the microphone itself is captured via avfoundation
// ":default" (see micArgs), not by index, so ffmpeg picks the system default at
// capture time and this list is what makes a surprising default — a virtual
// audio driver shadowing the real mic — visible to the operator. Pure, so the
// selection logic is tested without ffmpeg.
func selectDevices(video, audio []avDevice, wantScreen bool) (screenIndex int, mics []string, err error) {
	if len(audio) == 0 {
		return 0, nil, fmt.Errorf("no avfoundation audio input devices found; connect or enable a microphone")
	}
	for _, d := range audio {
		mics = append(mics, d.name)
	}

	screenIndex = -1
	if wantScreen {
		for _, d := range video {
			if strings.Contains(d.name, "Capture screen") {
				screenIndex = d.index
				break
			}
		}
		if screenIndex == -1 {
			return 0, nil, fmt.Errorf("no avfoundation \"Capture screen\" device found; screen capture is unavailable")
		}
	}
	return screenIndex, mics, nil
}

// probeSink is a concurrency-safe, bounded sink for the enumeration child's
// output: os/exec copies each pipe on its own goroutine while probeDevices may
// return early on the abandon path, and those copiers then keep writing here.
// It keeps the leading bytes — the section headers and device rows parseAVDevices
// needs print first, so unlike lockedBuffer (diagnostic tail) the head is the
// valuable window. A device listing is a few KB; the cap only bounds a
// misbehaving binary.
type probeSink struct {
	mu  sync.Mutex
	buf []byte
}

// probeSinkRetain caps probeSink at far above any real device listing.
const probeSinkRetain = 1 << 20

func (s *probeSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Report the full count even when the retention cap discards the tail: a
	// bounded sink absorbs-and-drops overflow, and returning a short count with a
	// nil error violates io.Writer and makes os/exec's copy goroutine abort the
	// pump with ErrShortWrite (mid-listing, on a flood past the cap).
	n := len(p)
	if room := probeSinkRetain - len(s.buf); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		s.buf = append(s.buf, p...)
	}
	return n, nil
}

func (s *probeSink) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.buf)
}

// probeDevices runs ffmpeg's avfoundation device listing, validates an audio
// input exists, and resolves the (when wantScreen) screen index. The command
// exits non-zero by design after printing the list to stderr, so a non-nil exec
// error is expected; only a failure to find the needed devices is fatal. It
// returns the detected audio-input names for the caller to log. Impure (spawns
// a child process), but the binary path is a parameter, so both the timeout and
// the by-design-exit paths are covered hermetically by tests with a fake ffmpeg.
//
// The wait is structured as start → own reap goroutine → select against the
// deadline, rather than exec.CommandContext + CombinedOutput: Cmd.Wait blocks in
// Process.Wait before it consumes the context cancellation, so with a child
// pinned in an uninterruptible kernel wait (the wedged-driver case the deadline
// exists for) CombinedOutput would hang forever despite the expired context —
// SIGKILL delivery is deferred until the kernel call returns. Here the deadline
// fires regardless: the child is SIGKILLed, given probeKillGrace to be reaped,
// and otherwise abandoned (the reap and pipe-copy goroutines leak by design —
// nothing can reap an unkillable process, and record aborts on the error).
func probeDevices(ffmpeg string, wantScreen bool) (screenIndex int, mics []string, err error) {
	cmd := exec.Command(ffmpeg, "-hide_banner", "-f", "avfoundation", "-list_devices", "true", "-i", "")
	var sink probeSink
	cmd.Stdout = &sink
	cmd.Stderr = &sink
	if startErr := cmd.Start(); startErr != nil {
		return 0, nil, fmt.Errorf("run ffmpeg device listing: %w", startErr)
	}
	done := make(chan error, 1) // buffered: the reap goroutine must not leak blocked on the abandon path
	go func() { done <- cmd.Wait() }()

	var runErr error
	timedOut := false
	select {
	case runErr = <-done:
	case <-time.After(deviceListTimeout):
		timedOut = true
		_ = cmd.Process.Kill()
		select {
		case runErr = <-done:
		case <-time.After(probeKillGrace):
			return 0, nil, fmt.Errorf("ffmpeg avfoundation device listing hung for %s and survived SIGKILL — a capture device or kernel driver is unresponsive; disconnect or disable it, then re-run", deviceListTimeout)
		}
	}

	video, audio := parseAVDevices(sink.text())
	screenIndex, mics, err = selectDevices(video, audio, wantScreen)
	if err == nil {
		// A listing that raced the deadline but parsed completely still wins: the
		// select above picks randomly when the exit and the timer are both ready,
		// and discarding a valid enumeration would abort a recording session with a
		// misleading "device is unresponsive" for a probe that in fact succeeded.
		return screenIndex, mics, nil
	}
	if timedOut {
		return 0, nil, fmt.Errorf("ffmpeg avfoundation device listing timed out after %s — a capture device or driver is unresponsive; disconnect or disable it, then re-run", deviceListTimeout)
	}
	// ffmpeg prints the listing to stderr and then exits non-zero by design, so an
	// *exec.ExitError is expected and ignored — the listing is still in the sink.
	// But a Wait that failed for a different reason (an I/O error copying the
	// pipes; a start-time failure is already caught above) means the listing never
	// arrived, and selectDevices would misreport "no microphone found", sending the
	// operator to check hardware and permissions for a process that never ran to
	// completion. Surface it.
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		return 0, nil, fmt.Errorf("run ffmpeg device listing: %w", runErr)
	}
	// selectDevices found no usable device. The benign by-design exit prints the
	// listing to stderr, but an ffmpeg that genuinely failed — built without
	// avfoundation ("Unknown input format: 'avfoundation'"), or killed by a
	// signal — also lands here as an *exec.ExitError with the real cause sitting
	// in the sink, which parseAVDevices then reads as an empty listing. Without the
	// tail the operator is told to check their microphone for what is actually a
	// toolchain fault. Surface ffmpeg's own last words so the true cause reaches
	// them.
	if tail := outputTail([]byte(sink.text())); tail != "" {
		return 0, nil, fmt.Errorf("%w; ffmpeg said: %s", err, tail)
	}
	return 0, nil, err
}

// outputTail returns a trimmed, bounded tail of ffmpeg's output for inclusion in
// an error, so a diagnostic reaches the operator without dumping the whole
// listing. Empty in, empty out.
func outputTail(out []byte) string {
	const max = 400
	s := strings.TrimSpace(string(out))
	if len(s) > max {
		s = "..." + s[len(s)-max:]
	}
	return s
}
