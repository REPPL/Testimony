package coderefs

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/REPPL/Testimony/internal/analyze"
	"github.com/REPPL/Testimony/internal/drafttests"
	"github.com/REPPL/Testimony/internal/session"
)

// outputExample is the worked output-shape example embedded in the request. It
// is illustrative text, not validated data.
const outputExample = `{"rubric":"testimony-coderefs/v1","refs":[
  {"id":"R-001","finding":"F-001","session":"sample-session",
   "path":"src/settings/ProfileForm.tsx","line":46,"role":"owner",
   "status":"proposed"},
  {"id":"R-002","finding":"F-001","session":"sample-session",
   "path":"src/settings/saveProfile.ts","line":12,"role":"handler",
   "status":"proposed"}
]}`

// EmitRequest builds the single, self-contained mapping request for the session
// in dir: a versioned rubric, the session context, the repository path, and,
// for each confirmed finding that carries a selector or route, that finding's
// own record plus its event window, so that an agent given only this text and
// the repository can answer. window is the event-window half-width in seconds.
// Nothing in the session directory is mutated.
//
// A session with no mappable finding is staged loudly — the refusal names the
// finding count by status and nothing is emitted — because eligibility is the
// whole point of the step: only evidence a human already vouched for, and only
// evidence with an anchor, can be mapped.
func EmitRequest(dir, repo string, window float64) (string, error) {
	// The repository is checked here as Ingest checks it, so the rule is a
	// property of the API: an emitted request telling the host to resolve anchors
	// against a path that is not a directory (or, for "", the current directory)
	// would be a request for the wrong tree.
	root, err := newRepoRoot(repo)
	if err != nil {
		return "", err
	}
	man, err := session.LoadManifest(dir)
	if err != nil {
		return "", err
	}
	findings, verdicts, err := loadFindings(dir)
	if err != nil {
		return "", err
	}
	// Eligibility is checked before the timeline is read, so a session with
	// nothing to map hears why it is empty rather than being sent to run merge
	// for a request it could not fill either way.
	mappable := eligible(findings, verdicts)
	if len(mappable) == 0 {
		return "", noMappableFindings(dir, findings, verdicts)
	}
	entries, err := analyze.LoadTimeline(dir)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Testimony code-mapping rubric: %s\n\n", RubricVersion)

	b.WriteString("## Stance\n\n")
	b.WriteString("Every reference you return is a *proposal*, born `proposed`; a human accepts or " +
		"rejects it afterwards. Only the confirmed findings below are eligible — a human has " +
		"already vouched for each one. Resolve each anchor by **reading the repository** at the " +
		"path given below: never invent a path, never guess a line, and omit a finding rather than " +
		"answer it with a path that does not exist. Never alter the quote.\n\n")

	b.WriteString("## Instructions\n\n")
	b.WriteString("Return zero or more references per finding, in finding-id order. For each reference:\n\n")
	b.WriteString("- **`path`** — the repo-relative path of the file, with forward slashes, no leading " +
		"`/`, and no `.` or `..` segment. It must exist as a regular file under the repository.\n")
	b.WriteString("- **`line`** — optional, 1-based: the line the anchor resolves to. Give it only when " +
		"you have read the file and can name the line; omit it otherwise.\n")
	b.WriteString("- **`role`** — why the path is relevant, one of `owner` (the file that renders the " +
		"element), `handler` (the code that handles its interaction), `route` (the router entry for " +
		"the route), or `test` (a test that exercises it). A human reads the role to judge the " +
		"reference; it is validated as a member of the set, never for truth.\n")
	b.WriteString("- A `data-testid` selector is a literal string to search the source for; a route is an " +
		"entry in a router table you must read and interpret. Prefer the component that owns the " +
		"element over the test that exercises it.\n\n")

	b.WriteString("## Rubric\n\n")
	b.WriteString("Field definitions:\n\n")
	b.WriteString("- `id` — `R-NNN`, zero-padded and unique within your answer.\n")
	b.WriteString("- `finding` — the id of the confirmed finding this reference resolves.\n")
	b.WriteString("- `session` — the session this reference belongs to, copied unchanged from the session context below.\n")
	b.WriteString("- `path` — repo-relative, at most 512 bytes, as described above.\n")
	b.WriteString("- `line` — optional integer, at least 1 and at most the file's line count.\n")
	b.WriteString("- `role` — `owner`, `handler`, `route`, or `test`.\n\n")
	b.WriteString("Reading each finding record below — two of its fields are about the record, not about your reference:\n\n")
	b.WriteString("- `status` is the finding's **birth state**, and it reads `unverified` on every " +
		"finding this tool writes: a finding is born a candidate. It is *not* the finding's current " +
		"status. Every finding below is confirmed, and its header names the date the verdict was recorded.\n")
	b.WriteString("- `mode` is the capture mode: `A` is the application under test, `B` is reference " +
		"capture of a third-party app. Only mode `A` findings are eligible, so every finding below is mode `A`.\n\n")
	b.WriteString("Hard constraints (each is enforced when your answer is ingested):\n\n")
	b.WriteString("- `path` must name an existing regular file under the repository; a path that does not exist, names a directory or a symlink, or escapes the repository is refused.\n")
	b.WriteString("- `line`, when given, must be within the file's length.\n")
	b.WriteString("- `finding` must name one of the confirmed findings below; a finding that is unverified, rejected, a duplicate, or carries no selector or route is not eligible.\n")
	b.WriteString("- `session` must equal the session named below.\n")
	b.WriteString("- Any field outside the six above — a confidence, a snippet, a note — is refused; the reference carries what ingest can verify, and the human decision is the only quality signal.\n")
	b.WriteString("- `status` is ignored: every reference is written as `proposed`, whatever your answer says.\n")
	b.WriteString("- An answer in which any reference fails a check is refused as a whole, with every error reported.\n\n")

	// The repository path is the operator's own machine's path, rendered so the
	// host can open it. It is written to the operator's terminal or to a file the
	// operator chooses, never into the session directory. It goes through
	// SafeInline like every other prose value: an operator-supplied path is not
	// attacker-authored, but the escape set is one home for all inline values.
	b.WriteString("## Repository\n\n")
	fmt.Fprintf(&b, "- Path: %s\n", session.SafeInline(root.abs))
	b.WriteString("- Read it to resolve each anchor. Testimony verifies at ingest that every path you " +
		"return exists under it and that every line is in range; it never writes into it.\n\n")

	b.WriteString("## Session\n\n")
	fmt.Fprintf(&b, "- Session: %s\n", safeOrNone(man.Session))
	fmt.Fprintf(&b, "- App: %s\n", safeOrNone(man.App))
	fmt.Fprintf(&b, "- Participant: %s\n", safeOrNone(man.Participant))
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
	b.WriteString("Each finding below is confirmed by a human, carries a selector or route, and is " +
		"eligible. Its header names the anchor to resolve; its own record follows verbatim as it is " +
		"stored (its `status` is the birth state, see the rubric above), then its event window: the " +
		"timeline entries around it, in time order, which show the interaction the anchor was part of.\n\n")
	eff := analyze.EffectiveStatus(findings, verdicts)
	for _, f := range mappable {
		fmt.Fprintf(&b, "Finding %s — %s, severity %d, at [%s], %s, %s:\n\n",
			session.SafeInline(f.ID), safeOrDash(f.Type), f.Severity, clock(f.T), anchorPhrase(f), confirmedOn(eff[f.ID]))
		line, err := json.Marshal(f)
		if err != nil {
			return "", err
		}
		b.WriteString("```jsonl\n")
		b.WriteString(session.SafeText(string(line)))
		b.WriteString("\n```\n\n")

		b.WriteString("Event window:\n\n")
		b.WriteString("```jsonl\n")
		for _, e := range drafttests.Window(entries, f, window) {
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
	fmt.Fprintf(&b, "Answer with a single JSON document: `{\"rubric\":\"%s\",\"refs\":[ … ]}`. "+
		"A bare top-level array of references is also accepted. Output JSON only, no prose.\n\n", RubricVersion)
	b.WriteString("```json\n")
	b.WriteString(outputExample)
	b.WriteString("\n```\n")

	return b.String(), nil
}

// anchorPhrase names a finding's selector and route in its prose header, so the
// host sees what to resolve before it reads the record. Both are
// attacker-authorable; each renders inside a code span, where backslash escapes
// do not apply, so the code-span rule is used (SafeText, backticks stripped so
// the span cannot be closed early) rather than SafeInline, which would show the
// host `\[data-testid=x\]` for a selector it is meant to search for literally.
// An anchor that renders as nothing is omitted rather than shown blank;
// eligible guarantees at least one is present.
func anchorPhrase(f analyze.Finding) string {
	var parts []string
	if f.UI != nil {
		if !session.CodeRendersEmpty(f.UI.Selector) {
			parts = append(parts, "selector "+codeSpan(f.UI.Selector))
		}
		if !session.CodeRendersEmpty(f.UI.Route) {
			parts = append(parts, "route "+codeSpan(f.UI.Route))
		}
	}
	if len(parts) == 0 {
		return "no anchor"
	}
	return strings.Join(parts, " on ")
}

// codeSpan renders untrusted text inside a Markdown code span; report.mdCode's
// rule, shared with render.mdCode.
func codeSpan(s string) string {
	return "`" + strings.ReplaceAll(session.SafeText(s), "`", "") + "`"
}

// safeOrNone applies session.SafeInline and falls back to "(none)" when the
// result renders as nothing; the twin of drafttests.safeOrNone.
func safeOrNone(s string) string {
	t := session.SafeInline(s)
	if strings.TrimSpace(t) == "" {
		return "(none)"
	}
	return t
}

// safeOrDash is safeOrNone for a value rendered mid-sentence.
func safeOrDash(s string) string {
	t := session.SafeInline(s)
	if strings.TrimSpace(t) == "" {
		return "—"
	}
	return t
}

// confirmedOn renders the human verdict behind an eligible finding, so the
// request never shows a record whose `status` reads "unverified" without saying
// in the same line what made it eligible.
func confirmedOn(st analyze.Status) string {
	at := session.SafeInline(st.At)
	if strings.TrimSpace(at) == "" {
		return "confirmed by human verdict"
	}
	return "confirmed by human verdict on " + at
}
