# Purpose

Testimony captures manual test and demo sessions the way usability research
does it — a screen recording plus concurrent *think-aloud* narration — and
turns them into machine-readable, time-aligned records: a word-timestamped
transcript merged with a timestamped interaction stream. From there, an
analysis layer derives findings (bugs, friction, inconsistencies,
preferences) and maps them back to the codebase. The name describes the
artefact the pipeline produces — a first-person spoken account offered as
evidence — and happens to begin with *test*.

## Operating modes

Two modes share the same capture rig:

- **Mode A — testing our own applications.** A test user (initially the
  maintainer) works through tasks in one of our applications while thinking
  aloud. The goal is structured findings — bugs, friction points,
  inconsistencies, preferences — each anchored to a moment in time, a UI
  element, and ultimately a location in the codebase.
- **Mode B — reference capture of existing applications.** The maintainer or
  a participant demos third-party applications while narrating what they
  like, dislike, or find notable. The goal is a tagged design-preferences
  corpus feeding requirements and design decisions. Mode B has no access to
  the target app's internals, so the transcript carries the semantic load and
  keyframes extracted from the video supply the referents
  (see [`04-analysis.md`](04-analysis.md)).

## Method grounding

The capture method is the concurrent think-aloud protocol, the standard
verbalisation technique in usability research (Ericsson & Simon's *Protocol
Analysis*; Nielsen's usability engineering tradition). What is new is not the
method but the analysis economics: transcription and first-pass coding —
historically the expensive part — are automated, while the human retains the
final judgement.

This is an active research frontier. Relevant evidence as of mid-2026:

- A CHI 2026 randomised controlled trial compares an agentic audio moderator
  against a human moderator in think-aloud usability testing
  ([ACM DL](https://dl.acm.org/doi/10.1145/3772318.3791653)).
- An IUI 2025 preliminary study examines LLMs for usability-testing analysis
  ([ACM DL](https://dl.acm.org/doi/10.1145/3708557.3716341)); related work
  compares human and AI usability evaluators
  ([Springer](https://link.springer.com/chapter/10.1007/978-3-032-30044-7_9))
  and explores LLM-simulated usability testing (UXAgent,
  [CHI 2025](https://dlnext.acm.org/doi/10.1145/3706599.3719729)).
- In industry, LLM analysis of interaction event streams is in production use
  — e.g. Decipher AI summarises rrweb session recordings with LLMs
  ([write-up](https://getdecipher.com/blog/generating-rrwb-session-summaries)).

The consistent finding: LLMs are effective *first-pass* coders of think-aloud
data but both miss and occasionally invent problems. **Design stance:
AI-as-second-coder.** Every AI-generated finding carries `status: unverified`
until a human confirms or rejects it, and the verification decision is
retained — which also yields precision/recall proxies over time. Validity
limits and the ethics posture are constraints, not commentary: see
[`../02-constraints/04-ethics.md`](../02-constraints/04-ethics.md).
