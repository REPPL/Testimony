---
schema_version: 1
id: "iss-2609120440245259"
slug: "internal-demo-demo-test-go-s-bind-address-table-uses-redacte"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "bughunt-round-48"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/demo/demo_test.go"
resolution: "[redacted-address] replaced with the RFC 5737 address 192.0.2.5; abcd lint reports zero errors"
impact: internal
---

internal/demo/demo_test.go's bind-address table uses [redacted-address], a live private-range IPv4 rather than an RFC 5737 documentation address; abcd lint reports it as the repo's one privacy error

## Grounds

- pursued: the test only needs a non-loopback, non-wildcard host string, so a documentation address preserves its assertion; a test failure after the swap would show it wrong
