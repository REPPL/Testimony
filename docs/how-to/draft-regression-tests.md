# Draft regression tests

This guide covers the drafting layer: turning a **confirmed** finding into a
proposed regression test case, deciding its fate by hand, and rendering the
accepted ones as a Markdown test plan your docs-as-code records can hold.
Testimony delegates the model work to an assistant of your choice —
`draft-tests` never calls a model, holds no keys, and adds no network
dependency. It emits a request, you run it, and it validates the answer.

Prerequisite: a session with at least one finding whose current verdict is
`confirmed`, and a merged `timeline.jsonl`. The step sits downstream of
verification on purpose — only evidence a person already vouched for can become
a test — so [analyse the session](analyse-a-session.md) first and confirm what
you believe. A session with no confirmed finding is refused, with the finding
count by status, rather than drafted from.

The flow is five steps: **emit** the drafting request, **run** it with your
assistant, **ingest** the answer, **review** the drafts, and **render** the plan.

## 1. Emit the drafting request

`testimony draft-tests` writes a single, self-contained prompt: a versioned
rubric, the session context, and — for each confirmed finding — that finding's
own record plus its **event window** from the timeline. Send it to stdout to read
it, or to a file to hand off:

```sh
testimony draft-tests -session sessions/<dir> -out request.md
```

The event window is the only material the steps may be reconstructed from, so its
width matters. `-window` sets the half-width in seconds around the finding's
cited evidence, and it defaults to **10** rather than `report`'s 2.5: a repro
needs the lead-up and the aftermath, not only the moment. On the bundled sample
the 10-second window around `F-001` holds the whole sequence — click the
display-name field, type "Alice", click save, click save again — plus the
utterance in which the participant states what they expected. At 2.5 seconds that
utterance falls outside the window, and a draft made from it has nothing to
ground "expected" in.

```sh
testimony draft-tests -session sessions/<dir> -window 20    # a slower, more deliberate session
```

Nothing in the session directory changes.

## 2. Run it with your assistant of choice

Give the request to any assistant — a chat model, an agent host, or a colleague.
Ask it to follow the instructions and return **only** the JSON answer. Save that
answer to a file, for example `tests.json`.

The expected shape is a JSON object with a `tests` array (a bare array is also
accepted):

```json
{"rubric":"testimony-testdraft/v1","tests":[
  {"id":"T-001","finding":"F-001","session":"sample-session",
   "title":"Saving gives no confirmation",
   "steps":["Open #general in the settings prototype.",
            "Change the display name to Alice.",
            "Click the Save button ([data-testid=save-btn])."],
   "expected":"The save is confirmed on screen — a toast, or the button briefly disabled.",
   "observed":"Nothing visibly changes, so there is no way to tell the save landed.",
   "rationale_quote":"I clicked save and nothing happened",
   "severity":3,"status":"proposed"}
]}
```

One finding may yield more than one draft: a defect that shows up in two
distinct sequences is two test cases.

## 3. Ingest the answer

Validate the answer against the draft schema and write `tests.jsonl`:

```sh
testimony draft-tests -session sessions/<dir> -ingest tests.json
```

Ingest is the validation boundary, and it never trusts the model. It rejects,
with a precise message, any draft that names a finding which is not currently
`confirmed`, claims a session other than this one, carries a
`rationale_quote` that is not its finding's quote byte for byte, restates a
different `severity`, lists no step, leaves `title`, `expected`, or `observed`
empty, or carries a stray field. All errors are reported at once and nothing is
written until the whole answer is clean, so you can fix a batch in one pass.
Every draft lands `status: proposed`, whatever the answer claimed.

The quote and the severity are held to **equality** rather than merely checked
for plausibility. That is what keeps the drafting step incapable of introducing
evidence: it can only carry forward what a person already confirmed, and a
mismatch is the cheapest signal that the draft was linked to the wrong finding.

You can also pipe the answer straight in with `-ingest -`:

```sh
your-assistant < request.md | testimony draft-tests -session sessions/<dir> -ingest -
```

Once a decision exists in `tests.jsonl`, ingest refuses to overwrite the file:
the human record is retained, so a re-draft needs a fresh session directory or a
removed `tests.jsonl`.

## 4. Review the drafts

Each draft is a *proposal* until you judge it. `testimony review -kind tests`
walks the proposed drafts and records your decision:

```sh
testimony review -session sessions/<dir> -kind tests
```

For each draft it shows its id, the source finding with its type, severity and
clock, the title, the steps, the expected and observed behaviour, and the
participant's quote, then prompts `[a]ccept [e]dit [r]eject [s]kip [q]uit`.
Choosing `e` asks for each editable field in turn, showing the current value; a
blank answer keeps it, and steps are read one per line until a blank line. A pass
that changes nothing is recorded as an acceptance.

Your decision is *appended* to `tests.jsonl` with today's date — the draft line
is never overwritten, so the record of what the machine proposed and what you
decided both survive, and an edit can never re-point a draft at a different
finding or session.

To record a single decision without the interactive walk (handy in scripts):

```sh
testimony review -session sessions/<dir> -kind tests -test T-001 -decision accepted
testimony review -session sessions/<dir> -kind tests -test T-003 -decision rejected
testimony review -session sessions/<dir> -kind tests -test T-002 -decision edited -edit edit.json
```

`-edit FILE` (or `-edit -` for stdin) holds the replacement fields as a JSON
object — a subset of `title`, `steps`, `expected`, and `observed`, with at least
one member:

```json
{"title":"Saving a display name gives no confirmation","steps":["Open #general.","Click Save."]}
```

Interactive review needs stdin to be a character device (an interactive terminal
is one); when it is a pipe or a redirected regular file (as in CI) it prints a
notice and exits without blocking. A later decision overrides an earlier one, and
both are kept.

## 5. Render the test plan

Render the accepted drafts as Markdown test-case blocks:

```sh
testimony draft-tests -session sessions/<dir> -render -out tests.md
```

Each block names its source finding and session, the decision and its date, the
numbered steps, the expected and observed behaviour, and the participant's quote
as the rationale — so a failing test leads back to the evidence that motivated
it. Only drafts whose effective status is `accepted` or `edited` are rendered,
with the latest edit applied; a proposal is not a test, and a rejected draft
stays in `tests.jsonl` for the record rather than for the plan. Rendering with no
accepted draft is refused, so `-out` cannot turn an existing plan into an empty
document.

Where the rendered plan lives is your choice: Testimony never writes into the
application's repository, so the output defaults to stdout and `-out` puts it
wherever your docs-as-code test records are kept. The **record** stays with the
session, in `tests.jsonl`, linked to its finding.

For the exact field rules, see the
[session directory reference](../reference/session-directory.md#testsjsonl); for
every flag, the [command-line reference](../reference/cli.md).
