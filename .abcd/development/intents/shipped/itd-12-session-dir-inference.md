---
id: itd-12
slug: session-dir-inference
spec_id: spc-2609120417292267
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: [itd-10]
severity: minor
---

# `cd` Into a Session and Every Command Just Knows

## Press Release

> **Every `testimony` command now infers `-session` from where you are standing.** `cd sessions/2026-08-08_173038` and run `testimony report` with no flags — the command recognises the current directory as a session (it holds a `manifest.json`) and uses it directly. The explicit `-session DIR` flag still works and still wins when given, for scripts, CI, and anyone working from outside the session directory.
>
> "I kept re-typing or copy-pasting the same timestamped path into every command in a session's lifecycle," said Carol, running a full transcribe-merge-report-analyze-review pass. "Once I could just `cd` there once, the whole thing felt like working inside the session instead of aiming commands at it from a distance."

## Why This Matters

Every pipeline command (`transcribe`, `merge`, `report`, `analyze`, `review`) currently requires an explicit `-session DIR`, refused at a usage error when omitted. A single session's lifecycle runs several of these commands in sequence against the same directory, so the same path gets typed or pasted repeatedly — small friction on its own, compounding across a session and worse across many sessions in one sitting. `git`, and plenty of other directory-scoped CLIs, solve exactly this by inferring their working context from the current directory rather than requiring it named on every invocation. Pairing this with `itd-10`'s proposed fixed default session location makes the natural workflow "go to today's session, run the commands," rather than "look up and paste a path every time."

## Why This Matters Less Without itd-10

This intent stands on its own — inference from the current directory works regardless of where sessions live — but it compounds with `itd-10`: a fixed, predictable session root makes `cd`-ing to "the session I am working on right now" a short, memorable path rather than a long one buried under whatever directory happened to be current at capture time.

## What's In Scope

- When `-session` is omitted, each command checks whether the current working directory itself looks like a session directory (holds a `manifest.json`) and uses it if so.
- An explicit `-session DIR` flag always overrides inference, unchanged from today — no behaviour change for existing scripts, CI, or anyone naming a session explicitly.
- Applies uniformly across `transcribe`, `merge`, `report`, `analyze`, and `review` — the five commands that already require `-session`.
- A clear usage error when neither an explicit flag nor a usable current directory is available, distinct from today's generic "-session is required" message, naming what was checked.

## What's Out of Scope

- Walking up parent directories looking for a session root (the way `git` searches upward for `.git`) — inference is limited to the exact current directory; a session directory that has been `cd`-ed into directly, not merely a descendant of one.
- Any change to `record`/`demo`, which create sessions rather than operate on an existing one and take `-out`, a different flag with a different meaning.
- Multiple candidate session directories in the current directory, or any kind of session selection UI — inference only applies when the current directory itself is unambiguously one session.

## Acceptance Criteria

- **Given** the operator's current directory contains a `manifest.json`, **when** a pipeline command runs with no `-session` flag, **then** it operates on the current directory exactly as if `-session .` had been given.
- **Given** an explicit `-session DIR` flag, **when** a pipeline command runs from any directory, **then** the flag is used and the current directory is not consulted at all.
- **Given** the operator's current directory does not contain a `manifest.json` and no `-session` flag is given, **then** the command refuses with a usage error naming that neither an explicit `-session` nor a usable current directory was found.

## Scope Conditions

- The current working directory is resolvable and readable by the process running the command. <!-- cond: cond-2609120425032969 -->
- A `manifest.json` in the current directory is the session marker; its content is not validated at inference time — downstream loading validates it exactly as it does for an explicitly named session. <!-- cond: cond-2609120425034664 -->
- The operator stands in the session directory itself: a parent of it, or a subdirectory inside it, carries no marker and is not a session. <!-- cond: cond-2609120425037279 -->
- stderr reaches the operator — the inference line is the only signal that a session was inferred rather than named, and a caller that discards stderr loses it. <!-- cond: cond-2609120425034377 -->
- The claim covers the five commands that already take `-session`; `record` and `demo` create sessions under `-out` and are outside it. <!-- cond: cond-2609120425033039 -->

## Open Questions

- Should inference also cover `record`'s or `demo`'s own `-out` root (running from inside the sessions root rather than inside one specific session), or is that a different, unrelated ergonomic gap?
- Does silently defaulting to the current directory risk a mistaken run against the wrong session for an operator who forgot which directory they were in — should the command print which session it inferred, every time, to make the implicit choice visible?

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-f0dc788dae6b -->
Fidelity review — receipt rcp-f0dc788dae6b (verifier abcd:intent-auditor claude-opus-5[1m]).

Provenance: abcd:intent-auditor@claude-opus-5[1m] · rubric_hash sha256:d88bf7359e80476783ab022d38024c3e2260c3172f02b45a4852bd656392f874 · prompt_hash sha256:67ba034ab672d0c825ace052bd5094d4ecb39f1c9ca7299a194d21eb0578648e
Input attestations: diff:working tree vs HEAD (branch feat/itd-12-session-dir-inference): git diff + git status --short@sha256:260560412fcbed60f82ee8d6e6b8700cde1d9fea0dea83350a5c9e8630c0d3af; rubric:.abcd/.work.local/reviews/rcp-f0dc788dae6b.request.md@sha256:d88bf7359e80476783ab022d38024c3e2260c3172f02b45a4852bd656392f874; intent:.abcd/development/intents/shipped/itd-12-session-dir-inference.md@sha256:67ba034ab672d0c825ace052bd5094d4ecb39f1c9ca7299a194d21eb0578648e;

Acceptance rollup: MET 2 · MET_WITH_CONCERNS 1 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET_WITH_CONCERNS: resolveSession stats manifest.json in the current directory and returns the literal ".", which is the only value any downstream package sees, so the inferred path is byte-identical to the explicit `-session .` path; TestSessionInferredFromCurrentDirectory pins merge writing timeline.jsonl and report writing report.md into the cwd at exit 0. Concern: the run is not output-identical to `-session .` — the inferred path additionally prints one stderr line, an addition the press release does not promise (signed off in the spec's Decisions, answering the intent's own Open Question about making the implicit choice visible), and the happy path is pinned for merge and report only, not for transcribe, analyze or review.
  evidence: internal/cli/cli.go:533 — "if _, err := os.Stat(session.ManifestFile); err == nil {"
  evidence: internal/cli/cli.go:535 — "return ".", nil"
  evidence: internal/cli/cli_test.go:363 — "func TestSessionInferredFromCurrentDirectory(t *testing.T) {"
  evidence: internal/cli/cli.go:534 — "fmt.Fprintf(os.Stderr, "%s: using session . (inferred from the current directory)\n", fs.Name())"
- ac-2 — MET: resolveSession learns from fs.Visit that -session was given and returns the flag value verbatim before the os.Stat of the current directory is ever reached, so on the explicit path the cwd is not consulted and nothing extra is printed; TestExplicitSessionWinsOverInference runs `report -session TARGET` from inside a decoy session and asserts the decoy is left with no report.md and no "inferred" line appears.
  evidence: internal/cli/cli.go:527 — "if set {"
  evidence: internal/cli/cli.go:531 — "return dir, nil"
  evidence: internal/cli/cli_test.go:403 — "func TestExplicitSessionWinsOverInference(t *testing.T) {"
  evidence: internal/cli/cli_test.go:423 — "report rendered into the current directory instead of the named session"
- ac-3 — MET: step 4 of resolveSession returns a usage error naming both things checked — the absent flag and the absent marker, the marker name taken from session.ManifestFile rather than a literal — routed through usageErr (exit 2); TestNoSessionAndNoManifestIsAUsageError pins the exact text and exit code for all five commands, and the run passes under -race.
  evidence: internal/cli/cli.go:537 — "return "", fmt.Errorf("%s: -session is required (no -session flag, and the current directory holds no %s)","
  evidence: internal/cli/cli_test.go:429 — "func TestNoSessionAndNoManifestIsAUsageError(t *testing.T) {"
  evidence: internal/cli/cli_test.go:432 — "for _, cmd := range []string{"merge", "report", "transcribe", "analyze", "review"} {"
  evidence: docs/reference/cli.md:21 — "report: -session is required (no -session flag, and the current directory holds no manifest.json)"

Gap audit:
- honoured:
  - cd into a session directory (one holding a manifest.json) and a pipeline command with no -session operates on it
    evidence: internal/cli/cli.go:533 — "if _, err := os.Stat(session.ManifestFile); err == nil {"
    evidence: internal/cli/cli_test.go:371 — "stderr := captureStderr(t, func() { code = Run([]string{"merge"}) })"
  - the explicit -session DIR flag still works and still wins when given, unchanged for scripts and CI
    evidence: internal/cli/cli.go:527 — "if set {"
    evidence: internal/cli/cli_test.go:403 — "func TestExplicitSessionWinsOverInference(t *testing.T) {"
  - applies uniformly across transcribe, merge, report, analyze and review, from one resolution point so the five cannot drift
    evidence: internal/cli/cli.go:520 — "func resolveSession(fs *flag.FlagSet, dir string) (string, error) {"
    evidence: internal/cli/cli.go:92 — "sess, err := resolveSession(fs, *dir)"
    evidence: internal/cli/cli.go:112 — "sess, err := resolveSession(fs, *dir)"
    evidence: internal/cli/cli.go:194 — "sess, err := resolveSession(fs, *dir)"
    evidence: internal/cli/cli.go:317 — "sess, err := resolveSession(fs, *dir)"
    evidence: internal/cli/cli.go:398 — "sess, err := resolveSession(fs, *dir)"
  - a usage error distinct from today's generic "-session is required", naming what was checked
    evidence: internal/cli/cli.go:537 — "no -session flag, and the current directory holds no %s"
    evidence: internal/cli/cli_test.go:437 — "want := "testimony: " + cmd + ": -session is required (no -session flag, and the current directory holds no manifest.json)""
  - no walking up parent directories — inference is limited to the exact current directory
    evidence: internal/cli/cli.go:533 — "os.Stat(session.ManifestFile)"
    evidence: docs/reference/cli.md:21 — "Inference covers the exact current directory only: no parent directory is searched"
  - no change to record/demo, which create sessions and take -out
    evidence: internal/cli/cli.go:138 — "fs := flag.NewFlagSet("record", flag.ExitOnError)"
    evidence: internal/cli/cli.go:59 — "fs := flag.NewFlagSet("demo", flag.ExitOnError)"
  - the rule is recorded on the user-facing surfaces — help footer, reference page section, all five flag tables, changelog
    evidence: internal/cli/cli.go:45 — "Omitting -session on transcribe, merge, report, analyze, or review uses the"
    evidence: docs/reference/cli.md:17 — "## Session directory inference"
    evidence: CHANGELOG.md:19 — "infer their session"
- diverged:
  - "operates on the current directory exactly as if -session . had been given" — the inferred run additionally prints one stderr line, so it is not output-identical to the explicit invocation. A deliberate, spec-signed-off addition that answers the intent's own Open Question about making the implicit choice visible, but it is an addition the press release and the In-Scope list do not state.
    evidence: internal/cli/cli.go:534 — "fmt.Fprintf(os.Stderr, "%s: using session . (inferred from the current directory)\n", fs.Name())"
    evidence: .abcd/development/specs/closed/spc-2609120417292267-session-dir-inference.md:100 — "An inferred session is announced on stderr, once, before any work"
  - "no behaviour change ... for anyone naming a session explicitly" — an explicitly empty -session changed message: it previously refused with `<cmd>: -session is required` and now refuses with `<cmd>: -session must not be empty`. The exit code (2) is unchanged and the new guard is the safety measure that stops an unset shell variable falling through to inference, but a caller matching on the old text sees a different string.
    evidence: internal/cli/cli.go:529 — "return "", fmt.Errorf("%s: -session must not be empty", fs.Name())"
    evidence: internal/cli/cli_test.go:451 — "func TestEmptySessionIsAUsageErrorNotInference(t *testing.T) {"
  - the press-release headline "Every testimony command now infers -session" overstates the delivery: five of the seven commands infer, as the intent's own In-Scope and Out-of-Scope sections intend — record and demo do not.
    evidence: internal/cli/cli.go:138 — "fs := flag.NewFlagSet("record", flag.ExitOnError)"
    evidence: internal/cli/cli.go:45 — "Omitting -session on transcribe, merge, report, analyze, or review uses the"
- missing:
  - the successful-inference path is pinned by test for merge and report only; transcribe, analyze and review are exercised for the two refusal paths but no test shows any of them actually operating on an inferred session. Uniform coverage rests on the shared helper, not on evidence per command.
    evidence: internal/cli/cli_test.go:364 — "t.Run("merge", func(t *testing.T) {"
    evidence: internal/cli/cli_test.go:379 — "t.Run("report", func(t *testing.T) {"

Scope-condition dispositions:
- cond-2609120425032969 — untested: resolveSession never calls os.Getwd and simply stats the relative marker path, so an unresolvable or unreadable working directory would fall through to the usage error rather than be reported as such; nothing in the delivered diff exercises or contradicts that case.
- cond-2609120425034664 — survived: inference is an os.Stat of session.ManifestFile only — the file is never opened or parsed at inference time, and the marker name comes from the shared constant rather than a literal, so downstream loading validates it exactly as for a named session; the merge case runs against a session holding nothing but a manifest.
  evidence: internal/cli/cli.go:533 — "if _, err := os.Stat(session.ManifestFile); err == nil {"
  evidence: internal/cli/cli_test.go:348 — "func manifestOnlySession(t *testing.T) string {"
  evidence: internal/session/session.go:49 — "ManifestFile = "manifest.json""
- cond-2609120425037279 — survived: the single stat is of the bare relative marker with no upward walk and no loop, so only the exact current directory can ever carry the marker — a parent or a subdirectory of a session is not a session; the reference page states the same rule. No test stands specifically inside a session subdirectory, so the guarantee rests on the code shape.
  evidence: internal/cli/cli.go:533 — "os.Stat(session.ManifestFile)"
  evidence: docs/reference/cli.md:21 — "no parent directory is searched, so a directory *inside* a session (or above one) is not a session"
- cond-2609120425034377 — survived: the inference line is written to os.Stderr and nowhere else, and it is the only output distinguishing an inferred run from a named one, so the assumption is load-bearing exactly as the condition states; the tests read it through captureStderr.
  evidence: internal/cli/cli.go:534 — "fmt.Fprintf(os.Stderr, "%s: using session . (inferred from the current directory)\n", fs.Name())"
  evidence: internal/cli/cli_test.go:371 — "stderr := captureStderr(t, func() { code = Run([]string{"merge"}) })"
- cond-2609120425033039 — survived: resolveSession is called at exactly five sites — merge, report, transcribe, analyze, review — and the record and demo cases are untouched by the diff, keeping their -out flag and their create-a-session meaning outside the claim.
  evidence: internal/cli/cli.go:92 — "sess, err := resolveSession(fs, *dir)"
  evidence: internal/cli/cli.go:398 — "sess, err := resolveSession(fs, *dir)"
  evidence: internal/cli/cli.go:138 — "fs := flag.NewFlagSet("record", flag.ExitOnError)"
## Grounds

- pursued: an operator who has cd-ed into a session runs the pipeline commands without a -session flag and gets the same result as with -session .; a wrong-session run would show this conjecture wrong, which the printed inference line is there to surface
