package coderefs

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/REPPL/Testimony/internal/analyze"
	"github.com/REPPL/Testimony/internal/drafttests"
	"github.com/REPPL/Testimony/internal/session"
	"github.com/REPPL/Testimony/internal/timeline"
)

// maxTitleRunes caps the derived issue title. A title is presentation, and an
// issue tracker truncates a long one anyway; the cap keeps the heading on one
// line whatever the quote's length.
const maxTitleRunes = 80

// Render returns one Markdown issue draft per mapped finding — a finding that
// at least one reference in refs.jsonl names — in finding-id order: a title
// derived from the finding, its anchor and clock, the participant's quote, the
// reproduction steps derived from the event window, and every reference for the
// finding with its current status, so a draft rendered before review is visibly
// unreviewed. Review does not gate the render.
//
// The title and the steps are derived by the CLI, not the model, because the
// render must work from the records alone and cannot invent a step. Steps the
// CLI derives are mechanical and may read flatly; that is the trade for a render
// that needs no second model round-trip.
//
// Nothing is written into the application's repository, and the render does
// not open it: it reads manifest.json, timeline.jsonl, findings.jsonl, and
// refs.jsonl only. The result is a hand-off artefact for the operator to file
// where they choose.
func Render(dir string) (string, error) {
	man, err := session.LoadManifest(dir)
	if err != nil {
		return "", err
	}
	findings, _, err := loadFindings(dir)
	if err != nil {
		return "", err
	}
	refs, decisions, err := loadRefs(dir)
	if err != nil {
		return "", err
	}
	entries, err := analyze.LoadTimeline(dir)
	if err != nil {
		return "", err
	}
	eff := EffectiveStatus(refs, decisions)

	// Group references by finding, in finding-id order, keeping each group's
	// references in id order. A reference whose finding is no longer in
	// findings.jsonl (a hand-edited session) has nothing to render under and is
	// skipped; a file with no renderable finding is refused rather than rendered
	// empty.
	byFinding := map[string][]Ref{}
	for _, r := range refs {
		byFinding[session.SafeText(r.Finding)] = append(byFinding[session.SafeText(r.Finding)], r)
	}
	var ids []string
	for id := range byFinding {
		if findingByID(findings, id) != nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return "", noMappedFindings(dir, refs, decisions)
	}
	sort.Strings(ids)

	var b strings.Builder
	fmt.Fprintf(&b, "# Issue drafts — %s\n\n", mdOrNone(man.Session))
	fmt.Fprintf(&b, "Drafted from mapped findings in session %s (app %s, participant %s). %d of %d references accepted.\n\n",
		codeOrNone(man.Session), codeOrNone(man.App), codeOrNone(man.Participant), countAccepted(refs, eff), len(refs))

	for k, id := range ids {
		f := *findingByID(findings, id)
		group := byFinding[id]
		sort.SliceStable(group, func(i, j int) bool { return group[i].ID < group[j].ID })

		fmt.Fprintf(&b, "## %s — %s\n\n", mdInline(f.ID), title(f))
		fmt.Fprintf(&b, "**Severity** %d · **Anchor** %s · **At** [%s]\n\n", f.Severity, anchorLine(f), clock(f.T))
		fmt.Fprintf(&b, "> “%s”\n> — %s, [%s]\n\n", mdOrPlaceholder(f.Quote, "no quote"), mdOrNone(man.Participant), clock(f.T))

		b.WriteString("### Steps to reproduce\n\n")
		for n, s := range steps(drafttests.Window(entries, f, DefaultWindow), f) {
			fmt.Fprintf(&b, "%d. %s\n", n+1, s)
		}
		b.WriteString("\n")

		b.WriteString("### Suspected files\n\n")
		var refIDs []string
		for _, r := range group {
			st := eff[r.ID]
			loc := session.SafeText(r.Path)
			if r.Line > 0 {
				loc = fmt.Sprintf("%s:%d", loc, r.Line)
			}
			fmt.Fprintf(&b, "- %s (%s) — %s\n", mdCode(loc), mdOrDash(r.Role), decisionPhrase(st))
			refIDs = append(refIDs, mdCode(r.ID))
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "Session %s · finding %s · references %s\n", codeOrNone(man.Session), mdCode(f.ID), strings.Join(refIDs, ", "))
		if k < len(ids)-1 {
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

func countAccepted(refs []Ref, eff map[string]Status) int {
	n := 0
	for _, r := range refs {
		if eff[r.ID].Value == "accepted" {
			n++
		}
	}
	return n
}

// decisionPhrase renders a reference's current status for the suspected-files
// list: "accepted 2026-09-16", "rejected 2026-09-16", or "proposed".
func decisionPhrase(st Status) string {
	if st.Value == "proposed" || strings.TrimSpace(session.SafeText(st.At)) == "" {
		return mdOrDash(st.Value)
	}
	return mdInline(st.Value) + " " + mdInline(st.At)
}

// title derives the issue title from the finding: its type and the first clause
// of its quote, capped at maxTitleRunes. It is presentation, derived by the CLI
// so the render works from the records alone.
func title(f analyze.Finding) string {
	typ := strings.TrimSpace(session.SafeText(f.Type))
	if typ == "" {
		typ = "finding"
	}
	// The first clause is the text up to the first separator, skipping any
	// separators the quote opens with so a quote like ".NET crashed" does not
	// empty the clause and drop the title to the bare type.
	clause := strings.TrimLeft(strings.TrimSpace(session.SafeText(f.Quote)), ".,;:!? ")
	if i := strings.IndexAny(clause, ".,;:!?"); i >= 0 {
		clause = strings.TrimSpace(clause[:i])
	}
	if clause == "" {
		return mdInline(typ)
	}
	t := typ + ": " + clause
	if r := []rune(t); len(r) > maxTitleRunes {
		t = strings.TrimRightFunc(string(r[:maxTitleRunes-1]), unicode.IsSpace) + "…"
	}
	return mdInline(t)
}

// anchorLine renders the finding's selector and route as code spans for the
// header line; eligibility at ingest guarantees at least one, but the render
// reads a file that may have been hand-edited, so an absent anchor falls back to
// a placeholder rather than a blank.
func anchorLine(f analyze.Finding) string {
	var parts []string
	if f.UI != nil {
		if !session.CodeRendersEmpty(f.UI.Selector) {
			parts = append(parts, mdCode(f.UI.Selector))
		}
		if !session.CodeRendersEmpty(f.UI.Route) {
			parts = append(parts, mdCode(f.UI.Route))
		}
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, " on ")
}

// steps derives the reproduction steps from the finding's event window: one
// orientation line from the first event's route, then one imperative line per
// event at or before the finding's t, naming the selector where the event
// carries one. Utterances are not steps; they are what the quote already
// carries. A window with no such event yields a single line saying so, rather
// than an empty list under a heading that promises steps.
func steps(window []timeline.Entry, f analyze.Finding) []string {
	var out []string
	oriented := false
	for _, e := range window {
		if e.Src != "event" || e.T > f.T {
			continue
		}
		route := payloadString(e, "route")
		if !oriented {
			oriented = true
			if !session.CodeRendersEmpty(route) {
				out = append(out, "Open "+mdCode(route)+".")
			}
		}
		out = append(out, describeEvent(e))
	}
	if len(out) == 0 {
		return []string{"(no interaction event precedes the finding in its event window)"}
	}
	return out
}

// describeEvent turns one interaction event into an imperative step. Every
// value comes from an attacker-authorable timeline, so each goes through the
// shared escape set: selectors and values in code spans, text inline.
func describeEvent(e timeline.Entry) string {
	kind := strings.TrimSpace(session.SafeText(payloadString(e, "kind")))
	selector := payloadString(e, "selector")
	text := payloadString(e, "text")
	value := payloadString(e, "value")
	target := ""
	switch {
	case !session.CodeRendersEmpty(selector):
		target = mdCode(selector)
	case strings.TrimSpace(session.SafeText(text)) != "":
		target = "“" + mdInline(text) + "”"
	}
	switch kind {
	case "click":
		if target == "" {
			return "Click."
		}
		return "Click " + target + "."
	case "input":
		if session.CodeRendersEmpty(value) {
			if target == "" {
				return "Edit the field."
			}
			return "Edit " + target + "."
		}
		if target == "" {
			return "Enter " + mdCode(value) + "."
		}
		return "Enter " + mdCode(value) + " in " + target + "."
	case "":
		kind = "interact with"
	}
	// Capitalise the first rune, not the first byte: kind is attacker-authorable
	// and a multi-byte first character sliced at kind[:1] would put invalid UTF-8
	// into the rendered draft after every sanitiser has run. The verb then goes
	// through the inline escape like every other value, so a kind of
	// `![x](http://h/b.png)` cannot become a live image in the pasted issue.
	first, size := utf8.DecodeRuneInString(kind)
	verb := mdInline(string(unicode.ToUpper(first)) + kind[size:])
	if target == "" {
		return verb + "."
	}
	return verb + " " + target + "."
}

func payloadString(e timeline.Entry, key string) string {
	if s, ok := e.Payload[key].(string); ok {
		return s
	}
	return ""
}

// mdInline neutralises the inline Markdown an attacker-authored record could
// otherwise smuggle into the rendered draft; the escape set lives in
// session.SafeInline, shared with every Markdown artefact.
func mdInline(s string) string { return session.SafeInline(s) }

// mdCode renders untrusted text inside a Markdown code span, where backslash
// escapes do not apply: backticks are stripped so the span cannot be closed
// early. This is report.mdCode's rule, applied for the same reason.
func mdCode(s string) string {
	return "`" + strings.ReplaceAll(session.SafeText(s), "`", "") + "`"
}

func codeOrNone(s string) string {
	if session.CodeRendersEmpty(s) {
		return "`(none)`"
	}
	return mdCode(s)
}

func mdOrNone(s string) string { return mdOrPlaceholder(s, "(none)") }

func mdOrDash(s string) string { return mdOrPlaceholder(s, "—") }

// mdOrPlaceholder decides presence on the rendered form, not the raw one.
func mdOrPlaceholder(s, placeholder string) string {
	if strings.TrimSpace(session.SafeText(s)) == "" {
		return placeholder
	}
	return mdInline(s)
}
