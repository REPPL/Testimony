---
schema_version: 1
id: "iss-2609120520334220"
slug: "imported-terminal-output-keeps-ansi-csi-sequences-raw-report"
severity: "minor"
category: "observation"
source: "user-observation"
found_during: "itd-11-live-verification"
origin: researcher-authored
production_mode: hand-written
found_at: "internal/cast/coalesce.go"
---

Imported terminal output keeps ANSI CSI sequences raw; report's SafeText strips only the ESC byte, so a coloured ls line renders as '[1m[34mApplications[39;49m[0m …' in report.md and the same residue reaches analyze's request text — a live asciinema 2.4.0 session showed every ls line unreadable. The spec chose raw evidence plus NO_COLOR=1 guidance and named stripping as a reversible follow-up; a small CSI state machine applied at report/emit time (not on the archived cast) would keep the evidence and fix the rendering
