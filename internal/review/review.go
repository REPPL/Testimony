// Package review records human verdicts on candidate findings. A verdict is
// appended to findings.jsonl as a separate, non-destructive record (never an
// in-place rewrite of the finding), so the finding's birth state and the full
// verdict history survive as the precision measure the method stands on
// (architecture note §2; itd-2 press release). Interactive review is TTY-gated
// so CI never blocks; a single verdict can also be recorded non-interactively.
package review

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/REPPL/Testimony/internal/analyze"
	"github.com/REPPL/Testimony/internal/session"
)

// maxClockSeconds bounds a value clock will format, mirroring report.maxClockSeconds:
// a real session stamp is minutes to hours, and 1e9 seconds (~31 years) stays
// well inside int64 so the float64→int conversion in clock can never go out of
// range on an attacker-authored findings.jsonl time.
const maxClockSeconds = 1e9

// Options configures a review run.
type Options struct {
	Dir     string    // session directory
	Finding string    // non-interactive: the finding to judge (F-NNN)
	Verdict string    // non-interactive: confirmed | rejected | duplicate-of-F-NNN
	In      io.Reader // interactive input
	Out     io.Writer // status and prompts
	IsTTY   bool      // whether In is an interactive terminal
	Today   string    // ISO date stamped onto verdicts (YYYY-MM-DD)
}

// Run records verdicts for the session. With -finding/-verdict it records one
// verdict non-interactively; otherwise it walks the unverified findings
// interactively (skipping cleanly when stdin is not a terminal).
func Run(opts Options) error {
	findings, verdicts, err := analyze.Load(opts.Dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("no %s (run `testimony analyze -ingest` first)", session.FindingsFile)
		}
		return err
	}

	if opts.Finding != "" || opts.Verdict != "" {
		return single(opts, findings)
	}

	if !opts.IsTTY {
		fmt.Fprintln(opts.Out, "review: stdin is not a terminal; skipping the interactive walk "+
			"(use -finding F-NNN -verdict confirmed|rejected|duplicate-of-F-NNN for a single verdict).")
		return nil
	}
	return walk(opts, findings, verdicts)
}

// single records one verdict non-interactively.
func single(opts Options, findings []analyze.Finding) error {
	if opts.Finding == "" {
		return fmt.Errorf("-finding is required with -verdict")
	}
	if opts.Verdict == "" {
		return fmt.Errorf("-verdict is required with -finding")
	}
	verdict, of, err := ParseVerdictFlag(opts.Verdict)
	if err != nil {
		return err
	}
	if err := checkTargets(findings, opts.Finding, verdict, of); err != nil {
		return err
	}
	// checkTargets passed, so the id is present in the snapshot; bind the verdict
	// to that finding so AppendVerdict can confirm it is unchanged at write time.
	target := findByID(findings, opts.Finding)
	rec := analyze.Verdict{Kind: "verdict", Finding: opts.Finding, Verdict: verdict, Of: of, At: opts.Today}
	if err := AppendVerdict(opts.Dir, rec, target); err != nil {
		return err
	}
	fmt.Fprintln(opts.Out, describe(rec))
	return nil
}

// findByID returns a pointer to the finding with the given id, or nil. The
// returned pointer is into a copy, safe to retain.
func findByID(findings []analyze.Finding, id string) *analyze.Finding {
	for i := range findings {
		if findings[i].ID == id {
			f := findings[i]
			return &f
		}
	}
	return nil
}

// errPersist marks an error that arose while writing a verdict to disk, as
// distinct from the validation errors the walk raises for an unrecognised
// keystroke or a bad duplicate target. The walk must be able to tell them
// apart: a validation error is a genuine retry situation and is printed as a
// hint, whereas a failed append means the human's decision — the precision
// evidence the method stands on — never reached findings.jsonl. Conflating the
// two let `testimony review` print a retry hint and exit 0 while silently
// losing the verdict, so anything wrapping this sentinel aborts the walk and
// propagates to the CLI's non-zero exit.
var errPersist = errors.New("recording the verdict failed")

// walk interactively judges each unverified finding in id order.
func walk(opts Options, findings []analyze.Finding, verdicts []analyze.Verdict) error {
	eff := analyze.EffectiveStatus(findings, verdicts)
	var queue []analyze.Finding
	for _, f := range findings {
		if eff[f.ID].Value == "unverified" {
			queue = append(queue, f)
		}
	}
	sort.Slice(queue, func(i, j int) bool { return queue[i].ID < queue[j].ID })
	if len(queue) == 0 {
		fmt.Fprintln(opts.Out, "No unverified findings to review.")
		return nil
	}

	r := bufio.NewReader(opts.In)
	for i, f := range queue {
		fmt.Fprintf(opts.Out, "\n(%d/%d) ", i+1, len(queue))
		printFinding(opts.Out, f)
		for {
			fmt.Fprint(opts.Out, "[c]onfirm [r]eject [d]uplicate-of [s]kip [q]uit: ")
			choice, err := readLine(r)
			if err != nil {
				fmt.Fprintln(opts.Out, "\n(end of input) stopping.")
				return nil
			}
			done, quit, verr := applyChoice(opts, findings, f, choice, r)
			if verr != nil {
				// Only an invalid choice or an invalid duplicate target is
				// worth re-prompting for; a persistence failure is not
				// something the analyst can retype their way out of, and
				// swallowing it here would end the run successfully with the
				// verdict lost.
				if errors.Is(verr, errPersist) {
					return verr
				}
				fmt.Fprintf(opts.Out, "  %v\n", verr)
				continue
			}
			if quit {
				return nil
			}
			if done {
				break
			}
		}
	}
	return nil
}

// applyChoice handles one keystroke. done means advance to the next finding;
// quit means stop the walk.
func applyChoice(opts Options, findings []analyze.Finding, f analyze.Finding, choice string, r *bufio.Reader) (done, quit bool, err error) {
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "c":
		return true, false, record(opts, f, analyze.Verdict{Kind: "verdict", Finding: f.ID, Verdict: "confirmed", At: opts.Today})
	case "r":
		return true, false, record(opts, f, analyze.Verdict{Kind: "verdict", Finding: f.ID, Verdict: "rejected", At: opts.Today})
	case "d":
		fmt.Fprint(opts.Out, "  duplicate of (F-NNN): ")
		target, rerr := readLine(r)
		if rerr != nil {
			return false, false, fmt.Errorf("no target given")
		}
		target = strings.TrimSpace(target)
		if !analyze.IsFindingID(target) {
			return false, false, fmt.Errorf("invalid target %q (want F-NNN)", target)
		}
		if err := checkTargets(findings, f.ID, "duplicate", target); err != nil {
			return false, false, err
		}
		return true, false, record(opts, f, analyze.Verdict{Kind: "verdict", Finding: f.ID, Verdict: "duplicate", Of: target, At: opts.Today})
	case "s", "":
		fmt.Fprintln(opts.Out, "  skipped.")
		return true, false, nil
	case "q":
		return false, true, nil
	default:
		return false, false, fmt.Errorf("unrecognised choice %q", choice)
	}
}

func record(opts Options, judged analyze.Finding, rec analyze.Verdict) error {
	if err := AppendVerdict(opts.Dir, rec, &judged); err != nil {
		// Wrapped so walk can distinguish a lost verdict from a mistyped
		// keystroke; see errPersist.
		return fmt.Errorf("%w: %v", errPersist, err)
	}
	fmt.Fprintf(opts.Out, "  %s\n", describe(rec))
	return nil
}

// checkTargets validates that the finding exists and, for a duplicate, that the
// target exists and differs.
func checkTargets(findings []analyze.Finding, id, verdict, of string) error {
	if !contains(findings, id) {
		return fmt.Errorf("finding %s not found", id)
	}
	if verdict == "duplicate" {
		if of == id {
			return fmt.Errorf("a finding cannot be a duplicate of itself")
		}
		if !contains(findings, of) {
			return fmt.Errorf("duplicate target %s not found", of)
		}
	}
	return nil
}

// ParseVerdictFlag parses a -verdict flag value into the stored enum. The CLI
// value "duplicate-of-F-NNN" becomes verdict "duplicate" with of "F-NNN", so
// the stored set stays exactly confirmed|rejected|duplicate.
func ParseVerdictFlag(s string) (verdict, of string, err error) {
	switch s {
	case "confirmed":
		return "confirmed", "", nil
	case "rejected":
		return "rejected", "", nil
	}
	if rest, ok := strings.CutPrefix(s, "duplicate-of-"); ok {
		if !analyze.IsFindingID(rest) {
			return "", "", fmt.Errorf("invalid duplicate target %q (want F-NNN)", rest)
		}
		return "duplicate", rest, nil
	}
	return "", "", fmt.Errorf("invalid verdict %q (want confirmed|rejected|duplicate-of-F-NNN)", s)
}

// AppendVerdict appends one verdict record to findings.jsonl without touching
// any existing line (append-only; latest verdict wins for display).
//
// expect, when non-nil, is the finding the analyst was shown when they made this
// decision. AppendVerdict re-reads the current findings under its lock and
// refuses if the targeted id is gone or now names a different finding — see
// verifyTarget. Callers pass nil only when there is no snapshot to bind against
// (there are none in production; the review paths always pass the judged
// finding).
func AppendVerdict(dir string, v analyze.Verdict, expect *analyze.Finding) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	// Hold the verdict to MaxJSONLLine, the shared read-side invariant every other
	// JSONL writer respects (session.WriteJSONL, analyze.oversizedFindings,
	// demo.tooLongForJSONL). A verdict carries its finding id verbatim, and in an
	// exchanged or hand-edited findings.jsonl that id can be just under the 4 MiB
	// scanner cap — small enough that the finding line loads, large enough that the
	// verdict's own framing tips the line over it. Appending it would durably brick
	// the verdict history this package exists to protect: every later analyze.Load,
	// review, report, and holdsVerdicts would fail with "token too long".
	if len(b)+1 > session.MaxJSONLLine {
		return fmt.Errorf("verdict for %s encodes to %d bytes, over the %d-byte %s line limit",
			v.Finding, len(b)+1, session.MaxJSONLLine, session.FindingsFile)
	}
	path := filepath.Join(dir, session.FindingsFile)
	// O_RDWR rather than O_WRONLY because the record cannot be framed correctly
	// without first reading the byte already at the end of the file.
	f, err := session.OpenFileNoFollow(path, os.O_APPEND|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	// Take an exclusive advisory lock across the probe → write → rollback sequence.
	// Two `testimony review` processes appending to one session's findings.jsonl
	// would otherwise race: A measures the end, B appends a full verdict past it, A's
	// write fails part-way (ENOSPC — the case the rollback exists for), and A's
	// Truncate then cuts the file back below B's committed record, deleting it.
	// writeVerdict re-measuring the end only shrinks that window; the lock closes it,
	// so the length A rolls back to is the true end before A's own bytes. The lock
	// releases with the descriptor on Close.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return err
	}
	// Under the lock, confirm the verdict still targets the finding the analyst
	// judged. review.Run snapshots findings once (analyze.Load) and then blocks on
	// the operator for the whole interactive walk; a concurrent `analyze -ingest`
	// can truncate-and-rewrite findings.jsonl in that gap — permitted until the
	// first verdict exists — and because finding ids restart at F-001 the verdict
	// would otherwise attach to a different finding under the same id, silently
	// misattributing the human decision this file exists to hold. The re-check runs
	// under the same exclusive lock analyze.commitFindings takes, so the re-ingest
	// is either already visible here (mismatch → refuse, no verdict written) or
	// serialised after this append and then blocked by its own verdict-guard.
	if expect != nil {
		if err := verifyTarget(f, v, *expect); err != nil {
			f.Close()
			return err
		}
	}
	if err := writeVerdict(f, append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	// Return the Close error so a verdict is never reported recorded when its
	// bytes did not reach disk (write-back deferred to close on NFS/full device).
	return f.Close()
}

// verifyTarget re-reads the findings currently in f (the locked findings.jsonl
// descriptor) and confirms the verdict v still applies to the finding expect —
// the one the analyst was shown. It refuses if the id has vanished or now names
// a different finding, and for a duplicate verdict if the "of" target has
// vanished. f is read via a SectionReader over ReadAt, which leaves the file
// offset untouched, and the descriptor is O_APPEND, so the subsequent write
// still lands at the true end of file.
func verifyTarget(f *os.File, v analyze.Verdict, expect analyze.Finding) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	findings, _, err := analyze.ParseRecords(io.NewSectionReader(f, 0, info.Size()), session.FindingsFile)
	if err != nil {
		return err
	}
	cur := findByID(findings, v.Finding)
	if cur == nil {
		return fmt.Errorf("finding %s is no longer in %s; it changed since review started — re-run `testimony review`",
			v.Finding, session.FindingsFile)
	}
	if !analyze.SameIdentity(*cur, expect) {
		return fmt.Errorf("finding %s changed since review started (a re-analysis rewrote %s); re-run `testimony review` before recording a verdict",
			v.Finding, session.FindingsFile)
	}
	if v.Verdict == "duplicate" && findByID(findings, v.Of) == nil {
		return fmt.Errorf("duplicate target %s is no longer in %s; re-run `testimony review`",
			v.Of, session.FindingsFile)
	}
	return nil
}

// verdictFile is the subset of *os.File that writeVerdict needs; a fake
// satisfies it in tests to exercise the partial-write rollback.
type verdictFile interface {
	io.Writer
	io.ReaderAt
	Seek(offset int64, whence int) (int64, error)
	Truncate(size int64) error
}

// writeVerdict frames rec so it lands as its own physical line and writes it,
// rolling the file back if the write only partly lands.
//
// A findings.jsonl need not end in a newline: it may have been hand edited,
// produced by another tool, or left short by a crash part-way through an
// earlier write. Appending blindly would fuse the verdict onto that
// unterminated final line, producing one physical line holding two JSON
// objects — which makes not just those two records but the entire file
// unparseable to every reader. So probe the last byte and open a fresh line
// when it is not already one. (O_APPEND still puts the write at the end,
// whatever the seek offset; the seek is only to learn the size.)
//
// The write itself needs the same rollback the capture path has in
// demo.appendRecords: os.File.Write gives no atomicity guarantee, so a full
// disk fills the remaining space, returns a short count, and leaves a
// truncated, newline-less fragment (e.g. `{"kind":"verdict","find`) behind.
// That fragment would fuse with the next successful write into one malformed
// physical line and make the whole findings.jsonl — the human verdict record
// this package exists to protect — unparseable to every reader. So on any
// write error the file is truncated back to the length it had immediately
// before the write. The size is re-measured at that point rather than reusing
// the offset the newline probe learned: the descriptor is O_APPEND, so the
// write lands at the true end of file, which a concurrent appender (a second
// `testimony review`, or an analyze ingest) may have moved on since the probe.
// Truncating to the stale offset would delete that other writer's record
// instead of only our own partial bytes.
func writeVerdict(f verdictFile, rec []byte) error {
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if end > 0 {
		var last [1]byte
		if _, err := f.ReadAt(last[:], end-1); err != nil {
			return err
		}
		if last[0] != '\n' {
			rec = append([]byte{'\n'}, rec...)
		}
	}
	before, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if _, err := f.Write(rec); err != nil {
		// Best-effort roll back any partial bytes; surface the original error.
		f.Truncate(before)
		return err
	}
	return nil
}

// printFinding writes a finding to the analyst's terminal. Every
// attacker-influenceable field (a finding in a downloaded session is untrusted)
// is passed through session.SafeText first, so embedded ESC/ANSI or control
// bytes cannot manipulate the terminal.
func printFinding(w io.Writer, f analyze.Finding) {
	fmt.Fprintf(w, "%s — %s, severity %d, [%s]\n", session.SafeText(f.ID), session.SafeText(f.Type), f.Severity, clock(f.T))
	fmt.Fprintf(w, "  “%s”\n", session.SafeText(f.Quote))
	fmt.Fprintf(w, "  anchor: %s\n", session.SafeText(anchor(f)))
}

func anchor(f analyze.Finding) string {
	if f.UI != nil && (f.UI.Selector != "" || f.UI.Route != "") {
		parts := []string{}
		if f.UI.Selector != "" {
			parts = append(parts, f.UI.Selector)
		}
		if f.UI.Route != "" {
			parts = append(parts, f.UI.Route)
		}
		return strings.Join(parts, " ")
	}
	return "evidence " + strings.Join(f.Evidence, ", ")
}

// describe echoes a recorded verdict to the analyst's terminal. Its fields
// derive from an attacker-authorable finding id in a downloaded session, so
// each is passed through SafeText — matching printFinding and
// report.renderFindings — lest an ESC/ANSI byte in the id drive the terminal.
func describe(v analyze.Verdict) string {
	if v.Of != "" {
		return fmt.Sprintf("recorded: %s %s of %s (%s)",
			session.SafeText(v.Finding), session.SafeText(v.Verdict), session.SafeText(v.Of), session.SafeText(v.At))
	}
	return fmt.Sprintf("recorded: %s %s (%s)",
		session.SafeText(v.Finding), session.SafeText(v.Verdict), session.SafeText(v.At))
}

func contains(findings []analyze.Finding, id string) bool {
	for _, f := range findings {
		if f.ID == id {
			return true
		}
	}
	return false
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
}

// clock renders a session-relative time for the review prompt. Negative times
// are legitimate — an external recording whose creation_time predates the
// manifest t0 yields a negative offset, and analyze.indexTimeline deliberately
// admits findings anchored there — so the sign is rendered rather than clamped
// away. Clamping showed the analyst 00:00 for a pre-t0 finding, the wrong
// moment on the very surface where they record the verdict. This mirrors
// report.clock; see the note in review_test.go about the duplication.
func clock(sec float64) string {
	// Defend the float64→int conversion below against a non-finite or
	// astronomically large sec from a hand-authored findings.jsonl (printFinding
	// renders f.T, and analyze.Load does not bound it): int(sec+0.5) would be an
	// out-of-range conversion the Go spec leaves implementation-defined, printing a
	// nonsensical stamp on the surface where the analyst records a verdict. Mirrors
	// report.clock's guard — the class fix for both copies of this function.
	if math.IsNaN(sec) || math.Abs(sec) > maxClockSeconds {
		return "--:--"
	}
	neg := sec < 0
	if neg {
		sec = -sec
	}
	s := int(sec + 0.5)
	sign := ""
	// The sign is taken from the rounded value, not the raw one, so a time a
	// fraction of a second before t0 prints as 00:00 rather than the nonsense
	// "-00:00".
	if neg && s > 0 {
		sign = "-"
	}
	return fmt.Sprintf("%s%02d:%02d", sign, s/60, s%60)
}
