---
id: itd-4
slug: reference-capture
spec_id: null
severity: minor
---

# Capture What You Admire in Other People's Apps

## Press Release

> **Testimony captures reference sessions of third-party applications.** The same rig — screen recording plus narration — runs against apps we don't own: no instrumentation, so keyframes extracted at utterance timestamps stand in for the event stream, with multimodal identification answering "what UI pattern is on screen while the speaker says this?". The analysis rubric shifts from defect-finding to preference elicitation, and tagged observations accumulate into a pattern library — each pattern recording the app, whether it was liked or disliked, why, a keyframe reference, and where it might apply.
>
> "When I say 'I want the command palette to feel like this one, except for the ranking', that opinion used to evaporate by the next design meeting," said Carol, product designer. "Now it's a tagged pattern with a clip and my exact words, sitting in the library when requirements are being written."

## Why This Matters

Design preferences gathered from existing applications are requirements input of the most concrete kind — but spoken admiration is the most perishable evidence there is. Reference capture gives those reactions the same evidentiary treatment as usability findings: timestamped, quoted, and anchored to what was actually on screen. Because the target app's internals are unreachable, the transcript carries the semantic load and video supplies the referents — which is exactly the fallback channel the pipeline already has.

## What's In Scope

- Reference-mode capture with the standard rig: screen recording and narration, no target-app instrumentation.
- Keyframe extraction at utterance timestamps plus multimodal identification of the on-screen referent.
- The preference-elicitation rubric: `{pattern, app, liked|disliked, why, screenshot_ref, applicability}`.
- The pattern library as a tagged, browsable corpus of patterns with keyframes, feeding requirements work.
- Optional event capture via a browser extension when the reference app is a website.

## What's Out of Scope

- Codebase mapping — there is no codebase to map to.
- Publishing keyframes or clips of third-party apps outside the private corpus (recordings are for private research and reference use).
- Automated competitive analysis or scraping; every session is a person narrating deliberately.
- Native-app event instrumentation of any kind.

## Acceptance Criteria

- **Given** a reference session of a third-party app, **when** the analysis pass runs, **then** findings follow the preference schema with pattern, app, liked/disliked, rationale, and a keyframe reference.
- **Given** an utterance about something on screen, **when** a keyframe is extracted at its timestamp, **then** the identified UI pattern is attached to the finding without the event stream existing.
- **Given** several analysed reference sessions, **when** Carol browses the pattern library, **then** patterns are retrievable by tag with their keyframes and quotes intact.
