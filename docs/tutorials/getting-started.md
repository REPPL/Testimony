# Getting started

In this tutorial you capture a complete think-aloud session — voice plus clicks — and turn it into a time-aligned Markdown report. It takes about five minutes, and you need a Mac with a microphone.

You follow the same path Alice takes on her first session: she installs Testimony, explores the built-in demo app while talking, and ends with a report that shows what she said next to what she did.

## 1. Install Testimony

One command installs the `testimony` binary into `~/.local/bin` — no admin
rights needed — and then offers to set up the transcription dependencies
(ffmpeg and a local speech-recognition engine), which step 6 relies on:

```sh
curl -fsSL https://raw.githubusercontent.com/REPPL/Testimony/main/install.sh | sh
```

Answer **y** when it offers ffmpeg and choose **whisperx** as the engine. The
installer verifies every download against a pinned checksum or the publisher's
signature, and tells you if `~/.local/bin` still needs adding to your `PATH`.

Check the install:

```sh
testimony version
```

## 2. Start a capture session

```sh
testimony record -demo
```

One command creates a fresh session directory (for example `sessions/2026-07-17_174858`), starts recording your microphone into that directory, serves a small instrumented settings app, and prints the URL and the exact commands you need later. The first run asks for **Microphone** permission — grant it in System Settings and run the command again. Keep this terminal open: it records your voice and captures your clicks for the whole session.

> If you would rather not capture the microphone, run `testimony demo` instead, record your voice separately in QuickTime Player, save the file, and pass it to `transcribe` in step 6 with `-audio ~/Desktop/session.m4a`. Everything else is the same.

## 3. Say the start marker

Say **"session start"** aloud. Recording is already running, so this spoken marker lands in the transcript and helps verify the clocks line up.

## 4. Explore and think aloud

Open the printed URL (http://localhost:8737) in your browser and work through the settings app while saying what you think, expect, and notice — out loud, continuously.

Alice changes the display name to "Alice", clicks **Save**, and says what she observes. The demo app contains at least one intentional usability flaw; find it by talking.

## 5. Stop the session

In the terminal, press `Ctrl+C`. The recorder finalises `audio.wav`, the capture server stops, and the exact next commands are printed with the real session directory.

## 6. Transcribe the recording

Point `transcribe` at the session directory — no audio file to name, because the recording is already in the session as `audio.wav`:

```sh
testimony transcribe -session sessions/2026-07-17_174858
```

This runs speech recognition locally on your machine — using the WhisperX engine you installed in step 1 — and writes `transcript.jsonl` into the session directory. It also prints the clock offset it uses to align the recording with the session — note it, and see [how alignment works](../explanation/how-alignment-works.md) if it ever looks wrong.

## 7. Merge speech and clicks

```sh
testimony merge -session sessions/2026-07-17_174858
```

This interleaves the transcript with the captured interactions into a single `timeline.jsonl`.

## 8. Generate and read the report

```sh
testimony report -session sessions/2026-07-17_174858
open sessions/2026-07-17_174858/report.md
```

The report pairs each utterance with the interface events around it:

```
**[00:22] P1:** “Hm. I clicked save and nothing happened. No message, no
spinner. I can't actually tell if it saved.”
  - [00:24] click `[data-testid=save-btn]` "Save" (#general)
```

That is a complete session: what Alice said, aligned with what she did, on the record.

## Where next

- [Transcribe a recording](../how-to/transcribe-a-recording.md) — engines, languages, and fixing a wrong offset.
- [Instrument your own app](../how-to/instrument-your-own-app.md) — capture sessions on your own web app instead of the demo.
- [Command-line reference](../reference/cli.md) and [session directory reference](../reference/session-directory.md).
