---
schema_version: 1
id: "iss-2609120444398227"
slug: "the-committed-pre-commit-name-guard-reads-its-private-layer"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "bughunt-round-48"
origin: researcher-authored
production_mode: hand-written
found_at: ".githooks/pre-commit"
---

The committed pre-commit name guard reads its private layer from the checkout's own .abcd/.work.local/private-names.txt, so a git worktree of this repo (which has its own untracked .abcd/.work.local tier) commits with the private guard INACTIVE unless the file is copied in by hand; abcd could resolve the private layer per repository (keyed on the root-commit SHA under ~/.abcd) rather than per checkout
