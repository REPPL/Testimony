# Testimony

Usability evidence, on the record.

**Testimony** captures manual test sessions the way usability research does it — a
screen recording plus concurrent *think-aloud* narration — and turns them into
machine-readable, time-aligned records: a word-timestamped transcript merged with a
timestamped interaction stream, rendered as a report that shows what was said next
to what was done.

```
 voice ──► local Whisper ──► transcript.jsonl ─┐
                                               ├─► timeline.jsonl ─► report.md
 clicks ──► rrweb / hooks ──► interactions.jsonl ┘
```

Raw audio and video never leave your machine; only derived text is analysed. See
[privacy](docs/explanation/privacy.md).

## Install

Build from source (requires Go):

```sh
git clone https://github.com/REPPL/Testimony.git
cd Testimony
go install ./cmd/testimony
```

Transcription additionally needs ffmpeg and a local Whisper engine — see
[transcribe a recording](docs/how-to/transcribe-a-recording.md) for the
engine options and their setup.

## Quickstart

Try the pipeline on the bundled synthetic session:

```sh
testimony merge  -session examples/sample-session
testimony report -session examples/sample-session
open examples/sample-session/report.md
```

Then capture a real one: `testimony demo` starts a capture session and prints
every step. The [getting-started tutorial](docs/tutorials/getting-started.md)
walks the whole path — record, think aloud, transcribe, merge, report — in about
five minutes. The result interleaves speech with interface events:

```
**[00:22] P1:** “Hm. I clicked save and nothing happened. No message, no
spinner. I can't actually tell if it saved.”
  - [00:24] click `[data-testid=save-btn]` "Save" (#general)
```

The demo app contains at least one intentional usability flaw. Find it by talking.

## Documentation

- [Tutorials](docs/tutorials/getting-started.md) — your first session, end to end.
- [How-to guides](docs/how-to/) — [transcribe a recording](docs/how-to/transcribe-a-recording.md)
  (engines, languages, offsets), [instrument your own app](docs/how-to/instrument-your-own-app.md).
- [Reference](docs/reference/) — the [command line](docs/reference/cli.md) and the
  [session directory](docs/reference/session-directory.md).
- [Explanation](docs/explanation/) — [how alignment works](docs/explanation/how-alignment-works.md),
  [privacy](docs/explanation/privacy.md).

## Session directory

Each session is one folder of small, inspectable files:

```
sessions/<timestamp>/
  manifest.json        # app, participant, tasks, t0_epoch_ms (the shared clock anchor)
  audio.wav            # 16 kHz mono extract (local only)
  events.rrweb.jsonl   # raw rrweb stream (archival)
  interactions.jsonl   # normalised interaction events
  transcript.jsonl     # time-aligned utterances
  timeline.jsonl       # merged, session-relative timeline
  report.md            # human-readable aligned record
```

Exact schemas: [session directory reference](docs/reference/session-directory.md).

## Status and roadmap

Working today: `demo` (instrumented capture), `transcribe` (local WhisperX or
whisper.cpp), `merge`, and `report`. `record` (managed screen/audio capture) is
stubbed — for now you record voice with QuickTime and `transcribe` does the rest.

Coming next, in user terms:

- **One-command capture** — `testimony record` starts every recorder and stamps
  the session for you.
- **Automated first-pass analysis** — findings (bugs, friction, inconsistencies,
  preferences) derived from the timeline, each staying *unverified* until you
  confirm or reject it.
- **Codebase mapping** — findings anchored to your code through the
  `data-testid` selectors they were captured against.
- **Reference capture** — narrated sessions over third-party apps, building a
  tagged corpus of design preferences.
- **A macOS app** wrapping the CLI core; Linux stays CLI-only.

## License

MIT — see [LICENSE](LICENSE).
