# Issue drafts — sample-session

Drafted from mapped findings in session `sample-session` (app `testimony demo`, participant `P1`). 1 of 2 references accepted.

## F-001 — bug: I clicked save and nothing happened

**Severity** 3 · **Anchor** `[data-testid=save-btn]` on `#general` · **At** [00:22]

> “I clicked save and nothing happened”
> — P1, [00:22]

### Steps to reproduce

1. Open `#general`.
2. Click `[data-testid=display-name]`.
3. Enter `Alice` in `[data-testid=display-name]`.
4. Click `[data-testid=save-btn]`.

### Suspected files

- `src/settings/ProfileForm.tsx:46` (owner) — accepted 2026-09-16
- `src/settings/saveProfile.ts:12` (handler) — proposed

Session `sample-session` · finding `F-001` · references `R-001`, `R-002`
