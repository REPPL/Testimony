# Verification

## Gates

Run from the repo root; CI (`.github/workflows/ci.yml`) runs the same gates on
every push and pull request, on Ubuntu. Two further CI jobs guard
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
non-empty and that the report renders the sample session's fixed content: the
`## Timeline` and `## Findings` headings, the confirmed `F-001` finding, the
"save button" utterance text, and the `save-btn` selector. Only one of these
actually guards the merge→report join — the exact
`**Utterances:** 10 · **Events:** 10` header count. The others all pass even
with `interactions.jsonl` deleted: "save button" comes from the utterance's
own text, and the `save-btn` selector renders from `findings.jsonl`
regardless of events, so the counts line is what catches a windowing or
attachment regression.

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
