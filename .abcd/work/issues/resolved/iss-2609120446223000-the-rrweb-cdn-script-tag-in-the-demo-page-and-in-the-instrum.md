---
schema_version: 1
id: "iss-2609120446223000"
slug: "the-rrweb-cdn-script-tag-in-the-demo-page-and-in-the-instrum"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "bughunt-round-48"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/demo/assets/index.html"
resolution: "integrity sha384 + crossorigin=anonymous on both rrweb script tags, hash verified against a second download and the registry's published sha256"
impact: fix
---

The rrweb CDN script tag in the demo page and in the instrument-your-own-app how-to pins a version but carries no Subresource Integrity (integrity + crossorigin) attribute and the server sets no CSP, so a compromised CDN response executes same-origin with the capture endpoints and can fabricate interaction records that satisfy every allowWrite guard; the page already guards on window.rrweb so a hash mismatch degrades like the documented offline case

## Grounds

- pursued: a browser refusing a tampered script and degrading to the no-rrweb case is safer than running unverified code on the capture origin; a legitimate CDN byte change for the pinned version would show the pin wrong
