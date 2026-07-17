# Intents

Intent records for Testimony.

## What an intent is

An intent captures *what user-facing capability exists once shipped*, written as a working-backwards press release rather than an engineering feature spec. Press releases are user-experience-shaped: they force clarity about what a person can now do before any engineering scope is drawn.

**Plumbing work does not get an intent.** Internal refactors, adapters, and scaffolding have no user moment; forcing press-release prose onto them produces strained writing. Plumbing belongs in the development brief at `../brief/`.

## IDs and filenames

Intent IDs follow the pattern `itd-N` — unpadded, assigned in capture order, never renumbered. Filenames are `itd-N-<slug>.md`. Sequencing is not encoded in the ID; an intent keeps its number for life.

## Lifecycle directories

| Directory | Meaning |
|---|---|
| `drafts/` | Captured but not yet committed to work. Cheap to draft and discard. |
| `planned/` | Committed capability awaiting its build; `spec_id` points at the linked spec once one is minted. |
| `shipped/` | The linked spec closed; the capability exists. |
| `superseded/` | Retired by absorption or reclassification; kept as historical record. |

Directory location is the single source of truth for lifecycle state — no intent carries a `status` field that could disagree with where it lives.

## File shape

Each intent has minimal YAML frontmatter (`id`, `slug`, `spec_id`, `severity`) followed by:

- **Headline** — the capability, named from the user's side.
- **Press Release** — 2–4 sentences in present tense, as if shipped, closing with a persona quote.
- **Why This Matters** — the underlying need.
- **What's In Scope / What's Out of Scope** — bullets; the out-of-scope list is the scope-creep fence.
- **Acceptance Criteria** — observable Given/When/Then outcomes; required before an intent can be planned.

## Persona convention

Customer quotes use the placeholder personas Alice, Bob, and Carol — selected by role, never by name, and always they/them. Real names never appear in intent prose.
