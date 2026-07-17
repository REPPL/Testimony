# CONTEXT

Shared team/agent orientation — what you need to know *right now* to be
useful. Short and pointer-heavy; durable design truth lives in
[`../development/`](../development/), personal session state in
`../.work.local/NEXT.md` (local, never committed).

## What this repo is

Testimony captures usability evidence, on the record. The repository is at
the foundations stage: the working conventions and record layout are in
place; the initial codebase is being implemented.

## Live constraints / sharp edges

- Build, test, and lint commands are not yet established. When the first
  code lands, record the exact commands in `AGENTS.md` — a fresh agent
  session must be able to build and test from `AGENTS.md` alone.
- The commit identity is pinned in `.abcd/config/identity.json` and set as
  repo-local git config; commits must use it (no machine hostnames in
  committed metadata).
- Never commit or push without the maintainer asking; substantive work goes
  on a branch and PR.
