package transcribe

// Bounded sinks for this package's subprocess output, closing the same "a
// media tool's diagnostic output is small" assumption internal/record already
// closed twice (stderrRetain's OOM rationale, probeSink's misbehaving-binary
// cap) — and transcribe is the one package that hands attacker-authorable
// media to a media parser: a crafted container's per-packet warnings grow
// ffmpeg's stderr with packet count, not file size, and a CPU-only whisper run
// chatters for hours. At most an 800-byte tail (tail()) or one JSON field is
// ever consumed, so an unbounded buffer is pure exposure.
//
// Unlike record's sinks these carry no mutex: every site assigns the SAME sink
// value to both cmd.Stdout and cmd.Stderr, which os/exec detects
// (interfaceEqual) and serves with one pipe and one copy goroutine, and the
// buffer is only read after cmd.Wait returns.

// combinedRetain caps how much of a child's combined output a transcribe
// subprocess site keeps — record's stderrRetain sizing, far above the ≤800
// bytes tail() ever reads.
const combinedRetain = 8 << 10

// tailSink retains only the trailing combinedRetain bytes written to it: the
// diagnostic window an error message ends with, which is all any caller reads.
type tailSink struct {
	buf []byte
}

func (s *tailSink) Write(p []byte) (int, error) {
	// Report the full count even when the cap discards bytes: a bounded sink
	// absorbs-and-drops, and a short count with a nil error violates io.Writer
	// and makes os/exec's copy goroutine abort the pump with ErrShortWrite
	// (probeSink.Write's lesson).
	n := len(p)
	if n >= combinedRetain {
		s.buf = append(s.buf[:0], p[n-combinedRetain:]...)
		return n, nil
	}
	if overflow := len(s.buf) + n - combinedRetain; overflow > 0 {
		s.buf = append(s.buf[:0], s.buf[overflow:]...)
	}
	s.buf = append(s.buf, p...)
	return n, nil
}

func (s *tailSink) bytes() []byte { return s.buf }

// probeHeadRetain caps deriveOffset's ffprobe stdout at far above any
// legitimate -show_format JSON (a few KB) — probeSink's sizing: the cap only
// bounds a misbehaving input's metadata dump.
const probeHeadRetain = 1 << 20

// headSink retains only the leading probeHeadRetain bytes: ffprobe prints the
// format object whole, so the head is the valuable window, and a JSON
// truncated by the cap simply fails to parse — the same graceful no-derivation
// path as a missing creation_time tag.
type headSink struct {
	buf []byte
}

func (s *headSink) Write(p []byte) (int, error) {
	n := len(p)
	if room := probeHeadRetain - len(s.buf); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		s.buf = append(s.buf, p...)
	}
	return n, nil
}

func (s *headSink) bytes() []byte { return s.buf }
