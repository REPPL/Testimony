# Verification

## Gates

Run from the repo root; CI (`.github/workflows/ci.yml`) runs the same gates on
every push and pull request, across Linux and macOS. Two further CI jobs guard
the supply chain: `gitleaks` scans the full history for committed secrets, and
`zizmor` audits the workflows themselves. A version tag (`vX.Y.Z`) triggers
`.github/workflows/release.yml`, which re-runs the full gate against the pushed
commit and then publishes the per-platform tarballs, a `SHA256SUMS` manifest,
and their SLSA build-provenance attestations.

```bash
gofmt -l .                              # format: any output fails
go build -o testimony ./cmd/testimony
go vet ./...
go test -race ./...
./testimony merge  -session examples/sample-session   # pipeline smoke
./testimony report -session examples/sample-session
```

The pipeline smoke test asserts that `timeline.jsonl` and `report.md` are
non-empty, that the report contains its `## Timeline` heading, and that the
"save button" moment — the demo app's intentional save-feedback flaw,
mirrored in the sample session — survives the merge→report join. That last
grep is the guard on the join logic itself: if windowing or attachment
breaks, the flaw's utterance/event pairing is what disappears first.

## Live end-to-end procedure (macOS)

CI cannot exercise capture or ASR, so a live run on the target Mac verifies
the full loop (ffmpeg + an ASR engine installed):

1. `./testimony demo` — note the printed session directory.
2. Start a QuickTime audio recording, say "session start" aloud, click
   through the demo app while thinking aloud, stop both.
3. `./testimony transcribe -session sessions/<dir> -audio <recording>.m4a`
   — check the printed offset and its provenance; if it looks wrong, locate
   the spoken marker in the transcript and re-run with `-offset`.
4. `./testimony merge -session sessions/<dir>` then
   `./testimony report -session sessions/<dir>`.
5. Read `report.md`: utterances must interleave with the right events — in
   particular, the save-button complaint must sit next to the save-button
   click.

A real captured session is kept under `sessions/` as evidence of the last
live verification.
