**English** | [日本語](README.ja.md)

> The design records linked throughout this file ([`docs/adr/`](docs/adr/)) are
> Japanese only.

# jira-dash

A TUI that presents a configuration-driven dashboard for Jira, similar to `gh dash`.
Define the ranges you care about as tabs via JQL, then view and create issues from there.

```
 My Issues │ Example Sprint (15) │ Example Backlog │ Other Backlog
→ PROJ-123  📘  基盤のリファクタリング                          進行中   1h  ╭──────────────
  PROJ-118  🔧  管理画面のログイン機能全般の見直し             To Do    2h  │  ## PROJ-123
```

## Prerequisites

- Go (build time only). jira-dash itself calls the Jira API directly, so no external CLI is required
- Credentials must exist at `~/.config/jira-cli/credentials.json` (or as `JIRA_*`
  environment variables). This file is written by `jira auth login` (`cmd/jira` in
  this repository)
  (for why the file is shared, see [docs/adr/0002](docs/adr/0002-credentials-shared-with-the-cli.md))
- macOS is assumed (the `y`/`Y` clipboard uses `pbcopy`)

## Installation

There are two binaries: the TUI itself, and the CLI called by both its auth flow and
its keybindings. Neither is placed on PATH directly; instead a symlink to `scripts/jd`
is placed there. `jd` builds from source if it's newer, then execs — so **there is no
manual build step**
(for the rationale and measurements, see [docs/adr/0012](docs/adr/0012-rebuild-on-launch.md)).

```bash
# The invoked name decides which binary runs. This isn't a shell alias because the
# dotfiles' .zshrc is tracked in a public repo. A symlink keeps it purely a PATH concern.
ln -s "$PWD/scripts/jd" ~/.local/bin/jhd
ln -s "$PWD/scripts/jd" ~/.local/bin/jira

jira auth login

cp config.yml.example config.local.yml
$EDITOR config.local.yml
mkdir -p ~/.config/jira-dash
ln -s "$PWD/config.local.yml" ~/.config/jira-dash/config.yml
```

Builds are placed in `.bin/` (already in `.gitignore`). Setting `JD_NO_AUTOBUILD=1`
skips the build entirely and runs whatever is currently in `.bin/` as-is — useful for
benchmarking, or when you want to keep working against a stable build while the source
is mid-edit.

`scripts/verify-against-old-cli` is a script that diffs this Go CLI's output against
the pre-migration TypeScript CLI's output. It only works while the old CLI is still
installed on the machine
(for the background, see [docs/adr/0001](docs/adr/0001-go-cli-instead-of-the-typescript-one.md)).

The config path can also be given via `--config <path>` or `JIRA_DASH_CONFIG` (both
expand a leading `~` themselves). `config.local.yml` is already in `.gitignore` — it
is never committed, since its JQL contains project keys and sprint names.

```
--config <path>      Config file (default: ~/.config/jira-dash/config.yml)
--section <title>    Open on the tab with this title. If the name doesn't exist, list the candidates and exit
--version            Print the version and exit
```

### Configuration is validated at startup

Anything that would rather fail loudly than be silently ignored is made to fail at
startup.

- **Unknown keys** (a typo like `limmit:`) — YAML silently drops these by default,
  leaving only the misconfigured feature broken. This forces the config to be flagged
  as bad instead
- **Duplicate key bindings** — `create` and `keybindings.issues` sharing a key, or
  either one claiming a key the dashboard itself already uses (`j` / `/` / `q` / `r`,
  etc). Whichever side loses silently loses based on `handleKey`'s evaluation order,
  so the only moment the losing side is visible at all is startup
- **A missing `dir`** — same reasoning as above
- **Empty values** — a section with no title/jql, a `create` entry with no type, a
  keybinding with no command

## Key bindings

| Key | Action |
|------|------|
| `h` / `l` / `←` / `→` / `tab` / `shift+tab` | Switch section |
| `j` / `k` / `gg` / `G` | Move |
| `p` | Toggle preview |
| `ctrl+d` / `ctrl+u` | Scroll the preview by half a screen (the row cursor doesn't move) |
| `/` | Filter (`esc` to clear). Does not refetch |
| `r` | Refetch this section |
| `y` / `Y` | Copy the issue key / URL |
| `?` | Help |
| `q` | Quit |

### Working directory (`dir`)

The directory a keybinding runs in is decided by `defaults.dir`, or a per-section
`dir` (the section takes priority). Because boards and repositories correspond
one-to-one, **the tab is the right granularity** for this, and a keybinding only
needs to be written once to be reused across every tab.

```yaml
defaults:
  dir: ~/src/github.com/example/some-repo
jiraSections:
  - title: Other board
    jql: project = OTHER
    dir: ~/src/github.com/example/other-repo   # section overrides defaults
```

This is used in two ways: it becomes the command's own cwd, and it's also available
as `{{.Dir}}`. Both exist because `tmux split-window` / `new-window` **don't inherit
cwd — they take it via `-c`** — so a command that opens a new pane or window needs
`-c {{.Dir}}`, while a self-contained command like `git log` is satisfied by cwd
alone.

A leading `~` is expanded. Path existence is checked **at startup**, not at the
moment a key is pressed — so that a typo doesn't surface much later as the invoked
command's own error about "can't enter this directory."

Things like `o` (open in browser) are entirely up to the `keybindings.issues` config.
`{{...}}` values are substituted already shell-quoted, so never quote the variable
yourself. Configured keys — including `create` entries — show up under `?` (using
`name:` if given, otherwise the command body itself).

Only keys with `terminal: true` are handed the terminal (the whole dashboard redraws
after the command finishes). Without it, stdout/stderr aren't taken over, and on
failure the last line of stderr shows in the footer. Reserve `terminal: true` for
things that draw their own screen, like editors or pagers — nothing else needs it.

`?` lays out keys (bright) and their descriptions (dim) below the footer, **aligned
into columns** (same as gh-dash). The column count is chosen from 4 down to 1 based
on available width, and each column's width is driven only by that column's own
contents. Long commands are truncated to fit their column — so one long line never
pushes the following columns off-screen.

## Keys that capture typed text (`prompt: true`)

Adding `prompt: true` to a `keybindings.issues` key changes it from immediate
execution to opening a box at the bottom of the screen that **captures what you type**
(multi-line, submit with `Ctrl+d`). Submitting empty is rejected — running with no
instruction at all is what keys without `prompt: true` are for.

The captured text can be passed via two different variables. **Using the wrong one
leaks data outward.**

| Variable | Contents | Use case |
|------|------|------|
| `{{.Prompt}}` | issue key + title + body + `---` + what you typed | passing to Claude |
| `{{.Input}}` | **only** what you typed | posting to Jira |

Mixing them up — e.g. using `{{.Prompt}}` for a comment post — ends up posting the
title and body into Jira along with the comment
(for why they're split into two, see [docs/adr/0009](docs/adr/0009-prompt-versus-input.md)).

### Passing to Claude

```yaml
- key: a
  name: ask claude
  prompt: true
  command: >-
    jhd-claude-split {{.Dir}}
    claude --permission-mode auto {{.Prompt}}
```

This opens in a pane so Claude and jhd sit **side by side on the same screen** (for
why a script splits the pane instead of a template string or window split, see
[docs/adr/0008](docs/adr/0008-claude-pane-budget.md)).

### `jhd-claude-split` (how panes are split)

`scripts/jhd-claude-split` opens **at most two** tmux panes. A third pane, or a call
from outside tmux, is rejected
(for the rationale, see [docs/adr/0008](docs/adr/0008-claude-pane-budget.md)).

```
┌────────────────────────────────┐
│ jira-dash          160x20      │  ← width never changes
├───────────────┬────────────────┤
│ pane 1  80x24 │ pane 2  79x24  │
└───────────────┴────────────────┘
```

- **Pane 1** splits jhd downward (`-fv -l 24`). Rows shrink, columns don't
- **Pane 2** splits **pane 1** sideways, not jhd — jhd stays at 160x20, unaffected
- **Pane 3** is rejected, with the reason printed on tmux's status line
- Closing one pane frees the slot back up (tracked via a tmux per-pane user option,
  `@jhd-claude`, which disappears along with the pane)

Installation:

```bash
ln -s "$PWD/scripts/jhd-claude-split" ~/.local/bin/jhd-claude-split
```

The issue body is included because the preview has **already been fetched** — without
it, the receiving side would have to pay for another Jira REST call (0.5–1.2s), and
might not even hold credentials to make one at all. When the body is empty, or is this
program's own placeholder string `*no description*`, nothing is inserted (so Claude
is never asked to reason about "there's no description").

Constraint: the body is only populated once the preview fetch has completed (150ms
debounce plus the Jira REST round trip). Pressing the key right after moving the
cursor yields only the title and instruction.

Keys without `prompt: true` still execute immediately as before — the right shape for
a fixed command like opening a browser.

### Posting a comment (`refresh: true`)

Using the same box but pointing the target at `jira comment add` turns it into a
comment post. The dashboard itself only ever writes to Jira for issue creation;
everything else is delegated to configured commands, by design
(see [docs/adr/0006](docs/adr/0006-writes-go-through-keybindings.md)).

```yaml
- key: m
  name: comment
  prompt: true
  refresh: true
  command: jira comment add {{.IssueKey}} -b {{.Input}}
```

`refresh: true` refetches both the row and the preview **once the command exits
successfully** (unlike `r`, the row is never cleared in the meantime). It defaults to
off — enabling it adds two more Jira REST calls (roughly 1–2.4s).

Writing `{{.Prompt}}` here would post the title and body into Jira as part of the
comment too. Use `{{.Input}}` for anything that posts.

## Keys that pick from a list (`choices` / `choicesFrom`)

A key with `choices` or `choicesFrom` opens the same box but **shows a list to pick
from** (`↑`/`↓` to move, `enter` to confirm, `esc` to cancel). The chosen value lands
in `{{.Choice}}`. For things like assignee or status — where the set of acceptable
values is short and fixed — picking is faster and more reliable than typing: nobody
can reliably type an accountId or a site-specific status name from memory.

Typing inside the box filters the list live (fuzzy — a match requires the typed
characters to appear in order, not necessarily contiguously, case-insensitive). The
match count is shown on the line above the list. Every character, including `j`/`k`,
goes toward filtering; movement is dedicated to `↑`/`↓`.

`choicesFrom` supports three sources.

```yaml
#: change status (from the transitions actually available right now)
- key: s
  name: status
  choicesFrom: transitions
  refresh: true
  command: jira edit {{.IssueKey}} -S {{.Choice}}

#: change assignee (from users assignable to this issue)
- key: A
  name: assign
  choicesFrom: assignees
  refresh: true
  command: jira edit {{.IssueKey}} -a {{.Choice}}

#: pick from a fixed list (choices — no API call, always the same options)
- key: p
  name: priority
  choices:
    - label: 高
      value: High
    - label: 低
      value: Low
  refresh: true
  command: jira edit {{.IssueKey}} --priority {{.Choice}}
```

- **`choicesFrom: transitions`** — the transitions actually available for this issue
  right now (`GET /issue/{key}/transitions`). No label; the transition name itself is
  passed straight through as `{{.Choice}}`
- **`choicesFrom: assignees`** — users assignable to this issue (`GET
  /user/assignable/search`). `label` is the display name, `value` is the accountId
- **`choicesFrom: statuses`** — candidates built not from config but from **the
  dashboard's own current state** (the statuses present among the current tab's rows,
  deduplicated, in order of appearance). A status with no matching row never appears

The reasoning behind these three sources (why `transitions` is the accurate one, why
`assignees` saves you from typing an accountId by hand, and why the API-free
`statuses` is kept around at all) is in [docs/adr/0010](docs/adr/0010-picker-choice-sources.md).

Because `transitions` / `assignees` require an API call, the box only opens once the
candidates arrive (the footer shows a loading indicator meanwhile). If the fetch
fails, the box never opens and the footer shows why instead.

If you want a fixed list — no request to the site, always the same options — drop
`choicesFrom` and enumerate `choices` instead. `label` and `value` are separate
specifically for cases where the value that needs to be sent differs from what should
be shown; omitting `label` displays `value` as-is.

Writing two or more of `prompt` / `choices` / `choicesFrom` on the same key **fails at
startup**. A single key can only open one kind of box; startup validation prevents the
otherwise ambiguous "first one checked wins" behaviour.

Your own accountId is printed by `jira auth status` (useful when hand-writing
`choices`).

## Creating issues

The mapping from key to issue type is set in config. Type names differ per site (a
Japanese-language site uses Japanese names), so they can't be hardcoded.

```yaml
create:
  - key: c
    type: Task
  - key: C
    type: Story
```

This key opens a box at the bottom of the screen (the same shape as gh-dash's
"Approve with comment…"). Submit with `Ctrl+d`, cancel with `esc` / `Ctrl+c` — inside
the box, `enter` inserts a newline, so the usable keys are listed along the box's
bottom edge.

**Only the title is typed.** The type is fixed by the key, and the project and sprint
are **inherited from the row under the cursor** — which is why a new issue lands right
next to the one you were looking at. The inherited sprint is the active one, or if
there isn't one, the future sprint (i.e. the named backlog). A closed sprint is never
chosen: issues retain every past sprint they belonged to, so the last entry in the
list is not necessarily the current sprint.

The box doesn't open on a tab with no rows (there's no project to inherit from). An
empty title is rejected without being submitted.

The input field is one line for `create` (`jira create -s` takes a single string
argument), three lines for `prompt: true` keys. The box's height is **subtracted from
the table**, not appended below the screen — so the footer never gets pushed off
screen.

## Configuration reference

- **Cache**: section contents are cached under `~/.cache/jira-dash/`; the dashboard
  renders from cache first and then refetches the latest in the background. The cache
  key is the JQL, not the section title — so renaming a tab keeps the cache alive,
  while changing the JQL creates a new cache entry
- **`sprintPrefix`**: setting this on a section filters sprint names by prefix match
  after the JQL fetch. **That section's `limit` needs to cover the count before
  filtering**, not after
- **`ORDER BY`**: a section's JQL must always include `ORDER BY`. Without it, results
  beyond `limit` can be dropped wholesale on a per-project basis
- **Write-capable keybindings**: the dashboard itself only ever writes to Jira for
  issue creation. Everything else — comments, status, assignee, etc. — goes entirely
  through configured keybindings (shell commands)

See [docs/adr/](docs/adr/) for the reasoning behind each of these settings.
