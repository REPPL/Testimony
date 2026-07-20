package analyze

import (
	"errors"
	"fmt"
	"strings"

	"github.com/REPPL/Testimony/internal/timeline"
)

// maxEvidence caps how many ids one finding may cite. A genuine finding anchors
// to a handful of utterances and events; the cap stops a hostile answer from
// smuggling a giant evidence array that (while every id is individually valid)
// serialises to a single findings.jsonl line larger than the downstream JSONL
// reader's per-line buffer, which would make the file — and re-ingest —
// permanently unreadable.
const maxEvidence = 64

// timelineIndex holds the derived facts a finding is validated against: the id
// set, the text of each spoken utterance (for verbatim quote matching), the
// event selector/route sets, and the session's end time.
type timelineIndex struct {
	ids       map[string]bool
	uttText   map[string]string
	selectors map[string]bool
	routes    map[string]bool
	start     float64
	end       float64
}

// indexTimeline builds a timelineIndex from merged timeline entries.
func indexTimeline(entries []timeline.Entry) timelineIndex {
	idx := timelineIndex{
		ids:       map[string]bool{},
		uttText:   map[string]string{},
		selectors: map[string]bool{},
		routes:    map[string]bool{},
	}
	for i, e := range entries {
		idx.ids[e.ID] = true
		end := e.T
		switch e.Src {
		case "speech":
			end = timeline.SpeechEnd(e)
			if s, ok := e.Payload["text"].(string); ok {
				idx.uttText[e.ID] = s
			}
		case "event":
			if s, ok := e.Payload["selector"].(string); ok && s != "" {
				idx.selectors[s] = true
			}
			if r, ok := e.Payload["route"].(string); ok && r != "" {
				idx.routes[r] = true
			}
		}
		// Seed idx.end on the first entry, exactly as idx.start is seeded below, so
		// the upper bound reflects the true maximum end even when every entry sits
		// at negative session-relative time (a recording predating t0, audio-only).
		// Growing from the zero value 0 would floor the end at 0 and admit a finding
		// anchored after the real (negative) session end.
		if i == 0 || end > idx.end {
			idx.end = end
		}
		// Track the earliest entry start so the finding-time lower bound matches
		// what the timeline can actually hold: an external recording whose
		// creation_time predates the manifest t0 yields a negative offset
		// (deriveOffset), so transcript.jsonl and the merged timeline legitimately
		// carry negative session-relative times. Hard-coding 0 as the floor would
		// reject a faithful finding anchored to such an utterance.
		if i == 0 || e.T < idx.start {
			idx.start = e.T
		}
	}
	return idx
}

// positioned pairs a decoded finding with at: its 1-based position in the
// answer the operator actually wrote. The two differ whenever an earlier
// element failed to decode, because Ingest drops those before validation; the
// pairing is what lets an error say "finding #3" and mean the third finding in
// the answer, which is the only index the operator can count to.
type positioned struct {
	finding Finding
	at      int
}

// atPositions pairs each finding with its own ordinal, for callers (Validate)
// whose findings did not come from a partially-decoded answer and so are
// already in answer order.
func atPositions(findings []Finding) []positioned {
	out := make([]positioned, len(findings))
	for i, f := range findings {
		out[i] = positioned{finding: f, at: i + 1}
	}
	return out
}

// validate runs every schema rule against the decoded findings and returns all
// errors (transactional and exhaustive), each naming the finding, the field,
// and the offending value. Positional labels come from each finding's recorded
// answer position, never from this loop's counter: an answer whose second
// element failed to decode would otherwise have its third element reported as
// "finding #2", sending the operator to edit the wrong one.
func validate(findings []positioned, idx timelineIndex) []error {
	var errs []error
	seen := map[string]int{}

	for _, p := range findings {
		f := p.finding
		label := f.ID
		if !IsFindingID(label) {
			label = fmt.Sprintf("finding #%d", p.at)
			errs = append(errs, fmt.Errorf("%s: id %q must match ^F-\\d{3}$", label, f.ID))
		} else if prev, dup := seen[f.ID]; dup {
			errs = append(errs, fmt.Errorf("%s: duplicate id (first seen at finding #%d)", f.ID, prev))
		} else {
			seen[f.ID] = p.at
		}

		// The floor is the earlier of 0 and the earliest entry start: a normal
		// session keeps 0 as the lower bound, while a session with negative-time
		// utterances (a recording predating t0) admits a finding anchored there.
		lo := 0.0
		if idx.start < lo {
			lo = idx.start
		}
		if f.T < lo || f.T > idx.end {
			errs = append(errs, fmt.Errorf("%s: t %g is outside the session [%g, %g]", label, f.T, lo, idx.end))
		}
		if !typeSet[f.Type] {
			errs = append(errs, fmt.Errorf("%s: type %q not in bug|friction|inconsistency|preference|idea", label, f.Type))
		}
		if f.Severity < 1 || f.Severity > 4 {
			errs = append(errs, fmt.Errorf("%s: severity %d out of range 1..4", label, f.Severity))
		}
		if f.Mode != "" && f.Mode != "A" && f.Mode != "B" {
			errs = append(errs, fmt.Errorf("%s: mode %q must be A or B", label, f.Mode))
		}

		// evidence: non-empty, every id real, at least one spoken (utt-*) anchor.
		hasUtt := false
		var uttTexts []string
		if len(f.Evidence) == 0 {
			errs = append(errs, fmt.Errorf("%s: evidence must be non-empty", label))
		}
		if len(f.Evidence) > maxEvidence {
			errs = append(errs, fmt.Errorf("%s: evidence lists %d ids, exceeding the limit of %d", label, len(f.Evidence), maxEvidence))
		}
		for _, id := range f.Evidence {
			if !idx.ids[id] {
				errs = append(errs, fmt.Errorf("%s: evidence id %q not found in the timeline", label, id))
				continue
			}
			if strings.HasPrefix(id, "utt-") {
				hasUtt = true
				if txt, ok := idx.uttText[id]; ok {
					uttTexts = append(uttTexts, txt)
				}
			}
		}
		if len(f.Evidence) > 0 && !hasUtt {
			errs = append(errs, fmt.Errorf("%s: evidence must include at least one utt-* (a spoken anchor)", label))
		}

		// quote: non-empty and a verbatim substring of one cited evidence
		// utterance's text (per-utterance, no normalisation).
		if f.Quote == "" {
			errs = append(errs, fmt.Errorf("%s: quote must be non-empty", label))
		} else if !containsAny(uttTexts, f.Quote) {
			errs = append(errs, fmt.Errorf("%s: quote is not a verbatim substring of any cited evidence utterance", label))
		}

		// ui selector/route, when present, must name a real event.
		if f.UI != nil {
			if f.UI.Selector != "" && !idx.selectors[f.UI.Selector] {
				errs = append(errs, fmt.Errorf("%s: ui.selector %q is not present on any timeline event", label, f.UI.Selector))
			}
			if f.UI.Route != "" && !idx.routes[f.UI.Route] {
				errs = append(errs, fmt.Errorf("%s: ui.route %q is not present on any timeline event", label, f.UI.Route))
			}
		}
	}
	return errs
}

// Validate reports all schema violations across the findings as one joined
// error (nil when clean). The findings are validated against the merged
// timeline in dir.
func Validate(dir string, findings []Finding) error {
	entries, err := loadTimeline(dir)
	if err != nil {
		return err
	}
	return errors.Join(validate(atPositions(findings), indexTimeline(entries))...)
}

func containsAny(texts []string, sub string) bool {
	for _, t := range texts {
		if strings.Contains(t, sub) {
			return true
		}
	}
	return false
}
