---
schema_version: 1
id: "iss-2609120422583063"
slug: "auto-release-yml-s-comment-above-secrets-inherit-justifies-t"
severity: "minor"
category: "documentation"
source: "user-observation"
found_during: "bughunt-round-48"
origin: researcher-authored
production_mode: hand-written
found_at: ".github/workflows/auto-release.yml"
resolution: "comment rewritten to state the real situation: release.yml needs only GITHUB_TOKEN; inherit is kept as the scaffold's shape"
impact: internal
---

auto-release.yml's comment above 'secrets: inherit' justifies the zizmor suppression by citing a site.yml call, a deploy stage, an internal/core test (TestReleaseChainPassesSecretsAtEveryLevel) and an issue id that do not exist in this repository — copy-paste bleed from another project's release workflow landed with #79; the suppression rationale is misleading to an auditor

## Grounds

- pursued: a zizmor suppression whose rationale cites nothing that exists in this repo misleads the next auditor; a truthful comment removes that, and a future re-scaffold overwriting it would show the fix was in the wrong layer
