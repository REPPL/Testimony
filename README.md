# Testimony

Usability evidence, on the record.

**Testimony** captures manual test sessions the way usability research does it — a
screen recording plus concurrent *think-aloud* narration — and turns them into
machine-readable, time-aligned records: a word-timestamped transcript merged with a
timestamped interaction stream. From there, an analysis layer derives findings
(bugs, friction, inconsistencies, preferences) and maps them back to the codebase.

```
 voice ──► local Whisper ──► transcript.jsonl ─┐
                                               ├─► timeline.jsonl ─► report.md ─► findings ─► code
 clicks ──► rrweb / hooks ──► interactions.jsonl ┘
```

The full design is in [docs/architecture.md](docs/architecture.md). Raw audio and
video never leave your machine; only derived text is analysed.

## Status: walking skeleton

What works today:

- `testimony demo` — serves a small instrumented settings app; your clicks and inputs
  stream to a fresh session directory (normalised interactions + raw rrweb archive)
  while you talk into a recorder of your choice.
- `testimony merge` — merges `transcript.jsonl` + `interactions.jsonl` into a single
  session-relative `timeline.jsonl`.
- `testimony report` — renders the timeline as a Markdown report, joining each
  utterance to the interface events around it (±2.5 s window by default).

What is stubbed: `record` (managed screen/audio capture) and `transcribe` (the
faster-whisper/WhisperX wrapper). Until then you record voice with QuickTime and
provide `transcript.jsonl` yourself — the format is three lines of JSON away
(see the bundled example).

## Quickstart

```sh
go build -o testimony ./cmd/testimony

# 1. Try the pipeline on the bundled synthetic session:
./testimony merge  -session examples/sample-session
./testimony report -session examples/sample-session
open examples/sample-session/report.md

# 2. Capture a real one:
./testimony demo          # then follow the printed instructions
```

The report interleaves what was said with what was done:

```
**[00:22] P1:** “Hm. I clicked save and nothing happened. No message, no
spinner. I can't actually tell if it saved.”
  - [00:24] click `[data-testid=save-btn]` "Save" (#general)
```

The demo app contains at least one intentional usability flaw. Find it by talking.

## Session directory

```
sessions/<timestamp>/
  manifest.json        # app, participant, tasks, t0_epoch_ms (the shared clock anchor)
  events.rrweb.jsonl   # raw rrweb stream (archival; full replay later)
  interactions.jsonl   # normalised events: {"t":<epoch_ms>,"kind","selector","text","value","route"}
  transcript.jsonl     # utterances: {"id","t0","t1","speaker","text"} (session-relative seconds)
  timeline.jsonl       # merged: {"t","src":"speech|event","id","payload"}
  report.md            # human-readable aligned record
```

Synchronisation rests on one wall clock: interaction timestamps are epoch ms,
`t0_epoch_ms` anchors them, and the transcript is audio-relative — start your
voice recording when the session starts (say “session start” aloud as a belt-and-
braces marker).

## Roadmap

Phases from the [architecture note](docs/architecture.md#12-phased-implementation):
transcribe wrapper (WhisperX, word-level timestamps) → analysis layer with
structured, human-verified findings → codebase mapping via `data-testid` anchors →
reference-capture mode for third-party apps. A macOS 26 app layer will wrap this
CLI core; Linux stays CLI-only.

## License

MIT — see [LICENSE](LICENSE).
