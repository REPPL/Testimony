// Package report renders a merged timeline as a human-readable Markdown
// session report. The report is the raw aligned record; structured findings
// arrive with the analysis layer (architecture note §7).
package report

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/REPPL/Testimony/internal/analyze"
	"github.com/REPPL/Testimony/internal/session"
	"github.com/REPPL/Testimony/internal/timeline"
)

// Render reads manifest + timeline from dir and returns the Markdown report.
// window is the utterance↔event join window in seconds.
func Render(dir string, window float64) (string, error) {
	man, err := session.LoadManifest(dir)
	if err != nil {
		return "", err
	}
	entries, err := session.ReadJSONL[timeline.Entry](filepath.Join(dir, session.TimelineFile))
	if err != nil {
		return "", fmt.Errorf("read timeline (run `testimony merge` first?): %w", err)
	}

	var speech, events []timeline.Entry
	for _, e := range entries {
		switch e.Src {
		case "speech":
			speech = append(speech, e)
		case "event":
			events = append(events, e)
		}
	}

	// Attach each event to the first utterance whose window contains it.
	attached := map[string][]timeline.Entry{} // utterance ID → events
	used := map[string]bool{}
	for _, u := range speech {
		for _, id := range timeline.EventsNear(entries, u, window) {
			if used[id] {
				continue
			}
			used[id] = true
			for _, e := range events {
				if e.ID == id {
					attached[u.ID] = append(attached[u.ID], e)
				}
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Session report — %s\n\n", man.Session)
	fmt.Fprintf(&b, "**App:** %s · **Participant:** %s · **Duration:** %s · **Utterances:** %d · **Events:** %d\n\n",
		orDash(man.App), orDash(man.Participant), clock(end(entries)), len(speech), len(events))
	if len(man.Tasks) > 0 {
		fmt.Fprintf(&b, "**Tasks:** %s\n\n", strings.Join(man.Tasks, "; "))
	}
	b.WriteString("## Timeline\n\n")

	ei := 0 // index into events, for standalone (unattached) ones
	flushStandaloneBefore := func(t float64) {
		for ei < len(events) && events[ei].T < t {
			if !used[events[ei].ID] {
				fmt.Fprintf(&b, "- [%s] %s\n", clock(events[ei].T), eventLine(events[ei]))
			}
			ei++
		}
	}

	for _, u := range speech {
		flushStandaloneBefore(u.T)
		fmt.Fprintf(&b, "\n**[%s] %s:** “%s”\n", clock(u.T), speaker(u), text(u))
		for _, e := range attached[u.ID] {
			fmt.Fprintf(&b, "  - [%s] %s\n", clock(e.T), eventLine(e))
		}
	}
	flushStandaloneBefore(1e18)

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
		fmt.Fprintf(b, "_Findings unavailable: %v_\n", err)
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
			fmt.Fprintf(b, "- **%s** %s · severity %d · [%s] — “%s” — %s",
				f.ID, f.Type, f.Severity, clock(f.T), f.Quote, findingAnchor(f))
			if st := eff[f.ID]; st.At != "" {
				if st.Of != "" {
					fmt.Fprintf(b, " · %s of %s (%s)", st.Value, st.Of, st.At)
				} else {
					fmt.Fprintf(b, " · %s (%s)", st.Value, st.At)
				}
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
}

// findingAnchor renders a finding's on-screen anchor: the ui selector (in
// backticks) and route when present, else the evidence ids.
func findingAnchor(f analyze.Finding) string {
	if f.UI != nil && (f.UI.Selector != "" || f.UI.Route != "") {
		var parts []string
		if f.UI.Selector != "" {
			parts = append(parts, "`"+f.UI.Selector+"`")
		}
		if f.UI.Route != "" {
			parts = append(parts, f.UI.Route)
		}
		return strings.Join(parts, " ")
	}
	return "evidence " + strings.Join(f.Evidence, ", ")
}

func end(entries []timeline.Entry) float64 {
	var max float64
	for _, e := range entries {
		t := e.T
		if e.Src == "speech" {
			t = timeline.SpeechEnd(e)
		}
		if t > max {
			max = t
		}
	}
	return max
}

func clock(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	s := int(sec + 0.5)
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

func speaker(u timeline.Entry) string {
	if s, ok := u.Payload["speaker"].(string); ok && s != "" {
		return s
	}
	return "P?"
}

func text(u timeline.Entry) string {
	if s, ok := u.Payload["text"].(string); ok {
		return s
	}
	return ""
}

func eventLine(e timeline.Entry) string {
	get := func(k string) string {
		if s, ok := e.Payload[k].(string); ok {
			return s
		}
		return ""
	}
	parts := []string{get("kind")}
	if sel := get("selector"); sel != "" {
		parts = append(parts, "`"+sel+"`")
	}
	if t := get("text"); t != "" {
		parts = append(parts, fmt.Sprintf("%q", t))
	}
	if v := get("value"); v != "" {
		parts = append(parts, fmt.Sprintf("value=%q", v))
	}
	if r := get("route"); r != "" {
		parts = append(parts, "("+r+")")
	}
	return strings.Join(parts, " ")
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
