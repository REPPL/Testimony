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
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/REPPL/Testimony/internal/analyze"
	"github.com/REPPL/Testimony/internal/session"
)

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
	rec := analyze.Verdict{Kind: "verdict", Finding: opts.Finding, Verdict: verdict, Of: of, At: opts.Today}
	if err := AppendVerdict(opts.Dir, rec); err != nil {
		return err
	}
	fmt.Fprintln(opts.Out, describe(rec))
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
		return true, false, record(opts, analyze.Verdict{Kind: "verdict", Finding: f.ID, Verdict: "confirmed", At: opts.Today})
	case "r":
		return true, false, record(opts, analyze.Verdict{Kind: "verdict", Finding: f.ID, Verdict: "rejected", At: opts.Today})
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
		return true, false, record(opts, analyze.Verdict{Kind: "verdict", Finding: f.ID, Verdict: "duplicate", Of: target, At: opts.Today})
	case "s", "":
		fmt.Fprintln(opts.Out, "  skipped.")
		return true, false, nil
	case "q":
		return false, true, nil
	default:
		return false, false, fmt.Errorf("unrecognised choice %q", choice)
	}
}

func record(opts Options, rec analyze.Verdict) error {
	if err := AppendVerdict(opts.Dir, rec); err != nil {
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
func AppendVerdict(dir string, v analyze.Verdict) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, session.FindingsFile)
	// O_RDWR rather than O_WRONLY because the record cannot be framed correctly
	// without first reading the byte already at the end of the file.
	f, err := session.OpenFileNoFollow(path, os.O_APPEND|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	rec := append(b, '\n')
	// A findings.jsonl need not end in a newline: it may have been hand edited,
	// produced by another tool, or left short by a crash part-way through an
	// earlier write. Appending blindly would fuse the verdict onto that
	// unterminated final line, producing one physical line holding two JSON
	// objects — which makes not just those two records but the entire file
	// unparseable to every reader. So probe the last byte and open a fresh line
	// when it is not already one. (O_APPEND still puts the write at the end,
	// whatever the seek offset; the seek is only to learn the size.)
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		f.Close()
		return err
	}
	if end > 0 {
		var last [1]byte
		if _, err := f.ReadAt(last[:], end-1); err != nil {
			f.Close()
			return err
		}
		if last[0] != '\n' {
			rec = append([]byte{'\n'}, rec...)
		}
	}
	if _, err := f.Write(rec); err != nil {
		f.Close()
		return err
	}
	// Return the Close error so a verdict is never reported recorded when its
	// bytes did not reach disk (write-back deferred to close on NFS/full device).
	return f.Close()
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

func clock(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	s := int(sec + 0.5)
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}
