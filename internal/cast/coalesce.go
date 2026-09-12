package cast

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/REPPL/Testimony/internal/session"
	"github.com/REPPL/Testimony/internal/timeline"
)

// The coalescing boundaries. A shell echoes a typed command back roughly one
// keystroke at a time, so one record per raw output event would fragment a
// single typed command across dozens of near-empty records. These are named
// constants rather than flags: an operator has no way to know what value to
// pass, and the values interact with report's -window, which is already a flag
// on the command that needs it.
const (
	// coalesceGapMS closes a record whose output never ends in a newline: a bare
	// prompt, a read prompt, a progress line. 250 ms sits above a human's typical
	// inter-keystroke interval (~100-200 ms), so a typed command still coalesces;
	// three orders of magnitude above the gaps inside a program's output burst,
	// so it never merges across a genuine pause; and an order of magnitude below
	// report's 2.5 s join window, so coalescing alone can never pull content
	// across a window boundary.
	coalesceGapMS = 250

	// maxCoalesceSpanMS caps how long one record may span, measured from its
	// first event. A record states one time — its first event's — so unbounded
	// coalescing would attribute minutes of output to a single early instant. One
	// second keeps the attribution error well inside report's 2.5 s default
	// window, so a record still joins to the utterance spoken over it.
	maxCoalesceSpanMS = 1000
)

// castEntryIDMargin is demo's eventIDGrowthMargin twin, for the same reason:
// timeline.EventEntry stamps the placeholder id "ev-001", while the real
// ordinal depends on the record's position among every interaction in the
// session, so a measured entry is a lower bound and this margin covers the
// ordinal's growth. 32 spare bytes cover an "ev-%03d" ordinal up to 32 digits.
// (demo's constant is unexported, so the value is restated here with the
// citation rather than reached across the package boundary.)
const castEntryIDMargin = 32

// coalescer turns a stream of output events into interaction records: one
// record per line the terminal displayed, closed at the first of a newline, a
// coalesceGapMS inter-event gap, a maxCoalesceSpanMS span, or the JSONL line
// budget. It holds one pending record's text at a time.
//
// A record's t is always the time of the event that contributed its FIRST rune
// — never fabricated, never averaged — so a record cut by any of the four
// boundaries is honestly timed, and continuation records after a split carry
// the time of the event whose data they open with.
type coalescer struct {
	t0       int64 // manifest t0, epoch milliseconds
	offsetMS int64 // cast→session offset, milliseconds

	records []timeline.Interaction
	dropped int // records whose text renders empty

	open    bool
	text    strings.Builder
	enc     int   // JSON-encoded length of the pending text
	budget  int   // encoded-text budget for the pending record
	firstMS int64 // cast-clock ms of the event that opened the record
	lastMS  int64 // cast-clock ms of the most recent event added
}

// add appends one output event's runes to the pending record, closing and
// opening records at the boundaries above.
func (c *coalescer) add(ev castEvent) error {
	ms := microsToMillis(ev.US)
	if c.open && (ms-c.lastMS >= coalesceGapMS || ms-c.firstMS >= maxCoalesceSpanMS) {
		if err := c.close(); err != nil {
			return err
		}
	}
	c.lastMS = ms
	for _, r := range ev.Data {
		if !c.open {
			if err := c.begin(ms); err != nil {
				return err
			}
		}
		n := encodedRuneLen(r)
		// A rune is appended only if it keeps the running total within budget;
		// otherwise the record closes and the rune opens the next one, so a split
		// never falls inside a rune and concatenating a split's records reproduces
		// the event's decoded data exactly. The pending record must be non-empty
		// to split, so a rune that cannot fit even an empty record's budget is
		// still appended — and caught by close's measured check — rather than
		// looping for ever on a record it can never open.
		if c.enc > 0 && c.enc+n > c.budget {
			if err := c.close(); err != nil {
				return err
			}
			if err := c.begin(ms); err != nil {
				return err
			}
		}
		c.text.WriteRune(r)
		c.enc += n
		// The newline is included in the record, and the next rune starts a new
		// one. This is the primary boundary and the one that makes the stream
		// legible: one record per line the terminal displayed. Carriage returns
		// are kept but are NOT a boundary — a progress bar emits many \r-separated
		// frames for one displayed line, and one record per frame would flood the
		// stream.
		if r == '\n' {
			if err := c.close(); err != nil {
				return err
			}
		}
	}
	return nil
}

// flush closes the pending record at end of stream.
func (c *coalescer) flush() error {
	if !c.open {
		return nil
	}
	return c.close()
}

// begin opens a record at the cast-clock instant ms and computes its encoded
// text budget.
//
// The budget is computed per record, from that record's own time, rather than
// once per run: the session-relative t a timeline entry carries varies in
// encoded length across a session (a 15-byte worst case against a 1-byte best
// one), and a record's time is known the moment it opens, so measuring it is
// exact where a single run-wide envelope would have to guess. The probe carries
// a one-rune text because timeline.BuildEntries omits an empty text entirely,
// so an envelope measured without one under-counts by the whole `,"text":""`
// scaffolding.
func (c *coalescer) begin(ms int64) error {
	probe := timeline.Interaction{T: c.recordTime(ms), Kind: OutputKind, Text: "x"}
	n, err := session.EncodedLen(timeline.EventEntry(probe, c.t0))
	if err != nil {
		return err
	}
	c.open = true
	c.firstMS = ms
	c.enc = 0
	c.text.Reset()
	c.budget = session.MaxJSONLLine - (n - 1) - castEntryIDMargin
	return nil
}

// close finishes the pending record.
func (c *coalescer) close() error {
	text := c.text.String()
	c.open = false
	c.enc = 0
	c.text.Reset()
	// A record whose text renders empty — a blank line, a lone carriage return —
	// is dropped and counted, so it does not become a timeline bullet showing
	// only the word terminal_output. Presence is decided on the rendered form,
	// the transcribe.mapSegments rule. The archived terminal.cast holds those
	// bytes verbatim, which is what makes this a rendering decision rather than
	// a loss of evidence.
	if strings.TrimSpace(session.SafeText(text)) == "" {
		c.dropped++
		return nil
	}
	rec := timeline.Interaction{T: c.recordTime(c.firstMS), Kind: OutputKind, Text: text}
	// The per-rune budget above is an assumption about another package's
	// encoder, so every finished record is additionally measured for real. The
	// check is unreachable if the table is right, and it is the difference
	// between a wrong table costing a refused run and a wrong table costing a
	// session no command can read back.
	n, err := session.EncodedLen(timeline.EventEntry(rec, c.t0))
	if err != nil {
		return err
	}
	if n+castEntryIDMargin > session.MaxJSONLLine {
		return fmt.Errorf("record %d encodes to a %d-byte timeline entry, over the %d-byte JSONL line limit; this is an importer bug — please report it with the cast that triggered it",
			len(c.records)+1, n, session.MaxJSONLLine)
	}
	c.records = append(c.records, rec)
	return nil
}

// recordTime places a cast-clock instant on the session clock. An interaction's
// t is epoch milliseconds, so the arithmetic is integer throughout.
func (c *coalescer) recordTime(ms int64) int64 {
	return c.t0 + c.offsetMS + ms
}

// microsToMillis rounds a recording-clock instant from the microsecond grain
// scanCast carries to the millisecond an interaction records, half away from
// zero — math.Round's rule, in integers. This is the one place the rounding
// happens, which is what keeps a v2 and a v3 reading of the same recording on
// the same millisecond (see castTimeGrain). A recording clock never runs
// negative, since v2 refuses a decrease from zero and v3 a negative interval,
// but the negative case is handled rather than assumed.
func microsToMillis(us int64) int64 {
	if us < 0 {
		return -((-us + 500) / 1000)
	}
	return (us + 500) / 1000
}

// encodedRuneLen is the number of bytes r costs once encoding/json has written
// it into a JSON string with SetEscapeHTML(false) — the settings
// session.WriteJSONL and session.EncodedLen both use. Escaping, not raw length,
// is what consumes the line budget: an ESC byte costs six bytes encoded, and
// ANSI-coloured output is dense in them.
//
// The table is pinned against encoding/json by TestEncodedRuneLenMatchesJSON,
// so a stdlib change that invalidates it fails there rather than as a refused
// import.
func encodedRuneLen(r rune) int {
	switch {
	case r == '"' || r == '\\':
		return 2
	case r == '\n' || r == '\r' || r == '\t':
		return 2
	case r < 0x20:
		// Every other C0 control, ESC (0x1b) included, becomes a six-byte
		// u-escape. Backspace and form feed are the one place the table
		// deliberately over-counts: encoding/json has written those two as a
		// six-byte u-escape in some Go versions and as a two-byte short escape in
		// others, and an over-count costs a few unused bytes of a 4 MiB budget
		// while an under-count costs a false refusal.
		return 6
	case r == 0x2028 || r == 0x2029:
		// Escaped unconditionally, whatever SetEscapeHTML says.
		return 6
	}
	// Everything else is written literally, DEL and <, >, & included (the last
	// three only because HTML escaping is off).
	if n := utf8.RuneLen(r); n > 0 {
		return n
	}
	// A rune no encoder can write is replaced by U+FFFD. Unreachable from a
	// string encoding/json decoded, which is the only source here.
	return utf8.RuneLen(utf8.RuneError)
}
