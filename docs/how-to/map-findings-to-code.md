# Map findings to code

This guide covers the mapping layer: turning a **confirmed** finding into
proposed references to the source files that own its on-screen anchor, deciding
each reference by hand, and rendering an issue draft you can file wherever your
tracker lives. Testimony delegates the model work to an assistant of your
choice: `map` never calls a model, holds no keys, and adds no network
dependency. It emits a request, you run it against the application's
repository, and it validates the answer.

Prerequisites: a session with at least one finding whose current verdict is
`confirmed` and whose `ui` carries a `selector` or a `route`, a merged
`timeline.jsonl`, and the application's repository checked out on this machine.
The step sits downstream of verification on purpose, so
[analyse the session](analyse-a-session.md) first and confirm what you believe.
A session with no such finding is refused, with the finding count by status,
rather than mapped. A terminal-session finding carries no selector or route, so
`map` has nothing to hand over for it.

The anchors resolve best when the application marks its interactive elements
with stable `data-testid` attributes, because a test id is a literal string the
assistant can search the source for, where a route is a router-table entry it
has to read and interpret. [Instrument your own app](instrument-your-own-app.md)
covers the attribute.

The flow is five steps: **emit** the mapping request, **run** it with your
assistant, **ingest** the answer, **review** the references, and **render** the
issue drafts.

## 1. Emit the mapping request

`testimony map` writes a single, self-contained prompt: a versioned rubric, the
session context, the path to the repository, and, for each confirmed finding
with an anchor, that finding's own record plus its event window from the
timeline. Send it to stdout to read it, or to a file to hand off:

```sh
testimony map -session ~/Testimony/sessions/<dir> -repo ~/code/your-app -out request.md
```

`-repo` names the repository root as the assistant will open it, so it is the
one place an absolute local path appears in the request. Keep the request out
of the session directory, which is what you hand to others: `map` refuses an
`-out` inside it, and nothing in the session directory changes.

`-window` sets the event window's half-width in seconds, as it does for
`draft-tests`, and defaults to 10.

## 2. Run it with your assistant of choice

Give the request to an assistant that can read the repository: an agent host
with the checkout open, or a colleague. Ask it to follow the instructions and
return **only** the JSON answer. Save that answer to a file, for example
`refs.json`.

The expected shape is a JSON object with a `refs` array (a bare array is also
accepted):

```json
{"rubric":"testimony-coderefs/v1","refs":[
  {"id":"R-001","finding":"F-001","session":"sample-session",
   "path":"src/settings/ProfileForm.tsx","line":46,"role":"owner",
   "status":"proposed"},
  {"id":"R-002","finding":"F-001","session":"sample-session",
   "path":"src/settings/saveProfile.ts","line":12,"role":"handler",
   "status":"proposed"}
]}
```

One finding may yield several references: the component that renders the
element, the handler behind it, the router entry, a test. The `role` says which,
and `line` is optional. There is no confidence field, and an answer that adds
one is refused: the human decision in step 4 is the only quality signal.

## 3. Ingest the answer

Validate the answer against the reference schema and the repository, and write
`refs.jsonl`:

```sh
testimony map -session ~/Testimony/sessions/<dir> -repo ~/code/your-app -ingest refs.json
```

Ingest is the validation boundary, and it never trusts the model. It rejects,
with a precise message, any reference that names a finding which is not
currently `confirmed` or carries no anchor, claims a session other than this
one, uses a role outside the set, names a path that does not exist as a regular
file under the repository or escapes it, or gives a line past the file's end.
All errors are reported at once and nothing is written until the whole answer is
clean, so you can fix a batch in one pass. Every reference lands
`status: proposed`, whatever the answer claimed.

The repository is opened read-only, to check that each path exists and each line
is in range, and nothing is written into it. The path check is a containment
check: a `..` segment, an absolute path, a symlink, or a path through a
symlinked directory that leaves the repository is refused.

You can also pipe the answer straight in with `-ingest -`:

```sh
your-assistant < request.md | testimony map -session ~/Testimony/sessions/<dir> -repo ~/code/your-app -ingest -
```

Once a decision exists in `refs.jsonl`, ingest refuses to overwrite the file:
the human record is retained, so a re-map needs a fresh session directory or a
removed `refs.jsonl`.

## 4. Review the references

Each reference is a *proposal* until you judge it. `testimony review -kind refs`
walks the proposed references and records your decision:

```sh
testimony review -session ~/Testimony/sessions/<dir> -kind refs -repo ~/code/your-app
```

For each reference it shows its id, path, line and role, the finding it
resolves with that finding's quote, anchor and clock, and, because `-repo` is
given, the source lines around the referenced line, then prompts
`[a]ccept [r]eject [s]kip [q]uit`. Without `-repo` it shows the record alone.

Your decision is *appended* to `refs.jsonl` with today's date. The reference
line is never overwritten, so the record of what the machine proposed and what
you decided both survive. There is no edit: a wrong path is rejected, and a
corrected one arrives through a fresh ingest.

To record a single decision without the interactive walk (handy in scripts):

```sh
testimony review -session ~/Testimony/sessions/<dir> -kind refs -ref R-001 -decision accepted
testimony review -session ~/Testimony/sessions/<dir> -kind refs -ref R-002 -decision rejected
```

Interactive review needs stdin to be a character device (an interactive terminal
is one); when it is a pipe or a redirected regular file (as in CI) it prints a
notice and exits without blocking. A later decision overrides an earlier one, and
both are kept.

## 5. Render the issue drafts

Render one issue draft per mapped finding:

```sh
testimony map -session ~/Testimony/sessions/<dir> -render -out issues.md
```

Each draft carries a title derived from the finding, its severity, anchor and
clock, the participant's own words, the reproduction steps derived from the
event window, and the suspected files: every reference for the finding, with
its role and its current status. A draft rendered before review therefore says
`proposed` beside each file, so a reader can see which paths a person has
vouched for. The title and the steps are derived by the CLI from the records,
not by the model, so the render needs no second round-trip and cannot invent a
step; the steps read mechanically for the same reason.

Nothing is filed anywhere: Testimony never opens an issue and never writes into
the application's repository, so the output defaults to stdout and `-out` puts it
wherever you assemble your tickets. The **record** stays with the session, in
`refs.jsonl`, linked to its finding.

For the exact field rules, see the
[session directory reference](../reference/session-directory.md#refsjsonl); for
every flag, the [command-line reference](../reference/cli.md#testimony-map).
