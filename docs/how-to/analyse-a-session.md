# Analyse a session

This guide covers the first-pass analysis layer: turning a merged
`timeline.jsonl` into structured findings, then confirming or rejecting each one
by hand. Testimony delegates the model work to an assistant of your choice — the
CLI never calls a model, holds no keys, and reaches no network. It emits a
request, you run it, and it validates the answer.

Prerequisite: a session with a merged timeline. If you have only a transcript and
interactions, run `testimony merge -session sessions/<dir>` first.

The flow is four steps: **emit** the request, **run** it with your assistant,
**ingest** the answer, and **review** the findings — then re-render the report.

## 1. Emit the analysis request

`testimony analyze` writes a single, self-contained prompt: a versioned rubric
plus the session's timeline. Send it to stdout to read it, or to a file to hand
off:

```sh
testimony analyze -session sessions/<dir> -out request.txt
```

The request pins a rubric version (`testimony-analysis/v1`), asks for two passes
(segment-level coding, then session-level synthesis), and states the output
shape. Nothing in the session directory changes.

## 2. Run it with your assistant of choice

Give the request to any assistant — a chat model, an agent host, or a colleague.
Ask it to follow the instructions and return **only** the JSON answer. Save that
answer to a file, for example `answer.json`.

The expected shape is a JSON object with a `findings` array (a bare array is also
accepted):

```json
{"rubric":"testimony-analysis/v1","findings":[
  {"id":"F-001","t":22.0,"type":"bug","severity":3,"mode":"A",
   "quote":"I clicked save and nothing happened",
   "evidence":["utt-004","ev-003","ev-004"],
   "ui":{"selector":"[data-testid=save-btn]","route":"#general"},
   "status":"unverified"}
]}
```

## 3. Ingest the answer

Validate the answer against the findings schema and write `findings.jsonl`:

```sh
testimony analyze -session sessions/<dir> -ingest answer.json
```

Ingest is the validation boundary, and it never trusts the model. It rejects, with
a precise message, any finding whose evidence id is not in the timeline, whose
quote is not spoken verbatim in a cited utterance, whose `type` or `severity` is
out of range, whose `ui` selector or route names no real event, or that carries a
stray field. All errors are reported at once and nothing is written until the
whole answer is clean, so you can fix a batch in one pass. Every finding lands
`status: unverified`, whatever the answer claimed.

You can also pipe the answer straight in with `-ingest -`:

```sh
your-assistant < request.txt | testimony analyze -session sessions/<dir> -ingest -
```

## 4. Review the findings

Each finding is a *candidate* until you judge it. `testimony review` walks the
unverified findings and records your verdict:

```sh
testimony review -session sessions/<dir>
```

For each finding it shows the clock, type, severity, the participant's quote, and
the on-screen anchor, then prompts `[c]onfirm [r]eject [d]uplicate-of [s]kip
[q]uit`. Choosing `d` asks which finding this duplicates (`F-NNN`). Your verdict
is *appended* to `findings.jsonl` with today's date — the original finding is
never overwritten, so the record of what the machine proposed and what you
decided both survive.

To record a single verdict without the interactive walk (handy in scripts):

```sh
testimony review -session sessions/<dir> -finding F-001 -verdict confirmed
testimony review -session sessions/<dir> -finding F-005 -verdict duplicate-of-F-001
```

Interactive review needs a terminal; when stdin is not a terminal (as in CI) it
prints a notice and exits without blocking.

## 5. Re-render the report

Rebuild `report.md` to see the findings grouped by verdict:

```sh
testimony report -session sessions/<dir>
open sessions/<dir>/report.md
```

The Findings section lists findings under **Confirmed**, **Unverified**,
**Duplicate**, and **Rejected**, each with its quote, anchor, and — where you
recorded one — the verdict and its date. Re-run `review` any time to change a
verdict; the latest one wins, and the history is kept.

For the exact field rules, see the
[session directory reference](../reference/session-directory.md#findingsjsonl);
for every flag, the [command-line reference](../reference/cli.md).
