---
id: itd-5
slug: participant-ready
spec_id: null
severity: major
---

# Ready for Real Participants, Not Just Ourselves

## Press Release

> **Testimony is ready for sessions with external participants.** Consent templates cover recording, transcription, AI-assisted analysis, and retention; participants appear in every derived artefact only as pseudonyms (`P1`, `P2`, …) with the key stored separately; retention automation deletes raw video at the end of its stated period while pseudonymised derived artefacts persist for analysis. For moderated sessions, speaker diarisation separates moderator from participant in the transcript, so quotes are attributed correctly.
>
> "The local-processing boundary is the strongest card in an ethics application, but only if the housekeeping is real," said Carol, session moderator. "Consent on file, pseudonyms everywhere derived, raw video that actually gets deleted on schedule — I can put this in front of an ethics committee and in front of a participant with equal confidence."

## Why This Matters

Everything before this point is safe because the participant is one of us. External participants bring UK GDPR and research-ethics obligations, and the pipeline's strongest privacy property — voice and screen never leave local hardware; only derived text reaches a cloud model — is only defensible if consent, pseudonymisation, and retention are enforced by tooling rather than by promise. Automating the housekeeping is what converts a personal instrument into a research instrument.

## What's In Scope

- Consent templates covering recording, transcription, AI-assisted analysis, and retention, with the consent reference recorded in the session manifest.
- Pseudonymous participant IDs in all derived artefacts, with the identity key stored separately from the session corpus.
- Retention automation: a stated retention period for raw video enforced by tooling; derived, pseudonymised artefacts retained on their own schedule.
- Speaker diarisation for moderated sessions, so transcript utterances carry the correct speaker label.
- Keyframes handled under the same care as video, since frames can contain personal data on screen.

## What's Out of Scope

- Any transfer of raw audio or video off the capture machine.
- A participant-recruitment or scheduling workflow.
- Institution-specific ethics paperwork beyond the templates; each study still files its own application.
- An agentic session moderator; moderated sessions have a human moderator.

## Acceptance Criteria

- **Given** a session with an external participant, **when** capture starts, **then** the manifest records a consent reference and refuses a participant session without one.
- **Given** derived artefacts from any session, **when** they are inspected, **then** the participant appears only as a pseudonym and the identity key is absent from the session directory.
- **Given** a raw video past its stated retention period, **when** the retention pass runs, **then** the video is deleted while pseudonymised derived artefacts remain.
- **Given** a moderated session with diarisation enabled, **when** the transcript is produced, **then** moderator and participant utterances carry distinct speaker labels.
