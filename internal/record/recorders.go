package record

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// micArgs builds the ffmpeg argv that captures the default microphone to a
// canonical 16 kHz mono PCM WAV — exactly the parameters transcribe.convertAudio
// produces, so the file is canonical ASR input needing no re-conversion. Pure:
// it takes the resolved avfoundation audio-device index and the output path, so
// it is unit-testable without a device.
func micArgs(micIndex int, outPath string) []string {
	return []string{
		"-f", "avfoundation",
		"-i", ":" + strconv.Itoa(micIndex),
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

// selectDevices picks the microphone and (when video is requested) screen
// avfoundation indices from parsed device lists. The screen is the video device
// whose name contains "Capture screen"; the microphone is audio index 0 (the
// system default input). Pure, so the selection logic is tested without ffmpeg.
func selectDevices(video, audio []avDevice, wantScreen bool) (micIndex, screenIndex int, err error) {
	if len(audio) == 0 {
		return 0, 0, fmt.Errorf("no avfoundation audio input devices found; connect or enable a microphone")
	}
	micIndex = audio[0].index

	screenIndex = -1
	if wantScreen {
		for _, d := range video {
			if strings.Contains(d.name, "Capture screen") {
				screenIndex = d.index
				break
			}
		}
		if screenIndex == -1 {
			return 0, 0, fmt.Errorf("no avfoundation \"Capture screen\" device found; screen capture is unavailable")
		}
	}
	return micIndex, screenIndex, nil
}

// probeDevices runs ffmpeg's avfoundation device listing and resolves the
// microphone and (when wantScreen) screen indices. The command exits non-zero
// by design after printing the list to stderr, so a non-nil exec error is
// expected; only a failure to find the needed devices is fatal. Impure (spawns
// ffmpeg); isolated here and skipped in CI.
func probeDevices(ffmpeg string, wantScreen bool) (micIndex, screenIndex int, err error) {
	cmd := exec.Command(ffmpeg, "-hide_banner", "-f", "avfoundation", "-list_devices", "true", "-i", "")
	out, _ := cmd.CombinedOutput() // always exits non-zero; the listing is on stderr
	video, audio := parseAVDevices(string(out))
	return selectDevices(video, audio, wantScreen)
}
