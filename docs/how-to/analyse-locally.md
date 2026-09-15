# Analyse a session locally

This guide covers the route that keeps a session's analysis on the machine that
recorded it, and records that it did. Follow it when an ethics protocol, a
participant population, or your own judgement rules out sending even derived text
to a cloud service.

Prerequisite: a session with a merged `timeline.jsonl`, and a model you can run
on the machine. Testimony neither ships, installs, nor configures one — see
"What Testimony does and does not do" below.

The route is four steps: **emit** the request to a file, **run** it against your
local model, **ingest** the answer with `-backend local`, and **read** the
provenance line in the report.

## 1. Emit the request to a file

```sh
testimony analyze -session sessions/<dir> -out request.txt
```

`analyze` reads `manifest.json` and `timeline.jsonl` and writes one
self-contained prompt. Nothing in the session directory changes, and nothing
leaves the machine: `analyze` never calls a model, holds no keys, and adds no
network dependency.

`request.txt` contains the whole of what the model sees — the rubric, the session
context, and the timeline lines. Read it before you go further if you want to
know exactly what you are about to hand over.

## 2. Run the request against your local model

Give `request.txt` to whatever you run on this machine, and save the JSON answer
beside the session:

```sh
your-local-runner < request.txt > answer.json
```

The request asks for JSON only. Any runner will do — the contract is the text in
`request.txt` and the JSON shape it asks for, not a particular tool. This is the
step where "local" is either true or not: it is true if, and only if, the program
you pipe into answers from this machine.

## 3. Ingest the answer, declaring the backend

```sh
testimony analyze -session sessions/<dir> -ingest answer.json \
  -backend local -model llama3.1:70b
```

Ingest validates the answer against the findings schema exactly as it does for
any other run — see [Analyse a session](analyse-a-session.md) for what it
rejects — and writes `findings.jsonl`. The difference is the first line:

```json
{"kind":"provenance","rubric":"testimony-analysis/v1","backend":"local","model":"llama3.1:70b","at":"2026-09-15"}
```

`-model` is free text and at most 200 characters. A value that renders as
nothing — whitespace, invisible characters, or backticks alone — is refused
rather than stored, so what the report shows is always what you typed. Use whatever names the model
you actually ran; a runner-qualified name such as `ollama/llama3.1:70b` is fine,
and is often the most useful thing to write.

The run confirms what it recorded:

```
validated 5 findings → sessions/<dir>/findings.jsonl (all unverified; local backend, model llama3.1:70b)
```

Omit `-backend` and the run still succeeds — the flags never break an existing
invocation — but the record then says the backend was not recorded, and the run
tells you so on stderr before it starts. There is no way to claim `unrecorded` on purpose: it is
what the absence of a declaration records.

## 4. Read the provenance line in the report

```sh
testimony report -session sessions/<dir>
```

The Findings section opens with the declaration:

```
_Provenance (as declared at ingest): local backend · model `llama3.1:70b` · rubric `testimony-analysis/v1` · ingested 2026-09-15._
```

That line travels with the report. When Carol hands the session to an ethics
reviewer six months later, the claim is on the page rather than in anybody's
memory of a shell session.

Review the findings as usual (`testimony review -session sessions/<dir>`); your
verdicts are appended below, and the provenance line is never rewritten.

## What Testimony does and does not do

**It does** carry your declaration with the evidence: written at the moment you
make it, replaced only when the findings it describes are replaced, and rendered
into the report that gets shared.

**It does not** verify the declaration. `analyze` never calls a model and has no
way to observe where `request.txt` was answered. The record says what you told
it, which is why it is worth only as much as your being the person who ran the
request.

**It does not** manage the model. Which model you run, how you run it, and
whether it is good enough for the second-coder role are yours to decide. On that
last question, Testimony's answer is the one it already gives for every backend:
every finding is born `unverified`, and the confirm/reject verdicts you retain
are the running measure of how well the analyser is doing. Now that findings
files name their backend, those verdicts can tell you how a local model compares
with a cloud one on your own sessions, rather than on somebody's benchmark.

## Where this leaves the privacy boundary

Voice and screen were already local, and so is transcription. Run this route and
the analysis is too, so no session content leaves the machine at any step. The
[privacy explanation](../explanation/privacy.md) sets out the boundary in full,
including the one disclosure that survives regardless — the demo page's
session-replay recorder, which a real session in your own instrumented app does
not use.

For every flag, see the [command-line reference](../reference/cli.md); for the
record's exact fields, the
[session directory reference](../reference/session-directory.md#findingsjsonl).
