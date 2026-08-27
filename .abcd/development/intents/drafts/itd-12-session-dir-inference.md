---
id: itd-12
slug: session-dir-inference
spec_id: null
kind: null
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

## Open Questions

- Should inference also cover `record`'s or `demo`'s own `-out` root (running from inside the sessions root rather than inside one specific session), or is that a different, unrelated ergonomic gap?
- Does silently defaulting to the current directory risk a mistaken run against the wrong session for an operator who forgot which directory they were in — should the command print which session it inferred, every time, to make the implicit choice visible?

## Audit Notes

_Empty. Populated by intent-fidelity-reviewer when intent moves to shipped/._
