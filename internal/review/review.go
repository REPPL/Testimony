// Package review is the pipeline's one human-decision surface, across all
// three record families. Its own half records verdicts on candidate findings: a
// verdict is appended to findings.jsonl as a separate, non-destructive record
// (never an in-place rewrite of the finding), so the finding's birth state and
// the full verdict history survive as the precision measure the method stands on
// (architecture note §2; itd-2 press release). Options.Kind dispatches the tests
// half to internal/drafttests, which records an accept / edit / reject decision
// on each drafted regression test the same appended way, and the refs half to
// internal/coderefs, which records an accept / reject decision on each proposed
// code reference — one verb for the whole pipeline, with the vocabularies kept
// per-kind because "edited" carries a payload no verdict or reference decision
// ever does (ADR 0001). Interactive review is gated on stdin
// being a character device so a redirected or piped run (CI) never blocks; a
// single decision can also be recorded non-interactively on either side.
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

	"github.com/REPPL/Testimony/internal/analyze"
	"github.com/REPPL/Testimony/internal/coderefs"
	"github.com/REPPL/Testimony/internal/drafttests"
	"github.com/REPPL/Testimony/internal/session"
)

// maxClockSeconds bounds a value clock will format, mirroring report.maxClockSeconds:
// a real session stamp is minutes to hours, and 1e9 seconds (~31 years) stays
// well inside int64 so the float64→int conversion in clock can never go out of
// range on an attacker-authored findings.jsonl time.
const maxClockSeconds = 1e9

// The record families review can walk. An empty Kind means KindFindings, so a
// caller that predates the tests side keeps the findings behaviour unchanged.
const (
	KindFindings = "findings"
	KindTests    = "tests"
	KindRefs     = "refs"
)

// ParseKindFlag validates a -kind flag value against the closed set, so the CLI
// can refuse an unknown record family as a wrong invocation rather than let it
// reach a dispatch that has no case for it. An empty value is the documented
// default.
func ParseKindFlag(s string) (string, error) {
	switch s {
	case "", KindFindings:
		return KindFindings, nil
	case KindTests:
		return KindTests, nil
	case KindRefs:
		return KindRefs, nil
	}
	return "", fmt.Errorf("invalid kind %q (want findings|tests|refs)", s)
}

// Options configures a review run. The Finding/Verdict pair belongs to
// KindFindings, the Test/Decision/EditIn set to KindTests, and the
// Ref/Decision/Repo set to KindRefs (Decision is shared by the two kinds that
// take one); the CLI refuses a flag from another family at the usage status, and
// Run refuses it too, so the pairing is a property of this API rather than of
// one caller's invariants.
type Options struct {
	Dir      string    // session directory
	Kind     string    // record family: "findings" (the default), "tests", or "refs"
	Finding  string    // non-interactive: the finding to judge (F-NNN)
	Verdict  string    // non-interactive: confirmed | rejected | duplicate-of-F-NNN
	Test     string    // non-interactive, -kind tests: the draft to decide (T-NNN)
	Decision string    // non-interactive, -kind tests or refs: accepted | edited | rejected (edited is tests only)
	EditIn   io.Reader // -kind tests, with Decision "edited": the replacement fields as a JSON object
	Ref      string    // non-interactive, -kind refs: the reference to decide (R-NNN)
	Repo     string    // -kind refs, optional: the application's repository, read only, for the source snippet
	In       io.Reader // interactive input
	Out      io.Writer // status and prompts
	IsTTY    bool      // whether In is an interactive terminal
	Today    string    // ISO date stamped onto verdicts and decisions (YYYY-MM-DD)
}

// Run records human decisions for the session. With -kind tests it delegates to
// the drafting layer's walk and with -kind refs to the mapping layer's;
// otherwise, with -finding/-verdict it records one verdict non-interactively,
// and with neither it walks the unverified findings interactively (skipping
// cleanly when stdin is not a terminal).
func Run(opts Options) error {
	kind, err := ParseKindFlag(opts.Kind)
	if err != nil {
		return err
	}
	// A flag belonging to another record family is a wrong invocation, not a
	// silently ignored one: a caller who typed -verdict against -kind tests meant
	// something this walk cannot do, and recording nothing while exiting 0 would
	// let a script believe the decision landed.
	if kind == KindRefs {
		if opts.Finding != "" || opts.Verdict != "" {
			return fmt.Errorf("-finding and -verdict apply to -kind findings, not -kind refs")
		}
		if opts.Test != "" || opts.EditIn != nil {
			return fmt.Errorf("-test and -edit apply to -kind tests, not -kind refs")
		}
		return coderefs.Review(coderefs.ReviewOptions{
			Dir:      opts.Dir,
			Repo:     opts.Repo,
			Ref:      opts.Ref,
			Decision: opts.Decision,
			In:       opts.In,
			Out:      opts.Out,
			IsTTY:    opts.IsTTY,
			Today:    opts.Today,
		})
	}
	if opts.Ref != "" || opts.Repo != "" {
		return fmt.Errorf("-ref and -repo apply to -kind refs, not -kind %s", kind)
	}
	if kind == KindTests {
		if opts.Finding != "" || opts.Verdict != "" {
			return fmt.Errorf("-finding and -verdict apply to -kind findings, not -kind tests")
		}
		return drafttests.Review(drafttests.ReviewOptions{
			Dir:      opts.Dir,
			Test:     opts.Test,
			Decision: opts.Decision,
			EditIn:   opts.EditIn,
			In:       opts.In,
			Out:      opts.Out,
			IsTTY:    opts.IsTTY,
			Today:    opts.Today,
		})
	}
	if opts.Test != "" || opts.Decision != "" || opts.EditIn != nil {
		return fmt.Errorf("-test, -decision and -edit apply to -kind tests, not -kind findings")
	}

	// analyze.Load reads dir/findings.jsonl, so a session directory that does
	// not exist at all satisfies fs.ErrNotExist exactly like one that exists
	// but has simply never been through `analyze -ingest` — and review,
	// unlike merge/report/analyze, never loads the manifest, so it had
	// nothing else to distinguish the two. Sending an operator with a bad
	// -session path to `analyze -ingest` first only relocates the same
	// failure one command later; checking the directory itself first names
	// the actual problem and reserves the ingest hint for the case it
	// actually describes: a real session that has not been analysed yet.
	if fi, err := os.Stat(opts.Dir); err != nil || !fi.IsDir() {
		if err == nil {
			err = fmt.Errorf("%s is not a directory", opts.Dir)
		}
		return fmt.Errorf("session directory: %w", err)
	}
	// The provenance record is discarded here: review judges findings, and the
	// declaration of what produced them changes nothing about the walk or the
	// verdicts it appends.
	_, findings, verdicts, err := analyze.Load(opts.Dir)
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
	// The verdict's Finding/Of must carry the finding's actual (raw) id, not the
	// operator's clean flag value: analyze.EffectiveStatus keys its map on each
	// finding's raw id, so a verdict recorded under -finding's rendered form would
	// silently fail to attach to a finding whose raw id findByID only matched via
	// SafeText (see findByID).
	if verdict == "duplicate" {
		if dup := findByID(findings, of); dup != nil {
			of = dup.ID
		}
	}
	rec := analyze.Verdict{Kind: "verdict", Finding: target.ID, Verdict: verdict, Of: of, At: opts.Today}
	if err := AppendVerdict(opts.Dir, rec, target); err != nil {
		return err
	}
	fmt.Fprintln(opts.Out, describe(rec))
	return nil
}

// findByID returns a pointer to the finding with the given id, or nil. Ids are
// compared in their session.SafeText form, matching analyze.ParseRecords' load-
// time uniqueness check: a finding's id renders through SafeText everywhere it
// is shown (report, review's printFinding), so an operator matching it via
// -finding, or an interactive duplicate-of target, only ever has the rendered
// form to type. Comparing raw would leave a finding whose raw id carries a
// stripped byte (e.g. a hand-edited findings.jsonl with an invisible
// character) permanently unreachable by the id it displays as. The returned
// pointer is into a copy, safe to retain.
func findByID(findings []analyze.Finding, id string) *analyze.Finding {
	want := session.SafeText(id)
	for i := range findings {
		if session.SafeText(findings[i].ID) == want {
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
	trimmed := strings.TrimSpace(choice)
	switch strings.ToLower(trimmed) {
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
		// Resolve the typed target back to its actual (raw) id; see the matching
		// comment in single() for why the clean typed form cannot be stored as-is.
		of := target
		if dup := findByID(findings, target); dup != nil {
			of = dup.ID
		}
		return true, false, record(opts, f, analyze.Verdict{Kind: "verdict", Finding: f.ID, Verdict: "duplicate", Of: of, At: opts.Today})
	case "s", "":
		fmt.Fprintln(opts.Out, "  skipped.")
		return true, false, nil
	case "q":
		return false, true, nil
	default:
		return false, false, fmt.Errorf("unrecognised choice %q", trimmed)
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
	// id/of print through session.SafeText even though every current caller passes
	// an operator flag or an IsFindingID-validated value: making the terminal-safety
	// local here, rather than a property of caller invariants, keeps a future caller
	// that passes an attacker-authored id straight from findings.jsonl from
	// reintroducing the ANSI-injection this mirrors from the verdict-error paths.
	if !contains(findings, id) {
		return fmt.Errorf("finding %s not found", session.SafeText(id))
	}
	if verdict == "duplicate" {
		if session.SafeText(of) == session.SafeText(id) {
			return fmt.Errorf("a finding cannot be a duplicate of itself")
		}
		if !contains(findings, of) {
			return fmt.Errorf("duplicate target %s not found", session.SafeText(of))
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
// The dangerous part of the write — the no-follow open, the exclusive lock, the
// two size pre-flights, the newline framing over an unterminated last line, the
// partial-write rollback, and returning the Close error — lives once in
// session.AppendRecord, shared with the decision records tests.jsonl holds. This
// function supplies only the vocabulary: the verdict's encoding, the labels the
// two size errors name it by, and the target re-check below.
//
// expect, when non-nil, is the finding the analyst was shown when they made this
// decision. session.AppendRecord runs the Verify closure over the current
// findings under its lock, and it refuses if the targeted id is gone or now names
// a different finding — see verifyTarget. Callers pass nil only when there is no
// snapshot to bind against (there are none in production; the review paths always
// pass the judged finding).
func AppendVerdict(dir string, v analyze.Verdict, expect *analyze.Finding) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	a := session.Append{
		Path:   filepath.Join(dir, session.FindingsFile),
		Record: b,
		// The finding id is attacker-authorable in an exchanged session and the
		// label reaches the operator's terminal through cli.fail, so it is
		// sanitised here rather than inside the shared primitive, which never sees
		// an id as such.
		Label: "verdict for " + session.SafeText(v.Finding),
		Kind:  "verdict",
	}
	if expect != nil {
		// Confirm, under the append lock, that the verdict still targets the finding
		// the analyst judged. review.Run snapshots findings once (analyze.Load) and
		// then blocks on the operator for the whole interactive walk; a concurrent
		// `analyze -ingest` can truncate-and-rewrite findings.jsonl in that gap —
		// permitted until the first verdict exists — and because finding ids restart
		// at F-001 the verdict would otherwise attach to a different finding under
		// the same id, silently misattributing the human decision this file exists to
		// hold. The re-check runs under the same exclusive lock the commit side
		// takes, so the re-ingest is either already visible here (mismatch → refuse,
		// no verdict written) or serialised after this append and then blocked by its
		// own verdict-guard.
		judged := *expect
		a.Verify = func(current io.Reader) error { return verifyTarget(current, v, judged) }
	}
	return session.AppendRecord(a)
}

// verifyTarget re-reads the findings currently in the locked findings.jsonl
// (session.AppendRecord hands it a reader over the file's contents, taken under
// the append lock) and confirms the verdict v still applies to the finding
// expect — the one the analyst was shown. It refuses if the id has vanished or
// now names a different finding, and for a duplicate verdict if the "of" target
// has vanished.
func verifyTarget(current io.Reader, v analyze.Verdict, expect analyze.Finding) error {
	_, findings, _, err := analyze.ParseRecords(current, session.FindingsFile)
	if err != nil {
		return err
	}
	cur := findByID(findings, v.Finding)
	if cur == nil {
		return fmt.Errorf("finding %s is no longer in %s; it changed since review started — re-run `testimony review`",
			session.SafeText(v.Finding), session.FindingsFile)
	}
	if !analyze.SameIdentity(*cur, expect) {
		return fmt.Errorf("finding %s changed since review started (a re-analysis rewrote %s); re-run `testimony review` before recording a verdict",
			session.SafeText(v.Finding), session.FindingsFile)
	}
	if v.Verdict == "duplicate" && findByID(findings, v.Of) == nil {
		return fmt.Errorf("duplicate target %s is no longer in %s; re-run `testimony review`",
			session.SafeText(v.Of), session.FindingsFile)
	}
	return nil
}

// printFinding writes a finding to the analyst's terminal. Every
// attacker-influenceable field (a finding in a downloaded session is untrusted)
// is passed through session.SafeText first, so embedded ESC/ANSI or control
// bytes cannot manipulate the terminal.
//
// type and quote are decided on the SafeText-rendered form, not the raw one,
// matching anchor below: analyze.Load validates neither, so a hand-edited or
// exchanged findings.jsonl reaching this sink directly must fall through to a
// placeholder rather than print a dangling ", severity" with nothing before
// it, or a blank quotation.
func printFinding(w io.Writer, f analyze.Finding) {
	typ := session.SafeText(f.Type)
	if strings.TrimSpace(typ) == "" {
		typ = "—"
	}
	quote := session.SafeText(f.Quote)
	if strings.TrimSpace(quote) == "" {
		quote = "no quote"
	}
	fmt.Fprintf(w, "%s — %s, severity %d, [%s]\n", session.SafeText(f.ID), typ, f.Severity, clock(f.T))
	fmt.Fprintf(w, "  “%s”\n", quote)
	fmt.Fprintf(w, "  anchor: %s\n", session.SafeText(anchor(f)))
}

// anchor renders a finding's on-screen anchor for printFinding. Presence is
// decided on the SafeText-rendered form, not the raw one: printFinding wraps
// the whole return value in session.SafeText before printing, so a selector
// or route that is non-empty raw but strips to nothing under SafeText
// (invisible-only Unicode) must fall through to the evidence ids rather than
// render a blank (or merely whitespace) anchor with no fallback. The evidence
// ids themselves are filtered the same way: an id that strips to nothing is
// dropped, and an evidence list with no renderable id left falls back to
// "no evidence" rather than the dangling "evidence " label.
func anchor(f analyze.Finding) string {
	if f.UI != nil {
		var parts []string
		if sel := session.SafeText(f.UI.Selector); strings.TrimSpace(sel) != "" {
			parts = append(parts, sel)
		}
		if route := session.SafeText(f.UI.Route); strings.TrimSpace(route) != "" {
			parts = append(parts, route)
		}
		if len(parts) > 0 {
			return strings.Join(parts, " ")
		}
	}
	var ids []string
	for _, id := range f.Evidence {
		if strings.TrimSpace(session.SafeText(id)) != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return "no evidence"
	}
	return "evidence " + strings.Join(ids, ", ")
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

// contains reports whether id (in its session.SafeText form) names one of
// findings; see findByID for why the comparison is SafeText, not raw.
func contains(findings []analyze.Finding, id string) bool {
	return findByID(findings, id) != nil
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
