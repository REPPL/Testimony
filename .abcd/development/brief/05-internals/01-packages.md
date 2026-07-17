# Package map

Everything lives under `internal/`; `cmd/testimony/main.go` is a thin
entrypoint that calls `cli.Run` and exits with its return code.

- **`internal/cli`** — the command-line interface: usage text, one
  `flag.FlagSet` per subcommand (`demo`, `transcribe`, `merge`, `report`,
  `record` stub, `version`, `help`), and dispatch into the other packages.
  Holds the `Version` variable stamped by the release process. Errors print
  as `testimony: <err>` and map to exit codes (1 failure, 2 usage/stub).
- **`internal/demo`** — the instrumented demo app: an embedded single-page
  settings prototype (`assets/index.html`, via `go:embed`) plus an HTTP
  server that creates the session directory and appends the two capture
  streams (`interactions.jsonl`, `events.rrweb.jsonl`) line by line.
- **`internal/session`** — the on-disk layout of a session: the `Manifest`
  schema, well-known file-name constants, and generic JSONL read/write
  helpers (`ReadJSONL[T]`/`WriteJSONL[T]`) used by every other package.
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
  event↔utterance attachment pass over `EventsNear`, standalone-event
  interleaving, and the pending Findings section.
