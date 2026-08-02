# How alignment works

Testimony's central claim — *this* was said while *that* was clicked — rests on getting several independent recorders onto one clock. This page explains how that happens and why the pieces are shaped the way they are.

## One wall clock

Everything runs on one machine, so one wall clock can anchor everything. When a session starts, `record` or `demo` stamps the moment into the manifest as `t0_epoch_ms` (epoch milliseconds). That number is the origin of session time: every session-relative timestamp is `(epoch_ms − t0_epoch_ms) / 1000` seconds.

The interaction stream needs no further treatment. Each captured click or input carries an epoch-millisecond timestamp taken in the browser (`Date.now()`), on the same clock as `t0_epoch_ms`; the merge step subtracts and divides, and interaction times are on the session clock exactly. Clock drift between streams is negligible over a 20–40 minute session precisely because there is only one clock.

## The audio clock and its offset

The voice recording is the awkward stream. A transcription engine reports times relative to *the start of the recording file* — the audio clock — and nothing forces you to press record at the same instant the session starts. The gap between the two is the **audio-to-session offset**, and every transcript time is shifted by it:

```
session_time = audio_time + offset
```

Which offset applies depends on where the audio came from. A session captured with `testimony record` needs none: capture starts at `t0`, so the offset is 0 by construction and no derivation is attempted. For an external recording (`transcribe -audio`), the offset is derived automatically when it can be: `transcribe` reads the recording's embedded creation timestamp (the `creation_time` tag recorders such as QuickTime write) and subtracts the manifest's `t0_epoch_ms`; if the tag is missing or unreadable, it falls back to 0 — correct whenever you start recording at the moment the session starts. The derived value is then persisted beside the audio in `audio.offset.json`, so a later re-run over the converted `audio.wav` reuses it instead of silently assuming 0. Whatever the path, the command prints the offset and its provenance, so the value is never silent.

Derivation can be wrong — a recorder may stamp the wrong moment, or omit the tag. That is why sessions begin with a spoken marker: saying "session start" aloud at t0 plants a phrase that appears in the transcript at a known session time (roughly zero). If the report looks misaligned, the marker's transcript time reveals the true offset, and an explicit `-offset` overrides derivation entirely. A belt-and-braces anchor, recoverable after the fact.

## The join window

With both streams on the session clock, the report joins them: each interface event is attached to the first utterance (in time) whose span, widened by ±2.5 seconds on each side (the `-window` flag), contains it. When two utterances' widened spans overlap, an event in the overlap goes to the earlier utterance, not both — each event appears exactly once.

A window — rather than exact matching — is deliberate. Transcription timestamps are alignment *estimates*, not measurements. And people do not narrate in lockstep with their hands: "I'll press save… there" often brackets the click rather than coinciding with it. The window absorbs both kinds of slack. Its width is a trade-off: too narrow and genuine pairs are missed; too wide and unrelated events pile onto an utterance. Around two to three seconds sits well for think-aloud narration, and the flag exists because sessions differ.

## Why word-level timestamps matter

Plain speech recognition emits segment-level times, which can be off by several seconds — as coarse as the join window itself, which makes joining speech to an individual click unreliable. WhisperX adds forced alignment: it pins each recognised word to its own moment in the audio.

That precision is what the pipeline is built around. When a participant says "I expected *this* button to save immediately", the word "this" carries its own timestamp, close to the click on the thing it refers to. The join itself operates on whole utterances — each utterance's span, widened by the window — so what the alignment buys is trustworthy utterance boundaries: word-accurate segment times keep the join honest at its default width, and they are the reason WhisperX is the preferred engine — whisper.cpp works, but its segment-level times lean harder on the window.
