# Validity and ethics

## Validity limits

- **Reactivity.** Concurrent think-aloud alters behaviour and timing. Use
  Ericsson & Simon level 1–2 verbalisation instructions (say what you are
  thinking; do not explain or justify), and never use these sessions for
  performance or timing measurements.
- **ASR error.** Accents, domain jargon, and cross-talk degrade transcripts;
  VAD and a domain-vocabulary initial prompt are the mitigations. Word-level
  timestamps are alignment *estimates* — hence the join window rather than
  exact matching.
- **LLM analysis error.** Misses and fabrications are documented in the
  literature; hence AI-as-second-coder, `unverified` by default, and retained
  human verdicts as an ongoing precision measure.
- **Selector fragility.** Auto-generated selectors rot as the DOM changes;
  the `data-testid` convention
  ([`03-invariants.md`](03-invariants.md)) is the countermeasure.
- **Small-n.** Findings are qualitative signals, not statistics; the classic
  ~5-users-per-round heuristic is the operating assumption for Mode A rounds.

## Privacy and ethics

The sensitive artefacts — a participant's voice and screen — stay on local
hardware. ASR is local; raw audio/video never leave the machine. Only derived
text (transcript, serialised events, and any keyframes the analyst explicitly
releases) reaches a cloud LLM; a fully local variant (local LLM for analysis)
is the fallback if an ethics protocol requires it.

For sessions with external participants:

- informed consent covering recording, transcription, AI-assisted analysis,
  and retention;
- pseudonymous participant IDs in all derived artefacts (`P1`, `P2`, …) with
  the key stored separately;
- a stated retention period for raw video, with derived, pseudonymised
  artefacts retained longer for analysis.

UK GDPR applies; run under university auspices this is a standard
ethics-approval shape, and the local-processing boundary is the strongest
card in that application. Keyframes require the same care as video — they can
contain personal data on screen. Recordings of third-party apps (Mode B) are
for private research/reference use only.
