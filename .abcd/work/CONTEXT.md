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
(record human verdicts, appended non-destructively) — and the
regression-test drafting layer, `draft-tests` (emit a drafting request
carrying each confirmed finding and its event window, validate the answer
into `tests.jsonl`, render the accepted drafts as a Markdown test plan)
with `review -kind tests` for the accept / edit / reject pass. The oracle is
host-delegated: the CLI never calls a model, holds no keys, and adds no
network dependency; every finding is born `unverified`, every drafted test is
born `proposed`, and each ingest is the sole validation boundary for its own
answer. Next is codebase mapping (itd-3), then Mode B / the
pattern library (itd-4). Command and file contracts in
[`../../docs/reference/cli.md`](../../docs/reference/cli.md) and
[`../../docs/reference/session-directory.md`](../../docs/reference/session-directory.md).

## Live constraints / sharp edges

- Build, test, and check commands live in `AGENTS.md` — a fresh agent
  session must be able to build and test from `AGENTS.md` alone.
- The commit identity is pinned in `.abcd/config/identity.json` and set as
  repo-local git config; commits must use it (no machine hostnames in
  committed metadata).
- Never commit or push without the maintainer asking; substantive work goes
  on a branch and PR.
