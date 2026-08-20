package transcribe

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// withShortHeartbeat shrinks heartbeatInterval for the duration of a test
// instead of running a real multi-second sleep, restoring it afterward.
func withShortHeartbeat(t *testing.T, interval time.Duration) {
	t.Helper()
	orig := heartbeatInterval
	heartbeatInterval = interval
	t.Cleanup(func() { heartbeatInterval = orig })
}

func TestRunWithHeartbeatEmitsPeriodicStatus(t *testing.T) {
	withShortHeartbeat(t, 5*time.Millisecond)
	var log bytes.Buffer
	cmd := exec.Command("sh", "-c", "sleep 0.05; echo done")
	raw, err := runWithHeartbeat(cmd, &log, "whisperx")
	if err != nil {
		t.Fatalf("runWithHeartbeat: %v", err)
	}
	if !strings.Contains(string(raw), "done") {
		t.Fatalf("captured output missing command's own output: %q", raw)
	}
	if !strings.Contains(log.String(), "whisperx: still running") {
		t.Fatalf("expected at least one heartbeat line, got %q", log.String())
	}
}

func TestRunWithHeartbeatNoStatusForFastCommand(t *testing.T) {
	withShortHeartbeat(t, time.Hour)
	var log bytes.Buffer
	cmd := exec.Command("sh", "-c", "exit 0")
	if _, err := runWithHeartbeat(cmd, &log, "whisperx"); err != nil {
		t.Fatalf("runWithHeartbeat: %v", err)
	}
	if log.Len() != 0 {
		t.Fatalf("expected no heartbeat line for a command finishing well inside one interval, got %q", log.String())
	}
}

func TestRunWithHeartbeatCapturesOutputOnFailure(t *testing.T) {
	withShortHeartbeat(t, time.Hour)
	var log bytes.Buffer
	cmd := exec.Command("sh", "-c", "echo boom >&2; exit 1")
	raw, err := runWithHeartbeat(cmd, &log, "whisper-cli")
	if err == nil {
		t.Fatal("expected a non-nil error from a failing command")
	}
	if !strings.Contains(string(raw), "boom") {
		t.Fatalf("captured output missing the failing command's stderr: %q", raw)
	}
}

func TestRunWithHeartbeatNilLogSafe(t *testing.T) {
	withShortHeartbeat(t, time.Millisecond)
	cmd := exec.Command("sh", "-c", "sleep 0.02")
	if _, err := runWithHeartbeat(cmd, nil, "whisperx"); err != nil {
		t.Fatalf("runWithHeartbeat with nil log: %v", err)
	}
}
