---
id: itd-1
slug: record-command
spec_id: spc-1
severity: major
kind: standalone
---

# One Command Starts the Whole Session

## Press Release

> **Testimony ships `testimony record` — a managed capture launcher that starts a think-aloud session in one command.** It launches the screen and microphone recorders, writes the session `manifest.json` (app under test, build hash, participant pseudonym, task list, consent reference), and stamps the shared `t0` wall-clock anchor that every downstream stream aligns to. The QuickTime hand-off — start the recorder by hand, note the time, hope the clocks agree — is gone; a session directory arrives ready for `transcribe`, `merge`, and `report`.
>
> "Before, every session began with a checklist of things I could get wrong — which recorder, which folder, what time it actually started," said Alice, usability researcher. "Now I type one command, talk through the tasks, and stop. The manifest and the t0 anchor are just there."

## Why This Matters

The rest of the pipeline stands or falls on synchronisation: an utterance can only be joined to a click if both sit on one wall clock. Manual capture makes `t0` a human's job, and a mis-noted start time silently corrupts every downstream join. A managed launcher makes the anchor a recorded fact rather than a recollection, and turns session setup from a ritual into a command — which is what makes running many small sessions cheap enough to be routine.

## What's In Scope

- `testimony record` launcher: starts screen and audio capture, creates the session directory, writes `manifest.json` with `t0` (epoch ms), app under test, build/commit hash, participant pseudonym, task list, and consent reference.
- The spoken-marker fallback convention ("session start") documented and prompted for, as the belt-and-braces anchor if a stream loses its absolute clock.
- Clean session stop that finalises the manifest and leaves artefacts named as the session layout expects (`screen.mp4`, `audio.wav`, event stream files).

## What's Out of Scope

- Transcription, merging, analysis, or reporting — those verbs already exist or are separate intents.
- In-app interaction instrumentation (rrweb snippets, asciinema wrappers) beyond invoking recorders that are present.
- Any GUI layer; this is the CLI capture surface.
- Third-party app capture specifics (itd-4) and participant consent tooling (itd-5).

## Acceptance Criteria

- **Given** a configured machine, **when** Alice runs `testimony record` with an app and task list, **then** a session directory exists containing a `manifest.json` with a populated `t0_epoch_ms`, and screen plus audio capture are running.
- **Given** a running session, **when** Alice stops it, **then** the recorders terminate cleanly and the session directory contains the capture artefacts under their expected names.
- **Given** a completed session, **when** `testimony transcribe` and `testimony merge` run over it, **then** they consume the manifest's `t0` without any manual clock entry.
