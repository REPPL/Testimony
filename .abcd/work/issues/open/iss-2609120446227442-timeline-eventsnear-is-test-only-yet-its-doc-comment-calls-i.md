---
schema_version: 1
id: "iss-2609120446227442"
slug: "timeline-eventsnear-is-test-only-yet-its-doc-comment-calls-i"
severity: "nitpick"
category: "observation"
source: "user-observation"
found_during: "bughunt-round-48"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/timeline/timeline.go"
---

timeline.EventsNear is test-only yet its doc comment calls it the documented statement of the join window, and it has drifted from report's real join (no used-event dedup) — drift risk, not a live bug
