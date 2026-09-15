---
id: itd-8
slug: local-analysis
spec_id: spc-2609150759135349
kind: standalone
suggested_kind: null
reclassification_history: []
builds_on: []
severity: major
---

# A Findings File That Says Where It Was Analysed

## Press Release

> **Testimony records where a session's analysis happened.** `analyze -ingest` takes `-backend local|cloud` and an optional `-model NAME`, and writes that declaration into the session as the first line of `findings.jsonl`, beside the findings it accompanies — so a findings file always states which rubric coded it, on which side of the machine boundary, with which model, and on what date. `report` prints the declaration under its Findings heading, so the artefact people actually read carries the claim too. And the documentation sets out the fully local route end to end: emit the request, run it against a model hosted on the same machine, ingest the answer with `-backend local` — at which point no session content has left the machine at any step, and the findings file says so. Passing neither flag changes nothing but what is recorded: the file states that the backend was not recorded, and the run says so as it goes.
>
> "The ethics committee asks where the data goes, and 'nowhere' is a much shorter answer than a paragraph about derived text," said Carol, session moderator. "But what got the protocol through was not the promise. It was being able to hand them a findings file that names the model which produced it and says it ran on the machine in our locked office."

## Why This Matters

The local-processing boundary is the pipeline's strongest privacy property and, as the ethics constraints record, the strongest card in a research-ethics application. This intent was first drafted as a flag that would point the rubric at a locally hosted model. There is no such flag to build. The repository's oracle is host-delegated by design — the CLI never calls a model, holds no keys, and adds no network dependency — so there is no backend for a flag to select, and a flag pretending to select one would be the first lie the tool ever told about itself. What the pipeline can honestly offer is the other half of the same promise. The operator already chooses where the emitted request runs; what is missing is any trace of that choice in the evidence.

That absence is the real gap. An analysis run against a model on the operator's own machine and one run against a cloud model produce byte-identical findings files. Six months later, when a supervisor, a co-author, or an ethics reviewer asks which it was, the answer rests on somebody's memory of a shell session. Recording the operator's declaration at the moment of ingest turns it into evidence that travels with the findings: it survives the session directory being archived, copied, or handed to a colleague, and it is on the page of the report rather than in the head of the person who ran it. The record is a declaration, not a measurement — Testimony has no way to observe where the request ran, and says so plainly wherever the claim appears. What it does guarantee is that the claim is written down, at the moment it is made, by the person who made it.

The original draft also asked for a stated quality floor below which a local backend is not fit for the second-coder role. That is deliberately deferred rather than built. The pipeline already carries the instrument that answers it: every finding is born `unverified`, and `review` retains each `confirmed | rejected | duplicate` verdict as an ongoing precision measure. Once findings files name their backend and model, the verdicts already accumulating on disk become a comparison between backends — measured from real sessions, not asserted in advance. A floor written into the tool today would be a number nobody had measured, and enforcing it would mean refusing a run on the strength of a claim the CLI cannot check. The retained verdicts are the honest instrument; this intent makes their results attributable.

## What's In Scope

- `-backend local|cloud` and an optional `-model NAME` on `analyze -ingest`, written into the session as a single provenance record beside the findings the same run validates.
- `report` rendering that declaration under its Findings heading, so the shareable artefact carries it.
- A `findings.jsonl` written before this change — one carrying no provenance record — still loading normally everywhere, and reading as "not recorded" in the report.
- Documentation of the fully local workflow end to end: emit the request, run it against a locally hosted model, ingest with local provenance; and a privacy statement that no session content leaves the machine, stated as conditional on that workflow.
- Unchanged behaviour when neither flag is given, apart from what the run records and announces.

## What's Out of Scope

- Any change to how the model work is done. The CLI still never calls a model, holds no keys, and adds no network dependency; no flag selects a backend, because there is no backend in the CLI for a flag to select.
- Verifying the declaration. Testimony records what the operator states and cannot observe where the request actually ran; every surface that renders the record says so.
- Shipping, vendoring, or managing a local model, or advising which one to run.
- A stated quality floor for a local backend, deferred to the retained verdicts as set out above.
- Recording a hostname, address, or machine name. A session directory is an exchange unit; the record carries only what is safe to share.
- The same provenance question for the drafting layer's `tests.jsonl`, which has its own rubric and its own record, and for the codebase-mapping step (itd-3).
- Local transcription, which is already local by design and is not part of this choice.

## Scope Conditions

- The operator has already stood up whatever answers the emitted request; Testimony neither installs, configures, nor reaches it. <!-- cond: cond-2609150759135103 -->
- `analyze -ingest` is the only writer of the provenance record, so a `findings.jsonl` assembled by hand or by another tool may carry none and is read as "not recorded" rather than refused. <!-- cond: cond-2609150759137440 -->
- The declaration is taken on trust: the CLI cannot observe where the request ran, so the record's worth rests on the operator being the person who ran it. <!-- cond: cond-2609150759138300 -->
- The session directory remains an exchange unit, so the record holds only a backend word, a model name, a rubric version, and a date. <!-- cond: cond-2609150759134348 -->

## Acceptance Criteria

- **Given** a session with a merged timeline and a clean answer JSON, **when** `analyze -ingest FILE -backend local -model NAME` runs, **then** the first line of `findings.jsonl` is a provenance record naming the rubric version, the backend `local`, the model `NAME`, and the date, and the finding lines that follow are byte-for-byte the lines a run without those flags would have written.
- **Given** a `findings.jsonl` carrying a provenance record, **when** `report` runs, **then** the Findings section states the backend, the model, the rubric version and the date on one line before the status groups, and notes that the backend is the operator's declaration.
- **Given** a `findings.jsonl` written before this change, carrying no provenance record, **when** `report` and `review` read it, **then** both load it unchanged and the report's Findings section states that the provenance is not recorded.
- **Given** an `analyze -ingest` run with neither `-backend` nor `-model`, **when** it completes, **then** the finding lines written are unchanged from today's, the provenance record states that the backend was not recorded, and the command names that choice on stderr rather than recording it silently.
- **Given** a `findings.jsonl` that already holds verdict records, **when** `analyze -ingest` runs with any combination of the provenance flags, **then** the run is refused with the existing message and neither the verdicts nor the existing provenance record is rewritten.
- **Given** an operator following the documented local route, **when** they read the how-to, **then** it names every command in the route in order — emit, run against the locally hosted model, ingest with `-backend local`, report — and the privacy page states the "no session content leaves the machine" conclusion explicitly as conditional on that route.

## Open Questions

- Should the runner that answered the request — an agent CLI, a local serving tool, a colleague — be a field of its own, or does a free-text `-model` carry it well enough? A field named for a host invites an address, which is exactly what a shared session directory must not carry.
- Should `tests.jsonl` carry the same record for the drafting step, and if so does it share this record's shape or keep its own?
- Once several sessions carry a backend and a model, what is the smallest honest comparison the retained verdicts support — a per-backend confirm rate, or something that accounts for the analyst having seen the findings in a different order?
- Should a later revision make `-backend` required when ingesting, once no script depends on today's default, and what deprecation would that need?

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-234149889a14 -->
Fidelity review — receipt rcp-234149889a14 (verifier abcd:intent-auditor claude-opus-5[1m]).

Provenance: abcd:intent-auditor@claude-opus-5[1m] · rubric_hash sha256:ab4fb122d86a2334f7bd3e84cc059514ada55bf7d42852a6152117a979a9eac6 · prompt_hash sha256:8ae89997220e872a3ec8025bf57338f8b193c79a6b957faaf3694f93cc94e97d
Input attestations: diff:HEAD..working tree (worktree .abcd/.work.local/worktrees/itd-8, branch feat/itd-8-analysis-provenance, HEAD 3142ecc)@sha256:fe12eb00736802f4897ef6b6b5c62e84f27bdfc18ca467a41fcb7a1106976201;

Acceptance rollup: MET 6 · MET_WITH_CONCERNS 0 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: commitFindings prepends the marshalled Provenance record ahead of every finding in the same Records slice, the record carries rubric/backend/model/at, and TestIngestWritesProvenanceAsFirstLine asserts both the first-line shape and byte-identical finding lines against a run without the flags
  evidence: internal/analyze/ingest.go:185
  evidence: internal/analyze/analyze.go:85
  evidence: internal/analyze/analyze_test.go:1250
  evidence: internal/cli/cli_test.go:1270
- ac-2 — MET: renderProvenance is called directly after the Findings heading and before the status grouping, emitting one line carrying backend, model, rubric and ingest date prefixed "as declared at ingest"; the report test pins both the exact string and the heading < provenance < first-group ordering
  evidence: internal/report/report.go:171
  evidence: internal/report/report.go:267
  evidence: internal/report/report_test.go:1042
  evidence: internal/report/report_test.go:1061
- ac-3 — MET: ParseRecords returns a nil *Provenance when no provenance line is present rather than erroring, report renders "_Provenance: not recorded._" for that case, and review's walk over a provenance-free fixture is byte-identical to one carrying the record
  evidence: internal/analyze/analyze.go:233
  evidence: internal/report/report.go:247
  evidence: internal/report/report_test.go:1078
  evidence: internal/review/review.go:128
  evidence: internal/review/review_test.go:1019
  evidence: internal/analyze/analyze_test.go:1384
- ac-4 — MET: with no -backend the CLI prints the "recording the provenance as backend not recorded" notice to stderr before reading the answer and NewProvenance writes backend "unrecorded"; the CLI test asserts the notice is on stderr not stdout, that the record says unrecorded and carries no model key, and the analyze parity test shows the finding lines are unchanged
  evidence: internal/cli/cli.go:475
  evidence: internal/analyze/analyze.go:152
  evidence: internal/cli/cli_test.go:1315
  evidence: internal/analyze/analyze_test.go:1283
- ac-5 — MET: the verdict-overwrite guard is unchanged and runs inside commitFindings under the lock, independent of the provenance argument; TestIngestRefusesVerdictFileWithProvenanceFlags re-ingests a verdict-bearing file with different backend and model flags, gets the existing "refusing to overwrite" message, and asserts the file is byte-identical before and after, so the existing provenance line is untouched
  evidence: internal/analyze/ingest.go:202
  evidence: internal/analyze/analyze_test.go:1329
- ac-6 — MET: docs/how-to/analyse-locally.md walks the four steps in order — analyze -out, the local runner, analyze -ingest -backend local, report — and privacy.md now states the "no session content leaves the machine at any step" conclusion with the conditional attached to exactly that route
  evidence: docs/how-to/analyse-locally.md:12
  evidence: docs/how-to/analyse-locally.md:19
  evidence: docs/how-to/analyse-locally.md:37
  evidence: docs/how-to/analyse-locally.md:48
  evidence: docs/how-to/analyse-locally.md:78
  evidence: docs/explanation/privacy.md:16

Gap audit:
- honoured:
  - analyze -ingest takes -backend local|cloud and an optional -model NAME, and writes that declaration into the session as the first line of findings.jsonl
    evidence: internal/cli/cli.go:398
    evidence: internal/analyze/ingest.go:185
  - report prints the declaration under its Findings heading, so the artefact people read carries the claim too
    evidence: internal/report/report.go:171
    evidence: internal/report/report.go:242
  - the documentation sets out the fully local route end to end: emit the request, run it against a locally hosted model, ingest with -backend local
    evidence: docs/how-to/analyse-locally.md:12
    evidence: docs/README.md:4
  - no change to how the model work is done; the CLI still never calls a model, holds no keys, and adds no network dependency, and no flag selects a backend
    evidence: internal/analyze/analyze.go:152
    evidence: docs/reference/cli.md:202
  - every surface that renders the record says Testimony cannot verify where the request ran
    evidence: internal/report/report.go:267
    evidence: docs/how-to/analyse-locally.md:100
    evidence: docs/reference/session-directory.md:136
  - the record carries only a backend word, a model name, a rubric version, and a date — no hostname, address, or machine name
    evidence: internal/analyze/analyze.go:85
    evidence: docs/reference/session-directory.md:140
  - the same provenance question for the drafting layer's tests.jsonl is out of scope and stays out
    evidence: internal/drafttests/drafttests.go:420
    evidence: internal/drafttests/drafttests_test.go:401
  - no stated quality floor for a local backend; the retained verdicts are left as the instrument
    evidence: docs/how-to/analyse-locally.md:105
- diverged:
  - "Passing neither flag changes nothing but what is recorded" — in fact the ingest success line on stdout changed for every invocation, from "(all unverified)" to "(all unverified; backend not recorded)", so a caller parsing that documented line sees a different string even with no flags passed; the divergence is documented rather than hidden
    evidence: internal/cli/cli.go:495
    evidence: internal/cli/cli.go:845
    evidence: docs/reference/cli.md:230
  - "a findings.jsonl assembled by hand or by another tool may carry none and is read as 'not recorded' rather than refused" — tolerance holds for zero records and for an uninterpretable backend, but a hand-assembled file carrying two interpretable provenance records is now a hard read error rather than a tolerated read
    evidence: internal/analyze/analyze.go:320
    evidence: internal/analyze/analyze_test.go:1425
    evidence: docs/reference/session-directory.md:152
  - the committed example session's findings.jsonl gained a backend:unrecorded provenance line, so the shipped sample now declares an unrecorded backend rather than carrying no record at all
    evidence: examples/sample-session/findings.jsonl:1
- missing: (none)

Scope-condition dispositions:
- cond-2609150759135103 — survived: the delivery adds no install, configuration, or network path to a model: the how-to hands request.txt to whatever the operator already runs, and NewProvenance only records a word the operator typed
  evidence: docs/how-to/analyse-locally.md:37
  evidence: docs/how-to/analyse-locally.md:105
  evidence: internal/analyze/analyze.go:152
- cond-2609150759137440 — narrowed: analyze -ingest is indeed the only writer and a file with no record reads as "not recorded", but the reader added a hard refusal for a file carrying two interpretable provenance records, so "read rather than refused" no longer holds for every hand-assembled file
  narrowing: holds for a foreign findings.jsonl carrying zero provenance records or one whose backend is outside the closed set (both read as "not recorded"); a file carrying two interpretable provenance records is refused outright by ParseRecords, which fails report, review and draft-tests on that file
  evidence: internal/analyze/analyze.go:320
  evidence: internal/analyze/analyze.go:297
  evidence: internal/report/report.go:247
  evidence: internal/analyze/analyze_test.go:1425
- cond-2609150759138300 — survived: nothing in the delivery attempts to observe or verify the backend, and every rendering surface labels the record a declaration: the report line, the reference pages, the how-to, and the privacy explanation
  evidence: internal/report/report.go:267
  evidence: docs/reference/cli.md:225
  evidence: docs/how-to/analyse-locally.md:100
  evidence: docs/explanation/privacy.md:20
- cond-2609150759134348 — survived: the Provenance struct is closed at kind, rubric, backend, model and at — no host, address, path or machine field — and the model free text is bounded at 200 runes and sanitised at every sink
  evidence: internal/analyze/analyze.go:85
  evidence: internal/analyze/analyze.go:47
  evidence: internal/cli/cli.go:845
  evidence: docs/reference/session-directory.md:140
## Grounds

- pursued: a findings file that states which backend and model produced it, on which side of the machine boundary, is what an ethics reviewer needs and what makes the local route auditable; what would show it wrong is operators leaving the backend unrecorded on most sessions, which the printed notice is there to surface
