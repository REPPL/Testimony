# Verification

## Gates

Run from the repo root; CI (`.github/workflows/ci.yml`) runs the same gates on
every push and pull request, on Ubuntu, plus checks with no local command
below: an installer syntax check (`sh -n install.sh && bash -n install.sh`),
installer flag-handling tests (`--help`/`--dir`/`--version`/`--bogus`), and a
compile-only cross-check for the other release platforms. Two further CI jobs
guard the supply chain: `gitleaks` scans the full history for committed
secrets, and `zizmor` audits the workflows themselves. A version tag
(`vX.Y.Z`) triggers
`.github/workflows/release.yml`, which re-runs the format/build/vet/race/smoke
gates, the installer checks, `gitleaks`, and `zizmor` against the pushed
commit, then publishes the per-platform tarballs, a `SHA256SUMS` manifest, and
their SLSA build-provenance attestations. release.yml also runs one gate CI
never does: it asserts `install.sh`'s `VERSION=` pin names the tag being
released, so a stale pin fails the release rather than shipping the `curl |
sh` install path a version behind. The supply-chain jobs are repeated
here rather than trusted from an earlier CI run because a tag can point at a
commit that was never pushed through a branch (the installer-syntax step's own
comment names the same gap) — release.yml checks out `github.sha` for every
gate job, so all of them, including these two, run against the exact commit
whose tarballs will ship.

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
"save button" utterance text, the `save-btn` selector, the exact
`**Utterances:** 10 · **Events:** 10` header count, and one indented event
bullet naming that same selector. The header count is what catches events
going missing from the merge: every other assertion up to it still passes with
`interactions.jsonl` deleted ("save button" comes from the utterance's own
text, and the `save-btn` selector renders from `findings.jsonl` regardless of
events). But the header count is computed from raw entry counts, before the
join, so it stays `10 · 10` even when every event is detached from the speech
it accompanies — the indented bullet is what catches that: an unattached event
renders as a standalone, unindented bullet, so only a genuinely joined event
can produce one at the expected indent.

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
