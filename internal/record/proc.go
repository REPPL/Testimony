package record

import (
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

// stderrRetain caps how much of a child's stderr lockedBuffer keeps. A record
// session is long-running by design, and avfoundation floods stderr when a device
// stalls ("frame dropped", mux-queue warnings), so an unbounded buffer could grow by
// hundreds of MB over a session and OOM the parent — which, because each recorder is
// in its own process group with the parent as the only signaller, would orphan the
// ffmpeg children still recording. Only the trailing bytes are ever read (tail() uses
// ≤1200), so retaining a bounded window loses nothing diagnostic while bounding memory.
const stderrRetain = 8 << 10

// lockedBuffer is a concurrency-safe, memory-bounded sink for a child's stderr:
// os/exec copies stderr on its own goroutine while the lifecycle reads the tail from
// another. It keeps only the trailing stderrRetain bytes.
type lockedBuffer struct {
	mu      sync.Mutex
	buf     []byte // trailing bytes only, len capped at stderrRetain
	dropped bool   // true once any earlier bytes were discarded
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(p)
	if n >= stderrRetain {
		// This write alone overflows the window; keep only its own tail, without
		// growing the backing array.
		l.buf = append(l.buf[:0], p[n-stderrRetain:]...)
		l.dropped = true
		return n, nil
	}
	l.buf = append(l.buf, p...)
	if len(l.buf) > stderrRetain {
		// Compact the trailing window to the front of the same backing array
		// (overlapping forward copy, which append/copy handle), so memory stays bounded.
		drop := len(l.buf) - stderrRetain
		l.buf = append(l.buf[:0], l.buf[drop:]...)
		l.dropped = true
	}
	return n, nil
}

// tail returns the trailing portion of the captured stderr for error messages,
// prefixing an ellipsis whenever earlier bytes were dropped (by the retention cap or
// the tail bound), so a truncation is visible rather than silent.
func (l *lockedBuffer) tail() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	const max = 1200
	b := l.buf
	if len(b) > max {
		return "…" + string(b[len(b)-max:])
	}
	if l.dropped {
		return "…" + string(b)
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
	killed  bool          // set by stopChild when the grace expired and SIGKILL was sent; read after stopAll
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
		// The recorder did not finalise its container within the grace period. SIGKILL
		// leaves whatever bytes were flushed — for an MP4, whose moov atom is written
		// only on clean shutdown, that is very likely a truncated, unplayable file. Mark
		// it so finaliseOutputs surfaces the risk instead of blessing the file by size.
		// killed is written here on the caller's goroutine (stopAll, sequential) and
		// read only after stopAll returns, so no synchronisation is needed.
		_ = c.p.Signal(syscall.SIGKILL)
		c.killed = true
		<-c.done
	}
}
