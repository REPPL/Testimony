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

One line, no admin rights required (binary goes to `~/.local/bin`; the SHA-256 of
the release artefact is pinned in the script):

```sh
curl -fsSL https://raw.githubusercontent.com/REPPL/Testimony/main/install.sh | sh
```

The installer then offers to set up what `transcribe` needs — ffmpeg and a local
ASR engine (WhisperX or whisper.cpp) — via Homebrew where available, or as
user-local installs for machines without admin rights. Prefer to read before you
run (sensible), or pass flags:

```sh
curl -fsSLO https://raw.githubusercontent.com/REPPL/Testimony/main/install.sh
less install.sh && sh install.sh                 # inspect first
curl -fsSL .../install.sh | sh -s -- --no-deps   # binary only
curl -fsSL .../install.sh | sh -s -- --yes       # non-interactive, with dependencies
```

Or build from source (requires Go): `git clone` this repository, then
`go install ./cmd/testimony`. Engine options and setup:
[transcribe a recording](docs/how-to/transcribe-a-recording.md).

## Quickstart

Try the pipeline on the bundled synthetic session:

```sh
testimony merge  -session examples/sample-session
testimony report -session examples/sample-session
open examples/sample-session/report.md
```

Then capture a real one: `testimony record -demo` starts a capture session —
recording your voice and clicks in one command — and prints every step. The
[getting-started tutorial](docs/tutorials/getting-started.md) walks the whole
path — record, think aloud, transcribe, merge, report — in about five minutes. The result interleaves speech with interface events:

```
**[00:22] P1:** “Hm. I clicked save and nothing happened. No message, no
spinner. I can't actually tell if it saved.”
  - [00:24] click `[data-testid=save-btn]` "Save" (#general)
```

The demo app contains at least one intentional usability flaw. Find it by talking.

## Documentation

- [Tutorials](docs/tutorials/getting-started.md) — your first session, end to end.
- [How-to guides](docs/how-to/) — [transcribe a recording](docs/how-to/transcribe-a-recording.md)
  (engines, languages, offsets), [analyse a session](docs/how-to/analyse-a-session.md)
  (findings and verdicts), [instrument your own app](docs/how-to/instrument-your-own-app.md).
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
  findings.jsonl       # analysis findings + verdicts
  report.md            # human-readable aligned record
```

Exact schemas: [session directory reference](docs/reference/session-directory.md).

## Status and roadmap

Working today: `record` (managed capture — one command starts the recorders and
stamps the session), `demo` (instrumented capture), `transcribe` (local WhisperX
or whisper.cpp), `merge`, `report`, and the first-pass analysis layer — `analyze`
(emit an analysis request, then validate the answer into findings) and `review`
(record human verdicts). `record` captures the microphone by default; screen
video is opt-in with `-video`. Analysis is host-delegated — the CLI never calls a
model or the network — and every finding is *unverified* by default until you
confirm or reject it.

Coming next, in user terms:

- **Codebase mapping** — findings anchored to your code through the
  `data-testid` selectors they were captured against.
- **Reference capture** — narrated sessions over third-party apps, building a
  tagged corpus of design preferences.
- **A macOS app** wrapping the CLI core; Linux stays CLI-only.

## License

MIT — see [LICENSE](LICENSE).
