package analyze

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/REPPL/Testimony/internal/session"
	"github.com/REPPL/Testimony/internal/timeline"
)

// outputExample is the worked output-shape example embedded in the request. It
// is illustrative text, not validated data.
const outputExample = `{"rubric":"testimony-analysis/v1","findings":[
  {"id":"F-001","t":22.0,"type":"bug","severity":3,"mode":"A",
   "quote":"I clicked save and nothing happened",
   "evidence":["utt-004","ev-003","ev-004"],
   "ui":{"selector":"[data-testid=save-btn]","route":"#general"},
   "status":"unverified"}
]}`

// chunk splits the timeline into task-aligned chunks. v1 returns the whole
// timeline as a single chunk: timeline.jsonl carries no task-boundary markers
// and the manifest task list has no timestamps, so the mapping is not derivable
// from the data. This seam lets a future revision split at real task boundaries
// (a spoken marker or a task field on entries) with no change to the prompt
// contract. Flagged divergence from the note (§7).
func chunk(entries []timeline.Entry, _ session.Manifest) [][]timeline.Entry {
	return [][]timeline.Entry{entries}
}

// EmitRequest builds the single, self-contained analysis request for the
// session in dir: a versioned rubric, the session context, and the timeline,
// so that an agent given only this text can answer. Nothing in the session
// directory is mutated.
func EmitRequest(dir string) (string, error) {
	man, err := session.LoadManifest(dir)
	if err != nil {
		return "", err
	}
	entries, err := loadTimeline(dir)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Testimony analysis rubric: %s\n\n", RubricVersion)

	b.WriteString("## Stance\n\n")
	b.WriteString("This is a think-aloud usability analysis, and you are the second coder. " +
		"Every finding you draft is a *candidate*, born `unverified`; a human confirms or " +
		"rejects it afterwards. Analyse only the supplied text. Never invent evidence: do " +
		"not name an utterance, event, selector, or route that is not in the timeline below, " +
		"and never quote words the participant did not say.\n\n")

	b.WriteString("## Two passes\n\n")
	b.WriteString("**Pass 1 — segment coding.** Read the timeline in order. For each moment where " +
		"the participant expresses a defect, friction, inconsistency, preference, or idea, draft " +
		"a candidate finding with `type`, `severity`, a verbatim `quote`, `evidence` ids, and " +
		"`ui` when an on-screen referent is clear.\n\n")
	b.WriteString("**Pass 2 — session synthesis.** Deduplicate candidates that describe the same " +
		"underlying issue (keep one, cite the strongest evidence), assign the final `severity`, " +
		"note cross-task patterns, and attribute each finding to a task from the task list.\n\n")

	b.WriteString("## Rubric\n\n")
	b.WriteString("`type` is one of:\n\n")
	b.WriteString("- **bug** — the system behaves incorrectly or against the participant's reasonable expectation.\n")
	b.WriteString("- **friction** — the participant succeeds but with avoidable effort, hesitation, or doubt.\n")
	b.WriteString("- **inconsistency** — one part of the product contradicts another (behaviour, wording, or feel).\n")
	b.WriteString("- **preference** — the participant states a like or dislike that is not a defect.\n")
	b.WriteString("- **idea** — the participant proposes a change or improvement.\n\n")
	b.WriteString("`severity` is an integer on the usability-severity scale:\n\n")
	b.WriteString("- **1** cosmetic — **2** minor — **3** major — **4** blocker.\n\n")
	b.WriteString("For a `preference` or `idea`, the same integer expresses strength or priority rather than defect gravity.\n\n")
	b.WriteString("Hard constraints (each is enforced when your answer is ingested):\n\n")
	b.WriteString("- `quote` must be copied **verbatim** from the `text` of one of that finding's cited evidence utterances — byte for byte, one spoken moment, no paraphrase and no joining across utterances.\n")
	b.WriteString("- `evidence` ids must be real timeline ids, and at least one must be a spoken `utt-*` utterance.\n")
	b.WriteString("- `ui.selector` and `ui.route`, when present, must each come from an event in the timeline.\n")
	b.WriteString("- `t` is the moment of the finding, within the session; set it to the cited utterance's start time.\n")
	b.WriteString("- When the referent is verbal-only and ambiguous (\"this thing here\"), still cite the utterance, and set `ui` only if an event names the element (keyframe extraction is a later capability).\n\n")

	// The manifest is attacker-authorable — a session directory is an exchange
	// unit, so it may have been shared or downloaded — and the request is printed
	// to the operator's terminal before it is handed to an agent. Every
	// manifest-derived string therefore goes through session.SafeText, matching
	// report and review: without it an App, Participant, or task carrying ESC
	// drives ANSI sequences in the terminal, and one carrying a newline forges
	// Markdown structure (a fake "## " heading or extra rubric instructions) inside
	// the request the agent is asked to obey. The timeline block below needs the
	// same treatment for a narrower reason: json.Marshal escapes the C0 controls
	// and ESC, but it passes the Unicode Bidi_Control set through as raw bytes, so
	// an exchanged session's transcript or event text could still smuggle a
	// Trojan-Source reordering (CVE-2021-42574) into the request printed to the
	// operator's terminal — the exact spoofing SafeText strips on the report and
	// review paths. Each marshalled line is run through SafeText below; JSON's own
	// structural bytes are ASCII and pass through it unchanged.
	b.WriteString("## Session\n\n")
	fmt.Fprintf(&b, "- App: %s\n", session.SafeText(orNone(man.App)))
	fmt.Fprintf(&b, "- Participant: %s\n", session.SafeText(orNone(man.Participant)))
	if len(man.Tasks) > 0 {
		b.WriteString("- Tasks:\n")
		for i, t := range man.Tasks {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, session.SafeText(t))
		}
	} else {
		b.WriteString("- Tasks: (none recorded)\n")
	}
	b.WriteString("\n")

	b.WriteString("## Timeline\n\n")
	b.WriteString("Each line is one timeline entry (`utt-*` speech, `ev-*` event) on the session clock:\n\n")
	b.WriteString("```jsonl\n")
	for _, ch := range chunk(entries, man) {
		for _, e := range ch {
			line, err := json.Marshal(e)
			if err != nil {
				return "", err
			}
			b.WriteString(session.SafeText(string(line)))
			b.WriteByte('\n')
		}
	}
	b.WriteString("```\n\n")

	b.WriteString("## Answer\n\n")
	fmt.Fprintf(&b, "Answer with a single JSON document: `{\"rubric\":\"%s\",\"findings\":[ … ]}`. "+
		"A bare top-level array of findings is also accepted. Output JSON only, no prose.\n\n", RubricVersion)
	b.WriteString("```json\n")
	b.WriteString(outputExample)
	b.WriteString("\n```\n")

	return b.String(), nil
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
