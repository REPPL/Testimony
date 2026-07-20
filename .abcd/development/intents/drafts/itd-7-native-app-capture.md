---
id: itd-7
slug: native-app-capture
spec_id: null
kind: null
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
---

# Study the Mac and iPhone Apps, Not Just the Web Ones

## Press Release

> **Testimony captures think-aloud sessions against native macOS and iOS applications.** Screen capture comes from ScreenCaptureKit on the Mac and from `simctl` on the iOS simulator or a cabled device, with narration recorded on the same clock as always. Native apps expose no interaction stream, so the transcript carries the semantic load and keyframes extracted at utterance timestamps supply the referents — the same fallback channel reference capture already relies on. Later, a debug build can log its own events and upgrade a native session to the full evidence model.
>
> "The Mac app is the one people actually touch, and it was the one I had the least evidence about," said Alice, usability researcher. "Now a native session produces the same report as a browser session — a bit less precise about which control they hit, but their words and the screen at that second are there."

## Why This Matters

macOS is the primary target and a macOS app layer is planned, so the tool would otherwise be unable to study the platform it is built for. Native capture is deliberately the weaker evidence story: without instrumentation there is no selector, no route, and no event to join against, so the join narrows to "what was on screen when they said this". Recording that honestly — as a keyframe-anchored session that yields findings but not `code_refs` — is better than either pretending native sessions are equivalent to instrumented ones or leaving the platform unstudied.

Separating this from terminal capture (itd-6) keeps the two evidence models from being conflated: a `.cast` stream is a real interaction stream that greps to source, while a native session in phase 1 is video plus voice. They deserve different acceptance bars.

## What's In Scope

- macOS capture via ScreenCaptureKit, and iOS capture via `xcrun simctl io booted recordVideo` for the simulator and a cabled-device path.
- Keyframe extraction at utterance timestamps as the referent channel, reusing the mechanism reference capture defines rather than inventing a second one.
- A session that merges and reports with no interaction stream present — a transcript-only timeline is a first-class outcome, not a degraded error.
- Recording in the manifest that a session is keyframe-anchored, so downstream steps and readers know which evidence channel backs a finding.

## What's Out of Scope

- Debug-build event instrumentation (`NSApplication.sendEvent` / `UIWindow.sendEvent` overrides, gesture-recogniser logging) — noted as the later upgrade path, not built here.
- Codebase mapping from native sessions; without accessibility identifiers there is no reliable anchor, and itd-3 already fences this out.
- Capturing third-party native applications, which is reference capture (itd-4) under a different rubric and different copyright footing.
- The macOS app layer that wraps the CLI core — a delivery surface, not a capture target.

## Acceptance Criteria

- **Given** a macOS application session recorded with narration and no interaction stream, **when** `merge` and `report` run, **then** a transcript-only timeline and report are produced without error.
- **Given** an utterance whose referent is ambiguous in text alone, **when** a keyframe is requested at that timestamp, **then** a frame extracted from the session recording is available as that finding's evidence.
- **Given** a keyframe-anchored session, **when** findings are produced, **then** each records that its evidence channel is a keyframe rather than an interaction event.

## Open Questions

- Is the keyframe path sufficient in practice, or does the absence of an event stream make native findings too vague to act on? This is the open question the architecture note poses, and only real sessions will answer it.
- Does iOS device capture over a cable share a clock with the Mac reliably enough for the join window, or does it need the spoken-marker correction?
- Should accessibility identifiers, where an app already sets them, be harvested as a partial anchor — a middle path between no instrumentation and a debug build?

## Audit Notes

_Empty. Populated by intent-fidelity-reviewer when intent moves to shipped/._
