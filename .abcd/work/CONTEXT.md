# CONTEXT

Shared team/agent orientation — what you need to know *right now* to be
useful. Short and pointer-heavy; durable design truth lives in
[`../development/`](../development/), personal session state in
`../.work.local/NEXT.md` (local, never committed).

## What this repo is

Testimony captures usability evidence, on the record. A Go CLI
(`testimony`, standard library only) with `record` (managed capture),
`demo`, `transcribe`, `merge`, and `report` working end-to-end, plus the
first-pass analysis layer — `analyze` (emit a host-delegated analysis
request, then validate the answer into `findings.jsonl`) and `review`
(record human verdicts, appended non-destructively). The oracle is
host-delegated: the CLI never calls a model, holds no keys, and adds no
network dependency; every finding is born `unverified` and ingest is the sole
validation boundary. Next is codebase mapping (itd-3), then Mode B / the
pattern library (itd-4). Design in `docs/architecture.md`.

## Live constraints / sharp edges

- Build, test, and check commands live in `AGENTS.md` — a fresh agent
  session must be able to build and test from `AGENTS.md` alone.
- The commit identity is pinned in `.abcd/config/identity.json` and set as
  repo-local git config; commits must use it (no machine hostnames in
  committed metadata).
- Never commit or push without the maintainer asking; substantive work goes
  on a branch and PR.
