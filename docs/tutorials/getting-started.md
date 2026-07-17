# Getting started

In this tutorial you capture a complete think-aloud session — voice plus clicks — and turn it into a time-aligned Markdown report. It takes about five minutes, and you need a Mac with a microphone.

You follow the same path Alice takes on her first session: she installs Testimony, explores the built-in demo app while talking, and ends with a report that shows what she said next to what she did.

## 1. Install Testimony

Install the toolchain and the transcription dependencies with [Homebrew](https://brew.sh) — step 6 needs ffmpeg and a local speech-recognition engine:

```sh
brew install go ffmpeg uv
uv tool install whisperx
```

Then build Testimony from source:

```sh
git clone https://github.com/REPPL/Testimony.git
cd Testimony
go install ./cmd/testimony
```

`go install` puts the binary in `$(go env GOPATH)/bin` (usually `~/go/bin`); add that directory to your `PATH` if it is not already there:

```sh
export PATH="$HOME/go/bin:$PATH"
```

Check the install:

```sh
testimony version
```

## 2. Start a capture session

```sh
testimony demo
```

The command creates a fresh session directory (for example `sessions/2026-07-17_174858`), starts a small instrumented settings app, and prints the URL and the exact commands you need later. Keep this terminal open — it captures your clicks for the whole session.

## 3. Start recording your voice

1. Open QuickTime Player and choose **File → New Audio Recording**.
2. Click the record button.
3. Say **"session start"** aloud. This spoken marker lands in the transcript and helps verify the clocks line up.

## 4. Explore and think aloud

Open the printed URL (http://localhost:8737) in your browser and work through the settings app while saying what you think, expect, and notice — out loud, continuously.

Alice changes the display name to "Alice", clicks **Save**, and says what she observes. The demo app contains at least one intentional usability flaw; find it by talking.

## 5. Stop both recorders

1. Stop the QuickTime recording and save the file — Alice saves hers as `~/Desktop/session.m4a`.
2. In the terminal, press `Ctrl+C` to stop the capture server.

## 6. Transcribe the recording

Point `transcribe` at the session directory the demo printed and at your audio file:

```sh
testimony transcribe -session sessions/2026-07-17_174858 -audio ~/Desktop/session.m4a
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
