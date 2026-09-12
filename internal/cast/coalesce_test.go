package cast

import (
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/REPPL/Testimony/internal/session"
	"github.com/REPPL/Testimony/internal/timeline"
)

// testT0 is the anchor every hermetic case in this package uses. It matches the
// t0 the session-directory reference's own examples carry.
const testT0 = 1784300400000

// run feeds a sequence of output events through a coalescer and returns the
// records it produced.
func run(t *testing.T, offsetMS int64, evs ...castEvent) *coalescer {
	t.Helper()
	c := &coalescer{t0: testT0, offsetMS: offsetMS}
	for _, ev := range evs {
		if err := c.add(ev); err != nil {
			t.Fatalf("add(%+v): %v", ev, err)
		}
	}
	if err := c.flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	return c
}

func out(t float64, data string) castEvent {
	return castEvent{T: t, Code: codeOutput, Data: data}
}

func texts(recs []timeline.Interaction) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.Text
	}
	return out
}

func TestCoalescerBoundaries(t *testing.T) {
	cases := []struct {
		name      string
		events    []castEvent
		wantText  []string
		wantTimes []int64 // session-relative milliseconds
	}{
		{
			// The echo of a typed command carries no newline until Enter, so the
			// whole command arrives as one record.
			name: "keystroke echo coalesces into one record",
			events: []castEvent{
				out(1.000, "l"), out(1.090, "s"), out(1.180, " "),
				out(1.270, "-"), out(1.360, "a"), out(1.450, "\r\n"),
			},
			wantText:  []string{"ls -a\r\n"},
			wantTimes: []int64{1000},
		},
		{
			name: "newline closes a record and the next rune opens one",
			events: []castEvent{
				out(1.000, "first\nsecond\n"),
			},
			wantText:  []string{"first\n", "second\n"},
			wantTimes: []int64{1000, 1000},
		},
		{
			name: "a trailing fragment is closed by flush",
			events: []castEvent{
				out(1.000, "line\nprompt$ "),
			},
			wantText:  []string{"line\n", "prompt$ "},
			wantTimes: []int64{1000, 1000},
		},
		{
			// A bare prompt never ends in a newline; the gap to the next output is
			// what closes it.
			name: "a 250 ms gap closes an unterminated record",
			events: []castEvent{
				out(1.000, "$ "), out(1.250, "ls\r\n"),
			},
			wantText:  []string{"$ ", "ls\r\n"},
			wantTimes: []int64{1000, 1250},
		},
		{
			name: "a gap just under 250 ms keeps one record",
			events: []castEvent{
				out(1.000, "$ "), out(1.249, "ls\r\n"),
			},
			wantText:  []string{"$ ls\r\n"},
			wantTimes: []int64{1000},
		},
		{
			// A record states one time, so the span cap bounds how stale that
			// instant can be.
			name: "the one-second span cap closes a long burst",
			events: []castEvent{
				out(0.000, "a"), out(0.200, "b"), out(0.400, "c"),
				out(0.600, "d"), out(0.800, "e"), out(1.000, "f"),
			},
			wantText:  []string{"abcde", "f"},
			wantTimes: []int64{0, 1000},
		},
		{
			name: "a span just under one second keeps one record",
			events: []castEvent{
				out(0.000, "a"), out(0.200, "b"), out(0.400, "c"),
				out(0.600, "d"), out(0.800, "e"), out(0.999, "f"),
			},
			wantText:  []string{"abcdef"},
			wantTimes: []int64{0},
		},
		{
			// Both boundaries fall on the same event: it closes once, not twice.
			name: "gap and span together close one record",
			events: []castEvent{
				out(0.000, "a"), out(1.500, "b"),
			},
			wantText:  []string{"a", "b"},
			wantTimes: []int64{0, 1500},
		},
		{
			// A progress bar's frames are one record, because \r is not a boundary.
			name: "carriage returns stay inside one record",
			events: []castEvent{
				out(0.000, "10%\r"), out(0.100, "50%\r"), out(0.200, "100%\r\n"),
			},
			wantText:  []string{"10%\r50%\r100%\r\n"},
			wantTimes: []int64{0},
		},
		{
			name: "an empty output event contributes no runes",
			events: []castEvent{
				out(0.000, ""), out(0.100, "hi\n"),
			},
			wantText:  []string{"hi\n"},
			wantTimes: []int64{100},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := run(t, 0, tc.events...)
			if got := texts(c.records); !equalStrings(got, tc.wantText) {
				t.Fatalf("records = %q, want %q", got, tc.wantText)
			}
			for i, want := range tc.wantTimes {
				if got := c.records[i].T - testT0; got != want {
					t.Errorf("record %d: session-relative t = %d ms, want %d", i+1, got, want)
				}
			}
			for i, rec := range c.records {
				if rec.Kind != OutputKind {
					t.Errorf("record %d: kind = %q, want %q", i+1, rec.Kind, OutputKind)
				}
				if rec.Selector != "" || rec.Value != "" || rec.Route != "" {
					t.Errorf("record %d: carries a field outside t/kind/text: %+v", i+1, rec)
				}
			}
		})
	}
}

func TestCoalescerDropsRecordsThatRenderEmpty(t *testing.T) {
	c := run(t, 0,
		out(0.000, "\r\n"), // a blank line
		out(0.500, "\r"),   // a lone carriage return
		out(1.000, "​​\n"), // invisible-only Unicode
		out(1.500, "real\n"),
	)
	if got := texts(c.records); !equalStrings(got, []string{"real\n"}) {
		t.Fatalf("records = %q, want one real record", got)
	}
	if c.dropped != 3 {
		t.Errorf("dropped = %d, want 3", c.dropped)
	}
}

func TestCoalescerAppliesTheOffset(t *testing.T) {
	c := run(t, -30000, out(0.000, "early\n"))
	if len(c.records) != 1 {
		t.Fatalf("got %d records, want 1", len(c.records))
	}
	// t stays a positive epoch-millisecond value, so CheckInteraction's
	// positivity rule is met while the merged session-relative time goes
	// negative.
	if want := int64(testT0 - 30000); c.records[0].T != want {
		t.Errorf("t = %d, want %d", c.records[0].T, want)
	}
}

// TestCoalescerSplitsOversizedEvent is criterion 7's unit: one output event
// larger than the line limit becomes several records, each within the limit,
// with every rune preserved and no boundary inside a rune.
func TestCoalescerSplitsOversizedEvent(t *testing.T) {
	// Mixed ASCII, multi-byte runes, and ESC bytes — the last cost six encoded
	// bytes each, which is what makes the encoded budget rather than the raw
	// length the binding limit. No newline, so only the budget can split it.
	unit := "abcé中\x1b[0;34m"
	data := strings.Repeat(unit, 12<<20/len(unit))
	c := run(t, 0, out(0.000, data))
	if len(c.records) < 2 {
		t.Fatalf("got %d records, want more than one", len(c.records))
	}
	var joined strings.Builder
	for i, rec := range c.records {
		if !utf8.ValidString(rec.Text) {
			t.Errorf("record %d is not valid UTF-8; a split fell inside a rune", i+1)
		}
		n, err := session.EncodedLen(timeline.EventEntry(rec, testT0))
		if err != nil {
			t.Fatalf("record %d: %v", i+1, err)
		}
		if n+castEntryIDMargin > session.MaxJSONLLine {
			t.Errorf("record %d's timeline entry is %d bytes, over the %d-byte limit", i+1, n, session.MaxJSONLLine)
		}
		joined.WriteString(rec.Text)
	}
	if joined.String() != data {
		t.Errorf("concatenating the records does not reproduce the event's data (%d bytes vs %d)", joined.Len(), len(data))
	}
	// Every record but the last should be close to the budget, or the splitter is
	// cutting far earlier than it needs to.
	first := c.records[0]
	if n, _ := session.EncodedLen(timeline.EventEntry(first, testT0)); n < session.MaxJSONLLine/2 {
		t.Errorf("first record's entry is only %d bytes; the budget is not being used", n)
	}
}

// TestEncodedRuneLenMatchesJSON pins the per-rune table against the encoder
// session.WriteJSONL actually uses, so a stdlib change that invalidates it fails
// here rather than as a refused import.
//
// The table must never under-count: an under-count over-fills the budget and
// costs a false importer-bug refusal, while an over-count costs only a few
// unused bytes of a 4 MiB budget. Equality is therefore asserted for every rune
// except backspace and form feed, which encoding/json has written as a six-byte
// u-escape in some Go versions and as a two-byte short escape in others — the
// table takes the larger, on the safe side of that difference.
func TestEncodedRuneLenMatchesJSON(t *testing.T) {
	overCounted := map[rune]bool{'\b': true, '\f': true}
	var runes []rune
	for r := rune(0); r < 0x80; r++ {
		runes = append(runes, r)
	}
	runes = append(runes,
		0x00a3,         // pound sign, 2 bytes
		0x00e9,         // e-acute, 2 bytes
		0x4e2d,         // CJK, 3 bytes
		0x1f600,        // emoji, 4 bytes
		0x2028, 0x2029, // line and paragraph separator
		utf8.RuneError, // U+FFFD, what the decoder substitutes
		0x00ad, 0x200b, // soft hyphen, zero-width space
	)
	for _, r := range runes {
		// Measure one rune's contribution as the difference between a two-rune
		// and a one-rune payload, so the envelope around it cancels out.
		base, err := session.EncodedLen(map[string]string{"t": "x"})
		if err != nil {
			t.Fatal(err)
		}
		with, err := session.EncodedLen(map[string]string{"t": "x" + string(r)})
		if err != nil {
			t.Fatal(err)
		}
		got, want := encodedRuneLen(r), with-base
		if got < want {
			t.Errorf("encodedRuneLen(%U) = %d, under encoding/json's %d; the budget would overfill", r, got, want)
		}
		if got != want && !overCounted[r] {
			t.Errorf("encodedRuneLen(%U) = %d, encoding/json spends %d", r, got, want)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestCoalescerRefusesRecordOverTheLineLimit exercises the measured check that
// backs the per-rune table. It is unreachable if the table is right, so the
// budget is deliberately falsified here — which is exactly the failure the check
// exists to turn into a refusal rather than a session no command can read back.
func TestCoalescerRefusesRecordOverTheLineLimit(t *testing.T) {
	c := &coalescer{t0: testT0}
	if err := c.begin(0); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.budget = math.MaxInt32
	text := strings.Repeat("x", session.MaxJSONLLine)
	c.text.WriteString(text)
	c.enc = len(text)
	err := c.close()
	if err == nil {
		t.Fatal("want a refusal, got nil")
	}
	for _, want := range []string{"record 1 encodes to", "JSONL line limit", "this is an importer bug"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if len(c.records) != 0 {
		t.Errorf("a refused record was kept: %d records", len(c.records))
	}
}
