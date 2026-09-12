package cast

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// collect runs scanCast over src and returns every event it delivered.
func collect(t *testing.T, src, name string) (castHeader, []castEvent) {
	t.Helper()
	var got []castEvent
	hdr, err := scanCast(strings.NewReader(src), name, nil, func(ev castEvent) error {
		got = append(got, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("scanCast: %v", err)
	}
	return hdr, got
}

func TestScanCastV2AbsoluteTimes(t *testing.T) {
	src := `{"version":2,"timestamp":1784300398}
[0,"o","a"]
[0.25,"o","b"]
[12.5,"o","c"]
`
	hdr, got := collect(t, src, "v2")
	if hdr.Version == nil || *hdr.Version != 2 {
		t.Fatalf("version = %v, want 2", hdr.Version)
	}
	if hdr.Timestamp == nil || *hdr.Timestamp != 1784300398 {
		t.Fatalf("timestamp = %v, want 1784300398", hdr.Timestamp)
	}
	// Microseconds, the grain the clock is carried on, compared exactly.
	want := []int64{0, 250_000, 12_500_000}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d", len(got), len(want))
	}
	for i, ev := range got {
		if ev.US != want[i] {
			t.Errorf("event %d: us = %d, want %d", i+1, ev.US, want[i])
		}
		if ev.Line != i+2 {
			t.Errorf("event %d: line = %d, want %d", i+1, ev.Line, i+2)
		}
	}
}

// TestScanCastV3RunningSum pins the one seam the "identical timeline from
// either format" guarantee rests on: a v3 cast's intervals reconstructed into
// the absolute times a v2 cast states outright.
func TestScanCastV3RunningSum(t *testing.T) {
	cases := []struct {
		name      string
		intervals []float64
		want      []int64 // absolute microseconds
	}{
		{"simple", []float64{0, 0.5, 0.5}, []int64{0, 500_000, 1_000_000}},
		{"zero intervals", []float64{0, 0, 0}, []int64{0, 0, 0}},
		{"first interval non-zero", []float64{1.25, 0.25}, []int64{1_250_000, 1_500_000}},
		{"millisecond tail", []float64{0.001, 0.001, 0.001, 0.001}, []int64{1000, 2000, 3000, 4000}},
		// The sum is exact on the grain, so a run of half-millisecond ties lands
		// on the same integer a v2 cast states outright — the divergence class
		// TestV2AndV3Agree pins end to end.
		{"half-millisecond ties", []float64{0.0015, 0.0015, 0.0015, 0.0015, 0.0015, 0.0015, 0.0015},
			[]int64{1500, 3000, 4500, 6000, 7500, 9000, 10500}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			b.WriteString("{\"version\":3}\n")
			for _, iv := range tc.intervals {
				fmt.Fprintf(&b, "[%g,\"o\",\"x\"]\n", iv)
			}
			_, got := collect(t, b.String(), "v3")
			if len(got) != len(tc.want) {
				t.Fatalf("got %d events, want %d", len(got), len(tc.want))
			}
			for i, ev := range got {
				// The sum is an integer, so this is an exact comparison rather than a
				// tolerance — which is the point of the grain.
				if ev.US != tc.want[i] {
					t.Errorf("event %d: us = %d, want %d", i+1, ev.US, tc.want[i])
				}
			}
		})
	}
}

// TestScanCastLongV3Tail checks that a long run of intervals does not drift
// past the millisecond a record records.
func TestScanCastLongV3Tail(t *testing.T) {
	const n = 5000
	var b strings.Builder
	b.WriteString("{\"version\":3}\n")
	for i := 0; i < n; i++ {
		b.WriteString("[0.001,\"o\",\"x\"]\n")
	}
	_, got := collect(t, b.String(), "v3")
	if len(got) != n {
		t.Fatalf("got %d events, want %d", len(got), n)
	}
	// Five thousand 1 ms intervals sum to exactly five seconds, with no drift to
	// tolerate: the accumulator is an integer.
	if last, want := got[n-1].US, int64(5_000_000); last != want {
		t.Errorf("final absolute time = %d us, want %d", last, want)
	}
}

func TestScanCastDeliversEveryCode(t *testing.T) {
	src := `{"version":2}
[0,"o","out"]
[0.1,"i","key"]
[0.2,"r","80x24"]
[0.3,"m","marker"]
[0.4,"x","0"]
[0.5,"z","future"]
`
	_, got := collect(t, src, "codes")
	want := []string{"o", "i", "r", "m", "x", "z"}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d", len(got), len(want))
	}
	for i, ev := range got {
		if ev.Code != want[i] {
			t.Errorf("event %d: code = %q, want %q", i+1, ev.Code, want[i])
		}
	}
}

func TestScanCastSkipsBlankLines(t *testing.T) {
	src := "{\"version\":2}\n\n[0,\"o\",\"a\"]\n   \n[0.5,\"o\",\"b\"]\n"
	_, got := collect(t, src, "blanks")
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	// The line numbers still count the blank lines, so a refusal names the line
	// the operator can find in the file.
	if got[0].Line != 3 || got[1].Line != 5 {
		t.Errorf("lines = %d, %d; want 3, 5", got[0].Line, got[1].Line)
	}
}

func TestScanCastRefusals(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			"not an object",
			"[0,\"o\",\"x\"]\n",
			[]string{"cast:1:", "not an asciicast header"},
		},
		{
			"json null header",
			"null\n",
			[]string{"cast:1:", "not an asciicast header"},
		},
		{
			"not json at all",
			"not json\n",
			[]string{"cast:1:", "not an asciicast header"},
		},
		{
			"no version field",
			"{\"timestamp\":1784300398}\n",
			[]string{"cast:1:", "carries no \"version\" field"},
		},
		{
			"null version",
			"{\"version\":null}\n",
			[]string{"cast:1:", "carries no \"version\" field"},
		},
		{
			"version 1",
			"{\"version\":1}\n",
			[]string{"cast:", "asciicast version 1 is not supported", "version 2 and 3"},
		},
		{
			"version 4",
			"{\"version\":4}\n",
			[]string{"cast:", "asciicast version 4 is not supported"},
		},
		{
			"version as a string",
			"{\"version\":\"2\"}\n",
			[]string{"cast:", "asciicast version \"2\" is not supported"},
		},
		{
			"non-integer timestamp",
			"{\"version\":2,\"timestamp\":\"noon\"}\n",
			[]string{"cast:1:", "is not an integer number of seconds", "-offset SECONDS"},
		},
		{
			"event not an array",
			"{\"version\":2}\n{\"t\":1}\n",
			[]string{"cast:2:", "malformed asciicast event"},
		},
		{
			"event with two elements",
			"{\"version\":2}\n[0.5,\"o\"]\n",
			[]string{"cast:2:", "malformed asciicast event"},
		},
		{
			"event time not a number",
			"{\"version\":2}\n[\"0.5\",\"o\",\"x\"]\n",
			[]string{"cast:2:", "malformed asciicast event"},
		},
		{
			"event code not a string",
			"{\"version\":2}\n[0.5,3,\"x\"]\n",
			[]string{"cast:2:", "malformed asciicast event"},
		},
		{
			"event data not a string",
			"{\"version\":2}\n[0.5,\"o\",7]\n",
			[]string{"cast:2:", "malformed asciicast event"},
		},
		{
			// encoding/json refuses an out-of-range literal into float64 rather
			// than yielding +Inf, so it lands on the malformed path.
			"event time out of float range",
			"{\"version\":2}\n[1e400,\"o\",\"x\"]\n",
			[]string{"cast:2:", "malformed asciicast event"},
		},
		{
			"v2 time decreasing",
			"{\"version\":2}\n[1,\"o\",\"a\"]\n[0.5,\"o\",\"b\"]\n",
			[]string{"cast:3:", "precedes the previous event's 1", "must not decrease"},
		},
		{
			"v3 interval negative",
			"{\"version\":3}\n[0.1,\"o\",\"a\"]\n[-0.2,\"o\",\"b\"]\n",
			[]string{"cast:3:", "event interval -0.2 is negative", "must not be negative"},
		},
		{
			"absolute time beyond the clock bound",
			"{\"version\":2}\n[2e9,\"o\",\"a\"]\n",
			[]string{"cast:2:", "that is no recording clock"},
		},
		{
			"v3 sum beyond the clock bound",
			"{\"version\":3}\n[1e9,\"o\",\"a\"]\n[1e9,\"o\",\"b\"]\n",
			[]string{"cast:3:", "that is no recording clock"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := scanCast(strings.NewReader(tc.src), "cast", nil, func(castEvent) error { return nil })
			if err == nil {
				t.Fatal("want a refusal, got nil")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestScanCastRefusesEmptyInput(t *testing.T) {
	_, err := scanCast(strings.NewReader(""), "cast", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "not an asciicast header") {
		t.Fatalf("error = %v, want a missing-header refusal", err)
	}
}

func TestScanCastRefusesOverlongLine(t *testing.T) {
	src := "{\"version\":2}\n[0,\"o\",\"" + strings.Repeat("x", maxCastLine) + "\"]\n"
	_, err := scanCast(strings.NewReader(src), "cast", nil, func(castEvent) error { return nil })
	if err == nil {
		t.Fatal("want a refusal, got nil")
	}
	for _, want := range []string{"cast:2:", "line exceeds", "refusing to read"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

// repeater emits chunk for ever, so the whole-file bound can be exercised
// without allocating 64 MiB of fixture.
type repeater struct {
	chunk []byte
	off   int
}

func (r *repeater) Read(p []byte) (int, error) {
	n := copy(p, r.chunk[r.off:])
	r.off = (r.off + n) % len(r.chunk)
	return n, nil
}

func TestScanCastRefusesOverlongFile(t *testing.T) {
	line := "[0,\"o\",\"" + strings.Repeat("x", 64<<10) + "\"]\n"
	src := io.MultiReader(strings.NewReader("{\"version\":2}\n"), &repeater{chunk: []byte(line)})
	_, err := scanCast(src, "cast", nil, func(castEvent) error { return nil })
	if err == nil {
		t.Fatal("want a refusal, got nil")
	}
	for _, want := range []string{"cast:", "exceeds", "across", "refusing to read"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestScanCastPropagatesCallbackErrors(t *testing.T) {
	sentinel := errors.New("stop")
	_, err := scanCast(strings.NewReader("{\"version\":2}\n[0,\"o\",\"x\"]\n"), "cast",
		func(castHeader) error { return sentinel }, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("header callback error = %v, want %v", err, sentinel)
	}
	_, err = scanCast(strings.NewReader("{\"version\":2}\n[0,\"o\",\"x\"]\n"), "cast",
		nil, func(castEvent) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("event callback error = %v, want %v", err, sentinel)
	}
}

func TestLiteralTruncatesAndNeutralises(t *testing.T) {
	long := "\"" + strings.Repeat("é", 100) + "\""
	got := literal([]byte(long))
	if !strings.HasSuffix(got, "…") {
		t.Errorf("literal over the cap = %q, want a truncation", got)
	}
	if n := len([]rune(got)); n != 33 {
		t.Errorf("literal truncated to %d runes, want 33 (32 plus the ellipsis)", n)
	}
	// A cast is attacker-authorable and this text reaches a terminal, so the
	// control bytes an ANSI sequence rides on are stripped first.
	if got := literal([]byte("\"a\x1b[31mb\"")); strings.ContainsRune(got, 0x1b) {
		t.Errorf("literal kept an ESC byte: %q", got)
	}
}
