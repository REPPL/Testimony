// Package report renders a merged timeline as a human-readable Markdown
// session report. The report is the raw aligned record; structured findings
// arrive with the analysis layer (architecture note §7).
package report

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path/filepath"
	"sort"
	"strings"

	"github.com/REPPL/Testimony/internal/analyze"
	"github.com/REPPL/Testimony/internal/session"
	"github.com/REPPL/Testimony/internal/timeline"
)

// maxClockSeconds bounds a value clock will format. A real session stamp is
// minutes to hours; 1e9 seconds (~31 years) is far past any genuine one while
// staying well inside int64 so the float64→int conversion below can never go
// out of range. timeline.jsonl and findings.jsonl are attacker-authorable and
// reach clock without passing timeline.checkedUtterances, so this sink must
// defend itself rather than trust its input.
const maxClockSeconds = 1e9

// Render reads manifest + timeline from dir and returns the Markdown report.
// window is the utterance↔event join window in seconds.
func Render(dir string, window float64) (string, error) {
	man, err := session.LoadManifest(dir)
	if err != nil {
		return "", err
	}
	entries, err := timeline.ReadEntries(filepath.Join(dir, session.TimelineFile))
	if err != nil {
		return "", fmt.Errorf("read timeline (run `testimony merge` first?): %w", err)
	}
	// Refuse an entry this renderer cannot place. The src switch below buckets
	// into speech and events with no default, so an unknown src (reachable only
	// from a hand-edited or exchanged timeline) previously vanished from the
	// rendered timeline while end() still counted its time into the Duration
	// header — the report asserted a session span its own timeline never
	// reached, and the entry's id stayed citable by findings. Checked before
	// the sort so the named entry ordinal reflects the file's record order.
	if err := timeline.CheckSrc(entries); err != nil {
		return "", err
	}
	// Order the record by time rather than trusting the file's line order. The
	// timeline this reads is not necessarily the one merge wrote: a hand-edited or
	// exchanged timeline.jsonl reaches Render directly — the same assumption the
	// join step below already refuses to make about ids — and an out-of-order one
	// rendered [00:50] above [00:10], so report.md, which is read as the
	// chronological record of the session, silently misstated when things happened.
	// The standalone-event flush below also walks events forward on the assumption
	// they ascend. sort.SliceStable with the same less function timeline.Merge uses
	// keeps the two orderings identical, so a merge-produced timeline is unchanged.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].T < entries[j].T })

	var speech, events []timeline.Entry
	for _, e := range entries {
		switch e.Src {
		case "speech":
			speech = append(speech, e)
		case "event":
			events = append(events, e)
		}
	}

	// Attach each event to the first utterance whose window contains it. Both the
	// buckets and the used[] dedup key on POSITION, never on ID: timeline.Merge
	// copies a transcript's id and never validates it, and report reads
	// timeline.jsonl directly — an exchanged or hand-edited one bypasses merge's
	// ev-%03d synthesis entirely — so several utterances or several events can share
	// one id. An ID-keyed utterance bucket collapses same-id utterances into one, and
	// an ID-keyed event lookup (the pre-fix inner `for e := range events { if e.ID ==
	// id }`) attaches EVERY event sharing a matched id — including ones outside the
	// window — to that utterance, then the id-keyed standalone dedup hides them.
	// report.md is the human evidence artefact, so both fabricate the record of what
	// the participant was doing while they spoke. Indexing by position on both sides
	// removes the dependence on ID uniqueness; the window test is inlined here (it is
	// timeline.EventsNear's body) so events is scanned by index rather than by id.
	attached := make([][]timeline.Entry, len(speech)) // utterance index → events
	usedEvent := make([]bool, len(events))            // event index → already attached
	for i, u := range speech {
		lo := u.T - window
		hi := timeline.SpeechEnd(u) + window
		for j, e := range events {
			if usedEvent[j] {
				continue
			}
			if e.T >= lo && e.T <= hi {
				usedEvent[j] = true
				attached[i] = append(attached[i], e)
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Session report — %s\n\n", mdOrDash(man.Session))
	fmt.Fprintf(&b, "**App:** %s · **Participant:** %s · **Duration:** %s · **Utterances:** %d · **Events:** %d\n\n",
		mdOrDash(man.App), mdOrDash(man.Participant), clock(end(entries)), len(speech), len(events))
	// Presence is decided per task on its rendered form, not raw emptiness
	// (the findingAnchor/eventLine pattern): a manifest's tasks are
	// operator-supplied and unvalidated by session.SaveManifest, so a
	// whitespace-only or invisible-only-Unicode entry must not render as a
	// bare "; " separator with nothing either side.
	var tasks []string
	for _, task := range man.Tasks {
		if inlineRendersEmpty(task) {
			continue
		}
		tasks = append(tasks, mdInline(task))
	}
	if len(tasks) > 0 {
		fmt.Fprintf(&b, "**Tasks:** %s\n\n", strings.Join(tasks, "; "))
	}
	b.WriteString("## Timeline\n\n")

	ei := 0 // index into events, for standalone (unattached) ones
	flushStandaloneBefore := func(t float64) {
		for ei < len(events) && events[ei].T < t {
			if !usedEvent[ei] {
				fmt.Fprintf(&b, "- [%s] %s\n", clock(events[ei].T), eventLine(events[ei]))
			}
			ei++
		}
	}

	for i, u := range speech {
		flushStandaloneBefore(u.T)
		fmt.Fprintf(&b, "\n**[%s] %s:** “%s”\n", clock(u.T), speaker(u), text(u))
		for _, e := range attached[i] {
			fmt.Fprintf(&b, "  - [%s] %s\n", clock(e.T), eventLine(e))
		}
	}
	// Flush every remaining standalone event. The sentinel is +Inf, not a finite
	// literal: an earlier 1e18 bound silently dropped any event whose t was at or
	// past it (reachable from a hand-authored timeline.jsonl), omitting evidence
	// from the report while merge and report both exited 0. Every JSON-decodable t
	// is finite and so strictly less than +Inf, so this flushes all of them.
	flushStandaloneBefore(math.Inf(1))

	b.WriteString("\n## Findings\n\n")
	renderFindings(&b, dir)
	return b.String(), nil
}

// renderFindings appends the Findings section, grouping findings.jsonl by
// effective status. When no findings file exists it leaves a short, non-fatal
// notice. Report reads only derived text; it never touches media.
func renderFindings(b *strings.Builder, dir string) {
	findings, verdicts, err := analyze.Load(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			b.WriteString("_No findings yet — run `testimony analyze` then `testimony review`._\n")
			return
		}
		// The raw error carries the joined filesystem path (an absolute one when the
		// operator passed an absolute -session), which on macOS embeds the username,
		// and it is the one string in this function not passed through SafeText. The
		// report is the human-facing artefact a session directory is built to share,
		// so leaking that path — or unescaped control bytes from a malformed line —
		// into it is an info-disclosure the generic notice avoids; the detailed error
		// still surfaces on stderr when the operator re-runs analyze or review.
		b.WriteString("_Findings unavailable: findings.jsonl could not be read (run `testimony analyze`/`review` to see why)._\n")
		return
	}

	eff := analyze.EffectiveStatus(findings, verdicts)
	byStatus := map[string][]analyze.Finding{}
	for _, f := range findings {
		s := eff[f.ID].Value
		byStatus[s] = append(byStatus[s], f)
	}

	groups := []struct{ key, heading string }{
		{"confirmed", "Confirmed"},
		{"unverified", "Unverified"},
		{"duplicate", "Duplicate"},
		{"rejected", "Rejected"},
	}
	for _, g := range groups {
		group := byStatus[g.key]
		sort.Slice(group, func(i, j int) bool { return group[i].ID < group[j].ID })
		fmt.Fprintf(b, "### %s (%d)\n\n", g.heading, len(group))
		if len(group) == 0 {
			b.WriteString("_None._\n\n")
			continue
		}
		for _, f := range group {
			// type and quote are decided on the rendered form too
			// (mdOrDash/inlineRendersEmpty): analyze.Load validates neither, so a
			// hand-edited or exchanged findings.jsonl reaches this sink directly,
			// the same threat model every other field on this line already
			// defends against.
			quote := f.Quote
			if inlineRendersEmpty(quote) {
				quote = "no quote"
			} else {
				quote = mdInline(quote)
			}
			fmt.Fprintf(b, "- **%s** %s · severity %d · [%s] — “%s” — %s",
				mdInline(f.ID), mdOrDash(f.Type), f.Severity, clock(f.T), quote, findingAnchor(f))
			// Presence is decided on the rendered form, not the raw one
			// (inlineRendersEmpty): an at/of that is non-empty raw but renders
			// to nothing or to whitespace only must be treated as absent
			// rather than render a blank or dangling verdict suffix.
			if st := eff[f.ID]; !inlineRendersEmpty(st.At) {
				if !inlineRendersEmpty(st.Of) {
					fmt.Fprintf(b, " · %s of %s (%s)", mdInline(st.Value), mdInline(st.Of), mdInline(st.At))
				} else {
					fmt.Fprintf(b, " · %s (%s)", mdInline(st.Value), mdInline(st.At))
				}
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
}

// findingAnchor renders a finding's on-screen anchor: the ui selector (in
// backticks) and route when present, else the evidence ids.
//
// Presence is decided on the rendered form, not the raw one (codeRendersEmpty/
// inlineRendersEmpty): a selector or route that is non-empty raw but renders
// to nothing or to whitespace only (invisible-only Unicode, a selector of
// backticks alone, or literal whitespace) must fall through to the evidence
// ids rather than render a blank or whitespace-only anchor with no fallback.
// The evidence ids themselves are filtered the same way: an id that renders
// to nothing is dropped, and an evidence list with no renderable id left
// falls back to "no evidence" rather than the dangling "evidence " label.
func findingAnchor(f analyze.Finding) string {
	if f.UI != nil {
		var parts []string
		if !codeRendersEmpty(f.UI.Selector) {
			parts = append(parts, mdCode(f.UI.Selector))
		}
		if !inlineRendersEmpty(f.UI.Route) {
			parts = append(parts, mdInline(f.UI.Route))
		}
		if len(parts) > 0 {
			return strings.Join(parts, " ")
		}
	}
	var ids []string
	for _, id := range f.Evidence {
		if !inlineRendersEmpty(id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return "no evidence"
	}
	return "evidence " + mdInline(strings.Join(ids, ", "))
}

func end(entries []timeline.Entry) float64 {
	var max float64
	for i, e := range entries {
		t := e.T
		if e.Src == "speech" {
			t = timeline.SpeechEnd(e)
		}
		// Seed the maximum from the first entry, exactly as analyze.indexTimeline
		// seeds idx.end, rather than growing it from the zero value: a session whose
		// recording predates the manifest t0 has every entry at a negative
		// session-relative time, and a zero-seeded maximum would report that
		// session's span as 00:00 instead of where it truly ends.
		if i == 0 || t > max {
			max = t
		}
	}
	return max
}

// clock renders a session-relative time as a signed MM:SS stamp. Negative times
// are legitimate and deliberately supported — an external recording whose
// creation_time predates the manifest t0 yields a negative offset, so
// transcript, timeline and findings can all carry pre-t0 anchors — and the
// earlier clamp to zero made every one of them print as [00:00], so report.md
// silently misstated when they happened. The sign is split off before the
// arithmetic because %02d over a negative integer division renders garbage such
// as "-1:-30".
func clock(sec float64) string {
	// Defend the float64→int conversion below. A non-finite or astronomically
	// large sec — reachable from a hand-authored timeline.jsonl or findings.jsonl,
	// which bypass timeline.checkedUtterances — makes int(sec+0.5) an out-of-range
	// conversion the Go spec leaves implementation-defined: on arm64 it saturates
	// to MaxInt64 and prints a nonsensical duration like "153722867280912930:07";
	// on amd64 it wraps negative, defeating the sign handling below. Such a value
	// is not a time, so render a visibly-broken placeholder rather than fabricate a
	// precise-looking wrong stamp in the human evidence artefact.
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

// mdInline neutralises the inline Markdown an attacker-authored string could
// otherwise smuggle into report.md, the shareable evidence artefact. session.SafeText
// already strips the C0/C1/bidi bytes and — decisively — the newlines that could forge
// block structure (a heading, a list item), so a sanitised string can never begin a
// line and only INLINE constructs remain reachable; SafeText passes their triggers
// (`\ ` * _ [ ] ( ) ! < > ~`, backtick included) through untouched. Without this an
// event or finding text of `![x](http://host/beacon.png)` renders a live remote image
// — a tracking/exfil beacon fired the instant the shared report is opened in any
// Markdown viewer — and `[label](http://host)` an active link disguised as evidence.
// Backslash-escaping each trigger renders it as literal text in a viewer and keeps it
// readable in source. Ordinary transcript, selector, and route text carries none of
// these bytes, so the report of a normal session is byte-for-byte unchanged.
// The escape set lives in session.SafeInline, shared with the emitted analysis
// request, so the two Markdown artefacts built from untrusted text cannot drift.
func mdInline(s string) string {
	return session.SafeInline(s)
}

// mdCode renders untrusted text inside a Markdown code span, where backslash escapes
// do not apply. session.SafeText leaves the backtick that would close the span early
// and let the tail render as active markup, so backticks are stripped from the span
// content (a real CSS selector or route never carries one); everything else is literal
// inside the span and needs no escaping.
func mdCode(s string) string {
	return "`" + strings.ReplaceAll(session.SafeText(s), "`", "") + "`"
}

// codeRendersEmpty reports whether mdCode(s) would carry no meaningful
// content — s reduces to nothing but whitespace once SafeText and backtick
// removal are applied (invisible-only Unicode, backticks alone, or literal
// whitespace, e.g. a lone tab, which SafeText maps to a space). A caller
// deciding whether to show a code span at all, rather than fall back to
// something more informative, must judge presence on this rendered form —
// judging it on s's raw emptiness lets a value that renders as nothing (or
// as invisible whitespace) through as if it were real content.
func codeRendersEmpty(s string) bool {
	return strings.TrimSpace(strings.ReplaceAll(session.SafeText(s), "`", "")) == ""
}

// inlineRendersEmpty is codeRendersEmpty's mdInline sibling: mdInline escapes
// a backtick rather than stripping it, so a lone backtick is meaningful,
// visible content there, unlike inside a code span.
func inlineRendersEmpty(s string) bool {
	return strings.TrimSpace(session.SafeText(s)) == ""
}

// Presence is decided on the rendered form, not the raw one
// (inlineRendersEmpty): a speaker that is non-empty raw but renders to
// nothing or to whitespace only (invisible-only Unicode, or literal
// whitespace) must fall through to the "P?" placeholder rather than render
// a blank speaker label with no fallback.
func speaker(u timeline.Entry) string {
	if s, ok := u.Payload["speaker"].(string); ok && !inlineRendersEmpty(s) {
		return mdInline(s)
	}
	return "P?"
}

// Presence is decided on the rendered form, not the raw one
// (inlineRendersEmpty), matching speaker above: timeline.checkedUtterances
// never validates Text, so a hand-edited or exchanged timeline.jsonl — or a
// transcript segment that is non-empty raw but invisible-only Unicode, which
// transcribe's own whitespace-only drop does not catch — must fall through
// to a placeholder rather than render a blank quotation attributed to a real
// speaker and timestamp.
func text(u timeline.Entry) string {
	if s, ok := u.Payload["text"].(string); ok && !inlineRendersEmpty(s) {
		return mdInline(s)
	}
	return "no words"
}

// eventLine renders one event. Every payload string is untrusted — an event's
// kind/selector/route/text/value come from the unauthenticated capture endpoint
// (or an attacker-authored timeline in a downloaded session) — so each is routed
// through mdInline (plain sinks) or mdCode (the selector code span). Both apply
// session.SafeText, stripping the control bytes that forge report structure or
// inject ANSI, and additionally neutralise the inline Markdown (an image beacon,
// an active link) that SafeText alone leaves intact.
func eventLine(e timeline.Entry) string {
	raw := func(k string) string {
		if s, ok := e.Payload[k].(string); ok {
			return s
		}
		return ""
	}
	// kind is decided on its rendered form too, like every field below it
	// (inlineRendersEmpty): timeline.checkInteraction only requires the raw
	// bytes be non-empty, so an interaction whose kind is invisible-only
	// Unicode passes validation and must not render as a silent gap where
	// the event name belongs.
	parts := []string{mdOrDash(raw("kind"))}
	// Every field below is decided on its rendered form, not its raw one
	// (codeRendersEmpty/inlineRendersEmpty): a field that is non-empty raw but
	// renders to nothing or to whitespace only (invisible-only Unicode,
	// backticks alone in a code span, or literal whitespace) must be omitted
	// rather than appended as an empty or blank fragment.
	if sel := raw("selector"); !codeRendersEmpty(sel) {
		parts = append(parts, mdCode(sel))
	}
	if t := raw("text"); !inlineRendersEmpty(t) {
		parts = append(parts, `"`+mdInline(t)+`"`)
	}
	if v := raw("value"); !inlineRendersEmpty(v) {
		parts = append(parts, `value="`+mdInline(v)+`"`)
	}
	if r := raw("route"); !inlineRendersEmpty(r) {
		parts = append(parts, "("+mdInline(r)+")")
	}
	return orDash(strings.Join(parts, " "))
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// mdOrDash renders s inline, falling back to "—" when it is empty or renders
// to nothing (inlineRendersEmpty): manifest fields like App/Participant are
// operator-supplied and unvalidated by session.SaveManifest, so a
// whitespace-only or invisible-only value must not reach mdInline and reduce
// to a blank field with the dash placeholder skipped.
func mdOrDash(s string) string {
	if inlineRendersEmpty(s) {
		return "—"
	}
	return mdInline(s)
}
