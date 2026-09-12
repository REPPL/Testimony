// Package cast reads an asciinema recording (asciicast v2 or v3) and
// normalises its terminal-output events into a session's interaction stream.
// The package is named for the artefact it parses rather than for the verb it
// implements, because `import` is a Go keyword.
//
// It is transcribe's peer for the terminal: an artefact the CLI never
// produced, an anchor read out of that artefact's own metadata, an explicit
// -offset that always wins, a mandatory printed provenance line, an idempotent
// re-run, and an all-or-nothing write. Nothing here spawns a process, opens a
// pty, or touches the network — the operator records their own terminal and
// hands the file over afterwards.
package cast

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/REPPL/Testimony/internal/session"
	"github.com/REPPL/Testimony/internal/timeline"
	"github.com/REPPL/Testimony/internal/transcribe"
)

// OutputKind is the interaction kind every record this package writes carries.
// It is the importer's own marker: a re-run replaces exactly the records
// carrying it and leaves every other interaction untouched. It is a reserved
// kind (docs/reference/session-directory.md) for exactly that reason — an
// instrumented app posting it to the demo capture endpoint would have its
// record replaced by a later import.
const OutputKind = "terminal_output"

// Options configures one import run.
type Options struct {
	SessionDir string    // session directory (docs/reference/session-directory.md)
	Cast       string    // asciicast file; "" reuses the session's terminal.cast
	Offset     float64   // cast→session clock offset in seconds
	OffsetSet  bool      // true when -offset was given explicitly
	Log        io.Writer // status sink; defaults to os.Stderr when nil
}

// Run performs the import and returns the number of records written to
// interactions.jsonl.
//
// The order of operations is transcribe.Run's, for transcribe.Run's stated
// reason — every refusal fires before anything on disk changes, so a refused
// import leaves the session byte-for-byte as it found it:
//
//  1. load manifest.json and resolve t0 through session.Manifest.T0;
//  2. resolve the cast input (-cast, or the session's own terminal.cast);
//  3. scan the cast, resolving the offset from its header and building records
//     as events arrive;
//  4. validate the record set against what merge will later demand of it;
//  5. stage the archival cast copy into a temp file beside terminal.cast;
//  6. rewrite interactions.jsonl atomically;
//  7. rename the staged copy over terminal.cast.
func Run(opts Options) (int, error) {
	// Default the log sink as transcribe.Run does, so a caller that leaves Log
	// nil gets progress on stderr instead of a panic at the first status line.
	if opts.Log == nil {
		opts.Log = os.Stderr
	}
	// An explicit offset is validated here as well as at the CLI boundary (where
	// it is a usage error, exit 2): Run is also called directly, and a non-finite
	// Offset would otherwise reach an int64 conversion with no defined answer.
	// transcribe.CheckOffset is the one home for the rule.
	if opts.OffsetSet {
		if err := transcribe.CheckOffset(opts.Offset); err != nil {
			return 0, err
		}
	}
	man, err := session.LoadManifest(opts.SessionDir)
	if err != nil {
		return 0, err
	}
	// Unlike transcribe, import needs t0 even on the explicit -offset path: the
	// artefact it writes is epoch-millisecond-timed and -offset is defined
	// relative to the session clock. Refusing here is not a gap — merge already
	// refuses a session whose interactions.jsonl is non-empty and whose manifest
	// carries no usable t0, so importing into such a session would persist
	// records no command could ever read back.
	t0, err := man.T0()
	if err != nil {
		return 0, fmt.Errorf("anchoring the terminal cast: %w", err)
	}

	src, name, external, err := resolveCast(opts.SessionDir, opts.Cast)
	if err != nil {
		return 0, err
	}

	f, err := openCast(src, external)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	co := coalescer{t0: t0}
	var drops dropTally
	var offsetMS int64
	var provenance string
	_, err = scanCast(f, name, func(h castHeader) error {
		// The offset is resolved between the header and the first event, because
		// the header's timestamp is what anchors it and every record's t needs it.
		off, prov, rerr := resolveOffset(name, opts, man, h)
		if rerr != nil {
			return rerr
		}
		offsetMS, provenance = off, prov
		co.offsetMS = off
		return nil
	}, func(ev castEvent) error {
		// Only output becomes records. Everything else is dropped and counted by
		// code — the intent's "evidence is not silently dropped" applied to whole
		// event classes. Dropping i (input) is unconditional and has no flag: a
		// cast recorded with input capture still cannot put keystrokes into
		// interactions.jsonl, whatever the operator's recorder did.
		if ev.Code != codeOutput {
			drops.add(ev.Code)
			return nil
		}
		return co.add(ev)
	})
	if err != nil {
		return 0, err
	}
	if err := co.flush(); err != nil {
		return 0, err
	}

	// The provenance line is printed on every run, so "offset 0 because the
	// header said nothing" is never a silent assumption.
	fmt.Fprintf(opts.Log, "offset: %+.2fs (%s)\n", float64(offsetMS)/1000, provenance)
	drops.report(opts.Log, co.dropped)

	if len(co.records) == 0 {
		// A cast holding no importable output would otherwise silently delete a
		// prior import's records — the hazard transcribe's zero-utterance guard
		// and merge's zero-entry guard both refuse. The two ways to get here are
		// named apart, because they call for different remedies: a cast with no
		// output events at all is the wrong file (or one recorded with only input
		// capture), while a cast whose output all rendered empty is a real
		// recording of a terminal that displayed nothing legible.
		if co.dropped > 0 {
			return 0, fmt.Errorf("%s holds no importable output (%d record(s) rendered empty); refusing to rewrite %s",
				name, co.dropped, session.InteractionsFile)
		}
		return 0, fmt.Errorf("%s holds no output events; refusing to rewrite %s", name, session.InteractionsFile)
	}
	if err := checkRecords(co.records, t0); err != nil {
		return 0, err
	}

	// The copy is staged before the records are written and committed after, so
	// a failure anywhere before the final rename leaves the session exactly as
	// it was. The only residual window is a same-directory rename failing after
	// the records landed — records with no archival copy, the less misleading of
	// the two possible residual states: the records are the evidence, the cast
	// is the archive.
	tmpPath := ""
	if external {
		// Copied from the descriptor the scan already read, rewound, rather than
		// by re-opening the path: between the two opens the operator's file can be
		// replaced, and the archive would then hold bytes the records did not come
		// from — silently contradicting the one byte-for-byte claim this design
		// makes.
		if tmpPath, err = stageCast(opts.SessionDir, name, f); err != nil {
			return 0, err
		}
		defer os.Remove(tmpPath)
	}
	replaced, err := rewriteInteractions(opts.SessionDir, name, t0, co.records)
	if err != nil {
		return 0, err
	}
	if replaced > 0 {
		fmt.Fprintf(opts.Log, "replaced %d %s record(s) from an earlier import\n", replaced, OutputKind)
	}
	if tmpPath != "" {
		if err := commitCast(tmpPath); err != nil {
			return 0, err
		}
	}
	return len(co.records), nil
}

// resolveCast decides which file this run reads. -cast names an external
// artefact the operator holds; omitting it — or pointing it at the session's
// own terminal.cast, which os.SameFile settles — reuses the archival copy in
// place, so "re-run with a corrected -offset" is a one-liner and the archival
// copy is never copied onto itself. name is the display name for messages.
func resolveCast(dir, flagCast string) (src, name string, external bool, err error) {
	archive := filepath.Join(dir, session.TerminalCastFile)
	if flagCast != "" && !sameFile(flagCast, archive) {
		// -cast carries no extension check, unlike -audio's closed .m4a/.mov/.wav
		// set: that set exists because ffmpeg accepts only those containers, while
		// here the header's version field is the authority on whether a file is
		// importable, and a name rule would refuse a legitimately-named cast.
		fi, serr := os.Stat(flagCast)
		if serr != nil {
			return "", "", false, fmt.Errorf("cast file: %w", serr)
		}
		if !fi.Mode().IsRegular() {
			return "", "", false, fmt.Errorf("refusing to read %s: it is not a regular file", flagCast)
		}
		return flagCast, flagCast, true, nil
	}
	return archive, archive, false, nil
}

// openCast opens the resolved cast. The session's own terminal.cast goes
// through the no-follow guard every other session-artefact read uses (a FIFO or
// symlink planted at the name in a received session is refused, not followed or
// blocked on); an operator-named -cast is opened plainly, the transcribe -audio
// precedent — that path the operator named themselves rather than received.
func openCast(src string, external bool) (*os.File, error) {
	if external {
		f, err := os.Open(src)
		if err != nil {
			return nil, fmt.Errorf("cast file: %w", err)
		}
		return f, nil
	}
	f, err := session.OpenFileNoFollowRead(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no %s in session %s and no -cast given: record a terminal with asciinema, then pass -cast FILE",
				session.TerminalCastFile, filepath.Dir(src))
		}
		return nil, err
	}
	return f, nil
}

// resolveOffset picks the cast→session offset in milliseconds, in
// transcribe.resolveOffset's order: an explicit -offset wins; otherwise the
// header's own timestamp anchors the cast; otherwise 0, with the provenance
// printed so the default is never silent.
//
// The derived case is exact integer arithmetic — no float enters it — because
// both operands are integers: the header's timestamp is whole Unix seconds in
// both formats, and t0_epoch_ms is whole milliseconds. Only an explicit -offset
// introduces a rounding step, to the nearest millisecond.
//
// name is the cast's display name, which the implausible-timestamp refusal
// must carry (the spec's message shape names the file).
func resolveOffset(name string, opts Options, man session.Manifest, hdr castHeader) (offsetMS int64, provenance string, err error) {
	// t0 is obtained through session.Manifest.T0, never the raw field, so an
	// absent (0) or negative anchor refuses the run rather than placing every
	// record about fifty-seven years into the session.
	t0, err := man.T0()
	if err != nil {
		return 0, "", fmt.Errorf("anchoring the terminal cast: %w", err)
	}
	if opts.OffsetSet {
		return int64(math.Round(opts.Offset * 1000)), "from -offset flag", nil
	}
	if hdr.Timestamp == nil {
		// transcribe's absent-metadata policy verbatim: default 0 and print why.
		// Refusing instead would make the terminal path stricter than the audio
		// path for the same class of missing metadata, and the remedy is identical
		// in both — the spoken "session start" marker as the cross-check, then
		// -offset to correct.
		return 0, "default 0: cast header carries no timestamp", nil
	}
	ts := *hdr.Timestamp
	if ts <= 0 {
		// Manifest.T0's reasoning applies unchanged: no recorder produces a capture
		// instant at or before 1 January 1970. A present-but-implausible anchor is
		// refused rather than defaulted, exactly as transcribe refuses a present
		// but implausible creation time while defaulting only on absence.
		return 0, "", fmt.Errorf("%s: header timestamp %d is not a recording instant; pass -offset SECONDS to anchor the cast explicitly", name, ts)
	}
	// Bound the magnitude in float before the multiplication, so an astronomical
	// header timestamp cannot overflow int64 on its way to the refusal.
	if off := float64(ts) - float64(t0)/1000; math.Abs(off) > maxCastSeconds {
		return 0, "", fmt.Errorf("derived cast offset %+.2fs exceeds %g in magnitude; the cast's header timestamp or the manifest t0 is implausible — pass -offset SECONDS to state it explicitly", off, maxCastSeconds)
	}
	// The caveat rides in the printed line, not only in the docs: the header
	// field is an integer in both formats, so the reconstructed clock can sit up
	// to a second adrift of t0's millisecond precision — material against
	// report's 2.5-second default join window.
	return ts*1000 - t0, "derived: cast header timestamp − manifest t0 (whole seconds, ±1s)", nil
}

// checkRecords validates the assembled record set against what merge will later
// demand of it: every record through timeline.CheckInteraction, the same guard
// demo's capture endpoint applies, so import cannot persist a record merge
// would refuse. The wrapped timeline entry's own size is checked as each record
// is closed (see coalescer.close), next to the budget that decision rests on.
func checkRecords(records []timeline.Interaction, t0 int64) error {
	for i, rec := range records {
		line, err := encodeRecord(rec)
		if err != nil {
			return fmt.Errorf("record %d: %w", i+1, err)
		}
		if err := timeline.CheckInteraction(line, t0); err != nil {
			return fmt.Errorf("record %d %v", i+1, err)
		}
	}
	return nil
}

// maxDropCodes bounds how many distinct event codes the drop tally names. A
// code is a single character in both formats, but a crafted cast can carry a
// different one on every line, which would otherwise grow the tally — and the
// line it prints — in step with the file.
const maxDropCodes = 16

// dropTally counts the events this run did not import, by code.
//
// The bound above is on the map's growth, and the overflow is a plain counter
// rather than an entry under a reserved key: "" is a legitimate event code a
// cast can carry, so a sentinel key would report a real empty-coded event as
// overflow, and overflow as a real event.
type dropTally struct {
	byCode   map[string]int
	overflow int // events whose code arrived past the bound
}

// add counts one dropped event.
//
// The input count is exempt from the bound. It is the one code whose count is a
// privacy disclosure rather than a scoping note — it is how an operator learns
// their recorder captured keystrokes — and a cast carrying sixteen junk codes
// before its first `i` would otherwise swallow that disclosure into the
// anonymous overflow and never print it. Exempting it cannot unbound the tally:
// it is a single fixed key, so the map holds at most maxDropCodes+1 entries.
func (d *dropTally) add(code string) {
	if d.byCode == nil {
		d.byCode = make(map[string]int, maxDropCodes)
	}
	if _, seen := d.byCode[code]; !seen && code != codeInput && len(d.byCode) >= maxDropCodes {
		d.overflow++
		return
	}
	d.byCode[code]++
}

// report prints what this run did not import. Input events are named on their
// own line because their drop is a privacy guarantee rather than a scoping
// decision, and the count tells an operator their recorder captured keystrokes.
func (d *dropTally) report(log io.Writer, blank int) {
	if n := d.byCode[codeInput]; n > 0 {
		fmt.Fprintf(log, "dropped %d input (i) event(s): keystrokes are never imported\n", n)
	}
	// Ordered by code, so the line is deterministic whatever order the map
	// iterates in — and ordered by the thing the operator reads it by, rather
	// than by the formatted string, whose leading count would otherwise sort
	// "10 resize" before "2 exit".
	codes := make([]string, 0, len(d.byCode))
	total := d.overflow
	for code, n := range d.byCode {
		if code == codeInput {
			continue
		}
		total += n
		codes = append(codes, code)
	}
	if total > 0 {
		sort.Strings(codes)
		others := make([]string, 0, len(codes)+1)
		for _, code := range codes {
			// The code is echoed from the cast, so it is neutralised and clipped: it
			// is attacker-authorable and this line reaches a terminal.
			others = append(others, fmt.Sprintf("%d %s (%s)", d.byCode[code], codeName(code), clip(session.SafeText(code), 8)))
		}
		if d.overflow > 0 {
			others = append(others, fmt.Sprintf("%d under further codes", d.overflow))
		}
		fmt.Fprintf(log, "dropped %d other event(s): %s\n", total, strings.Join(others, ", "))
	}
	if blank > 0 {
		fmt.Fprintf(log, "dropped %d record(s) that render empty\n", blank)
	}
}

// sameFile reports whether a and b resolve to the same on-disk file, so a
// -cast flag pointing at the session's own terminal.cast is treated as the
// in-place case rather than copying the archive onto itself. It is
// transcribe.sameFile's twin; the two packages share no code because the
// helper is three lines of os.Stat and copying it costs less than exporting a
// filesystem predicate from an ASR package.
func sameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}
