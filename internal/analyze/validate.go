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
	for _, e := range entries {
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
		if end > idx.end {
			idx.end = end
		}
	}
	return idx
}

// validate runs every schema rule against the decoded findings and returns all
// errors (transactional and exhaustive), each naming the finding, the field,
// and the offending value.
func validate(findings []Finding, idx timelineIndex) []error {
	var errs []error
	seen := map[string]int{}

	for i, f := range findings {
		label := f.ID
		if !IsFindingID(label) {
			label = fmt.Sprintf("finding #%d", i+1)
			errs = append(errs, fmt.Errorf("%s: id %q must match ^F-\\d{3}$", label, f.ID))
		} else if prev, dup := seen[f.ID]; dup {
			errs = append(errs, fmt.Errorf("%s: duplicate id (first seen at finding #%d)", f.ID, prev))
		} else {
			seen[f.ID] = i + 1
		}

		if f.T < 0 || f.T > idx.end {
			errs = append(errs, fmt.Errorf("%s: t %g is outside the session [0, %g]", label, f.T, idx.end))
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
	return errors.Join(validate(findings, indexTimeline(entries))...)
}

func containsAny(texts []string, sub string) bool {
	for _, t := range texts {
		if strings.Contains(t, sub) {
			return true
		}
	}
	return false
}
