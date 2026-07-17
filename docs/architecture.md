# Architecture Note: Think-Aloud Capture & Analysis Pipeline

| | |
|---|---|
| **Status** | Draft v0.2 — for discussion |
| **Date** | 2026-07-17 |
| **Author** | Alex Reppel (drafted with Claude) |
| **Context** | Development — Prototyping tool |
| **Name** | **`testimony`** — Go CLI core (Linux: CLI-only) with a macOS 26 "Liquid Glass" app layer (*Testimony*); standalone repo initially, later folded into `abcd` |

## 1. Purpose

This note describes a pipeline for capturing manual test and demo sessions as screen recordings with concurrent think-aloud narration, converting them into machine-readable, time-aligned data (transcript + interaction stream), analysing them with an LLM to surface bugs, inconsistencies, and user preferences, and mapping those findings back to the codebase.

The name *testimony* describes the artefact the pipeline produces — a first-person spoken account offered as evidence — and happens to begin with *test*.

The pipeline serves two distinct operating modes that share the same capture rig:

**Mode A — Testing our own applications.** A test user (initially: the author) works through tasks in one of our applications while thinking aloud. The goal is structured findings — bugs, friction points, inconsistencies, preferences — each anchored to a moment in time, a UI element, and ultimately a location in the codebase.

**Mode B — Reference capture of existing applications.** The author (or a participant) demos third-party applications while narrating what they like, dislike, or find notable. The goal is a tagged design-preferences corpus that feeds requirements and design decisions for the prototyping tool. Mode B has no access to the target app's internals, which changes the evidence available (see §8).

## 2. Method grounding

The capture method is the concurrent think-aloud protocol, the standard verbalisation technique in usability research (Ericsson & Simon's protocol analysis; Nielsen's usability engineering tradition). What is new here is not the method but the analysis economics: transcription and first-pass coding — historically the expensive, slow part — are automated, while the human retains the final judgement.

This is an active research frontier rather than settled practice. Relevant evidence as of mid-2026:

- A CHI 2026 randomized controlled trial compares an agentic audio moderator against a human moderator in think-aloud usability testing — AI participation in the *conduct* of sessions is being formally evaluated ([ACM DL](https://dl.acm.org/doi/10.1145/3772318.3791653)).
- An IUI 2025 preliminary study examines LLMs for usability testing analysis ([ACM DL](https://dl.acm.org/doi/10.1145/3708557.3716341)); related work compares human and AI usability evaluators ([Springer](https://link.springer.com/chapter/10.1007/978-3-032-30044-7_9)) and explores LLM-simulated usability testing (UXAgent, [CHI 2025](https://dlnext.acm.org/doi/10.1145/3706599.3719729)).
- In industry, LLM analysis of interaction event streams is in production use — e.g. Decipher AI summarises rrweb session recordings with LLMs to explain what users did before an error ([write-up](https://getdecipher.com/blog/generating-rrwb-session-summaries)).

The consistent finding across this literature: LLMs are effective *first-pass* coders of think-aloud data but both miss and occasionally invent problems. **Design stance: AI-as-second-coder.** Every AI-generated finding carries `status: unverified` until a human confirms or rejects it, and the verification decision is retained (§9). This also gives us precision/recall proxies over time.

## 3. System overview

```
        ┌────────────── CAPTURE (local Mac, one wall clock) ──────────────┐
        │   screen.mp4      audio.wav      events.jsonl / .cast           │
        │   (recording)     (microphone)   (rrweb / asciinema / none)     │
        └───────┬───────────────┬──────────────────┬─────────────────────┘
                │               │                  │
                │        [ASR: faster-whisper or whisper.cpp]
                │               │                  │
                │        [WhisperX forced alignment → word timestamps]
                │               │                  │
                │               ▼                  ▼
                │        transcript.jsonl    events (normalised)
                │               └────────┬─────────┘
                │                        ▼
                │              [merge → timeline.jsonl]
                │                        ▼
   keyframes ◄──┘              [analysis skill (LLM)]
   (extracted on demand                  ▼
    at utterance timestamps)      findings.jsonl
                                         ▼
                              [human verification pass]
                                         ▼
                            [codebase-mapping agent]
                                         ▼
                     report.md · issues · pattern library
```

Everything above the analysis step runs locally. The analysis step consumes only derived text (transcript + serialised events + optional keyframes), never raw audio/video — this is the privacy boundary (§10).

## 4. Capture layer

One launcher per session starts all recorders and writes a `manifest.yaml` recording the session's `t0` (epoch ms), app under test, build/commit hash, participant pseudonym, task list, and consent reference. A single wall clock is the synchronisation primitive: every stream either carries epoch timestamps natively or is anchored to `t0`. As a belt-and-braces fallback, the session begins with a spoken marker ("session start") which appears in both the transcript and, audibly, at a known offset in the recording.

Per platform:

| Target | Screen + audio | Interaction stream |
|---|---|---|
| Web app (own) | Browser/OS recorder (QuickTime, OBS, or `getDisplayMedia`) | **rrweb** embedded in the dev build — timestamped clicks *with CSS selectors*, inputs, scrolls, full DOM snapshots ([repo](https://github.com/rrweb-io/rrweb)) |
| Go CLI (own) | Terminal + mic; or asciinema alone if no GUI involved | **asciinema** — the `.cast` v2 format is already a timestamped event stream ([site](https://asciinema.org)) |
| macOS app (own) | ScreenCaptureKit / QuickTime | Phase 1: none (keyframe path, §8). Later: debug-build event logging (`NSApplication.sendEvent` override / `NSEvent` local monitor) |
| iOS app (own) | Simulator: `xcrun simctl io booted recordVideo`; device: QuickTime via cable | Phase 1: none. Later: debug-build `UIWindow.sendEvent` / gesture-recogniser logging |
| Third-party app (Mode B) | OS recorder | None by definition. Websites optionally via a browser extension injecting rrweb; native apps: keyframe path only |

Audio is extracted to 16 kHz mono WAV for ASR. Raw video is retained locally as the evidence of record and for keyframe extraction.

**Build-time convention (own web apps):** add stable `data-testid` attributes to interactive elements. rrweb selectors then become near-deterministic anchors for codebase mapping (§7), instead of fragile auto-generated class chains.

## 5. Transcription

Local Whisper, as proposed — with one upgrade: word-level alignment.

- **Engine:** `faster-whisper` (CTranslate2) or `whisper.cpp` (Metal-accelerated; well suited to the Mac Studio), model `large-v3-turbo` — near-large accuracy at ~6× speed ([2026 open-source STT benchmarks](https://northflank.com/blog/best-open-source-speech-to-text-stt-model-in-2026-benchmarks)).
- **Alignment:** **WhisperX** ([repo](https://github.com/m-bain/whisperX)) performs forced alignment (wav2vec2) to produce *word-level* timestamps, plus optional speaker diarisation for moderated sessions. Plain Whisper's segment-level timestamps (± several seconds) are too coarse to join speech to individual clicks; word-level alignment makes the utterance↔event join reliable.
- **Robustness:** enable VAD (built into faster-whisper) to suppress Whisper's hallucination-on-silence failure mode; supply an initial prompt with domain vocabulary (product names, UI terms) to reduce ASR errors on jargon.

Output: `transcript.jsonl`, one utterance per line:

```json
{"id":"utt-034","t0":128.42,"t1":131.90,"speaker":"P1",
 "text":"I expected this button to save immediately",
 "words":[{"w":"I","t":128.42},{"w":"expected","t":128.61}, "..."]}
```

## 6. Merge and unified timeline

A small merger (the `testimony` Go CLI) normalises all streams to session-relative seconds and interleaves them:

```json
{"t":129.01,"src":"event","id":"ev-482",
 "payload":{"kind":"click","selector":"[data-testid=save-btn]","text":"Save","route":"/settings"}}
{"t":128.42,"src":"speech","id":"utt-034",
 "payload":{"speaker":"P1","text":"I expected this button to save immediately"}}
```

Join rule: an utterance is associated with events inside a ±2–3 s window (word-level timestamps allow tightening this when a specific word — "this", "here" — needs anchoring). Clock drift over a 20–40 minute session is negligible when all recorders run on one machine; the spoken t0 marker allows correction if a stream's absolute anchor is lost.

`timeline.jsonl` is the single artefact the analysis layer consumes. It is small (kilobytes, not gigabytes), diffable, and archivable alongside the manifest.

## 7. Analysis and codebase mapping

**Analysis skill.** The timeline, chunked by task boundaries from the manifest, is analysed by an LLM under a fixed rubric (implemented as a reusable Claude skill so the coding scheme is versioned and consistent across sessions). Two passes: segment-level coding, then session-level synthesis (deduplication, severity, cross-task patterns). Findings are structured:

```json
{"id":"F-012","t":129.0,"type":"bug",
 "severity":2,"mode":"A",
 "quote":"I expected this button to save immediately",
 "evidence":["utt-034","ev-482"],
 "ui":{"selector":"[data-testid=save-btn]","route":"/settings"},
 "code_refs":[{"file":"src/components/SettingsForm.tsx","symbol":"handleSave","confidence":"high"}],
 "status":"unverified"}
```

`type` is one of `bug | friction | inconsistency | preference | idea`. When the transcript is ambiguous ("this thing here"), the analysis step may request a keyframe — a video frame extracted at the utterance timestamp (`ffmpeg -ss`) — and use a multimodal model to identify the referent. Keyframes are the *fallback* evidence channel; the transcript+events path is cheaper, more precise, and more auditable, so video is consulted only on demand.

**Codebase mapping.** A separate agentic step (Claude Code, or an agent with repo access) resolves each finding's UI anchor to code: `data-testid` and selectors are grepped directly; routes map through the router table; component names come from DOM structure or React/Vue devtools naming. Output is `code_refs` with a confidence level, plus optionally a drafted issue (title, repro from the event window, quote as user evidence, suspected file). This step is deliberately last and separable — it is the novel part of the pipeline, and the part most likely to need iteration. For Go CLIs the anchor is the command/flag/output text visible in the `.cast` stream, which greps well; for native apps, mapping starts from accessibility identifiers if set, otherwise from the keyframe.

**Human verification.** A review pass (a simple TUI or even the report itself) flips each finding to `confirmed | rejected | duplicate`. Verified findings can flow into the docs-as-code manual test records discussed previously — a session becomes evidence attached to a test run.

## 8. Mode B specifics — reference capture of existing apps

Mode B uses the identical capture rig minus instrumentation: screen recording + narration only (optionally rrweb via browser extension when the reference is a website). Consequences:

- The transcript carries the semantic load; the video supplies the referents. Keyframe extraction at utterance timestamps + multimodal identification ("what UI pattern is on screen while the speaker says X?") replaces the event stream.
- The analysis rubric shifts from defect-finding to preference elicitation: `{pattern, app, liked|disliked, why, screenshot_ref, applicability}`.
- Output accumulates into a **pattern library** (a project doc / repo folder of tagged patterns with keyframes), which becomes structured input for the prototyping tool's requirements — "I want the command palette to feel like X's, except…" backed by a clip and a quote.

Copyright/ToS note: recordings of third-party apps are for private research/reference use; keyframes should stay in the private corpus, not in published material.

## 9. Validity and limitations

- **Reactivity.** Concurrent think-aloud alters behaviour and timing. Use Ericsson & Simon level 1–2 verbalisation instructions (say what you're thinking, don't explain or justify) to minimise it, and do not use these sessions for performance/timing measurements.
- **ASR error.** Accents, domain jargon and cross-talk degrade transcripts; mitigations in §5. The word-level timestamps are alignment estimates, hence the join *window* rather than exact matching.
- **LLM analysis error.** Misses and fabrications are documented in the literature (§2); hence AI-as-second-coder, `unverified` by default, and retained human verdicts as an ongoing precision measure.
- **Selector fragility.** Auto-generated selectors rot as the DOM changes; the `data-testid` convention is the countermeasure and should be adopted before the first participant session.
- **Small-n.** Findings are qualitative signals, not statistics. The classic ~5-users-per-round heuristic is the operating assumption for Mode A rounds.

## 10. Privacy and ethics

The design keeps the sensitive artefacts — a participant's voice and screen — on local hardware. ASR is local (Whisper on the Mac Studio); raw audio/video never leave the machine. Only derived text (transcript, serialised events, and any keyframes the analyst explicitly releases) reaches a cloud LLM; a fully local variant (local LLM for analysis) is the fallback if an ethics protocol requires it.

For sessions with external participants: informed consent covering recording, transcription, AI-assisted analysis, and retention; pseudonymous participant IDs in all derived artefacts (`P1`, `P2`…) with the key stored separately; a stated retention period for raw video, with derived, pseudonymised artefacts retained longer for analysis. UK GDPR applies; if run under university auspices, this is a standard ethics-approval shape and the local-processing boundary is the strongest card in that application. Keyframes require the same care as video — they can contain personal data on screen.

## 11. Session artefacts

```
sessions/2026-07-17_p1_webapp/
  manifest.json         # app, commit, participant, tasks, consent ref, t0_epoch_ms
  screen.mp4            # evidence of record (local only)
  audio.wav             # 16 kHz mono extract, written by `testimony transcribe` (local only)
  events.rrweb.jsonl    # raw rrweb stream (archival) / .cast file
  interactions.jsonl    # normalised interaction events (epoch ms)
  transcript.jsonl      # word-aligned utterances
  timeline.jsonl        # merged, session-relative
  findings.jsonl        # analysis output + verification verdicts
  report.md             # human-readable session report
```

## 12. Phased implementation

1. **Phase 0 — manual pilot (hours).** One 15-minute self-session on an existing web app: QuickTime + rrweb snippet in the dev build, WhisperX run by hand, merge eyeballed in a notebook. Purpose: validate sync quality and the join window before writing any tooling.
2. **Phase 1 — `testimony` CLI (Go).** `testimony record` (launcher + manifest + t0), `testimony transcribe` (wraps WhisperX/whisper.cpp), `testimony merge`, `testimony report`. Single static binary; sessions become one-command. The macOS 26 Liquid Glass app layer wraps this same core later; Linux remains CLI-only. *Status: `demo`, `transcribe`, `merge`, and `report` working; `record` is the remaining stub.*
3. **Phase 2 — analysis skill.** Rubric, finding schema, chunking, report generator; verification pass workflow.
4. **Phase 3 — codebase mapping.** `data-testid` convention in own apps; agentic mapping step; issue drafting.
5. **Phase 4 — Mode B.** Keyframe extraction + multimodal identification; pattern-library format; optional rrweb browser extension for reference websites.
6. **Phase 5 — participant-ready.** Consent templates, pseudonymisation, retention automation; moderated-session support (diarisation on).

Phases 0–2 deliver standalone value (searchable, analysed session records) even if 3–5 never happen.

## 13. Open questions

- Moderated vs unmoderated sessions for Mode A — does the agentic-moderator direction (CHI 2026) merit a later experiment?
- Native-app event instrumentation: worth the effort, or is the keyframe path sufficient in practice?
- Local-LLM analysis quality vs cloud — what is the acceptable quality floor for a fully local pipeline?
- Does the pattern library live as project docs, a repo, or inside the prototyping tool itself?
- Relationship to automated testing: can confirmed Mode A findings seed regression test cases (linking back to the docs-as-code test plans)?

## 14. References

- Ericsson, K.A. & Simon, H.A., *Protocol Analysis: Verbal Reports as Data* (MIT Press).
- [Agentic Audio Moderator vs Human Moderator in Think-Aloud Usability Testing (CHI 2026)](https://dl.acm.org/doi/10.1145/3772318.3791653)
- [Leveraging LLMs for Usability Testing (IUI 2025)](https://dl.acm.org/doi/10.1145/3708557.3716341) · [Humans vs. AI as Usability Evaluators](https://link.springer.com/chapter/10.1007/978-3-032-30044-7_9) · [UXAgent (CHI 2025)](https://dlnext.acm.org/doi/10.1145/3706599.3719729)
- [Decipher AI — summarising rrweb sessions with LLMs](https://getdecipher.com/blog/generating-rrwb-session-summaries)
- [rrweb](https://github.com/rrweb-io/rrweb) · [asciinema](https://asciinema.org) · [WhisperX](https://github.com/m-bain/whisperX) · [faster-whisper](https://github.com/SYSTRAN/faster-whisper) · [whisper.cpp](https://github.com/ggml-org/whisper.cpp)
- [Open-source STT benchmarks, 2026 (Northflank)](https://northflank.com/blog/best-open-source-speech-to-text-stt-model-in-2026-benchmarks)
- [AI-moderated research platforms compared, 2026](https://medium.com/@charles_31533/i-tested-5-ai-moderated-research-platforms-outset-listen-labs-conveo-strella-user-intuition-78917116c966)
