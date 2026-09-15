---
id: spc-2609150752365788
slug: fixed-session-location
intent: itd-10
origin: researcher-authored
production_mode: hand-written
---
# fixed-session-location

## Summary

The two capture commands — `record` and `demo` — stop defaulting their `-out`
root to the relative path `sessions`. When `-out` is omitted, both create the
new session under `~/Testimony/sessions`, one fixed root resolved against the
operator's home directory at the moment of the run, so a session lands in the
same place whatever directory the command was invoked from. The root is created
on demand, exactly as an explicit `-out` root already is. `-out DIR` is
unchanged: it wins wherever it is given, is used verbatim whether relative or
absolute, and the home directory is not consulted at all — `-out sessions`
reproduces the old behaviour byte for byte. With `-out` omitted and no home
directory to resolve, the command refuses at the usage status naming the flag
to pass, rather than falling back to a relative root. One helper in
`internal/cli` defines the default and both commands register its result as
their flag default, so `-h` shows the real directory a session will land in and
the two cannot drift. Every other command is untouched: they operate on a
session they are given or infer, and take no `-out` root at all.

## Design

### One definition of the default

`internal/cli` gains `defaultSessionRoot() (string, error)` —
`os.UserHomeDir()` joined with `Testimony/sessions` — and the display constant
`defaultRootDisplay`, the `~/Testimony/sessions` spelling used in the usage
block, the documentation, and the refusal message. Both commands call the
helper before registering their flags and pass its result straight to
`fs.String("out", root, …)`, so the default is stated once and the two commands
cannot diverge in what it means. Nothing downstream changes: the root reaches
`session.Create` as the same opaque string an explicit `-out` always did, and
`session.Create`'s existing `os.MkdirAll(outRoot, 0o755)` creates it and any
missing parent on demand.

Registering the *resolved* path as the flag default, rather than a `~` literal
expanded later, is what makes `testimony record -h` print
`(default "/…/Testimony/sessions")` — the real directory, which is the answer
to the question an operator opens `-h` to ask. The `~` form survives only where
a person reads prose: the usage block's `[-out ~/Testimony/sessions]`, the
reference tables, and the refusal. No expansion of `~` is performed anywhere on
a value the operator supplies; the shell does that, as it always has.

### The refusal

A home directory that cannot be resolved is refused, not papered over. The
check is scoped to the one path that needs a home: `fs.Visit` records whether
`-out` was given, and only when it was *not* given and the helper returned an
error does the command exit at the usage status:

```
record: -out is required (the default root ~/Testimony/sessions cannot be resolved: $HOME is not defined); pass -out DIR
```

`demo` refuses identically under its own name. The message names the root it
tried, carries the reason verbatim from `os.UserHomeDir`, and ends with the one
thing that gets the operator moving. A silent fall back to the relative
`sessions` would put the session beside whichever directory the operator
happened to be standing in — precisely the outcome this intent exists to end —
and would do it invisibly, on the one run where the operator has least reason
to suspect it.

The refusal sits after `rejectArgs` and before the existing empty-`-out` guard,
which keeps the two messages from competing: `-out ""` is an explicitly given
flag, so it keeps reporting `-out must not be empty` whether or not a home
exists, and an omitted `-out` with no home reports the root it could not
resolve. Both go through `usageErr` (2), the status every wrong invocation on
these commands already uses; a root that resolves but cannot be *created* stays
a runtime failure (1) from `session.Create`, naming the path it tried.

### What does not change

The start-up output still prints the session directory `session.Create`
returned — the real, resolved path, which is what the operator opens, hands on,
and passes to `transcribe`. The `-out` flag's help string, its position in the
usage block, both commands' exit statuses, and every other flag are as they
were. No environment variable and no configuration file is read: `-out` remains
the sole override, which is the intent's own out-of-scope fence and keeps the
answer to "where did my session go?" a function of the command line alone.

### Documentation

`docs/reference/cli.md` gains one `Where a new session lands` section holding
the rule for both commands — the fixed default, the on-demand creation, `-out`
as the only override with `-out sessions` named as the project-local form, and
the refusal verbatim — and each capture command's usage line and flag table
points at it. The tutorial, the how-to guides, `README.md`, and
`docs/reference/session-directory.md` carry example session paths that were
written from the old relative default; each moves to the new one, so no page
shows a path the documented default no longer produces.

## Decisions

- **The fixed default is `~/Testimony/sessions`, the press release's own path,
  chosen over an XDG-style `~/.local/share/testimony/sessions`.** Discoverability
  is the whole point of the change. A captured session is evidence — a folder an
  operator reopens weeks later, browses in a file manager, and hands to a
  colleague — not application state, and a dotted or deeply nested directory
  hides exactly the thing the intent set out to make findable. The convention
  XDG encodes is a good one for caches and state; this is neither.
- **The root is resolved via `os.UserHomeDir()` at invocation time, never
  cached and never expanded from a stored `~` literal.** Resolving per
  invocation means the path follows the account actually running the command,
  including a run under `sudo` or a different user, and there is no persisted
  copy to go stale.
- **An unresolvable home directory refuses; it never silently falls back to a
  relative path.** The fallback would be invisible and would recreate the
  scattered-session failure this intent removes, on the one run where the
  operator has least reason to look for it. The refusal is a usage-style error
  (exit 2) naming `-out`, because the invocation is recoverable by adding
  exactly one flag.
- **The refusal is scoped to an omitted `-out`.** An operator who names a root
  never needed a home directory, so a machine with no resolvable home must
  still be able to capture with `-out DIR`. The `fs.Visit` check is what keeps
  the new requirement off that path.
- **`-out DIR` stays the only override — no environment variable, no
  configuration file.** This is the intent's out-of-scope fence, and it is what
  keeps the location of a session a property of the command that created it:
  one place to look, and no invisible state that makes the same command put
  sessions in different places on two machines.
- **The `-out` flag registers the resolved path, so `-h` shows it, while prose
  keeps the `~` form.** The two audiences differ: `-h` is read to learn where
  the session will actually go, and documentation is read by someone who is not
  the operator whose home directory it would name. Committed text therefore
  never carries an absolute local path.
- **This is a behaviour change and is recorded as one.** A single `### Changed`
  bullet under `[Unreleased]` names the old relative `sessions/` default, the
  new root, and the migration in one line — pass `-out sessions` to keep the old
  behaviour — following the changelog's own rule that a change which can break
  an existing invocation is called out in the entry that records it.

## Test plan

In `internal/cli/cli_test.go`, using the existing `Run`-plus-`captureStderr`
pattern, the `chdir` helper, and `t.Setenv("HOME", …)`:

- **The default root** — `TestDefaultSessionRootIsUnderHome` calls the helper
  with `HOME` pointed at a temp directory and the working directory at a
  different one, and requires `<home>/Testimony/sessions`.
- **`record` creates under it** — `TestRecordCreatesUnderTheDefaultRoot` points
  `HOME` at a temp directory and plants a *regular file* at the exact default
  root, making it un-creatable: bare `record` exits 1 naming that path, and the
  working directory is left empty. Without `-demo`, `session.Create` is the
  first thing `record` does, so the failure can come from nowhere else and no
  recorder is ever spawned — which is also what keeps the test from blocking on
  a machine that has ffmpeg.
- **An explicit `-out` is unchanged** — `TestExplicitOutRootIsUnchanged` plants
  the same un-creatable marker at a relative `sessions` and requires
  `record -out sessions` to name it, leave `<home>/Testimony` uncreated, and
  never mention the home directory. It then clears `HOME` and requires
  `demo -addr bogus -out DIR`, `record -demo -addr bogus -out DIR`,
  `demo -out ""`, and `record -out ""` each to reach their own existing check
  rather than the default-root refusal.
- **The unresolvable-home refusal** — `TestUnresolvableHomeRefuses` clears
  `HOME` and requires bare `demo` and bare `record` to exit 2 with
  `<cmd>: -out is required (the default root ~/Testimony/sessions cannot be
  resolved: …); pass -out DIR`, with nothing written into the working directory.
- **The advertised surface** — `TestUsageShowsTheFixedDefaultRoot` pins
  `[-out ~/Testimony/sessions]` on both capture lines and the footer sentence,
  and fails if the old `[-out sessions]` is still advertised.

`demo`'s own creation path is not exercised end to end: it binds its listener
before creating the session, and the suite's established way of reaching
`demo` without binding a real port is a refused `-addr`, which stops earlier
still. Its default is covered by the shared helper's test and by the refusal
that can only fire because `demo` registers that helper's result.

All tests pass under `-race`; none runs in parallel, so the working-directory
and environment changes are safe.

## How acceptance criteria are satisfied

- **No `-out` → the session is created under the fixed default, not relative to
  the current directory** — both commands register `defaultSessionRoot()`'s
  result as the `-out` default, so the value reaching `session.Create` is
  `<home>/Testimony/sessions` and never depends on the working directory.
  `TestRecordCreatesUnderTheDefaultRoot` pins the path `record` tries and that
  the working directory stays empty; `TestDefaultSessionRootIsUnderHome` pins
  the root itself.
- **An explicit `-out DIR` → created under `DIR` exactly as today** — the flag
  default is the only thing that changed, so a given `-out` reaches
  `session.Create` verbatim and the home directory is never read.
  `TestExplicitOutRootIsUnchanged` proves the named root is the one used, that
  the default root is left untouched, and that an unresolvable home does not
  interfere with any explicit-`-out` invocation.
- **A default location that does not yet exist → created automatically** —
  creation is `session.Create`'s existing `os.MkdirAll(outRoot, 0o755)`, which
  is the same call an explicit `-out` root has always gone through, and it
  creates the missing `Testimony` parent as well. The un-creatable-root tests
  reach that call and report from it, which is what pins the default root as
  the argument it receives.
