---
id: itd-10
slug: fixed-session-location
spec_id: null
kind: null
suggested_kind: null
reclassification_history: []
builds_on: []
severity: minor
---

# Every Session Lands in the Same Place, However You Ran It

## Press Release

> **Testimony now defaults every new session to one fixed, discoverable location** (`~/Testimony/sessions`) instead of a `sessions/` folder relative to whatever directory `record` or `demo` happened to be run from. The existing `-out` flag still overrides it for anyone who wants project-scoped capture; nothing about `merge`, `report`, `analyze`, or `review` changes, since they already take an explicit `-session DIR`.
>
> "I ran `demo`, closed the terminal, and twenty minutes later couldn't remember which folder I'd been in when the session was created," said Bob, capturing their first session. "Once it always landed in the same place, I stopped worrying about it."

## Why This Matters

`record`'s and `demo`'s default `-out` is the relative path `sessions`, created under whatever the current working directory happens to be at the moment of capture. That is a reasonable default for project-scoped tooling invoked from inside a consistent repository, but it splinters session history across every directory a first-time or occasional operator happened to be sitting in — discoverable later only if they remember. A fixed default trades a little of that lightweight per-project convenience for predictability: sessions accumulate in one place unless an operator deliberately opts into a project-local `-out`.

## What's In Scope

- A fixed default output root for `record` and `demo` (for example `~/Testimony/sessions`), used whenever `-out` is not given.
- The existing `-out DIR` flag continues to override the default per invocation, unchanged.
- Docs (`README.md`, `docs/reference/cli.md`, the tutorials) updated to state the new default and how to override it.

## What's Out of Scope

- Any config-file-based persistent override; the existing `-out` flag remains the only override mechanism this intent assumes.
- Migrating or relocating session directories captured under the old relative default before this ships.
- `merge`, `report`, `analyze`, and `review`, which already require an explicit `-session DIR` and are unaffected either way.

## Acceptance Criteria

- **Given** no `-out` flag, **when** `record` or `demo` creates a new session, **then** the session directory is created under the fixed default location, not relative to the current working directory.
- **Given** an explicit `-out DIR` flag, **when** `record` or `demo` runs, **then** the session directory is created under `DIR` exactly as today.
- **Given** the fixed default location does not yet exist, **when** a session is created, **then** the directory is created automatically, matching today's root-creation behaviour for `-out`.

## Open Questions

- What is the right fixed default path — `~/Testimony/sessions`, an XDG-style `~/.local/share/testimony/sessions`, or something else? Platform conventions differ between macOS and Linux.
- Is the existing `-out` flag override sufficient, or does this need a persistent override too (an environment variable or config file)?
- Changing an existing default is a behaviour change, not a bug fix — does it need its own CHANGELOG entry and a migration note for anyone who scripted against the old relative `sessions/` default?

## Audit Notes

_Empty. Populated by intent-fidelity-reviewer when intent moves to shipped/._
