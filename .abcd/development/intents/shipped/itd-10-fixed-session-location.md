---
id: itd-10
slug: fixed-session-location
spec_id: spc-2609150752365788
kind: standalone
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

## Scope Conditions

- The home directory resolves for the process running the command; with no home to resolve, the default root does not exist and the command refuses rather than guessing one. <!-- cond: cond-2609150800063443 -->
- The default root is created on demand exactly as an explicit `-out` root is today, including any missing parent, and a root that cannot be created is a runtime failure naming the path it tried. <!-- cond: cond-2609150800069939 -->
- The operator can write under their own home directory; a read-only or quota-exhausted home makes the default root unusable, and `-out` is the answer. <!-- cond: cond-2609150800061963 -->
- `-out DIR` is the only override in scope: the default is not read from an environment variable, a configuration file, or any persisted state. <!-- cond: cond-2609150800068368 -->
- Sessions captured under the old relative default stay where they are; nothing moves them, and every command that operates on an existing session still takes the path it is given. <!-- cond: cond-2609150800063485 -->
- The path is written `~/Testimony/sessions` wherever it appears in text, and the printed session directory stays the real, resolved path the operator can act on. <!-- cond: cond-2609150800063390 -->

## Open Questions

- What is the right fixed default path — `~/Testimony/sessions`, an XDG-style `~/.local/share/testimony/sessions`, or something else? Platform conventions differ between macOS and Linux.
- Is the existing `-out` flag override sufficient, or does this need a persistent override too (an environment variable or config file)?
- Changing an existing default is a behaviour change, not a bug fix — does it need its own CHANGELOG entry and a migration note for anyone who scripted against the old relative `sessions/` default?

## Audit Notes

<!-- abcd-review: INGESTED receipt=rcp-5ef93c710261 -->
Fidelity review — receipt rcp-5ef93c710261 (verifier abcd:intent-auditor claude-opus-5[1m]).

Provenance: abcd:intent-auditor@claude-opus-5[1m] · rubric_hash sha256:3540a217931626f34372df0e4e460ac8cb662b950d8f734afdc24f102e7354bc · prompt_hash sha256:aafe064e67fdd5f51b57b7164fe104a47c91afa806b5cb04923995a4adce9871
Input attestations: diff:HEAD..working tree@sha256:af2e5d4ac801d4002d04fbd12ae4e071c7830eddc8e0653880ff0cc52bfa7102;

Acceptance rollup: MET 2 · MET_WITH_CONCERNS 1 · NOT_MET 0 · INCONCLUSIVE 0

Per-criterion verdicts:
- ac-1 — MET: both capture commands register defaultSessionRoot()'s home-anchored result as the -out default, and a passing test shows bare record tries < home>/Testimony/sessions and leaves the working directory empty
  evidence: internal/cli/cli.go:855
  evidence: internal/cli/cli.go:171
  evidence: internal/cli/cli.go:77
  evidence: internal/cli/cli_test.go:1268
  evidence: internal/demo/demo.go:104
- ac-2 — MET: the flag default is the only thing that changed, so a given -out reaches session.Create verbatim; the passing test shows record -out sessions uses the relative root, never touches the home root, and that an unresolvable home does not interfere with any explicit -out invocation
  evidence: internal/cli/cli_test.go:1302
  evidence: internal/cli/cli.go:192
  evidence: internal/cli/cli.go:206
- ac-3 — MET_WITH_CONCERNS: creation is session.Create's unchanged os.MkdirAll(outRoot, 0o755), the identical call an explicit -out root has always used and one that creates the missing Testimony parent; concern: no delivered test demonstrates the default root actually being created, every new default-root test pins a failure path, and the pre-existing TestCreate is handed an already-existing root
  evidence: internal/session/session.go:76
  evidence: internal/record/record.go:138
  evidence: internal/cli/cli_test.go:1268
  evidence: internal/session/session_test.go:16

Gap audit:
- honoured:
  - a fixed default output root ~/Testimony/sessions for record and demo, used whenever -out is not given, defined once so the two cannot drift
    evidence: internal/cli/cli.go:855
    evidence: internal/cli/cli.go:835
    evidence: internal/cli/cli_test.go:1248
  - the existing -out DIR flag continues to override the default per invocation, unchanged
    evidence: internal/cli/cli_test.go:1302
    evidence: internal/cli/cli.go:171
  - docs (README, docs/reference/cli.md, session-directory.md, the tutorials and how-to guides) updated to state the new default and how to override it; no page still shows the old relative default
    evidence: docs/reference/cli.md:39
    evidence: docs/reference/cli.md:41
    evidence: README.md:96
    evidence: docs/reference/session-directory.md:3
  - merge, report, analyze and review are unaffected, still operating on a session they are given or infer
    evidence: internal/cli/cli.go:904
    evidence: CHANGELOG.md:98
  - the behaviour change is recorded as one, naming the old default, the new root, and the one-flag migration
    evidence: CHANGELOG.md:87
    evidence: CHANGELOG.md:89
- diverged:
  - the press release describes only a change of default location, but the delivery adds a new refusal: with -out omitted and no resolvable home, record and demo now exit 2 where they previously succeeded against a relative sessions/ root — a signed-off divergence, carried by the intent's own scope condition and by the spec rather than by the press release text
    evidence: internal/cli/cli.go:91
    evidence: internal/cli/cli.go:192
    evidence: internal/cli/cli_test.go:1355
  - the ~/Testimony/sessions spelling is not used in every text surface: the -out flag registers the resolved path, so testimony record -h prints (default "/< home>/Testimony/sessions") — a deliberate, spec-recorded choice, but a delta from the condition's 'wherever it appears in text'
    evidence: internal/cli/cli.go:171
    evidence: internal/cli/cli.go:77
- missing:
  - no delivered test demonstrates a session directory actually landing under the fixed default root: the new default-root coverage is entirely failure-path (an un-creatable root, an unresolvable home), and demo's creation path is not exercised end to end at all
    evidence: internal/cli/cli_test.go:1268
    evidence: internal/cli/cli_test.go:1355
    evidence: internal/session/session_test.go:16

Scope-condition dispositions:
- cond-2609150800063443 — survived: an unresolvable home with -out omitted exits at the usage status naming the root, the reason and the flag, and never falls back to a relative root; the check is scoped by fs.Visit to the omitted-flag path
  evidence: internal/cli/cli.go:91
  evidence: internal/cli/cli.go:867
  evidence: internal/cli/cli_test.go:1355
- cond-2609150800069939 — survived: the resolved root reaches session.Create as the same opaque string an explicit -out always did, so creation is the unchanged os.MkdirAll that also makes the missing Testimony parent, and a root that cannot be created stays a runtime failure naming the path it tried
  evidence: internal/session/session.go:76
  evidence: internal/cli/cli_test.go:1268
- cond-2609150800061963 — survived: an unusable default root is reported at exit 1 naming that path and -out remains a fully home-free escape hatch, proven with HOME cleared; the unusability was exercised by a file planted at the root rather than by a read-only home, but the effect path is the same one
  evidence: internal/cli/cli_test.go:1268
  evidence: internal/cli/cli_test.go:1302
- cond-2609150800068368 — survived: the default comes only from os.UserHomeDir joined with Testimony/sessions; no environment variable, configuration file or persisted state is read anywhere on the path, and the docs state -out as the sole override
  evidence: internal/cli/cli.go:855
  evidence: docs/reference/cli.md:43
- cond-2609150800063485 — survived: the change is confined to the two capture commands' -out default and the refusal; no relocation or migration code exists, resolveSession is untouched, and every existing-session command still takes the path it is given or infers
  evidence: internal/cli/cli.go:904
  evidence: CHANGELOG.md:98
- cond-2609150800063390 — narrowed: the ~ spelling holds in the usage block, the reference docs, the README and the refusal message, and the start-up output still prints the real resolved directory; but the -out flag default is registered resolved, so -h prints an absolute home path instead
  narrowing: holds for prose surfaces — the usage block, the documentation and the refusal — and for the printed session directory; it does not hold for the -out flag default in `testimony record -h` / `testimony demo -h`, which deliberately shows the resolved absolute path
  evidence: internal/cli/cli.go:171
  evidence: internal/cli/cli.go:835
  evidence: internal/record/record.go:150
  evidence: internal/cli/cli_test.go:1382
## Grounds

- pursued: one fixed, discoverable default root means an occasional operator always knows where a session went; a wrong guess would show as operators routinely overriding it with -out to keep project-local capture
