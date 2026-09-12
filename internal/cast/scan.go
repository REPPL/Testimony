package cast

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"unicode/utf8"

	"github.com/REPPL/Testimony/internal/session"
)

// Asciicast event codes. Only output becomes records; the rest are dropped and
// counted (see Run). An unrecognised code is dropped rather than refused, so a
// future asciicast revision that adds one does not turn every cast it writes
// into an unimportable file; the printed count is what keeps that tolerance
// honest.
const (
	codeOutput = "o"
	codeInput  = "i"
	codeResize = "r"
	codeMarker = "m"
	codeExit   = "x"
)

// codeName gives an event code its asciicast name, for the printed drop line.
func codeName(code string) string {
	switch code {
	case codeOutput:
		return "output"
	case codeInput:
		return "input"
	case codeResize:
		return "resize"
	case codeMarker:
		return "marker"
	case codeExit:
		return "exit"
	default:
		return "unrecognised"
	}
}

// A cast is the same kind of input session.ReadJSONL bounds — line-oriented,
// operator-supplied, and possibly received rather than recorded here — so the
// scan is bounded the same way, at its own scale.
const (
	// maxCastLine bounds one line. A single o event can legitimately be one large
	// write (a cat of a file), and JSON escaping inflates it — an ESC byte encodes
	// to six bytes — so session.MaxJSONLLine's 4 MiB would refuse casts whose
	// records this importer can split and persist perfectly well. A line at this
	// bound still splits into records that fit.
	maxCastLine = 16 << 20 // 16 MiB

	// maxCastBytes bounds the whole file. A cast large enough to matter cannot
	// become records that fit interactions.jsonl's own 16 MiB cap anyway, so this
	// exists only to bound the scan itself.
	maxCastBytes = 64 << 20 // 64 MiB

	// maxCastSeconds bounds an event's absolute time on the recording clock,
	// mirroring timeline's maxUtteranceSeconds and transcribe's maxOffsetSeconds,
	// so a time this importer accepts is a time merge accepts.
	maxCastSeconds = 1e9

	// castTimeGrain is the integer grain the recording clock is carried on:
	// microseconds. It is what makes the v2 and v3 readings of one recording
	// agree bit-for-bit, which is the whole "the operator never needs to know
	// which format their recorder wrote" guarantee.
	//
	// Carrying the clock in float64 seconds does not give that. v2 rounds a
	// stated absolute time to milliseconds; v3 rounds a float64 running sum of
	// intervals. At a half-millisecond tie the two land on different
	// milliseconds — seven 0.0015 s intervals sum to 0.010499999999999999 and
	// round to 10 ms, while v2's stated 0.0105 rounds to 11 ms — and one
	// millisecond is enough to flip a 250 ms coalescing gap, so the same
	// recording becomes one record in one format and two in the other.
	// Accumulating on an exact integer grain removes the class: both formats
	// reach the identical integer, and the rounding to milliseconds happens once,
	// at the same place, from the same number.
	//
	// A microsecond is exact for every time either format writes — both cap a
	// time at six decimal places — and 1e9 seconds on this grain is 1e15, three
	// orders of magnitude inside int64.
	castTimeGrain = 1e6

	// maxCastMicros is maxCastSeconds on that grain.
	maxCastMicros = int64(maxCastSeconds * castTimeGrain)
)

// castHeader is the first line of a cast, reduced to the two fields this
// importer needs. Both are pointers so an absent field stays distinguishable
// from a genuine 0 — the timeline.rawInteraction.T and
// transcribe.offsetSidecar.OffsetSeconds precedent. Every other header field
// (width, height, term, env, theme, command, title, duration, idle_time_limit)
// is ignored: the importer needs the version and the anchor and nothing else,
// and unknown fields must not be rejected, since both formats are extensible.
type castHeader struct {
	Version   *int
	Timestamp *int64
}

// castEvent is one event line. US is always absolute MICROSECONDS since
// recording start, so the v2/v3 difference is resolved inside scanCast and
// nothing downstream of it knows which format was read — the single seam behind
// the "identical timeline from either format" guarantee. It is an integer
// rather than a float64 of seconds because that guarantee is bit-for-bit: see
// castTimeGrain.
type castEvent struct {
	Line int
	US   int64
	Code string
	Data string
}

// scanCast streams an asciicast: the header first, then one callback per event
// line. onHeader runs after the header is decoded and before the first event,
// so the caller can resolve the cast→session offset — which the header's
// timestamp anchors, and which every record's time needs — without buffering
// the events or reading the file twice; it may be nil. Both callbacks' errors
// abort the scan unchanged.
//
// Blank lines are skipped, matching every other line reader in the repository.
// Every refusal names the line it fired on and leaves the caller with nothing
// written, because the whole scan runs before any file in the session changes.
func scanCast(r io.Reader, name string, onHeader func(castHeader) error, fn func(castEvent) error) (castHeader, error) {
	var hdr castHeader
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxCastLine)
	line := 0
	var total int64
	// v2 keeps the previous absolute time; v3 keeps the running sum of intervals.
	// Both are microseconds (castTimeGrain), so the sum is exact and the two
	// formats' readings of one recording cannot diverge: each event's time is
	// rounded to the grain once, on the way in, and the rounding to the
	// millisecond a record records happens once more, downstream, from the same
	// integer whichever format was read.
	var clock int64
	haveHeader := false
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		// Counted before the blank-line skip, and including the newline, exactly
		// as session.ReadJSONL counts, so a file padded with blank lines past the
		// cap is refused rather than scanned past forever.
		total += int64(len(raw)) + 1
		if total > maxCastBytes {
			return hdr, fmt.Errorf("%s: exceeds %d bytes across %d lines; refusing to read", name, maxCastBytes, line)
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		if !haveHeader {
			var err error
			if hdr, err = parseHeader(raw, name, line); err != nil {
				return hdr, err
			}
			haveHeader = true
			if onHeader != nil {
				if err := onHeader(hdr); err != nil {
					return hdr, err
				}
			}
			continue
		}
		ev, err := parseEvent(raw, name, line, *hdr.Version, &clock)
		if err != nil {
			return hdr, err
		}
		if fn != nil {
			if err := fn(ev); err != nil {
				return hdr, err
			}
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			// The scanner stopped on the line after the last one it delivered.
			return hdr, fmt.Errorf("%s:%d: line exceeds %d bytes; refusing to read", name, line+1, maxCastLine)
		}
		return hdr, fmt.Errorf("%s: %w", name, err)
	}
	if !haveHeader {
		return hdr, fmt.Errorf("%s:1: not an asciicast header (expected a JSON object with a \"version\" field)", name)
	}
	return hdr, nil
}

// parseHeader decodes line 1. A cast with no header at all is caught here, too:
// an event line is a JSON array, not an object.
func parseHeader(raw []byte, name string, line int) (castHeader, error) {
	var hdr castHeader
	var obj map[string]json.RawMessage
	// A JSON null decodes into a nil map without error, so it is refused
	// explicitly rather than read as an object carrying no version.
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return hdr, fmt.Errorf("%s:%d: not an asciicast header (expected a JSON object with a \"version\" field)", name, line)
	}
	vraw, ok := obj["version"]
	if !ok || isJSONNull(vraw) {
		return hdr, fmt.Errorf("%s:%d: asciicast header carries no \"version\" field", name, line)
	}
	var version int
	if err := json.Unmarshal(vraw, &version); err != nil || (version != 2 && version != 3) {
		// Naming the file and the version found is the acceptance criterion's
		// exact wording. The value is echoed from its JSON literal, so a
		// non-integer version ("2", say) is named as written rather than as a
		// decode failure — and it is neutralised and truncated first, because a
		// cast is attacker-authorable and this message reaches a terminal.
		return hdr, fmt.Errorf("%s: asciicast version %s is not supported (import reads version 2 and 3)", name, literal(vraw))
	}
	hdr.Version = &version
	if traw, ok := obj["timestamp"]; ok && !isJSONNull(traw) {
		var ts int64
		if err := json.Unmarshal(traw, &ts); err != nil {
			return hdr, fmt.Errorf("%s:%d: asciicast header timestamp %s is not an integer number of seconds; pass -offset SECONDS to anchor the cast explicitly", name, line, literal(traw))
		}
		hdr.Timestamp = &ts
	}
	return hdr, nil
}

// parseEvent decodes one [time, code, data] line and resolves its time onto the
// absolute recording clock, in microseconds. clock carries the previous event's
// absolute time (v2) or the running interval sum (v3) across calls.
func parseEvent(raw []byte, name string, line, version int, clock *int64) (castEvent, error) {
	malformed := fmt.Errorf("%s:%d: malformed asciicast event (expected [time, code, data])", name, line)
	var el []json.RawMessage
	if err := json.Unmarshal(raw, &el); err != nil || len(el) != 3 {
		return castEvent{}, malformed
	}
	var t float64
	var code, data string
	// An out-of-range numeric literal (1e400) lands here too: encoding/json
	// refuses it into a float64 rather than yielding +Inf.
	if err := json.Unmarshal(el[0], &t); err != nil {
		return castEvent{}, malformed
	}
	if err := json.Unmarshal(el[1], &code); err != nil {
		return castEvent{}, malformed
	}
	if err := json.Unmarshal(el[2], &data); err != nil {
		return castEvent{}, malformed
	}
	// Bound the value before it reaches the grain: int64(math.Round(x)) has no
	// defined answer for a float past the integer range, so a 1e300 time (or
	// interval) must be refused here rather than converted. The bound is the
	// recording-clock bound itself, so nothing legitimate is refused earlier
	// than it would have been after accumulating.
	if math.Abs(t) > maxCastSeconds {
		return castEvent{}, fmt.Errorf("%s:%d: event time %gs exceeds %g seconds; that is no recording clock", name, line, t, maxCastSeconds)
	}
	us := int64(math.Round(t * castTimeGrain))
	switch version {
	case 2:
		// v2 times are absolute seconds since recording start.
		if us < *clock {
			return castEvent{}, fmt.Errorf("%s:%d: event time %g precedes the previous event's %g; asciicast v2 times must not decrease", name, line, t, float64(*clock)/castTimeGrain)
		}
		*clock = us
	default:
		// v3 times are intervals since the previous event.
		if us < 0 {
			return castEvent{}, fmt.Errorf("%s:%d: event interval %g is negative; asciicast v3 intervals must not be negative", name, line, t)
		}
		*clock += us
	}
	if *clock > maxCastMicros || *clock < -maxCastMicros {
		return castEvent{}, fmt.Errorf("%s:%d: event time %gs exceeds %g seconds; that is no recording clock", name, line, float64(*clock)/castTimeGrain, maxCastSeconds)
	}
	return castEvent{Line: line, US: *clock, Code: code, Data: data}, nil
}

// isJSONNull reports whether a raw JSON value is the literal null, which this
// importer treats as an absent field rather than as a malformed one.
func isJSONNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

// literal renders a raw JSON value for an error message: neutralised
// (session.SafeText, since a cast is attacker-authorable and the message
// reaches a terminal) and clipped, so a header field holding a megabyte of text
// cannot become the error.
func literal(raw json.RawMessage) string {
	return clip(session.SafeText(string(bytes.TrimSpace(raw))), 32)
}

// clip shortens s to at most max runes, marking the cut. The cut falls on a
// rune boundary, so a clipped string never ends mid-rune.
func clip(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	n := 0
	for i := range s {
		if n == max {
			return s[:i] + "…"
		}
		n++
	}
	return s
}
