---
id: itd-3
slug: codebase-mapping
spec_id: spc-2609152218094570
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: [itd-2, itd-9]
severity: major
---

# Findings Land in the Code, Not Just the Report

## Press Release

> **Testimony maps confirmed findings to the code that owns them.** Once a human has confirmed a finding, the mapping step hands its selector, route, and event window, together with the path to the application's repository, to the operator's chosen model. The model answers with the source locations it believes own that anchor. Testimony checks each answer against the repository, the file exists and the line is in range, and records it as a proposed reference. From a mapped finding, Testimony renders an issue draft: a title, reproduction steps from the event window, the participant's own words as evidence, and the suspected file. A person accepts or rejects each reference, and the decision is kept beside it. Nothing is filed anywhere, and nothing is written into the application's repository.
>
> "The gap was always between 'users struggled here' and 'this file, this handler'," said Alice, the maintainer. "Now a finding comes to me with the selector, the route, the suspect file, and the person's own words. I start fixing instead of reconstructing."

## Why This Matters

Session findings that stop at a report require a second act of translation before anyone can act on them, and that translation is where evidence goes stale. Anchoring interactive elements with stable `data-testid` attributes at build time makes selectors near-deterministic grep targets, so the translation can be delegated to a model and checked by the CLI, turning a qualitative observation into a workable, evidenced issue while the session is still fresh.

The step sits downstream of verification, as regression-test drafting (itd-9) does: only a finding a human has confirmed is mapped, every reference is born a proposal, and the human decision on it is retained beside the machine record. The mapping is the last step of the pipeline, kept separable so it can iterate without destabilising capture or analysis.

## What's In Scope

- Emitting a mapping request over each confirmed finding that carries a selector or route anchor: the anchor, the participant's quote, the event window, and the path to the application's repository.
- Ingesting the answer into a new record family, `refs.jsonl`, beside `findings.jsonl` and `tests.jsonl`: each reference names a confirmed finding and a repo-relative path that exists under the repository, with any line within the file's length, and is born `proposed`.
- A human accept / reject pass over each reference, appended non-destructively as verdicts and test decisions are, so the reference stays linked to its finding and session.
- Rendering an issue draft from a mapped finding: a title, reproduction steps from the event window, the participant's quote, and the suspected file.
- Reading the application's repository only to verify that a returned path exists and a line is in range.

## What's Out of Scope

- Automatic filing of issues into a tracker without human review.
- Anchoring a finding from a terminal session on the command or output text in the timeline; a terminal finding carries no selector or route, and the timeline text keeps its escape sequences, so that is its own draft (itd-2609152113364815).
- A confidence level on a reference. Ingest cannot validate a model-asserted word, and the human decision is the only quality signal.
- Grepping, router-table resolution, or any other reasoning about the application's source inside the CLI; resolution is the host's job.
- Writing anything into the application's repository.
- Native-app accessibility-identifier instrumentation beyond using identifiers already present.
- Mapping for third-party apps; Mode B has no codebase access by definition (itd-4).
- The `data-testid` anchoring convention itself, which the how-to for instrumenting your own app documents.

## Scope Conditions

- The session is a web session and at least one confirmed finding carries a selector or route anchor. A session with none is refused loudly, not mapped. <!-- cond: cond-2609152218105354 -->
- The application's repository is available locally and passed by path. Testimony reads it only to verify that a returned path exists and a line is in range, and never writes into it. <!-- cond: cond-2609152218108484 -->
- The application anchors interactive elements with stable `data-testid` attributes. Without them, resolution rests on selectors that may not survive a rebuild. <!-- cond: cond-2609152218105360 -->
- The host that answers the mapping request is the operator's chosen model. The CLI emits the request, never calls a model, holds no keys, and adds no network dependency. <!-- cond: cond-2609152218106626 -->
- `timeline.jsonl` is present, so each mapped finding's event window resolves for the issue draft. <!-- cond: cond-2609152218107939 -->

## Mechanism

We expect a `data-testid` selector or a route, handed to a model with the repository path, to resolve to the owning component because stable test ids are near-deterministic grep targets in the application's own source. It would be shown wrong by a high reject rate on references, or by references that land on the test suite instead of the component.

## Acceptance Criteria

- **Given** a session with a confirmed finding that carries a selector or route anchor, and a path to the application's repository, **when** the mapping request is emitted, **then** it carries that finding's anchor, quote, event window, and the repository path, and no finding that is unverified, rejected, or duplicate.
- **Given** a mapping answer from the host, **when** it is ingested, **then** each reference that names a confirmed finding and a repo-relative path that exists under the repository, with any line within the file's length, is written to `refs.jsonl` with status `proposed`, and an answer in which any reference fails a check is refused as a whole with every error reported.
- **Given** a mapped finding, **when** an issue draft is produced, **then** it contains a title, reproduction steps from the event window, the participant quote, and the suspected file.
- **Given** a proposed reference, **when** a human accepts or rejects it, **then** the decision is appended to `refs.jsonl` without rewriting the reference line, and the reference stays linked to its finding and session.
- **Given** a session with no confirmed finding that carries a selector or route, **when** the mapping step runs, **then** it refuses with the finding count by status and writes nothing.

## Open Questions

- Does a route anchor resolve as reliably as a test id? A route lives in a router table the model must read and interpret, where a test id is a literal string to search for.
- Should an accepted reference feed the regression-test draft (itd-9), so a test arrives with the file it exercises already named?

## Grounds

- pursued: itd-9 proved the emit, ingest, review, render shape on drafts, and mapping is the last pipeline step that turns a finding into something a developer opens in an editor; it would be shown wrong if reviewers reject most references, or if the issue drafts are not what a maintainer actually files

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-d9678d4d4b1c -->
Fidelity review — receipt rcp-d9678d4d4b1c (verifier abcd:intent-auditor claude-opus-5[1m]).

Provenance: abcd:intent-auditor@claude-opus-5[1m] · rubric_hash sha256:542ed2cd51ff938717a3f47b2b332e8d47910beec0ca7ecdfd238ae7edf5ced5 · prompt_hash sha256:b6903c509bd327dd021530fadba8ebf088b81858a93cecc855ffb41dd3e13d2e
Input attestations: diff:f81199f..working tree (uncommitted, incl. untracked internal/coderefs/, examples/sample-session/refs.jsonl, docs/how-to/map-findings-to-code.md)@sha256:b7fd7176e3eaa3962ad8fb997d15c95efd19526a79f021b9471b87e628cb5668;

Acceptance rollup: MET 4 · MET_WITH_CONCERNS 1 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: EmitRequest computes the eligible set before reading the timeline and renders, per finding, a header naming the selector and route, the finding's own JSON line (which carries the quote), and its event window from drafttests.Window, under a Repository section carrying the absolute -repo path; eligibility is confirmed-and-anchored-and-not-mode-B, so a smoke run over a copy of examples/sample-session emitted exactly one finding (F-001) out of five, omitting unverified F-002/F-004, rejected F-003 and duplicate F-005.
  evidence: internal/coderefs/emit.go:55 — "mappable := eligible(findings, verdicts)"
  evidence: internal/coderefs/emit.go:117 — "fmt.Fprintf(&b, "- Path: %s\n", session.SafeInline(root.abs))"
  evidence: internal/coderefs/emit.go:149 — "clock(f.T), anchorPhrase(f), confirmedOn(eff[f.ID])"
  evidence: internal/coderefs/emit.go:155 — "b.WriteString(session.SafeText(string(line)))"
  evidence: internal/coderefs/emit.go:160 — "for _, e := range drafttests.Window(entries, f, window) {"
  evidence: internal/coderefs/coderefs.go:309 — "if eff[f.ID].Value == "confirmed" && f.Mode != "B" && hasAnchor(f) {"
  evidence: internal/coderefs/emit_test.go:19 — "func TestEmitCarriesAnchorQuoteWindowAndRepo(t *testing.T) {"
- ac-2 — MET: Ingest is the sole validation boundary: it refuses a reference whose finding is not in the confirmed-and-anchored set, holds the path to a containment-plus-Lstat rule against the repository, checks a present line against the counted file length, forces status to "proposed" on every written reference, and joins all errors so nothing is committed on any failure; a smoke run wrote two references (one answer-claimed "accepted" laundered to "proposed") and a four-reference bad answer produced eight errors across three references — finding, session, role, path and line all reported for the same reference — with no refs.jsonl written.
  evidence: internal/coderefs/ingest.go:530 — "if _, ok := sources[session.SafeText(r.Finding)]; !ok {"
  evidence: internal/coderefs/ingest.go:186 — "func (root repoRoot) checkPath(raw string) (full, clean string, err error) {"
  evidence: internal/coderefs/ingest.go:225 — "fi, err := os.Lstat(full)"
  evidence: internal/coderefs/ingest.go:548 — "if perr == nil && p.line != nil {"
  evidence: internal/coderefs/ingest.go:110 — "refs[i].Status = "proposed""
  evidence: internal/coderefs/ingest.go:118 — "return nil, errors.Join(errs...)"
  evidence: internal/coderefs/ingest_test.go:256 — "func TestIngestIsTransactional(t *testing.T) {"
  evidence: examples/sample-session/refs.jsonl:1 — ""role":"owner","status":"proposed""
- ac-3 — MET_WITH_CONCERNS: Render emits one Markdown block per mapped finding carrying a CLI-derived title, the participant's quote, reproduction steps derived from drafttests.Window over timeline.jsonl, and a Suspected files list of every reference with its current status; a smoke run produced a four-step repro for F-001 with both references labelled. Concern: the steps are only as good as the window — when the window holds no interaction event at or before the finding's t, the Steps to reproduce heading is filled with the single line "(no interaction event precedes the finding in its event window)", which I reproduced live, so the draft can carry a steps heading with no step derived; and the render refuses outright (exit 1) when timeline.jsonl is absent rather than degrading.
  evidence: internal/coderefs/render.go:86 — "fmt.Fprintf(&b, "## %s — %s\n\n", mdInline(f.ID), title(f))"
  evidence: internal/coderefs/render.go:88 — "mdOrPlaceholder(f.Quote, "no quote")"
  evidence: internal/coderefs/render.go:91 — "for n, s := range steps(drafttests.Window(entries, f, DefaultWindow), f) {"
  evidence: internal/coderefs/render.go:104 — "fmt.Fprintf(&b, "- %s (%s) — %s\n", mdCode(loc), mdOrDash(r.Role), decisionPhrase(st))"
  evidence: internal/coderefs/render.go:203 — "return []string{"(no interaction event precedes the finding in its event window)"}"
  evidence: internal/coderefs/render.go:50 — "entries, err := analyze.LoadTimeline(dir)"
  evidence: internal/coderefs/testdata/issues.md:1 — "# Issue drafts"
- ac-4 — MET: A decision goes through AppendDecision → session.AppendRecord, the same append-only primitive the verdicts and test decisions use, with a Verify closure that re-reads the locked file and refuses if the target changed; there is no edit path, and commitRefs guards against a re-ingest overwriting a file that already holds decisions. Smoke run: accepting R-001 and rejecting R-002 appended lines 3 and 4 while the two reference lines stayed byte-identical (cmp clean), their finding and session fields intact, and a re-ingest of the same answer was refused.
  evidence: internal/coderefs/review.go:211 — "func AppendDecision(dir string, d Decision, expect *Ref) error {"
  evidence: internal/coderefs/review.go:226 — "return session.AppendRecord(a)"
  evidence: internal/session/records.go:56 — "func AppendRecord(a Append) error {"
  evidence: internal/coderefs/ingest.go:345 — "refusing to overwrite %s: it already holds decision records (the retained human record)"
  evidence: internal/coderefs/review_test.go:53 — "func TestDecisionIsAppendedAndRefLinesUnchanged(t *testing.T) {"
  evidence: examples/sample-session/refs.jsonl:3 — "{"kind":"decision","ref":"R-001","decision":"accepted","at":"2026-09-16"}"
- ac-5 — MET: The eligibility check runs first on both write paths — emit before the timeline is read, ingest before a byte of the answer — and the refusal carries the by-status tally with the anchored count beside the confirmed count; a session with the verdicts stripped refused on both emit and ingest at exit 1 with "5 findings: 0 confirmed (0 with a selector or route), 5 unverified, 0 duplicate, 0 rejected" and wrote no refs.jsonl, and a session with one confirmed but anchorless finding refused with "1 confirmed (0 with a selector or route)".
  evidence: internal/coderefs/emit.go:56 — "if len(mappable) == 0 {"
  evidence: internal/coderefs/ingest.go:47 — "if len(mappable) == 0 {"
  evidence: internal/coderefs/coderefs.go:332 — "%d findings: %d confirmed (%d with a selector or route), %d unverified, %d duplicate, %d rejected"
  evidence: internal/coderefs/coderefs.go:381 — "a finding is mappable when its verdict is confirmed and it carries a selector or route"
  evidence: internal/coderefs/emit_test.go:75 — "func TestEmitRefusesWithNoMappableFinding(t *testing.T) {"
  evidence: internal/coderefs/ingest_test.go:310 — "func TestIngestRefusesWithNoMappableFinding(t *testing.T) {"

Gap audit:
- honoured:
  - The mapping step hands the confirmed finding's selector, route, and event window, together with the path to the application's repository, to the operator's chosen model.
    evidence: internal/coderefs/emit.go:117 — "- Path: %s"
    evidence: internal/coderefs/emit.go:149 — "anchorPhrase(f), confirmedOn(eff[f.ID])"
    evidence: internal/coderefs/emit.go:160 — "drafttests.Window(entries, f, window)"
  - Testimony checks each answer against the repository and records it as a proposed reference in a new record family, refs.jsonl, beside findings.jsonl and tests.jsonl.
    evidence: internal/coderefs/ingest.go:186 — "func (root repoRoot) checkPath(raw string)"
    evidence: internal/coderefs/ingest.go:110 — "refs[i].Status = "proposed""
    evidence: internal/session/session.go:62 — "RefsFile = "refs.jsonl""
    evidence: docs/reference/session-directory.md:239 — "## `refs.jsonl`"
  - A person accepts or rejects each reference, and the decision is kept beside it, appended non-destructively as verdicts and test decisions are.
    evidence: internal/coderefs/review.go:211 — "func AppendDecision(dir string, d Decision, expect *Ref) error {"
    evidence: internal/review/review.go:47 — "KindRefs = "refs""
    evidence: internal/session/records.go:56 — "func AppendRecord(a Append) error {"
  - From a mapped finding, Testimony renders an issue draft: a title, reproduction steps from the event window, the participant's own words as evidence, and the suspected file.
    evidence: internal/coderefs/render.go:37 — "func Render(dir string) (string, error) {"
    evidence: internal/coderefs/render.go:96 — "b.WriteString("### Suspected files\n\n")"
    evidence: docs/how-to/map-findings-to-code.md:1 — "# Map findings to code"
  - Reading the application's repository only to verify that a returned path exists and a line is in range; nothing is written into the application's repository.
    evidence: internal/coderefs/ingest.go:138 — "func newRepoRoot(repo string) (repoRoot, error) {"
    evidence: internal/coderefs/ingest.go:264 — "func countLines(full string) (int, error) {"
    evidence: internal/coderefs/coderefs.go:12 — "Its only reads of the repository are the existence and line-count checks at ingest, and it never writes into it."
  - No confidence level on a reference: ingest cannot validate a model-asserted word, and the human decision is the only quality signal.
    evidence: internal/coderefs/ingest.go:477 — "dec.DisallowUnknownFields()"
    evidence: internal/coderefs/emit.go:107 — "Any field outside the six above — a confidence, a snippet, a note — is refused"
  - Nothing is filed anywhere: no automatic filing of issues into a tracker without human review.
    evidence: internal/coderefs/render.go:35 — "The result is a hand-off artefact for the operator to file where they choose."
    evidence: go.mod:1 — "module github.com/REPPL/Testimony"
  - Grepping, router-table resolution, or any other reasoning about the application's source stays out of the CLI; resolution is the host's job.
    evidence: internal/coderefs/coderefs.go:10 — "Resolution is the host's job."
    evidence: internal/coderefs/emit.go:84 — "a route is an entry in a router table you must read and interpret"
- diverged:
  - "Testimony maps confirmed findings to the code that owns them" — delivered as a two-phase host-delegated exchange (emit a request, then ingest and validate an answer) rather than one step; the CLI maps nothing itself and the operator must run a model between the halves.
    evidence: internal/coderefs/emit.go:35 — "func EmitRequest(dir, repo string, window float64) (string, error) {"
    evidence: internal/coderefs/ingest.go:33 — "func Ingest(dir, repo string, r io.Reader) ([]Ref, error) {"
    evidence: docs/reference/cli.md:297 — "## `testimony map`"
  - "Testimony checks each answer against the repository, the file exists and the line is in range" — the line is optional, so a reference with no line is admitted on the existence check alone; the in-range half of the promise binds only when the host chose to offer a line.
    evidence: internal/coderefs/ingest.go:548 — "if perr == nil && p.line != nil {"
    evidence: internal/coderefs/coderefs.go:84 — "Line int `json:"line,omitempty"`"
    evidence: internal/coderefs/ingest_test.go:69 — "func TestIngestLineIsOptional(t *testing.T) {"
  - "reproduction steps from the event window" — the steps are derived by the CLI, not the model (a strengthening against invention), but they are mechanical and degrade to a placeholder line under the Steps heading when the window carries no interaction event at or before the finding's t.
    evidence: internal/coderefs/render.go:186 — "func steps(window []timeline.Entry, f analyze.Finding) []string {"
    evidence: internal/coderefs/render.go:203 — "(no interaction event precedes the finding in its event window)"
    evidence: internal/coderefs/render_test.go:83 — "func TestRenderStepsEndAtTheFinding(t *testing.T) {"
  - "a confirmed finding that carries a selector or route anchor" is enforced as a non-empty ui.selector OR ui.route on a non-mode-B finding — a proxy for the intent's "web session" assumption, not a capture-mode check; a mode-A finding with a route and no selector is equally eligible, so the data-testid premise the Mechanism rests on is never required by the boundary.
    evidence: internal/coderefs/coderefs.go:290 — "return strings.TrimSpace(session.SafeText(f.UI.Selector)) != "" ||"
    evidence: internal/coderefs/coderefs.go:309 — "eff[f.ID].Value == "confirmed" && f.Mode != "B" && hasAnchor(f)"
- missing:
  - The Mechanism's stated falsifiers — "a high reject rate on references, or references that land on the test suite instead of the component" — are not instrumented: `test` is an equal member of the closed role set, nothing counts or flags it, and no surface reports an accept/reject rate; the signal exists only as lines a human would tally out of refs.jsonl by hand.
    evidence: internal/coderefs/coderefs.go:110 — "roleSet = map[string]bool{"owner": true, "handler": true, "route": true, "test": true}"
    evidence: internal/coderefs/emit.go:82 — "or `test` (a test that exercises it)"
    evidence: internal/coderefs/coderefs.go:343 — "%d references: %d accepted, %d proposed, %d rejected"

Scope-condition dispositions:
- cond-2609152218105354 — survived: A session with no confirmed anchored finding is refused loudly rather than mapped: the eligibility gate runs first on both write paths and the refusal names the tally, including how many confirmed findings carry an anchor; verified live at exit 1 on emit and on ingest with nothing written, for both a no-verdict session and a confirmed-but-anchorless one.
  evidence: internal/coderefs/coderefs.go:373 — "var ErrNoMappableFindings = errors.New("no mappable finding")"
  evidence: internal/coderefs/emit.go:56 — "if len(mappable) == 0 {"
  evidence: internal/coderefs/ingest.go:47 — "if len(mappable) == 0 {"
  evidence: internal/coderefs/coderefs.go:332 — "%d confirmed (%d with a selector or route)"
- cond-2609152218108484 — survived: The repository is passed by path, resolved once, and opened read-only for the existence and line-count checks alone; I ingested against a copy of the fixture repo with every file and directory made unwritable (chmod a-w) and the ingest succeeded, with a sha256 sweep of the tree byte-identical before and after.
  evidence: internal/coderefs/ingest.go:138 — "func newRepoRoot(repo string) (repoRoot, error) {"
  evidence: internal/coderefs/ingest.go:265 — "f, err := session.OpenFileNoFollowRead(full)"
  evidence: internal/coderefs/render.go:33 — "Nothing is written into the application's repository, and the render does not open it"
  evidence: internal/coderefs/ingest.go:225 — "fi, err := os.Lstat(full)"
- cond-2609152218105360 — narrowed: The assumption held everywhere it was exercised, but it was exercised only against a four-file synthetic fixture tree authored alongside the sample session to carry exactly the data-testid attributes that session names; the CLI neither requires nor inspects a data-testid, and nothing in the delivery bears on whether a selector survives a rebuild.
  narrowing: Holds for the fixture tree under internal/coderefs/testdata/repo, whose data-testid attributes were written to match examples/sample-session; no real application was mapped, and eligibility accepts any non-empty selector or route, so a route-only or fragile-selector finding is equally mappable and the rebuild-survival claim is untested.
  evidence: internal/coderefs/testdata/repo/src/settings/ProfileForm.tsx:46 — "< button data-testid="save-btn" onClick={() => saveProfile(name)}>"
  evidence: internal/coderefs/coderefs.go:290 — "strings.TrimSpace(session.SafeText(f.UI.Selector)) != "" ||"
  evidence: docs/how-to/map-findings-to-code.md:21 — "with stable `data-testid` attributes, because a test id is a literal string the"
  evidence: .github/workflows/ci.yml:170 — "./testimony map -session examples/sample-session -repo internal/coderefs/testdata/repo"
- cond-2609152218106626 — survived: The mapping layer is stdlib-plus-internal only and the module still declares no dependency: EmitRequest returns the request as text for the operator's chosen host, Ingest reads the answer from a file or stdin, and the package imports nothing under net/ and never shells out, so no model is called and no key is held on this path.
  evidence: go.mod:1 — "module github.com/REPPL/Testimony"
  evidence: internal/coderefs/coderefs.go:6 — "The CLI never calls a model, holds no keys, and adds no network dependency"
  evidence: internal/coderefs/emit.go:178 — "return b.String(), nil"
  evidence: internal/cli/cli.go:733 — "refs, err := coderefs.Ingest(sess, *repo, in)"
- cond-2609152218107939 — narrowed: The render does hard-require timeline.jsonl — it refuses at exit 1 with a run-merge hint when the file is absent, which I reproduced — but the condition's consequent is weaker than it reads: the window always yields a slice, not necessarily one the steps can be built from, so a present timeline does not guarantee that a mapped finding's event window resolves into reproduction steps.
  narrowing: Holds as "a timeline is required and its absence is refused"; it does not hold as "each mapped finding's event window resolves for the issue draft" — a window containing no interaction event at or before the finding's t renders the single placeholder line "(no interaction event precedes the finding in its event window)" under the Steps heading, which I reproduced on a session whose confirmed anchored finding sits at t=0.2.
  evidence: internal/coderefs/render.go:50 — "entries, err := analyze.LoadTimeline(dir)"
  evidence: internal/coderefs/render.go:91 — "steps(drafttests.Window(entries, f, DefaultWindow), f)"
  evidence: internal/coderefs/render.go:203 — "(no interaction event precedes the finding in its event window)"
  evidence: docs/reference/cli.md:318 — "Emit and render hint to run `merge` first when the timeline is missing"