# Record a terminal session

Testimony reads a terminal recording the same way it reads a voice recording made outside the tool: you record it yourself, then hand the file over. The recorder is [asciinema](https://asciinema.org), which writes an *asciicast* file; `testimony import` normalises that file's terminal output into the session's interaction stream, on the session clock, so a spoken stumble lands beside the command that caused it.

`record` is untouched by any of this. It gains no flag, starts no recorder, and ends exactly as it does for an audio-only session.

## Install asciinema

```sh
brew install asciinema      # or: pipx install asciinema
```

Either version works. Homebrew ships the 3.x line, which writes asciicast v3; PyPI ships 2.4.0, which writes asciicast v2. `import` reads both and tells the two apart from the file's own header, so no `--output-format` flag is needed and you never have to state which format you have.

## Record the session

You need two terminal windows: one for Testimony, one for the work.

1. **Window one — start the session.** This is the window that owns the session directory and the narration.

   ```sh
   testimony record -app "my CLI" -participant P1 -task "Build the project and read the output"
   ```

   Note the session directory it prints.

2. **Window two — start the terminal recording.** This is the window where the work happens.

   ```sh
   asciinema rec session.cast
   ```

3. **Say "session start" aloud.** The spoken marker is the cross-check for the clock (see *Fix a wrong clock offset* below), and it matters more here than for audio: an asciicast header timestamp is a whole number of seconds, so the reconstructed clock can sit up to a second away from the session anchor.

4. **Do the work in window two, thinking aloud.** Speak into window one's recording as you go — what you expect, what surprises you, what you cannot tell from the output.

5. **End the cast** the way asciinema always ends one: exit the recorded shell (`exit`, or `Ctrl-D`).

6. **End the session** with `Ctrl+C` in window one.

## Import the cast

```sh
testimony import -session ~/Testimony/sessions/<dir> -cast session.cast
```

`import` copies the cast into the session as `terminal.cast`, normalises its output into `interactions.jsonl`, and prints the clock offset it used and its provenance. Then finish the pipeline as usual:

```sh
testimony transcribe -session ~/Testimony/sessions/<dir>
testimony merge      -session ~/Testimony/sessions/<dir>
testimony report     -session ~/Testimony/sessions/<dir>
```

Each line the terminal displayed becomes one interaction record, which the report renders beside the utterance it falls next to:

```
**[00:22] P1:** “Wait, it says the build succeeded, but there's no binary.”
  - [00:21] terminal_output "make build"
  - [00:22] terminal_output "build finished in 1.2s"
```

## Do not capture input

Record output only, which is what plain `asciinema rec` does. Input capture is opt-in on both CLI lines — `--stdin` on 2.x, `--capture-input` (or `-I`) on 3.x — and the reason to leave it off is a password typed at a prompt that suppresses echo: the characters never appear on screen, so they are absent from the output stream, but input capture records them as keystrokes.

`import` drops every input event it finds, under any invocation, so keystrokes cannot reach the derived text even in a cast someone else handed you. It prints how many it dropped, which is how you learn that a recorder captured them. Those keystrokes do remain in the archived `terminal.cast`, exactly as the raw voice recording remains in `audio.wav` — `import` normalises the operator's evidence rather than rewriting it.

## Turn colour off

Record with colour disabled:

```sh
NO_COLOR=1 asciinema rec session.cast
```

Some tools ignore `NO_COLOR`; `TERM=dumb` persuades most of the rest.

Escape sequences are kept verbatim in the interaction records, because a record is evidence. The report's rendering strips the escape byte itself — no terminal control sequence ever reaches `report.md` — but the printable tail of a colour sequence survives, so a coloured `ls` reads as `terminal_output "[0;34mdocs[0m"`. Recording without colour avoids the litter, and it makes the records shorter, cheaper to hand to an analysis model, and easier to search.

## Privacy: read the timeline before you analyse it

A shell shows far more of the machine than a demo app does. Terminal output routinely carries usernames, hostnames, absolute paths, environment values, and occasionally a secret a tool prints. `testimony analyze` emits the whole timeline as part of its request, so:

- **Read or redact `timeline.jsonl` before running `analyze`.** It is a small, line-oriented file; skim it the way you would a document you are about to send someone.
- **Keep terminal sessions short.** The whole timeline goes into the request, so a verbose session makes a large one.
- **Treat `terminal.cast` as raw local evidence**, of the same class as `audio.wav`: it holds the bytes the terminal emitted, and it never leaves your machine.

See [privacy](../explanation/privacy.md) for the boundary the whole pipeline holds.

## Fix a wrong clock offset

`import` prints the offset it used and where it came from, for example:

```
offset: -2.00s (derived: cast header timestamp − manifest t0 (whole seconds, ±1s))
```

Three provenance forms appear:

| Printed provenance | Meaning |
|---|---|
| `from -offset flag` | you stated the offset; it always wins |
| `derived: cast header timestamp − manifest t0 (whole seconds, ±1s)` | taken from the cast's own header, to the nearest whole second |
| `default 0: cast header carries no timestamp` | the cast carries no anchor, so the recording is assumed to start at the session anchor |

If the report shows terminal output clearly misaligned with the speech, correct it from the spoken marker exactly as [fix a wrong clock offset](transcribe-a-recording.md#fix-a-wrong-clock-offset) describes for audio, then re-import. `-cast` is not needed the second time — the session already holds `terminal.cast`:

```sh
testimony import -session ~/Testimony/sessions/<dir> -offset -12.4
```

Re-run `testimony merge` and `testimony report` afterwards to rebuild the timeline and the report.

## One terminal recording per session

A session holds one terminal recording. Importing a different cast into the same session replaces the first one's records and its `terminal.cast`, because both are identified as this importer's own. Two terminals recorded side by side belong in two sessions.
