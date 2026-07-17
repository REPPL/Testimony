package record

import (
	"fmt"
	"strings"
)

// avSignatures are substrings ffmpeg's avfoundation input emits when a device
// cannot be opened. Their presence makes a permissions denial the most likely
// cause — but because a busy or absent device fails the same way, the message
// is phrased as "most likely", never asserted.
var avSignatures = []string{
	"avfoundation",
	"input/output error",
	"not authorized",
	"failed to",
	"abort",
}

// permissionPane maps a recorder stream to the exact System Settings pane the
// operator must enable.
func permissionPane(stream string) string {
	if stream == streamScreen {
		return "Screen Recording"
	}
	return "Microphone"
}

// looksLikeAVFailure reports whether the ffmpeg stderr tail carries an
// avfoundation open-failure signature.
func looksLikeAVFailure(stderrTail string) bool {
	low := strings.ToLower(stderrTail)
	for _, sig := range avSignatures {
		if strings.Contains(low, sig) {
			return true
		}
	}
	return false
}

// classifyRecorderExit turns a recorder that exited before we asked it to stop
// into an actionable operator message. It is honest about what it can and
// cannot prove, on two axes — when the exit happened and what the stderr says:
//
//   - Within the startup window with an avfoundation open-failure signature:
//     most likely a TCC denial. It names the exact System Settings pane for the
//     failing stream and phrases the cause as "most likely a permissions issue"
//     (an open failure cannot be proven TCC vs. a busy device).
//   - Within the startup window without such a signature: reported as a failed
//     start without claiming permissions — the ffmpeg tail carries the cause
//     (a broken build, a full disk, a bad argv), so pointing at a TCC pane
//     would misdirect.
//   - After the startup window: the recorder ran for a while, so it cannot be a
//     start-up denial. Reported as an unexpected mid-session stop (a device
//     disconnect or the recorder dying), never mislabelled as permissions.
//
// The raw ffmpeg tail is always appended for diagnosis — never a stack trace.
// Pure: the caller decides atStartup from the child's elapsed run time.
func classifyRecorderExit(stream string, exitErr error, stderrTail string, atStartup bool) string {
	tail := strings.TrimSpace(stderrTail)

	var b strings.Builder
	switch {
	case atStartup && looksLikeAVFailure(tail):
		fmt.Fprintf(&b, "%s capture failed to start", stream)
		writeExitCode(&b, exitErr)
		b.WriteString(" — most likely a permissions issue.\n")
		fmt.Fprintf(&b, "Open System Settings → Privacy & Security → %s, enable your terminal, then re-run.", permissionPane(stream))
	case atStartup:
		fmt.Fprintf(&b, "%s capture failed to start", stream)
		writeExitCode(&b, exitErr)
		b.WriteString(". The recorder exited immediately — check the ffmpeg output below for the cause.")
	default:
		fmt.Fprintf(&b, "%s capture stopped unexpectedly", stream)
		writeExitCode(&b, exitErr)
		b.WriteString(". The recorder exited before you asked it to stop — the device may have been disconnected or become unavailable.")
	}

	if tail != "" {
		fmt.Fprintf(&b, "\n\nffmpeg output:\n%s", tail)
	}
	return b.String()
}

// classifyMissingOutput turns a recorder that ran until we stopped it, yet left
// no usable artefact, into an actionable operator message. This is the case the
// mid-session classifier misses: the recorder never exited on its own — it
// blocked on the macOS permission prompt for the whole session, captured
// nothing, and was reaped only when SIGINT'd at stop — so classifyRecorderExit,
// which fires only when a child exits before we ask it to, never sees it.
//
// The absent or empty file is overwhelmingly a TCC denial: the permission for
// the failing stream was never granted for the terminal application that
// launched testimony. The message names the missing artefact and the exact
// System Settings pane, phrases the cause as "most likely" (a device that was
// busy or absent for the whole session fails the same way), and appends the raw
// ffmpeg tail for diagnosis. Pure: the caller supplies the artefact name and
// stderr tail.
func classifyMissingOutput(stream, artefact, stderrTail string) string {
	tail := strings.TrimSpace(stderrTail)
	pane := permissionPane(stream)

	var b strings.Builder
	fmt.Fprintf(&b, "%s capture produced no %s.\n", stream, artefact)
	fmt.Fprintf(&b, "The most likely cause is that macOS %s permission was never granted for the terminal application that launched testimony — the recorder stayed blocked on the permission prompt and captured nothing before stop.\n", pane)
	fmt.Fprintf(&b, "Grant it in System Settings → Privacy & Security → %s, then re-run record.", pane)

	if tail != "" {
		fmt.Fprintf(&b, "\n\nffmpeg output:\n%s", tail)
	}
	return b.String()
}

// writeExitCode appends the child's exit error in parentheses when present.
func writeExitCode(b *strings.Builder, exitErr error) {
	if exitErr != nil {
		fmt.Fprintf(b, " (%v)", exitErr)
	}
}
