---
schema_version: 1
id: "iss-2609120532387596"
slug: "abcd-s-intent-auditor-agent-emits-verdict-json-in-shapes-tha"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "itd-9-fidelity-audit"
origin: researcher-authored
production_mode: hand-written
found_at: ".abcd/.work.local/reviews"
---

abcd's intent-auditor agent emits verdict JSON in shapes that 'abcd intent audit ingest' dead-letters: two runs in one session added a 'quote' field to evidence entries and used {file,line} instead of {ref}; the ingest's DisallowUnknownFields is right, but the agent definition should carry the exact evidence shape (or the ingest should print the expected schema on dead-letter) so a host does not have to hand-convert
