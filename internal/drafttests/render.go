package drafttests

import (
	"fmt"
	"sort"
	"strings"

	"github.com/REPPL/Testimony/internal/session"
)

// Render returns the accepted test plan as Markdown: one test-case block per
// draft whose effective status is "accepted" or "edited", in id order, with the
// winning edit applied over the draft.
//
// "proposed" and "rejected" drafts are omitted — a proposal is not a test, and a
// rejected draft is retained in tests.jsonl for the record, not for the plan. A
// plan with no block in it is refused rather than returned, so `-out FILE` cannot
// truncate an existing test plan into an empty document.
//
// The result is a hand-off artefact: it is never written into the session
// directory unless the operator names a path there, because where a
// docs-as-code test plan lives is their repository's business, not this tool's.
func Render(dir string) (string, error) {
	man, err := session.LoadManifest(dir)
	if err != nil {
		return "", err
	}
	findings, _, err := loadFindings(dir)
	if err != nil {
		return "", err
	}
	drafts, decisions, err := loadDrafts(dir)
	if err != nil {
		return "", err
	}
	eff := EffectiveStatus(drafts, decisions)

	// One entry per rendered block: the draft with its winning edit applied, and
	// the decision that admitted it. The edit is applied to a copy here; the draft
	// line on disk is untouched, which is what keeps its link fields unreachable.
	type block struct {
		draft  Draft
		status Status
	}
	var plan []block
	for _, d := range drafts {
		st := eff[d.ID]
		if st.Value != "accepted" && st.Value != "edited" {
			continue
		}
		plan = append(plan, block{draft: st.Edit.Apply(d), status: st})
	}
	if len(plan) == 0 {
		return "", noAcceptedDrafts(dir, drafts, decisions)
	}
	sort.SliceStable(plan, func(i, j int) bool { return plan[i].draft.ID < plan[j].draft.ID })

	// Every inserted value is untrusted (a draft in an exchanged session is
	// attacker-authorable) and this document is one the operator pastes into their
	// own repository, so each goes through the shared escape set: mdInline
	// (session.SafeInline) wherever the value renders as prose, and mdCode inside
	// a code span, where a backslash escape does not apply and a stray backtick
	// would close the span early and let the tail render as active markup.
	var b strings.Builder
	fmt.Fprintf(&b, "# Regression tests — %s\n\n", mdOrNone(man.Session))
	fmt.Fprintf(&b, "Drafted from confirmed findings in session %s (app %s, participant %s). %d of %d drafts accepted.\n\n",
		codeOrNone(man.Session), codeOrNone(man.App), codeOrNone(man.Participant), len(plan), len(drafts))

	for k, blk := range plan {
		d, st := blk.draft, blk.status
		f := findingByID(findings, d.Finding)
		typ, at := "—", "--:--"
		if f != nil {
			typ, at = mdOrDash(f.Type), clock(f.T)
		}
		fmt.Fprintf(&b, "## %s — %s\n\n", mdInline(d.ID), mdOrPlaceholder(d.Title, "no title"))
		fmt.Fprintf(&b, "- **Source:** finding %s (%s, severity %d) in session %s, at [%s]\n",
			mdCode(d.Finding), typ, d.Severity, codeOrNone(d.Session), at)
		fmt.Fprintf(&b, "- **Decision:** %s (%s)\n\n", mdOrDash(st.Value), mdOrDash(st.At))

		b.WriteString("**Steps**\n\n")
		for n, s := range d.Steps {
			fmt.Fprintf(&b, "%d. %s\n", n+1, mdOrPlaceholder(s, "—"))
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "**Expected:** %s\n\n", mdOrPlaceholder(d.Expected, "no expected behaviour"))
		fmt.Fprintf(&b, "**Observed:** %s\n\n", mdOrPlaceholder(d.Observed, "no observed behaviour"))
		fmt.Fprintf(&b, "**Rationale (participant, [%s]):** “%s”\n", at, mdOrPlaceholder(d.RationaleQuote, "no quote"))
		if k < len(plan)-1 {
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

// mdInline neutralises the inline Markdown an attacker-authored draft could
// otherwise smuggle into the rendered plan. The escape set lives in
// session.SafeInline, shared with report.md and the emitted requests, so the
// Markdown artefacts built from untrusted text cannot drift.
func mdInline(s string) string { return session.SafeInline(s) }

// mdCode renders untrusted text inside a Markdown code span, where backslash
// escapes do not apply. session.SafeText leaves the backtick that would close the
// span early and let the tail render as active markup, so backticks are stripped
// from the span content (a real session name, app, or finding id never carries
// one); everything else is literal inside the span and needs no escaping. This is
// report.mdCode's rule, applied for the same reason.
func mdCode(s string) string {
	return "`" + strings.ReplaceAll(session.SafeText(s), "`", "") + "`"
}

// codeOrNone is mdCode with a "(none)" fallback for a value that renders as
// nothing: a manifest field is operator-supplied and unvalidated, so a
// whitespace-only or invisible-only one must not print as an empty code span.
func codeOrNone(s string) string {
	if strings.TrimSpace(strings.ReplaceAll(session.SafeText(s), "`", "")) == "" {
		return "`(none)`"
	}
	return mdCode(s)
}

func mdOrNone(s string) string { return mdOrPlaceholder(s, "(none)") }

func mdOrDash(s string) string { return mdOrPlaceholder(s, "—") }

// mdOrPlaceholder decides presence on the rendered form, not the raw one: a
// value that is non-empty raw but renders to nothing or to whitespace only must
// fall through to the placeholder rather than leave a blank where the plan
// promises content.
func mdOrPlaceholder(s, placeholder string) string {
	if strings.TrimSpace(session.SafeText(s)) == "" {
		return placeholder
	}
	return mdInline(s)
}
