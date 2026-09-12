package drafttests

import (
	"sort"

	"github.com/REPPL/Testimony/internal/analyze"
	"github.com/REPPL/Testimony/internal/session"
	"github.com/REPPL/Testimony/internal/timeline"
)

// Window returns the timeline entries around f, in time order: every entry whose
// time falls in [lo, hi], where lo and hi span f's cited evidence entries
// widened by window on both sides.
//
// The reproduction steps are the expensive part of a regression test, and this
// window is the only thing the drafting model is allowed to reconstruct them
// from — so it must hold the lead-up and the aftermath, not merely the moment.
// lo is the earliest cited entry's start minus window; hi is the latest cited
// entry's end (an utterance's t1, via timeline.SpeechEnd) plus window. Evidence
// ids are matched in their session.SafeText form, the form the request shows and
// the form analyze already validates against, so an id carrying a stripped byte
// resolves here exactly as it does there.
//
// Speech and event entries are both included: the utterances around the moment
// are what carry the *expected* behaviour, and the events are what carry the
// steps. Bounds are inclusive.
//
// A finding whose evidence resolves to no entry at all — impossible after
// analyze -ingest, reachable via a hand-edited findings.jsonl — falls back to
// [f.T - window, f.T + window], so the finding still arrives with context rather
// than with nothing.
//
// A negative window is legitimate: it narrows the span (and can empty it), the
// same latitude report's join window allows. The result is not separately
// capped, because its size is bounded by timeline.jsonl, which already carries
// the session's total-size limit.
func Window(entries []timeline.Entry, f analyze.Finding, window float64) []timeline.Entry {
	cited := make(map[string]bool, len(f.Evidence))
	for _, id := range f.Evidence {
		if s := session.SafeText(id); s != "" {
			cited[s] = true
		}
	}

	var lo, hi float64
	found := false
	for _, e := range entries {
		if !cited[session.SafeText(e.ID)] {
			continue
		}
		end := timeline.SpeechEnd(e)
		if !found {
			lo, hi, found = e.T, end, true
			continue
		}
		if e.T < lo {
			lo = e.T
		}
		if end > hi {
			hi = end
		}
	}
	if !found {
		lo, hi = f.T, f.T
	}
	lo -= window
	hi += window

	var out []timeline.Entry
	for _, e := range entries {
		if e.T >= lo && e.T <= hi {
			out = append(out, e)
		}
	}
	// Ordered here rather than assumed of the caller: the window is presented to
	// the model as the sequence to reconstruct steps from, so an out-of-order
	// timeline.jsonl — which reaches these readers directly when a session is
	// hand-edited or exchanged — must not hand it a repro in the wrong order.
	// SliceStable with the same less function timeline.Merge uses leaves a
	// merge-produced timeline untouched.
	sort.SliceStable(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out
}
