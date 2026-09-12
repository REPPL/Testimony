---
schema_version: 1
id: "iss-2609120446224860"
slug: "analyze-requires-an-utt-prefix-for-a-finding-s-spoken-anchor"
severity: "nitpick"
category: "observation"
source: "user-observation"
found_during: "bughunt-round-48"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/analyze/validate.go"
---

analyze requires an utt- prefix for a finding's spoken anchor (validate.go) while merge never enforces the documented utt-NNN id format, so a transcript from another tool merges fine but can never yield an ingestable finding, with an unexplanatory error; unconfirmed as a live defect
