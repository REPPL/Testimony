package drafttests

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/REPPL/Testimony/internal/analyze"
	"github.com/REPPL/Testimony/internal/session"
)

// outputExample is the worked output-shape example embedded in the request. It
// is illustrative text, not validated data.
const outputExample = `{"rubric":"testimony-testdraft/v1","tests":[
  {"id":"T-001","finding":"F-001","session":"sample-session",
   "title":"Saving gives no confirmation",
   "steps":["Open #general in the settings prototype.",
            "Change the display name to Alice.",
            "Click the Save button ([data-testid=save-btn])."],
   "expected":"The save is confirmed on screen — a toast, or the button briefly disabled.",
   "observed":"Nothing visibly changes, so there is no way to tell the save landed.",
   "rationale_quote":"I clicked save and nothing happened",
   "severity":3,"status":"proposed"}
]}`

// EmitRequest builds the single, self-contained regression-test drafting request
// for the session in dir: a versioned rubric, the session context, and, for each
// confirmed finding, that finding's own record plus its event window, so that an
// agent given only this text can answer. window is the event-window half-width in
// seconds. Nothing in the session directory is mutated.
//
// A session with no confirmed finding is staged loudly — the refusal names the
// finding count by status and nothing is emitted — because eligibility is the
// whole point of the step: only evidence a human already vouched for can become
// a test.
func EmitRequest(dir string, window float64) (string, error) {
	man, err := session.LoadManifest(dir)
	if err != nil {
		return "", err
	}
	findings, verdicts, err := loadFindings(dir)
	if err != nil {
		return "", err
	}
	// Eligibility is checked before the timeline is read, so a session with
	// nothing to draft from hears why it is empty rather than being sent to run
	// merge for a request it could not fill either way.
	confirmed := eligible(findings, verdicts)
	if len(confirmed) == 0 {
		return "", noConfirmedFindings(dir, findings, verdicts)
	}
	// analyze.LoadTimeline, not a local reader: the event window must not be built
	// over a timeline whose entry ids are ambiguous or whose src this pipeline
	// cannot place, and the "run merge first" hint is the one an operator without
	// a merged timeline needs.
	entries, err := analyze.LoadTimeline(dir)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Testimony regression-test drafting rubric: %s\n\n", RubricVersion)

	b.WriteString("## Stance\n\n")
	b.WriteString("Every test case you draft is a *proposal*, born `proposed`; a human accepts, " +
		"edits, or rejects it afterwards. Only the confirmed findings below are eligible — a " +
		"human has already vouched for each one. Reconstruct the reproduction steps **only** " +
		"from the event window supplied with each finding: never invent a step, a selector, or " +
		"a route that is not there. Never alter the quote, the severity, or the session.\n\n")

	b.WriteString("## Instructions\n\n")
	b.WriteString("Draft one or more test cases per confirmed finding, in finding-id order. For each draft:\n\n")
	b.WriteString("- **`steps`** — the reproduction, in time order. Each entry is one imperative " +
		"action a developer can follow, naming the selector or the route where the window names " +
		"it. The sequence **ends at the last cited evidence event at or before the finding's `t`**: " +
		"that event is the action the finding is anchored to. A cited evidence event *after* the " +
		"finding's `t` is not a step — it is part of what the participant did in response, so it " +
		"belongs in `observed`.\n")
	b.WriteString("- The `route` on the window's first event names where the participant already " +
		"is when the window opens. You may derive **one** opening orientation step from it (for " +
		"example \"Open #general\"); every other step must correspond to an event or an utterance " +
		"that is actually in the window.\n")
	b.WriteString("- **`expected`** — the behaviour the participant expected, grounded in their own utterances in the window.\n")
	b.WriteString("- **`observed`** — what the system actually did, grounded in the window's events and utterances.\n")
	b.WriteString("- **`title`** — one line naming the defect.\n\n")

	b.WriteString("## Rubric\n\n")
	b.WriteString("Field definitions:\n\n")
	b.WriteString("- `id` — `T-NNN`, zero-padded and unique within your answer.\n")
	b.WriteString("- `finding` — the id of the confirmed finding this draft came from.\n")
	b.WriteString("- `session` — the session this draft belongs to, copied unchanged from the session context below.\n")
	b.WriteString("- `title` — one line, at most 200 characters.\n")
	b.WriteString("- `steps` — a non-empty array of at most 32 non-empty strings, in time order.\n")
	b.WriteString("- `expected`, `observed` — non-empty prose.\n")
	b.WriteString("- `rationale_quote` — the finding's own `quote`, copied byte for byte.\n")
	b.WriteString("- `severity` — the finding's own `severity`, copied unchanged.\n\n")
	b.WriteString("Reading each finding record below — two of its fields are about the record, not about your draft:\n\n")
	b.WriteString("- `status` is the finding's **birth state**, and it reads `unverified` on every " +
		"finding this tool writes: a finding is born a candidate. It is *not* the finding's current " +
		"status. The human verdict records that confirmed these findings live alongside them and are " +
		"not shown here; every finding below is confirmed, and its header names the date the verdict " +
		"was recorded.\n")
	b.WriteString("- `mode` is the capture mode: `A` is the application under test, `B` is reference " +
		"capture of a third-party app. Only mode `A` findings are eligible, so every finding below is mode `A`.\n\n")
	b.WriteString("Hard constraints (each is enforced when your answer is ingested):\n\n")
	b.WriteString("- `rationale_quote` must **equal** the source finding's `quote` — byte for byte, not a re-derivation from the utterance. The drafting step carries evidence forward; it never introduces any.\n")
	b.WriteString("- `severity` must equal the source finding's `severity`. Triage order is a human product and is not yours to choose.\n")
	b.WriteString("- `session` must equal the session named below.\n")
	b.WriteString("- `finding` must name one of the confirmed findings below; a finding that is unverified, rejected, or a duplicate is not eligible.\n")
	b.WriteString("- `steps` must list at least one step.\n")
	b.WriteString("- `status` is ignored: every draft is written as `proposed`, whatever your answer says.\n\n")

	// The manifest is attacker-authorable — a session directory is an exchange
	// unit — and the request is printed to the operator's terminal before it is
	// handed to an agent. Every value rendered as prose or a list item outside a
	// code fence therefore goes through session.SafeInline, the one shared home
	// for the escape set: SafeText's layer stops an ESC-bearing value from driving
	// ANSI sequences in the terminal and a newline-bearing one from forging block
	// structure (a fake "## " heading, or extra instructions) inside the request
	// the agent is asked to obey, and the inline-escape layer stops the constructs
	// that need no newline — an unescaped `[x](http://…)` or image form otherwise
	// survives as an active link or a tracking beacon the moment a saved
	// request.md is previewed. Values rendered inside a fence (each marshalled
	// finding and timeline line below) go through SafeText only, which strips the
	// terminal-control and Trojan-Source bytes json.Marshal passes through; JSON's
	// own structural bytes are ASCII and pass through unchanged.
	b.WriteString("## Session\n\n")
	fmt.Fprintf(&b, "- Session: %s\n", safeOrNone(man.Session))
	fmt.Fprintf(&b, "- App: %s\n", safeOrNone(man.App))
	fmt.Fprintf(&b, "- Participant: %s\n", safeOrNone(man.Participant))
	// Presence and numbering are decided per task on the rendered form, not raw
	// emptiness (analyze.EmitRequest's pattern): a manifest's tasks are
	// operator-supplied and unvalidated, so a whitespace-only or
	// invisible-only-Unicode entry must not survive SafeText's Cf stripping and
	// print as a blank numbered item.
	var tasks []string
	for _, t := range man.Tasks {
		if rendered := session.SafeInline(t); strings.TrimSpace(rendered) != "" {
			tasks = append(tasks, rendered)
		}
	}
	if len(tasks) > 0 {
		b.WriteString("- Tasks:\n")
		for i, t := range tasks {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, t)
		}
	} else {
		b.WriteString("- Tasks: (none recorded)\n")
	}
	b.WriteString("\n")

	b.WriteString("## Confirmed findings\n\n")
	b.WriteString("Each finding below is confirmed by a human and eligible. Its own record is given " +
		"first, verbatim as it is stored (copy `quote` and `severity` from it byte for byte; its " +
		"`status` is the birth state, see the rubric above), then its event window: the timeline " +
		"entries around it, in time order, which are the only source for the steps.\n\n")
	// The verdict date comes from the same effective-status computation eligibility
	// does, so the header cannot claim a confirmation the eligible set did not agree
	// with. It is rendered rather than the record's own status field being rewritten:
	// the model must copy `quote` and `severity` out of this line byte for byte, and
	// substituting one field would make the record it is shown differ from the record
	// ingest validates against — the shown-vs-validated gap this package closes
	// everywhere else.
	eff := analyze.EffectiveStatus(findings, verdicts)
	for _, f := range confirmed {
		fmt.Fprintf(&b, "Finding %s — %s, severity %d, at [%s], %s:\n\n",
			session.SafeInline(f.ID), safeOrDash(f.Type), f.Severity, clock(f.T), confirmedOn(eff[f.ID]))
		line, err := json.Marshal(f)
		if err != nil {
			return "", err
		}
		b.WriteString("```jsonl\n")
		b.WriteString(session.SafeText(string(line)))
		b.WriteString("\n```\n\n")

		b.WriteString("Event window:\n\n")
		b.WriteString("```jsonl\n")
		for _, e := range Window(entries, f, window) {
			el, err := json.Marshal(e)
			if err != nil {
				return "", err
			}
			b.WriteString(session.SafeText(string(el)))
			b.WriteByte('\n')
		}
		b.WriteString("```\n\n")
	}

	b.WriteString("## Answer\n\n")
	fmt.Fprintf(&b, "Answer with a single JSON document: `{\"rubric\":\"%s\",\"tests\":[ … ]}`. "+
		"A bare top-level array of drafts is also accepted. Output JSON only, no prose.\n\n", RubricVersion)
	b.WriteString("```json\n")
	b.WriteString(outputExample)
	b.WriteString("\n```\n")

	return b.String(), nil
}

// safeOrNone applies session.SafeInline and falls back to "(none)" when the
// result renders as nothing (empty or whitespace-only): a manifest field is
// operator-supplied and unvalidated by session.SaveManifest, so a
// whitespace-only or invisible-only value must not survive SafeText's Cf
// stripping and print as a blank field with the "(none)" placeholder skipped.
// The twin of analyze.safeOrNone, for the same fields and the same reason.
func safeOrNone(s string) string {
	t := session.SafeInline(s)
	if strings.TrimSpace(t) == "" {
		return "(none)"
	}
	return t
}

// safeOrDash is safeOrNone for a value rendered mid-sentence, where an em dash
// reads better than a parenthesised word: a finding's type is not validated by
// analyze.Load, so a hand-edited or exchanged findings.jsonl can reach this sink
// with one that renders as nothing.
func safeOrDash(s string) string {
	t := session.SafeInline(s)
	if strings.TrimSpace(t) == "" {
		return "—"
	}
	return t
}

// confirmedOn renders the human verdict behind an eligible finding, so the
// request never shows a record whose `status` field reads "unverified" without
// saying in the same line what actually made it eligible. The date is
// attacker-authorable (a verdict in an exchanged session is unvalidated text) and
// renders outside any code fence, so it goes through SafeInline; a verdict
// carrying no date — or one that renders as nothing — drops the clause rather
// than printing a dangling "on".
func confirmedOn(st analyze.Status) string {
	at := session.SafeInline(st.At)
	if strings.TrimSpace(at) == "" {
		return "confirmed by human verdict"
	}
	return "confirmed by human verdict on " + at
}
