package transcribe

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunWithHeartbeatBoundsCapturedOutput is the unbounded-buffer regression
// for both whisper engine sites, which funnel through runWithHeartbeat: a
// media tool's diagnostic output is NOT small — record already bounds the
// identical stream at stderrRetain with an OOM rationale — and at most an
// 800-byte tail of this buffer is ever read, so retaining more than the cap
// buys nothing and grows with the child's chatter for the whole run.
func TestRunWithHeartbeatBoundsCapturedOutput(t *testing.T) {
	withShortHeartbeat(t, time.Hour)
	// ~300 KB of chatter, then a trailing marker: the retained window must be
	// capped yet still end with the child's last words, the only part tail()
	// ever surfaces.
	cmd := exec.Command("sh", "-c", `i=0; while [ $i -lt 300 ]; do printf '%01000d' 0; i=$((i+1)); done; printf END`)
	raw, err := runWithHeartbeat(cmd, nil, "whisperx")
	if err != nil {
		t.Fatalf("runWithHeartbeat: %v", err)
	}
	if len(raw) > combinedRetain {
		t.Fatalf("captured output not bounded: got %d bytes, cap is %d", len(raw), combinedRetain)
	}
	if !strings.HasSuffix(string(raw), "END") {
		t.Fatalf("bounded capture lost the trailing bytes: %q...", raw[:40])
	}
}

// TestDeriveOffsetBoundsProbeOutput pins the bounded-head behaviour of the
// ffprobe site: a crafted recording whose format-level metadata inflates the
// -show_format JSON past probeHeadRetain must degrade to the graceful
// (0, false) no-derivation path — the same outcome as a missing tag — rather
// than buffer the whole dump to read one timestamp. The companion
// TestDeriveOffsetReadsCreationTime proves the bound leaves a legitimate
// probe untouched.
func TestDeriveOffsetBoundsProbeOutput(t *testing.T) {
	bin := t.TempDir()
	// The pad tag pushes creation_time past the retention cap; tr turns
	// /dev/zero into printable filler so the JSON stays a plain string.
	script := `#!/bin/sh
printf '{"format":{"tags":{"pad":"'
head -c 1200000 /dev/zero | tr '\0' 'x'
printf '","creation_time":"2026-07-17T15:30:00.000000Z"}}}'
`
	if err := os.WriteFile(filepath.Join(bin, "ffprobe"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffprobe: %v", err)
	}
	// The fake stays first on PATH; /usr/bin and /bin stay reachable for the
	// head and tr the script itself pipes through.
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	if off, ok := deriveOffset("bob-interview.m4a", 1_000_000); ok {
		t.Fatalf("oversized probe output must degrade to no-derivation, got offset %v", off)
	}
}

// TestDeriveOffsetReadsCreationTime proves the happy path through the bounded
// head: a normal-sized -show_format JSON still yields creation − t0.
func TestDeriveOffsetReadsCreationTime(t *testing.T) {
	bin := t.TempDir()
	script := `#!/bin/sh
printf '{"format":{"tags":{"creation_time":"1970-01-01T00:20:00.000000Z"}}}'
`
	if err := os.WriteFile(filepath.Join(bin, "ffprobe"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffprobe: %v", err)
	}
	t.Setenv("PATH", bin)
	off, ok := deriveOffset("bob-interview.m4a", 900_000) // t0 at 15 min
	if !ok || off != 300 {
		t.Fatalf("derivation through the bounded head: got (%v, %v), want (300, true)", off, ok)
	}
}

// TestTailSinkRetention pins tailSink's io.Writer contract: the full count is
// reported even when the cap discards leading bytes (a short count with a nil
// error would make os/exec's copy goroutine abort the pump mid-run, the
// probeSink.Write lesson), and only the trailing window survives.
func TestTailSinkRetention(t *testing.T) {
	var s tailSink
	big := strings.Repeat("a", combinedRetain+123)
	if n, err := s.Write([]byte(big)); err != nil || n != len(big) {
		t.Fatalf("Write: got (%d, %v), want (%d, nil)", n, err, len(big))
	}
	if n, err := s.Write([]byte("tail")); err != nil || n != 4 {
		t.Fatalf("Write: got (%d, %v), want (4, nil)", n, err)
	}
	got := string(s.bytes())
	if len(got) > combinedRetain {
		t.Fatalf("retained %d bytes, cap is %d", len(got), combinedRetain)
	}
	if !strings.HasSuffix(got, "tail") {
		t.Fatalf("trailing window lost the newest bytes: ...%q", got[len(got)-8:])
	}
}

// TestHeadSinkRetention is TestTailSinkRetention's leading-window sibling for
// the ffprobe stdout sink.
func TestHeadSinkRetention(t *testing.T) {
	var s headSink
	if n, err := s.Write([]byte("head")); err != nil || n != 4 {
		t.Fatalf("Write: got (%d, %v), want (4, nil)", n, err)
	}
	big := strings.Repeat("b", probeHeadRetain)
	if n, err := s.Write([]byte(big)); err != nil || n != len(big) {
		t.Fatalf("Write: got (%d, %v), want (%d, nil)", n, err, len(big))
	}
	got := string(s.bytes())
	if len(got) > probeHeadRetain {
		t.Fatalf("retained %d bytes, cap is %d", len(got), probeHeadRetain)
	}
	if !strings.HasPrefix(got, "head") {
		t.Fatalf("leading window lost the oldest bytes: %q...", got[:8])
	}
}
