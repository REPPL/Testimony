# Package map

Everything lives under `internal/`; `cmd/testimony/main.go` is a thin
entrypoint that calls `cli.Run` and exits with its return code.

- **`internal/cli`** — the command-line interface: usage text, one
  `flag.FlagSet` per subcommand (`demo`, `record`, `transcribe`, `merge`,
  `report`, `analyze`, `draft-tests`, `review`, `version`, `help`), and dispatch
  into the other packages. Holds the `Version` variable stamped by the release
  process. Errors print as `testimony: <err>` and map to exit codes (1
  failure, 2 usage).
- **`internal/demo`** — the instrumented demo app: an embedded single-page
  settings prototype (`assets/index.html`, via `go:embed`) plus an HTTP
  server that creates the session directory and appends the two capture
  streams (`interactions.jsonl`, `events.rrweb.jsonl`) line by line.
- **`internal/record`** — the managed capture launcher: creates the session
  directory (via `session.Create`, the same code path `demo` uses), starts
  the microphone recorder — and, with `-video`, the screen recorder — as
  ffmpeg subprocesses, and stops them cleanly on Ctrl+C.
- **`internal/session`** — the on-disk layout of a session: the `Manifest`
  schema, well-known file-name constants, the shared size caps
  (`MaxJSONLLine`/`MaxJSONLBytes`/`MaxAnswerBytes`), and generic JSONL read/write
  helpers (`ReadJSONL[T]`/`WriteJSONL[T]`) used by every other package. It also
  holds the two dangerous session-file writes, each written once:
  `AppendRecord` (one appended record — the no-follow open, the exclusive lock,
  the two size pre-flights, the newline framing over an unterminated last line,
  an optional under-lock `Verify`, the partial-write rollback, and the returned
  Close error) and `CommitRecords` (a guarded whole-file replacement, buffered
  before the truncate and rolled back to empty on a short write). `findings.jsonl`
  and `tests.jsonl` both hold a machine record plus appended human records, so
  both write through these rather than `WriteJSONL`
  ([ADR 0001](../../decisions/adrs/0001-one-append-primitive-and-one-review-verb.md)).
- **`internal/timeline`** — the data model of the merged record: `Utterance`,
  `Word`, `Interaction`, and `Entry` types; `BuildEntries` (normalise both
  streams to session-relative seconds and sort); `EventsNear` (the join-window
  query); and `Merge`, the read-build-write pipeline behind the CLI command.
- **`internal/transcribe`** — the local ASR pipeline: engine detection
  (WhisperX preferred, whisper.cpp fallback), ffmpeg audio conversion,
  ffprobe offset derivation, per-engine subprocess runners that parse the
  engines' JSON output files into engine-neutral segments, and the mapping of
  segments to the `Utterance` schema. Fixture-tested against golden JSONL
  files in `testdata/`.
- **`internal/report`** — Markdown rendering of a merged timeline: the
  event↔utterance attachment pass (the same window test as
  `timeline.EventsNear`, inlined and keyed by position so the join does not
  depend on id uniqueness), standalone-event interleaving, and the rendered
  Findings section.
- **`internal/analyze`** — the first-pass analysis layer: emits a
  self-contained, host-delegated analysis request (a versioned rubric plus
  the session's timeline) and is the sole validation boundary for the
  model's answer, writing validated findings to `findings.jsonl`.
- **`internal/drafttests`** — the regression-test drafting layer: emits a
  self-contained, host-delegated drafting request (a versioned rubric plus each
  confirmed finding and its event window, via `Window`), is the sole validation
  boundary for the model's answer (writing proposed drafts to `tests.jsonl`),
  records the human accept / edit / reject decision as an appended record, and
  renders the accepted drafts as a Markdown test plan. Imports `analyze` (for
  `Load`, `EffectiveStatus`, and `LoadTimeline`), `session`, and `timeline`.
- **`internal/review`** — records human verdicts on candidate findings, each
  appended to `findings.jsonl` as a separate, non-destructive record rather
  than an in-place rewrite, so a finding's birth state and full verdict
  history both survive. Its `-kind` dispatch sends the tests side to
  `internal/drafttests`, so the pipeline has one human-decision verb.
