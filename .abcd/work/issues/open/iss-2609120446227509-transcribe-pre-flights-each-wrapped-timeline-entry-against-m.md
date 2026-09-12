---
schema_version: 1
id: "iss-2609120446227509"
slug: "transcribe-pre-flights-each-wrapped-timeline-entry-against-m"
severity: "nitpick"
category: "observation"
source: "user-observation"
found_during: "bughunt-round-48"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/transcribe/transcribe.go"
---

transcribe pre-flights each wrapped timeline entry against MaxJSONLLine (checkEntriesFit) but never against MaxJSONLBytes, unlike demo's entryBytes total guard — a transcript within a few percent of 16 MiB could merge-fail permanently; unconfirmed, and demo.go already concedes the combined total is unguaranteed
