package transcribe

import (
	"bytes"
	"io"
	"os/exec"
	"time"
)

// heartbeatInterval is how often runWithHeartbeat reports elapsed time to
// its log sink while an engine subprocess is still running. A var, not a
// const, so tests can shrink it instead of running a real multi-second
// sleep.
var heartbeatInterval = 5 * time.Second

// runWithHeartbeat runs cmd to completion, capturing its combined stdout and
// stderr the same way cmd.CombinedOutput() would, while printing an
// elapsed-time status line to log every heartbeatInterval so a slow engine
// run stays visibly alive. Both whisperx and whisper.cpp can run for minutes
// on CPU-only hardware with nothing else printed between transcribe's own
// offset line and completion; label names the engine in the status line
// (e.g. "whisperx"). A nil log discards the heartbeat, matching the
// zero-value Options a caller who never sets Log can pass.
func runWithHeartbeat(cmd *exec.Cmd, log io.Writer, label string) ([]byte, error) {
	if log == nil {
		log = io.Discard
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Start(); err != nil {
		return buf.Bytes(), err
	}

	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		start := time.Now()
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case now := <-ticker.C:
				io.WriteString(log, label+": still running ("+now.Sub(start).Round(time.Second).String()+" elapsed)\n")
			}
		}
	}()

	err := cmd.Wait()
	close(done)
	<-finished // the goroutine's last possible write to log happens-before this return
	return buf.Bytes(), err
}
