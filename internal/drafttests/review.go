package drafttests

import (
	"bufio"
	"bytes"
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

// ReviewOptions configures a `review -kind tests` run. It mirrors review.Options
// field for field where the two agree; the vocabulary differs because a
// decision's "edited" carries a payload no verdict ever does.
type ReviewOptions struct {
	Dir      string    // session directory
	Test     string    // non-interactive: the draft to decide (T-NNN)
	Decision string    // non-interactive: accepted | edited | rejected
	EditIn   io.Reader // with Decision "edited": the replacement fields as a JSON object
	In       io.Reader // interactive input
	Out      io.Writer // status and prompts
	IsTTY    bool      // whether In is an interactive terminal
	Today    string    // ISO date stamped onto decisions (YYYY-MM-DD)
}

// Review records human decisions on the session's test drafts. With
// -test/-decision it records one decision non-interactively; otherwise it walks
// the proposed drafts interactively (skipping cleanly when stdin is not a
// terminal, so CI never blocks).
func Review(opts ReviewOptions) error {
	// Load reads dir/tests.jsonl, so a session directory that does not exist at
	// all satisfies fs.ErrNotExist exactly like one that exists but has simply
	// never been through `draft-tests -ingest`. Checking the directory itself
	// first names the actual problem and reserves the ingest hint for the case it
	// actually describes — the same order review.Run uses for findings.
	if fi, err := os.Stat(opts.Dir); err != nil || !fi.IsDir() {
		if err == nil {
			err = fmt.Errorf("%s is not a directory", opts.Dir)
		}
		return fmt.Errorf("session directory: %w", err)
	}
	drafts, decisions, err := loadDrafts(opts.Dir)
	if err != nil {
		return err
	}

	if opts.Test != "" || opts.Decision != "" {
		return singleDecision(opts, drafts)
	}

	if !opts.IsTTY {
		fmt.Fprintln(opts.Out, "review: stdin is not a terminal; skipping the interactive walk "+
			"(use -test T-NNN -decision accepted|edited|rejected for a single decision).")
		return nil
	}
	return walk(opts, drafts, decisions)
}

// findingsFor reads the session's findings so the walk and the render can name
// each draft's source type and clock. They are decoration, not substance — the
// draft carries the steps, the expectation, and the quote — so an unreadable or
// absent findings.jsonl degrades to placeholders rather than blocking a human
// decision that is already overdue.
func findingsFor(dir string) []analyze.Finding {
	_, findings, _, err := analyze.Load(dir)
	if err != nil {
		return nil
	}
	return findings
}

func findingByID(findings []analyze.Finding, id string) *analyze.Finding {
	want := session.SafeText(id)
	for i := range findings {
		if session.SafeText(findings[i].ID) == want {
			f := findings[i]
			return &f
		}
	}
	return nil
}

// singleDecision records one decision non-interactively.
func singleDecision(opts ReviewOptions, drafts []Draft) error {
	if opts.Test == "" {
		return fmt.Errorf("-test is required with -decision")
	}
	if opts.Decision == "" {
		return fmt.Errorf("-decision is required with -test")
	}
	decision, err := ParseDecisionFlag(opts.Decision)
	if err != nil {
		return err
	}
	// The CLI refuses this at the usage status; checked here too so the refusal is
	// a property of the API rather than of one caller's invariants — an edit
	// carried alongside "accepted" or "rejected" would otherwise be silently
	// discarded.
	if decision != "edited" && opts.EditIn != nil {
		return fmt.Errorf("-edit applies only to -decision edited")
	}
	target := draftByID(drafts, opts.Test)
	if target == nil {
		return fmt.Errorf("test draft %s not found", session.SafeText(opts.Test))
	}
	var edit *Edit
	if decision == "edited" {
		// Every interactive path in this repo has a non-interactive twin; making
		// "edited" the one exception would put the only lossy decision out of reach
		// of a script or an agent host.
		if opts.EditIn == nil {
			return fmt.Errorf("-edit is required with -decision edited")
		}
		edit, err = ParseEdit(opts.EditIn)
		if err != nil {
			return err
		}
	}
	// The decision's Test must carry the draft's actual (raw) id, not the
	// operator's clean flag value: EffectiveStatus keys on the raw id, so a
	// decision recorded under the rendered form would silently fail to attach to a
	// draft whose raw id draftByID only matched via SafeText.
	rec := Decision{Kind: "decision", Test: target.ID, Decision: decision, At: opts.Today, Edit: edit}
	if err := AppendDecision(opts.Dir, rec, target); err != nil {
		return err
	}
	fmt.Fprintln(opts.Out, describe(rec))
	return nil
}

// ParseEdit decodes a replacement-fields object — the `-edit FILE` payload — with
// unknown fields disallowed, so an edit naming `finding`, `session`, `severity`,
// `rationale_quote`, or `id` is a hard error rather than a silently dropped key.
// That is the mechanism by which no human edit can ever re-point a draft at a
// different finding or session: the only way to change the link is to reject the
// draft and ingest a new one. Each present member is held to the draft's own rule
// for that field, and an edit naming no member at all is refused — an "edited"
// decision that changes nothing is not representable.
func ParseEdit(r io.Reader) (*Edit, error) {
	data, err := io.ReadAll(io.LimitReader(r, session.MaxAnswerBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > session.MaxAnswerBytes {
		return nil, fmt.Errorf("edit exceeds %d bytes: refusing to read", session.MaxAnswerBytes)
	}
	var e Edit
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return nil, fmt.Errorf("parse edit: %w", err)
	}
	if err := checkEdit(&e); err != nil {
		return nil, err
	}
	return &e, nil
}

// checkEdit holds each present member of an edit to the draft rule for that
// field, so a replacement can never be weaker than what it replaces.
func checkEdit(e *Edit) error {
	if e.empty() {
		return fmt.Errorf("edit names no field; give at least one of title, steps, expected, observed")
	}
	if e.Title != nil {
		t := strings.TrimSpace(session.SafeText(*e.Title))
		if t == "" {
			return fmt.Errorf("edit: title must be non-empty")
		}
		if n := len([]rune(t)); n > maxTitle {
			return fmt.Errorf("edit: title is %d characters, exceeding the limit of %d", n, maxTitle)
		}
	}
	if e.Steps != nil {
		steps := *e.Steps
		if len(steps) == 0 {
			return fmt.Errorf("edit: steps must be non-empty")
		}
		if len(steps) > maxSteps {
			return fmt.Errorf("edit: steps lists %d entries, exceeding the limit of %d", len(steps), maxSteps)
		}
		for i, s := range steps {
			if strings.TrimSpace(session.SafeText(s)) == "" {
				return fmt.Errorf("edit: step %d must be non-empty", i+1)
			}
		}
	}
	if e.Expected != nil && strings.TrimSpace(session.SafeText(*e.Expected)) == "" {
		return fmt.Errorf("edit: expected must be non-empty")
	}
	if e.Observed != nil && strings.TrimSpace(session.SafeText(*e.Observed)) == "" {
		return fmt.Errorf("edit: observed must be non-empty")
	}
	return nil
}

// errPersist marks an error that arose while writing a decision to disk, as
// distinct from the validation errors the walk raises for an unrecognised
// keystroke or a rejected edit. The walk must be able to tell them apart: a
// validation error is a genuine retry situation and is printed as a hint,
// whereas a failed append means the human's decision never reached tests.jsonl.
// Conflating the two would let the walk print a retry hint and exit 0 while
// silently losing the decision, so anything wrapping this sentinel aborts the
// walk and propagates to the CLI's non-zero exit.
var errPersist = errors.New("recording the decision failed")

// walk interactively decides each proposed draft in id order.
func walk(opts ReviewOptions, drafts []Draft, decisions []Decision) error {
	eff := EffectiveStatus(drafts, decisions)
	var queue []Draft
	for _, d := range drafts {
		if eff[d.ID].Value == "proposed" {
			queue = append(queue, d)
		}
	}
	sort.Slice(queue, func(i, j int) bool { return queue[i].ID < queue[j].ID })
	if len(queue) == 0 {
		fmt.Fprintln(opts.Out, "No proposed test drafts to review.")
		return nil
	}
	findings := findingsFor(opts.Dir)

	r := bufio.NewReader(opts.In)
	for i, d := range queue {
		fmt.Fprintf(opts.Out, "\n(%d/%d) ", i+1, len(queue))
		printDraft(opts.Out, d, findingByID(findings, d.Finding))
		for {
			fmt.Fprint(opts.Out, "[a]ccept [e]dit [r]eject [s]kip [q]uit: ")
			choice, err := readLine(r)
			if err != nil {
				fmt.Fprintln(opts.Out, "\n(end of input) stopping.")
				return nil
			}
			done, quit, verr := applyChoice(opts, d, choice, r)
			if verr != nil {
				// Only an invalid choice or an invalid edit is worth re-prompting
				// for; a persistence failure is not something the operator can
				// retype their way out of, and swallowing it here would end the run
				// successfully with the decision lost.
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

// applyChoice handles one keystroke. done means advance to the next draft; quit
// means stop the walk.
func applyChoice(opts ReviewOptions, d Draft, choice string, r *bufio.Reader) (done, quit bool, err error) {
	trimmed := strings.TrimSpace(choice)
	switch strings.ToLower(trimmed) {
	case "a":
		return true, false, record(opts, d, Decision{Kind: "decision", Test: d.ID, Decision: "accepted", At: opts.Today})
	case "r":
		return true, false, record(opts, d, Decision{Kind: "decision", Test: d.ID, Decision: "rejected", At: opts.Today})
	case "e":
		edit, eerr := promptEdit(opts.Out, r, d)
		if eerr != nil {
			return false, false, eerr
		}
		// An "edited" decision with an empty edit is not representable, so a pass
		// through the prompts that changed nothing is recorded as the acceptance it
		// actually was, rather than as a change that never happened.
		if edit.empty() {
			fmt.Fprintln(opts.Out, "  no changes; recorded as accepted.")
			return true, false, record(opts, d, Decision{Kind: "decision", Test: d.ID, Decision: "accepted", At: opts.Today})
		}
		return true, false, record(opts, d, Decision{Kind: "decision", Test: d.ID, Decision: "edited", At: opts.Today, Edit: edit})
	case "s", "":
		fmt.Fprintln(opts.Out, "  skipped.")
		return true, false, nil
	case "q":
		return false, true, nil
	default:
		return false, false, fmt.Errorf("unrecognised choice %q", trimmed)
	}
}

// promptEdit asks for each editable field in turn, showing the current value; a
// blank answer keeps it. steps are read a line at a time until a blank line, and
// a blank first line keeps the current steps. The returned edit names only the
// fields the operator actually replaced.
func promptEdit(w io.Writer, r *bufio.Reader, d Draft) (*Edit, error) {
	e := &Edit{}

	fmt.Fprintf(w, "  title [%s]: ", orPlaceholder(d.Title, "no title"))
	title, err := readLine(r)
	if err != nil {
		return nil, fmt.Errorf("no input given")
	}
	if t := strings.TrimSpace(title); t != "" {
		e.Title = &t
	}

	fmt.Fprintln(w, "  steps (one per line, blank line ends; a blank first line keeps them):")
	var steps []string
	// typed counts every step the operator actually entered; steps stops growing
	// one past the cap. The two differ precisely when the input is over-long, and
	// the refusal below reports typed — reporting len(steps) would say "33
	// entries" to someone who typed forty, naming a number they never chose.
	typed := 0
	for {
		fmt.Fprint(w, "    ")
		line, lerr := readLine(r)
		if lerr != nil {
			return nil, fmt.Errorf("no input given")
		}
		s := strings.TrimSpace(line)
		if s == "" {
			break
		}
		typed++
		// Read to the terminating blank line whatever the count, so the rest of the
		// operator's typing is never left behind to be consumed as the next
		// prompt's answer, but hold the slice one past the cap so an absurd paste
		// cannot grow it without bound.
		if len(steps) <= maxSteps {
			steps = append(steps, s)
		}
	}
	// Checked here rather than left to checkEdit, which can only see the truncated
	// slice. The message is checkEdit's, so both paths refuse in one voice.
	if typed > maxSteps {
		return nil, fmt.Errorf("edit: steps lists %d entries, exceeding the limit of %d", typed, maxSteps)
	}
	if len(steps) > 0 {
		e.Steps = &steps
	}

	fmt.Fprintf(w, "  expected [%s]: ", orPlaceholder(d.Expected, "no expected behaviour"))
	expected, err := readLine(r)
	if err != nil {
		return nil, fmt.Errorf("no input given")
	}
	if t := strings.TrimSpace(expected); t != "" {
		e.Expected = &t
	}

	fmt.Fprintf(w, "  observed [%s]: ", orPlaceholder(d.Observed, "no observed behaviour"))
	observed, err := readLine(r)
	if err != nil {
		return nil, fmt.Errorf("no input given")
	}
	if t := strings.TrimSpace(observed); t != "" {
		e.Observed = &t
	}

	if e.empty() {
		return e, nil
	}
	if err := checkEdit(e); err != nil {
		return nil, err
	}
	return e, nil
}

func record(opts ReviewOptions, judged Draft, rec Decision) error {
	if err := AppendDecision(opts.Dir, rec, &judged); err != nil {
		// Wrapped so walk can distinguish a lost decision from a mistyped
		// keystroke; see errPersist.
		return fmt.Errorf("%w: %v", errPersist, err)
	}
	fmt.Fprintf(opts.Out, "  %s\n", describe(rec))
	return nil
}

// AppendDecision appends one decision record to tests.jsonl without touching any
// existing line (append-only; the latest decision wins for display). The draft
// line is never rewritten, which is what keeps a draft's id, finding, session,
// severity, and rationale_quote unreachable by any later write.
//
// The dangerous part of the write lives once in session.AppendRecord, shared with
// the verdicts findings.jsonl holds; this function supplies the vocabulary and
// the target re-check.
//
// expect, when non-nil, is the draft the operator was shown when they made this
// decision. session.AppendRecord runs the Verify closure over the current drafts
// under its lock and refuses if the targeted id is gone or now names a different
// draft: `review -kind tests` snapshots the drafts once and then blocks on the
// operator, a concurrent `draft-tests -ingest` may truncate-and-rewrite in that
// gap (permitted until the first decision exists), and draft ids restart at
// T-001 — so without the re-check a decision would silently attach to a different
// draft.
func AppendDecision(dir string, d Decision, expect *Draft) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	a := session.Append{
		Path:   filepath.Join(dir, session.TestsFile),
		Record: b,
		// The draft id is attacker-authorable in an exchanged session and the label
		// reaches the operator's terminal through cli.fail, so it is sanitised here
		// rather than inside the shared primitive, which never sees an id as such.
		Label: "decision for " + session.SafeText(d.Test),
		Kind:  "decision",
	}
	if expect != nil {
		judged := *expect
		a.Verify = func(current io.Reader) error { return verifyTarget(current, d, judged) }
	}
	return session.AppendRecord(a)
}

// verifyTarget re-reads the drafts currently in the locked tests.jsonl
// (session.AppendRecord hands it a reader over the file's contents, taken under
// the append lock) and confirms the decision d still applies to the draft expect
// — the one the operator was shown.
func verifyTarget(current io.Reader, d Decision, expect Draft) error {
	drafts, _, err := ParseRecords(current, session.TestsFile)
	if err != nil {
		return err
	}
	cur := draftByID(drafts, d.Test)
	if cur == nil {
		return fmt.Errorf("test draft %s is no longer in %s; it changed since review started — re-run `testimony review -kind tests`",
			session.SafeText(d.Test), session.TestsFile)
	}
	if !SameIdentity(*cur, expect) {
		return fmt.Errorf("test draft %s changed since review started (a re-ingest rewrote %s); re-run `testimony review -kind tests` before recording a decision",
			session.SafeText(d.Test), session.TestsFile)
	}
	return nil
}

// printDraft writes a draft to the operator's terminal. Every
// attacker-influenceable field (a draft in a downloaded session is untrusted) is
// passed through session.SafeText first, so embedded ESC/ANSI or control bytes
// cannot manipulate the terminal, and presence is decided on the rendered form so
// a value that strips to nothing falls through to a placeholder rather than
// printing as a blank. f is the source finding when it is still readable: it
// supplies the type and the clock, and its absence degrades to placeholders
// rather than blocking the decision.
func printDraft(w io.Writer, d Draft, f *analyze.Finding) {
	typ, at := "—", "--:--"
	if f != nil {
		typ, at = orPlaceholder(f.Type, "—"), clock(f.T)
	}
	fmt.Fprintf(w, "%s — from %s (%s, severity %d), [%s]\n",
		session.SafeText(d.ID), session.SafeText(d.Finding), typ, d.Severity, at)
	fmt.Fprintf(w, "  %s\n", orPlaceholder(d.Title, "no title"))
	fmt.Fprintln(w, "  steps:")
	if len(d.Steps) == 0 {
		fmt.Fprintln(w, "    (none)")
	}
	for i, s := range d.Steps {
		fmt.Fprintf(w, "    %d. %s\n", i+1, orPlaceholder(s, "—"))
	}
	fmt.Fprintf(w, "  expected: %s\n", orPlaceholder(d.Expected, "no expected behaviour"))
	fmt.Fprintf(w, "  observed: %s\n", orPlaceholder(d.Observed, "no observed behaviour"))
	fmt.Fprintf(w, "  “%s”\n", orPlaceholder(d.RationaleQuote, "no quote"))
}

// describe echoes a recorded decision to the operator's terminal. Its fields
// derive from an attacker-authorable draft id in a downloaded session, so each is
// passed through SafeText — matching printDraft and review.describe.
func describe(d Decision) string {
	return fmt.Sprintf("recorded: %s %s (%s)",
		session.SafeText(d.Test), session.SafeText(d.Decision), session.SafeText(d.At))
}

// orPlaceholder renders untrusted text for the terminal, falling back to a
// placeholder when it renders as nothing (empty, whitespace-only, or
// invisible-only Unicode) — the review.printFinding pattern, applied to every
// field of a draft because ParseRecords validates none of them.
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
