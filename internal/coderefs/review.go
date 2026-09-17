package coderefs

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/REPPL/Testimony/internal/analyze"
	"github.com/REPPL/Testimony/internal/session"
)

// snippetContext is how many lines either side of a reference's line the
// interactive walk shows when the repository is given.
const snippetContext = 2

// ReviewOptions configures a `review -kind refs` run. It mirrors
// drafttests.ReviewOptions where the two agree; there is no edit payload,
// because a wrong path is rejected and a corrected one is ingested.
type ReviewOptions struct {
	Dir      string    // session directory
	Repo     string    // optional: the application's repository, read only, for the source snippet
	Ref      string    // non-interactive: the reference to decide (R-NNN)
	Decision string    // non-interactive: accepted | rejected
	In       io.Reader // interactive input
	Out      io.Writer // status and prompts
	IsTTY    bool      // whether In is an interactive terminal
	Today    string    // ISO date stamped onto decisions (YYYY-MM-DD)
}

// Review records human decisions on the session's references. With
// -ref/-decision it records one decision non-interactively; otherwise it walks
// the proposed references interactively (skipping cleanly when stdin is not a
// terminal, so CI never blocks).
func Review(opts ReviewOptions) error {
	if fi, err := os.Stat(opts.Dir); err != nil || !fi.IsDir() {
		if err == nil {
			err = fmt.Errorf("%s is not a directory", opts.Dir)
		}
		return fmt.Errorf("session directory: %w", err)
	}
	refs, decisions, err := loadRefs(opts.Dir)
	if err != nil {
		return err
	}

	if opts.Ref != "" || opts.Decision != "" {
		// The CLI refuses this at the usage status; checked here too so the rule is
		// a property of the API: the snippet is shown only by the walk, so a -repo
		// alongside a single decision would otherwise be silently ignored.
		if opts.Repo != "" {
			return fmt.Errorf("-repo applies to the interactive walk, not to -ref/-decision")
		}
		return singleDecision(opts, refs)
	}

	if !opts.IsTTY {
		fmt.Fprintln(opts.Out, "review: stdin is not a terminal; skipping the interactive walk "+
			"(use -ref R-NNN -decision accepted|rejected for a single decision).")
		return nil
	}
	return walk(opts, refs, decisions)
}

// findingsFor reads the session's findings so the walk can show each
// reference's finding: its quote and anchor are what the reviewer judges the
// path against. They are context, not substance, so an unreadable or absent
// findings.jsonl degrades to placeholders rather than blocking a decision.
func findingsFor(dir string) []analyze.Finding {
	_, findings, _, err := analyze.Load(dir)
	if err != nil {
		return nil
	}
	return findings
}

// singleDecision records one decision non-interactively.
func singleDecision(opts ReviewOptions, refs []Ref) error {
	if opts.Ref == "" {
		return fmt.Errorf("-ref is required with -decision")
	}
	if opts.Decision == "" {
		return fmt.Errorf("-decision is required with -ref")
	}
	decision, err := ParseDecisionFlag(opts.Decision)
	if err != nil {
		return err
	}
	target := refByID(refs, opts.Ref)
	if target == nil {
		return fmt.Errorf("reference %s not found", session.SafeText(opts.Ref))
	}
	// The decision's Ref carries the reference's actual (raw) id, not the
	// operator's clean flag value: EffectiveStatus keys on the raw id.
	rec := Decision{Kind: "decision", Ref: target.ID, Decision: decision, At: opts.Today}
	if err := AppendDecision(opts.Dir, rec, target); err != nil {
		return err
	}
	fmt.Fprintln(opts.Out, describe(rec))
	return nil
}

// errPersist marks an error that arose while writing a decision to disk, as
// distinct from the validation errors the walk raises for an unrecognised
// keystroke. A validation error is a retry situation; a failed append means the
// human's decision never reached refs.jsonl, so anything wrapping this sentinel
// aborts the walk and propagates to the CLI's non-zero exit.
var errPersist = errors.New("recording the decision failed")

// walk interactively decides each proposed reference in id order.
func walk(opts ReviewOptions, refs []Ref, decisions []Decision) error {
	eff := EffectiveStatus(refs, decisions)
	var queue []Ref
	for _, r := range refs {
		if eff[r.ID].Value == "proposed" {
			queue = append(queue, r)
		}
	}
	sort.Slice(queue, func(i, j int) bool { return queue[i].ID < queue[j].ID })
	if len(queue) == 0 {
		fmt.Fprintln(opts.Out, "No proposed references to review.")
		return nil
	}
	findings := findingsFor(opts.Dir)
	var root *repoRoot
	if opts.Repo != "" {
		if rr, err := newRepoRoot(opts.Repo); err == nil {
			root = &rr
		} else {
			fmt.Fprintf(opts.Out, "(source snippets unavailable: %s)\n", session.SafeText(err.Error()))
		}
	}

	r := bufio.NewReader(opts.In)
	for i, ref := range queue {
		fmt.Fprintf(opts.Out, "\n(%d/%d) ", i+1, len(queue))
		printRef(opts.Out, ref, findingByID(findings, ref.Finding), root)
		for {
			fmt.Fprint(opts.Out, "[a]ccept [r]eject [s]kip [q]uit: ")
			choice, err := readLine(r)
			if err != nil {
				fmt.Fprintln(opts.Out, "\n(end of input) stopping.")
				return nil
			}
			done, quit, verr := applyChoice(opts, ref, choice)
			if verr != nil {
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

// applyChoice handles one keystroke. done means advance to the next reference;
// quit means stop the walk.
func applyChoice(opts ReviewOptions, ref Ref, choice string) (done, quit bool, err error) {
	trimmed := strings.TrimSpace(choice)
	switch strings.ToLower(trimmed) {
	case "a":
		return true, false, record(opts, ref, Decision{Kind: "decision", Ref: ref.ID, Decision: "accepted", At: opts.Today})
	case "r":
		return true, false, record(opts, ref, Decision{Kind: "decision", Ref: ref.ID, Decision: "rejected", At: opts.Today})
	case "s", "":
		fmt.Fprintln(opts.Out, "  skipped.")
		return true, false, nil
	case "q":
		return false, true, nil
	default:
		return false, false, fmt.Errorf("unrecognised choice %q", trimmed)
	}
}

func record(opts ReviewOptions, judged Ref, rec Decision) error {
	if err := AppendDecision(opts.Dir, rec, &judged); err != nil {
		return fmt.Errorf("%w: %v", errPersist, err)
	}
	fmt.Fprintf(opts.Out, "  %s\n", describe(rec))
	return nil
}

// AppendDecision appends one decision record to refs.jsonl without touching any
// existing line (append-only; the latest decision wins for display). The
// reference line is never rewritten, which is what keeps its id, finding,
// session, and path unreachable by any later write.
//
// The dangerous part of the write lives once in session.AppendRecord, shared
// with the verdicts findings.jsonl holds and the decisions tests.jsonl holds;
// this function supplies the vocabulary and the target re-check.
//
// expect, when non-nil, is the reference the operator was shown when they made
// this decision. session.AppendRecord runs the Verify closure over the current
// references under its lock and refuses if the targeted id is gone or now names
// a different reference: a concurrent `map -ingest` may truncate-and-rewrite in
// the gap while the walk blocks on the operator (permitted until the first
// decision exists), and reference ids restart at R-001.
func AppendDecision(dir string, d Decision, expect *Ref) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	a := session.Append{
		Path:   filepath.Join(dir, session.RefsFile),
		Record: b,
		Label:  "decision for " + session.SafeText(d.Ref),
		Kind:   "decision",
	}
	if expect != nil {
		judged := *expect
		a.Verify = func(current io.Reader) error { return verifyTarget(current, d, judged) }
	}
	return session.AppendRecord(a)
}

// verifyTarget re-reads the references currently in the locked refs.jsonl and
// confirms the decision d still applies to the reference expect — the one the
// operator was shown.
func verifyTarget(current io.Reader, d Decision, expect Ref) error {
	refs, _, err := ParseRecords(current, session.RefsFile)
	if err != nil {
		return err
	}
	cur := refByID(refs, d.Ref)
	if cur == nil {
		return fmt.Errorf("reference %s is no longer in %s; it changed since review started — re-run `testimony review -kind refs`",
			session.SafeText(d.Ref), session.RefsFile)
	}
	if !SameIdentity(*cur, expect) {
		return fmt.Errorf("reference %s changed since review started (a re-ingest rewrote %s); re-run `testimony review -kind refs` before recording a decision",
			session.SafeText(d.Ref), session.RefsFile)
	}
	return nil
}

// printRef writes a reference to the operator's terminal: the finding's quote
// and anchor, then the path, line, and role, then, when the repository is
// given, the lines around the reference's line. Every attacker-influenceable
// field is passed through session.SafeText first, and presence is decided on
// the rendered form.
func printRef(w io.Writer, r Ref, f *analyze.Finding, root *repoRoot) {
	loc := orPlaceholder(r.Path, "no path")
	if r.Line > 0 {
		loc = fmt.Sprintf("%s:%d", loc, r.Line)
	}
	fmt.Fprintf(w, "%s — %s (%s), from %s\n",
		session.SafeText(r.ID), loc, orPlaceholder(r.Role, "no role"), session.SafeText(r.Finding))
	if f != nil {
		fmt.Fprintf(w, "  “%s”\n", orPlaceholder(f.Quote, "no quote"))
		fmt.Fprintf(w, "  anchor: %s [%s]\n", anchorText(*f), clock(f.T))
	} else {
		fmt.Fprintln(w, "  (finding not readable)")
	}
	if root != nil && r.Line > 0 {
		for _, l := range snippet(*root, r) {
			fmt.Fprintf(w, "  %s\n", l)
		}
	}
}

// anchorText renders the finding's selector and route for the terminal.
func anchorText(f analyze.Finding) string {
	var parts []string
	if f.UI != nil {
		if sel := session.SafeText(f.UI.Selector); strings.TrimSpace(sel) != "" {
			parts = append(parts, sel)
		}
		if route := session.SafeText(f.UI.Route); strings.TrimSpace(route) != "" {
			parts = append(parts, route)
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " on ")
}

// snippet returns the numbered source lines around r.Line, or a one-line note
// when the file cannot be shown. The path is re-checked through the same
// containment rule ingest applies, so a hand-edited refs.jsonl cannot make the
// walk open a file outside the repository, and the read is bounded the same way.
func snippet(root repoRoot, r Ref) []string {
	full, _, err := root.checkPath(r.Path)
	if err != nil {
		return []string{"(source unavailable: " + session.SafeText(err.Error()) + ")"}
	}
	f, err := session.OpenFileNoFollowRead(full)
	if err != nil {
		return []string{"(source unavailable: " + session.SafeText(err.Error()) + ")"}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, session.MaxJSONLBytes+1))
	if err != nil || len(data) > session.MaxJSONLBytes {
		return []string{"(source unavailable: file too large to show)"}
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if r.Line > len(lines) {
		return []string{fmt.Sprintf("(source unavailable: line %d is past the end of the file, %d lines)", r.Line, len(lines))}
	}
	lo, hi := r.Line-snippetContext, r.Line+snippetContext
	if lo < 1 {
		lo = 1
	}
	if hi > len(lines) {
		hi = len(lines)
	}
	var out []string
	for n := lo; n <= hi; n++ {
		mark := " "
		if n == r.Line {
			mark = ">"
		}
		out = append(out, fmt.Sprintf("%s %4d  %s", mark, n, session.SafeText(lines[n-1])))
	}
	return out
}

// describe echoes a recorded decision to the operator's terminal.
func describe(d Decision) string {
	return fmt.Sprintf("recorded: %s %s (%s)",
		session.SafeText(d.Ref), session.SafeText(d.Decision), session.SafeText(d.At))
}

// orPlaceholder renders untrusted text for the terminal, falling back to a
// placeholder when it renders as nothing.
func orPlaceholder(s, placeholder string) string {
	t := session.SafeText(s)
	if strings.TrimSpace(t) == "" {
		return placeholder
	}
	return t
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
}
