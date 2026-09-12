---
schema_version: 1
id: "iss-2609120446224871"
slug: "demo-s-capture-server-answers-a-mis-routed-capture-post-api"
severity: "major"
category: "observation"
source: "user-observation"
found_during: "bughunt-round-48"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/demo/demo.go"
resolution: "the '/' handler refuses non-GET/HEAD with 405 (Allow: GET, HEAD) and an /api/ catch-all answers 404, both through refuseWrite; the endpoints' own 405 also sets Allow: POST"
impact: fix
---

demo's capture server answers a mis-routed capture POST (/api/interactions/ with a trailing slash, /api/interaction typo, /api/events/) through the '/' catch-all with 200 OK and the demo HTML page, appending nothing and logging nothing — the one refusal path that bypasses refuseWrite, so a mis-instrumented app runs a whole session with an empty interactions.jsonl and no stderr signal; the '/' handler should refuse non-GET/HEAD via refuseWrite and an /api/ catch-all should 404 via refuseWrite

## Grounds

- pursued: every refusal reaching stderr keeps a mis-instrumented app audible during the session; a session that still ends with 0 events and no stderr line would show it wrong
