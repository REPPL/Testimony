<!-- BEGIN ABCD -->
<!--
  Managed by abcd (Agent-Based Configuration for Development).
  Do NOT hand-edit content inside the abcd-managed fences — `/abcd:ahoy`
  silently overwrites this block on drift (per itd-3). Per-repo rule
  customisation goes in <repo>/.abcd/rules.json instead.
-->

## abcd rule loader

This repository uses the abcd modular rules loader. On `UserPromptSubmit`, a hook
recall-matches the prompt against keyword triggers declared in the plugin-bundled
default domains and `<repo>/.abcd/rules.json`, and injects only the matched
domain rules into context — instead of force-loading the full ruleset every turn.
A prompt that matches no domain injects nothing (zero added tokens).

- Inspect rules: `abcd rules` renders the active set; `abcd rules <DOMAIN>`
  (case-insensitive) scopes to one domain.
- Per-repo overrides: edit `<repo>/.abcd/rules.json`. It is
  `{"schema_version": 1, "disabled": false, "domains": {}}` — add a domain key to
  override a default per-field (e.g. `{"ROADMAP": {"state": "dormant"}}` silences
  it while keeping its rules) or to declare a custom domain
  (`{"recall": [...], "rules": [...]}`).
- Kill switch: set `"disabled": true` at the top of `.abcd/rules.json`.
- Explicit activation: start a prompt with `*<DOMAIN>` (e.g. `*COMMITTING`,
  `*PII`) to inject that domain unconditionally — overrides a `dormant` state,
  but never the kill switch.

### Default domains

`COMMITTING`, `DOCUMENTATION`, `ROADMAP`, `ISSUES`, `INTENTS`, `LIFEBOAT`, `PII`,
`OPINIONS`. Each carries recall keywords and its rules, bundled in the abcd
binary; a repo overrides them per-field via `.abcd/rules.json`. `OPINIONS`
points at the canonical conventions under `.abcd/development/principles/` rather
than copying them.

### Reset triggers

`SessionStart` and `PreCompact` clear the per-session dedup ledger, so a matched
domain re-injects on the next prompt (the event-driven refresh that recovers
after compaction). Within a session the hook does not re-inject unchanged rules.

For internals see `.abcd/development/brief/05-internals/03-configuration.md`.

<!-- END ABCD -->

# Testimony

Testimony captures usability evidence, on the record.

## Current state

v0.4.0. A Go CLI (`testimony`, standard library only) whose seven pipeline
commands are all implemented and dispatched from `internal/cli` behind the
`cmd/testimony` entry point: `record` and `demo`
(capture), `transcribe`, `merge`, `report`, and the analysis layer `analyze`
and `review` — plus `version` and `help`. The model work is host-delegated —
the CLI never calls a model, holds no keys, and adds no network dependency. The user-facing documentation
is [`docs/README.md`](docs/README.md); the exact command and file contracts are
[`docs/reference/cli.md`](docs/reference/cli.md) and
[`docs/reference/session-directory.md`](docs/reference/session-directory.md).

Orientation lives in [`.abcd/work/CONTEXT.md`](.abcd/work/CONTEXT.md).

## Build, test, and checks

Run from the repo root. Requires Go (version per `go.mod`).

```bash
go build -o testimony ./cmd/testimony   # build the CLI
gofmt -l .                              # format gate: any output needs gofmt -w
go vet ./...                            # static checks
go test ./...                           # unit tests
go test -race ./...                     # race-enabled
go test -run TestEventsNearWindow ./internal/timeline/   # a single test
./testimony merge  -session examples/sample-session   # pipeline smoke:
./testimony report -session examples/sample-session   #   writes timeline.jsonl + report.md
sh -n install.sh && bash -n install.sh  # installer syntax
```

CI (`.github/workflows/ci.yml`) runs all of the above on every push and pull
request, plus checks with no local command above: installer flag-handling
tests (`--help`/`--dir`/`--version`/`--bogus`), a compile-only cross-check for
the other release platforms, a full-history `gitleaks` secret scan, and a
`zizmor` workflow-security audit.

<!-- working-conventions 2026-07-17 -->

## Working conventions

- **Three-tier working state.** `.abcd/development/` — durable record,
  committed: dated plans, research notes, and ADRs (MADR format, sequential
  `NNNN`, under `decisions/adrs/`). `.abcd/work/` — shared working state,
  committed: `CONTEXT.md` (current orientation) and `DECISIONS.md`
  (append-only, one-line decision log). `.abcd/.work.local/` — local
  ephemeral, gitignored: `NEXT.md` handover, `scratch/`, `logs/` — runtime
  artefacts go here, never in tracked directories.
- **Decisions.** One dated line in `.abcd/work/DECISIONS.md`; promote
  architecture-shaping decisions to ADRs.
- **Docs.** `docs/` is user-facing only, one Diátaxis type per page, present
  tense only. British English in prose; US English in code and commits. No
  stray root markdown beyond the standard set.
- **Privacy.** No absolute local paths, hostnames, usernames, emails,
  tokens, or private repository names in anything committed; repo-relative
  paths only. Commits use the pinned identity in
  `.abcd/config/identity.json` (repo-local git config matches it).
- **Personas.** Examples and user stories use Alice, Bob, and Carol — never
  other names.
- Refer to the maintainer as they/them in every artefact.
- **Git.** Never commit or push without being asked; substantive work goes
  on a branch and PR. New dependencies need explicit sign-off first.

<!-- /working-conventions -->

## Attribution

AI-assisted commits carry an `Assisted-by:` trailer, kernel format
(e.g. `Assisted-by: Claude:claude-fable-5`) — disclosure, not authorship.
Never `Co-Authored-By:` for AI: it asserts an authorship the tool does not
hold. The human is the author of record, responsible for all AI-assisted
output.
