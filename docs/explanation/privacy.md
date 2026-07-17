# Privacy

A think-aloud session captures two of the most sensitive things a person can hand over: their voice and their screen. Testimony is designed so that neither ever needs to leave the machine it was recorded on. This page explains the boundary, and what it asks of you when other people take part in your sessions.

## The privacy boundary

The rule is simple: **raw recordings stay local; only derived text is ever analysed.**

- Your voice recording and any screen recording remain files on your machine. No part of the pipeline uploads them anywhere.
- Speech recognition runs locally — the transcription engines execute on your own hardware, and the transcription step makes no network requests.
- The capture server listens on your machine and writes to a local session directory.
- What the pipeline produces for analysis is derived text: the transcript, the normalised event stream, the merged timeline, and the report. These are small, readable files you can inspect line by line before sharing them with anyone — or with any analysis tool.

The distinction matters because the derived text is a much narrower disclosure than the recording it came from. A transcript contains what was said; the audio contains a voiceprint. An event stream says a button was clicked; a screen recording shows everything else that was visible at the time. When an analysis layer (local or cloud) enters the picture, it sits on the far side of this boundary: it sees only the text you choose to give it, never the raw audio or video. If your setting demands it, a fully local analysis path keeps even the derived text on the machine.

One caveat deserves emphasis: anything extracted *from* a recording inherits its sensitivity. A video frame can show personal data on screen — treat stills and clips with the same care as the recording itself.

## Participant pseudonyms

Derived artefacts never need a participant's name. Sessions use pseudonymous IDs — `P1`, `P2`, … — in the manifest, the transcript, and the report. Keep the mapping from pseudonyms to real identities separate from the session directories, so the artefacts you archive, diff, and share stay pseudonymous on their own.

Pseudonymisation is not anonymisation: a voice is identifiable, and people say identifying things aloud. That is another reason the raw audio stays local while only text moves.

## Consent

For sessions where anyone other than you takes part, obtain informed consent that covers, specifically:

- being recorded (voice, and screen if applicable);
- transcription of the recording;
- AI-assisted analysis of the derived text;
- how long recordings and derived artefacts are retained.

State a retention period for raw recordings and honour it; derived, pseudonymised artefacts can reasonably be retained longer for analysis, and consent should say so. Data-protection law (in the UK, UK GDPR) applies to recordings of identifiable people, and institutional settings usually require ethics approval — the local-processing boundary described above is precisely the kind of safeguard such reviews look for, so it belongs in the application.

The posture throughout is that the participant offered their words as evidence. Handling that testimony with restraint — local by default, text-only outward, pseudonymous, retained no longer than stated — is what makes it fair to ask for. <!-- docs-lint: allow -->
