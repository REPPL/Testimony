package record

// Stream names identify a recorder and select its TCC permission pane.
const (
	streamMicrophone = "microphone"
	streamScreen     = "screen"
)

// plan is the pure platform decision: given the OS and whether -video was
// requested, it returns the recorder streams to start and any honest
// skip messages to print. Only macOS (avfoundation) has capture support;
// every other platform records nothing and says so, so the manifest and
// session directory still arrive valid.
func plan(goos string, video bool) (recorders []string, skips []string) {
	if goos != "darwin" {
		return nil, []string{
			"microphone capture is unavailable on this platform (macOS only for now)",
			"screen capture is unavailable on this platform (macOS only for now)",
			"record audio externally, then run: testimony transcribe -session DIR -audio FILE",
		}
	}
	recorders = []string{streamMicrophone}
	if video {
		recorders = append(recorders, streamScreen)
	}
	return recorders, nil
}
