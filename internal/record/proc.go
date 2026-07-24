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
	// DiedOf reports whether the process was terminated by sig, as opposed to
	// exiting on its own (cleanly or otherwise). Valid only after Wait has
	// returned; the answer is what lets stopChild tell a recorder its SIGKILL
	// actually cut short from one that had already finalised at the boundary.
	DiedOf(sig syscall.Signal) bool
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

// DiedOf reads the reaped state cmd.Wait recorded. Callers hold the
// happens-before edge (they read only after the reaper's close(done), which
// follows Wait), so ProcessState is stable here.
func (e *execProc) DiedOf(sig syscall.Signal) bool {
	ps := e.cmd.ProcessState
	if ps == nil {
		return false
	}
	ws, ok := ps.Sys().(syscall.WaitStatus)
	return ok && ws.Signaled() && ws.Signal() == sig
}

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
	killed  bool          // set by stopChild when its escalation SIGKILL terminated the child, or when the child could not be reaped at all; read after stopAll
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
// to SIGKILL. It returns after the child has been reaped, or — if even SIGKILL
// cannot reap it within stopReapGrace — after abandoning it, so the sequential
// shutdown never hangs on one wedged recorder.
func stopChild(c *liveChild, grace time.Duration) {
	_ = c.p.Signal(syscall.SIGINT)
	select {
	case <-c.done:
		return
	case <-time.After(grace):
	}
	// The recorder did not finalise its container within the grace period. SIGKILL
	// leaves whatever bytes were flushed — for an MP4, whose moov atom is written
	// only on clean shutdown, that is very likely a truncated, unplayable file. Mark
	// it so finaliseOutputs surfaces the risk instead of blessing the file by size.
	// killed is written here on the caller's goroutine (stopAll, sequential) and
	// read only after stopAll returns, so no synchronisation is needed.
	_ = c.p.Signal(syscall.SIGKILL)
	select {
	case <-c.done:
		// Condemn the artefact only when the SIGKILL is what actually terminated the
		// recorder. A child that finalised and exited cleanly right at the grace
		// boundary can still land here — the reaper closes done only after Wait
		// returns (which also joins the stderr copier), so there is a scheduling
		// window between a clean exit and done becoming readable, and select picks
		// randomly when both cases are ready. In that window the SIGKILL hits an
		// already-exited process (a no-op); marking such a child killed reported a
		// complete, playable recording as "likely truncated or unplayable" and failed
		// the run. The reaped wait status is ground truth for who ended the process,
		// and c.err/ProcessState are stable once done is closed.
		c.killed = c.p.DiedOf(syscall.SIGKILL)
	case <-time.After(stopReapGrace):
		// Even SIGKILL did not produce an exit within the reap grace: the child is
		// pinned in an uninterruptible kernel wait (a wedged capture driver defers
		// signal delivery until the kernel call returns — possibly never), so no wait
		// can reap it. Without this bound the whole shutdown hangs on the unbounded
		// receive: stopAll is sequential, so one wedged recorder keeps SIGINT from
		// ever reaching the rest and finaliseOutputs/stopDemo/nextCommands never run —
		// the operator's only recourse being to kill record externally, which skips
		// finalisation and orphans every still-live recorder. Abandon the reaper
		// goroutine (it closes done if the child ever dies; nothing else can reap it —
		// the same accepted leak as probeDevices' probeKillGrace path) and treat the
		// artefact as untrusted so finaliseOutputs surfaces it. This mirrors the bound
		// d9098a7 gave probeDevices, whose comment established that this very wait can
		// never complete for a wedged child.
		c.killed = true
	}
}
