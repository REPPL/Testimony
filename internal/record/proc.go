package record

import (
	"bytes"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// proc is the minimal process surface the lifecycle drives, so the SIGINT →
// wait → SIGKILL state machine is testable over a fake without spawning ffmpeg.
type proc interface {
	Start() error
	Signal(os.Signal) error
	Wait() error
}

// execProc adapts an *exec.Cmd to proc. Each recorder runs in its own process
// group (Setpgid) so the terminal's Ctrl+C does not reach ffmpeg directly —
// record signals each child itself, in order, giving a controlled shutdown.
type execProc struct {
	cmd *exec.Cmd
}

func (e *execProc) Start() error { return e.cmd.Start() }

func (e *execProc) Signal(sig os.Signal) error {
	if e.cmd.Process == nil {
		return nil
	}
	return e.cmd.Process.Signal(sig)
}

func (e *execProc) Wait() error { return e.cmd.Wait() }

// lockedBuffer is a concurrency-safe sink for a child's stderr: os/exec copies
// stderr on its own goroutine while the lifecycle reads the tail from another.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// tail returns the trailing portion of the captured stderr for error messages.
func (l *lockedBuffer) tail() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	const max = 1200
	b := l.buf.Bytes()
	if len(b) > max {
		return "…" + string(b[len(b)-max:])
	}
	return string(b)
}

// liveChild is one running recorder plus the machinery to reap it exactly once.
type liveChild struct {
	stream  string
	p       proc
	stderr  *lockedBuffer
	started time.Time     // when watching began; used to tell a start-up failure from a mid-session stop
	done    chan struct{} // closed once, when Wait returns
	err     error         // Wait result; read only after done is closed
}

// watch starts the single reaper goroutine. It must be called exactly once,
// after p.Start() succeeds.
func (c *liveChild) watch() {
	go func() {
		c.err = c.p.Wait()
		close(c.done)
	}()
}

// newLiveChild wraps an already-started proc and begins watching it.
func newLiveChild(stream string, p proc, stderr *lockedBuffer) *liveChild {
	c := &liveChild{stream: stream, p: p, stderr: stderr, started: time.Now(), done: make(chan struct{})}
	c.watch()
	return c
}

// stop asks one child to finalise its container: SIGINT (ffmpeg writes the
// trailer/moov atom and exits), wait up to grace, and only on timeout escalate
// to SIGKILL. It returns after the child has been reaped.
func stopChild(c *liveChild, grace time.Duration) {
	_ = c.p.Signal(syscall.SIGINT)
	select {
	case <-c.done:
	case <-time.After(grace):
		_ = c.p.Signal(syscall.SIGKILL)
		<-c.done
	}
}
