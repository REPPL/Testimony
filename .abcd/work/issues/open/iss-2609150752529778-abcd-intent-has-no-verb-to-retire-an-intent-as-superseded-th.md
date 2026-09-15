---
schema_version: 1
id: "iss-2609150752529778"
slug: "abcd-intent-has-no-verb-to-retire-an-intent-as-superseded-th"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "itd-6-supersession"
origin: researcher-authored
production_mode: hand-written
found_at: ".abcd/development/intents/README.md"
---

abcd intent has no verb to retire an intent as superseded: the lifecycle table documents intents/superseded/ but the only way to move a draft there is git mv plus a hand-typed superseded_by frontmatter key, so the record-id seam never sees the transition
