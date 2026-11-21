# Commands

Every tackroom command, grouped by task. Find what you want to do in the tables, each with a one-line description; flags, edge cases, exit codes, the config lookup order and output rules follow under [Reference](#reference). `tackroom help` lists the commands and `tackroom help --all` adds every form and flag.

```bash
tackroom help --all                  # the full set, as listed below
tackroom <command> --help            # flags of one command (exit code 0)
```

## Set up and sync

| Command | What it does |
|---|---|
| `tackroom setup` | Creates the config root, imports what your harnesses have, runs the first sync, installs the periodic job and prints a short summary. See [setup.md](setup.md). |
| `tackroom sync` | Writes each harness's native config from the repo. |
| `tackroom sync --plan-digest DIGEST` | Syncs only if the plan is still the one a dry run printed. |
| `tackroom status` | Shows per harness what is synced, drifted, in cooldown or blocked. |
| `tackroom pull` | Runs `git pull --ff-only` in the config root, then `sync`. This is what the periodic job runs. |
| `tackroom restore` | Lists the backups of native files, newest first, or puts one back. |
| `tackroom cron` | Installs, replaces or removes the periodic sync job. |

## Check health

| Command | What it does |
|---|---|
| `tackroom doctor` | Checks skills, roles, the lock and materialized copies, native hooks, the periodic job and external sources. |
| `tackroom doctor audit` | Lists audit findings per item. |
| `tackroom doctor audit accept <item> <rule> --reason "..."` | Records an accepted finding in `tackroom.lock`. |
| `tackroom doctor audit revoke <item> <rule>` | Removes that decision. |
| `tackroom doctor deps` | Checks the age of exact npm and PyPI versions your config references. |
| `tackroom sync deps` | Updates patch-level dependencies in the config root (Go modules, pnpm, uv), then runs the age check. |
| `tackroom hosts` | Shows tackroom on each machine: its version, sync state and config commit. |
| `tackroom hosts fix` | Runs the fix `tackroom hosts` names, on each host that has one. |
| `tackroom hosts setup HOST` | Sets up a machine that has no tackroom config yet, over SSH. |

## Skills

| Command | What it does |
|---|---|
| `tackroom skill new <name>` | Creates `skills/<name>/SKILL.md`. |
| `tackroom skill list` | Lists every entry in each harness skill root with its provenance and context cost. |
| `tackroom skill info <name>` | Shows one skill's origin and how each harness sees it. |
| `tackroom skill sources` | Lists configured external sources and ones installed outside tackroom. |
| `tackroom skill sources search <query>` | Searches skills.sh. |
| `tackroom skill sources add <owner/repo[/path]>` | Pins a source and syncs it. |
| `tackroom skill update [name ...]` | Advances pins. |
| `tackroom publish` | Pushes allowlisted skills to a remote registry. |

## MCP servers and hooks

| Command | What it does |
|---|---|
| `tackroom mcp list` | Prints the servers in `tackroom.yaml`. |
| `tackroom mcp add <name>` | Declares a server. |
| `tackroom mcp import <harness> <name>` | Copies a server a harness already has into `tackroom.yaml`, with secrets replaced by references. |
| `tackroom mcp remove <name>` | Deletes the entry; the next `sync` removes it from the harnesses. |
| `tackroom hook list` | Lists native hook registrations and marks which are managed. |
| `tackroom hook remove <query>` | Removes matching registrations. |

## Config and web view

| Command | What it does |
|---|---|
| `tackroom config validate` | Checks the config and prints its file and revision. |
| `tackroom config print` | Prints the shared, local and effective config. |
| `tackroom config inventory` | Prints, as JSON, what the web view's tabs draw. Read-only. |
| `tackroom config set <section/name/field> <value>` | Changes one field of an entry, as the web view's switches and cells do. |
| `tackroom view` | Opens the loopback web UI, starting it in the background when none runs. `--json` for scripts, `--stop`, `--foreground`. See [view.md](view.md). |

## Crons

| Command | What it does |
|---|---|
| `tackroom cron collect` | A read-only inventory of scheduled jobs here and over SSH. |
| `tackroom cron apply` | Installs, replaces or removes the jobs declared under `crons.jobs`. |
| `tackroom cron run ID`, `pause`, `resume` | Acts on a launchd job. |
| `tackroom cron schedule ID "MIN HOUR DOM MON DOW"` | Sets a new schedule for a job tackroom owns on this machine. |
| `tackroom cron add ID` | Declares an owned job and applies it. |
| `tackroom cron edit ID` | Changes the given fields of an owned job. |
| `tackroom cron remove ID` | Deletes the declaration and the unit. |

All seven are in [crons.md](crons.md). The hosts in `crons.hosts` are also the machines `tackroom hosts` reports on ([Check health](#check-health)).

## Terminals

| Command | What it does |
|---|---|
| `tackroom tmux` | Opens a tackroom workspace in tmux. See [terminals.md](terminals.md#tmux). |
| `tackroom zellij` | Opens a tackroom workspace in zellij. See [terminals.md](terminals.md#zellij). |
| `tackroom tern` | Opens a tackroom workspace in the Tern terminal. See [terminals.md](terminals.md#tern). |
| `tackroom rex` | Opens a tackroom workspace in the Rex terminal. See [terminals.md](terminals.md#rex). |

## Other

| Command | What it does |
|---|---|
| `tackroom version` | Prints the version; `tackroom --version` does the same. |
| `tackroom help [--all]` | Prints the usage. |

## Reference

### Set up and sync in detail

`tackroom setup [--from GIT_URL] [--overlay NAME] [--review] [--verbose] [--agents a,b] [--no-cron] [--dry-run] [--json]`

- Starter files go only into a config root without `tackroom.yaml`, and what your harnesses have is imported by copy.
- It asks nothing; `--review` asks before each step, and `--verbose` prints the full sync report.
- `--from` clones (or pulls) your config repository first, for another machine.
- `--overlay NAME` copies `overlays/NAME.yaml` from the config repository to `tackroom.local.yaml` before the first sync and never overwrites a different one ([Machine overlays](setup.md#machine-overlays)).
- `--yes` and `--memory` are accepted and do nothing. See [setup.md](setup.md).

`tackroom sync [--pull] [--dry-run] [--replace-conflicts] [--agents a,b] [--json] [--allow-fresh[=item,...]] [--reason TEXT]`

- `--pull` runs `tackroom pull` first.
- `--dry-run` prints the plan, including items in cooldown and blocked items, and writes nothing.
- `--replace-conflicts` replaces a native file that differs from the shared copy, after a backup ([troubleshooting.md](troubleshooting.md#conflicts)).
- `--allow-fresh` skips the cooldown of items, and `--reason` records why ([skills.md](skills.md#the-72-hour-cooldown)).
- With `--json`, a dry run also lists:
  - `plan`, one line per harness plus one for starter files a sync would add, update or remove;
  - the `destructive` items: every skill, role, MCP server, hook, plugin, Pi package and starter file the sync removes, and every role it overwrites;
  - a `plan_digest`.
- A real `sync` still syncs the other harnesses when one has a conflict, and then exits 1.

`tackroom sync --plan-digest DIGEST` syncs only if `sync --dry-run --json` would still print that `plan_digest`, and then syncs from the config it just checked. The [Herdr panel](terminals.md#herdr-panel)'s Sync uses it.

- The digest covers the whole dry-run document and the bytes of `tackroom.yaml` and `tackroom.local.yaml`, so a changed plan or any config edit is refused with exit 1.
- It does not cover the text inside skill and role files: an edit there that leaves the plan's lines the same still applies.
- An empty digest is an error.

`tackroom status [--verbose] [--exit-code] [--json] [--agents a,b]`: `-v` is short for `--verbose`, and `--exit-code` is described under [Exit codes](#exit-codes).

`tackroom pull`: a failed pull only warns.

`tackroom restore [<backup> | latest] [--dry-run]`: what it replaces is saved as a new backup, so `restore latest` undoes a restore. See [troubleshooting.md](troubleshooting.md#conflicts).

`tackroom cron [--interval 15m] [--deps] [--remove]`: `--deps` installs the weekly dependency job instead. See [setup.md](setup.md#sync-on-a-schedule).

### Check health in detail

- `tackroom doctor [--json] [--agents a,b] [--e2e]`: `--e2e` runs a real `sync`, `status` and `doctor` in order, so it writes.
- `tackroom doctor audit [--json]`. In `doctor audit accept`, the item is `kind:name`, for example `skill:foo`. The audit gate is in [skills.md](skills.md#the-audit-gate).
- `status`, `doctor`, `pull`, `doctor deps` and `sync deps` take `--skip-package-age`, which skips the registry lookups for package age.

`tackroom hosts [HOST...] [--json]` shows tackroom on each machine in `crons.hosts`, or on the hosts you name; with neither, only `local`, this machine.

- It probes every host at once, over SSH as `cron collect` does.
- It reports each one's tackroom version, whether it is set up and synced, and its config root's commit against this machine's: the same, behind, ahead, diverged, or unknown when this machine's git does not have it, plus uncommitted changes.
- It prints a table with the command that fixes each problem, or JSON with `--json`, where `fix_runnable` says whether `tackroom hosts fix` runs that command.
- It exits 0 whatever the machines report.

| State | When |
|---|---|
| `fail` | The machine cannot be reached, has no tackroom, its `status` fails (for example, no `tackroom.yaml`) or it runs a tackroom a major version behind this machine's. |
| `warn` | Its `tackroom.yaml` is not committed (so no other machine can pull it), it is out of sync, behind or diverged, has uncommitted changes or runs an older tackroom than this machine. |
| `ok` | Otherwise. |

`tackroom hosts fix [HOST...] [--dry-run]` runs the fix `tackroom hosts` names for each host you name, or for every host that has one.

- It runs only tackroom's own verbs on that host: `setup`, `pull` or `sync`, or `git pull --ff-only` in its config root and then `setup`, over SSH as the probe does, or this binary for `local`.
- Their output follows a line naming the host, then it probes the host again and prints its new row.
- A fix that commits and pushes on this machine is printed for you to run; tackroom never commits or pushes for you. So is `tackroom hosts setup HOST`, the fix for a machine that has tackroom but no config.
- A host with nothing to fix says so and counts as fine. `--dry-run` prints what it would run and runs nothing.

`tackroom hosts setup HOST [--from GIT_URL] [--overlay NAME] [--no-cron] [--install] [--dry-run]` sets up a machine that has no tackroom config yet: it runs `tackroom setup --from GIT_URL` there over SSH, with `--overlay` and `--no-cron` passed through.

- `--from` defaults to the `origin` remote of this machine's config root.
- It finds tackroom on the host as `hosts` does (PATH, then `~/.local/bin`, `~/go/bin`, `/opt/homebrew/bin` and `/usr/local/bin`). Without one it stops with the two install commands.
- With `--install` it runs `brew install yourconscience/tap/tackroom`, or `npm install -g tackroom` when the host has npm and no brew.
- A host that already has a `tackroom.yaml` is left alone: use `hosts fix`.
- Afterwards it prints the host's row and, when the host is not in `crons.hosts`, the lines to add to `tackroom.local.yaml`.
- `--dry-run` runs only the read-only probe on the host and prints the commands.

### Skills in detail

- `tackroom skill new <name> [--description TEXT]`
- `tackroom skill list [--agents a,b]`
- `tackroom skill sources [--json]`
- `tackroom skill sources add <owner/repo[/path]> [--skills a,b | --all] [--latest] [--no-sync] [--allow-fresh[=item,...]]`: a `--latest` head younger than 72 hours is in cooldown until it is 72 hours old or `--allow-fresh` skips the cooldown.
- `tackroom skill update [name ...] [--allow-fresh[=item,...]]`
- `tackroom publish [--target NAME] [--skills a,b] [--dry-run] [--json] [--yes]`: see [skills.md](skills.md#publish-skills).

### MCP servers and hooks in detail

- `tackroom mcp add <name> (--command CMD [--arg V] [--env K=V] | --url URL [--header 'Name: value']) [--agents a,b]`
- `tackroom mcp import <harness> <name> [--agents a,b]`
- `tackroom mcp remove <name>`: see [mcp.md](mcp.md).
- `tackroom hook list [--agents a,b] [query]`
- `tackroom hook remove [--dry-run] [--agents a,b] <query>`: see [hooks.md](hooks.md).

### Config and web view in detail

`tackroom config validate [--config PATH]`, `tackroom config print [--config PATH]` and `tackroom config inventory [--config PATH]` only read.

`config inventory` prints what the web view's Overview, Skills, Roles and MCP & hooks tabs draw: per harness the counts and state, per skill, role, MCP server and hook its state in each harness (`ok`, `drift`, `missing`, `conflict`, `off`, `disabled`, `unsupported`, `absent`, `error`), and each skill's origin. The [Herdr panel](terminals.md#herdr-panel) draws it.

`tackroom config set [--config PATH] <section/name/field> <value>` changes one field, for example `config set mcp_servers/exa/enabled false` or `config set hooks/notify/agents '["claude","codex"]'`.

- The path is always `section/name/field`: the section is `agents`, `mcp_servers`, `hooks`, `external_skills` or `publish_targets`, the name is an existing entry and the field is one that section's entries have.
- Anything else is refused, so it cannot add a misspelled key or replace an entry or a section.
- The value is JSON when it parses as JSON and text otherwise; `null` is refused.
- An entry defined in `tackroom.local.yaml` is changed there, otherwise `tackroom.yaml`. It prints the changed lines and does not sync.

`tackroom view [--no-open | --json | --foreground | --stop] [--addr 127.0.0.1:8765] [--secure-cookie] [--ssh-host user@host] [--token-file PATH]`: see [view.md](view.md).

- Without `--foreground` it reports the running view or starts one in the background, which exits after `ui.view.idle_timeout` (default `30m`) without requests, then opens it in the browser.
- `--json` prints `url`, `addr`, `pid`, `started` and `idle_timeout` as one JSON object and opens no browser; plugins and scripts use it ([caller contract](view.md#starting-stopping-and-the-caller-contract)).
- `--stop` stops the running view. Exit 0 means no view answers now (it stopped one or none ran); exit 1 means one still answers. `--foreground` serves in this terminal until Ctrl-C; under launchd or systemd plain `view` does too.

### Crons in detail

- `tackroom cron collect [--hosts local,HOST...] [--table]` prints JSON, or a table with `--table`.
- `tackroom cron apply [--dry-run]`
- `tackroom cron run|pause|resume [--host HOST] ID`
- `tackroom cron schedule ID "MIN HOUR DOM MON DOW"`
- `tackroom cron add ID --schedule "MIN HOUR DOM MON DOW" --command CMD [--arg ARG ...] [--host NAME] [--effect TEXT] [--output PATH] [--local] [--dry-run] [--no-apply]`
- `tackroom cron edit ID [--schedule EXPR] [--command CMD [--arg ARG ...]] [--host NAME] [--effect TEXT] [--output PATH] [--dry-run] [--no-apply]`
- `tackroom cron remove ID [--dry-run] [--no-apply]`

What each flag does is in [crons.md](crons.md).

### Terminals in detail

- `tackroom tern [--config PATH] [--dry-run]`
- `tackroom rex [--config PATH] [--dry-run]`
- `tackroom tmux [--config PATH] [--dry-run]`
- `tackroom zellij [--config PATH] [--dry-run]`

### Config resolution

tackroom reads one config file, chosen in this order:

1. `--config /path/to/tackroom.yaml`
2. `$TACKROOM_HOME/tackroom.yaml`
3. `~/.agents/tackroom.yaml`

It never looks in the current project. The folder holding the file is the config root. A `tackroom.local.yaml` next to the file overlays entries for one machine. Every command that reads config takes `--config`; `restore` does not. Every config file in one folder shares its `tackroom.lock` and `skills/`, so `sync`, `pull`, `skill sources add` and the web view's Sync stop with an error before they touch the lock, `skills/` or a harness when they run with a second file next to `tackroom.yaml` that does not list every source the lock pins with the same url, branch and `materialize`. `pull` runs its `git pull` first, so a commit that adds the sources to that file lets it sync.

The config root cannot be, or sit inside, a linked git worktree. Machine-local state goes under `$XDG_STATE_HOME/tackroom` (default `~/.local/state/tackroom`): backups, the web view token, the record of MCP servers tackroom wrote, and pull status.

### Exit codes

| Command | 0 | 1 |
|---|---|---|
| Any command | Success | Any error, including a bad flag, an unknown command or a missing config |
| `status` | In sync | Out of sync, or a harness config could not be read |
| `sync` | Synced | A conflict was left in place, or a harness config could not be read |
| `sync --dry-run` | Always, whatever it plans | An error |
| `doctor` | Every check passed | Any failed check or any warning |
| `doctor audit` | No unaccepted CRITICAL finding | An unaccepted CRITICAL finding in external content |
| `hosts` | Always, whatever the machines report | An unreadable config or another error |
| `hosts fix` | Every fix it ran worked and left its host with no further fix, and none is left for you to run; always with `--dry-run` | A fix failed, a host still names a fix after it, or a fix is left for you to run |
| `hosts setup` | Setup ran on the host, or `--dry-run` printed the commands | The host is unreachable, has no tackroom (and no `--install`), already has a config, or a command there failed |

`status --exit-code` is the only form that returns 2: 0 when in sync, 1 on drift, 2 on any other error, such as a missing config. It does not count items in cooldown or blocked items, hooks reported as unsupported, or the periodic sync warning as drift. `--help` exits 0.

### Output

`status`, `sync`, `doctor`, `hosts` and `cron collect --table` use color for the status marks and bold and dim for structure when they write to a terminal. When the output goes to a pipe or a file there are no escape codes at all, and reports wrap at 100 columns instead of the terminal width. Other commands, such as `skill list`, `mcp list` and `hook list`, always print plain text.

- `NO_COLOR` set to any non-empty value drops the color on a terminal; bold and dim stay. `CLICOLOR_FORCE=1` keeps the styling in a pipe.
- `sync` prints a summary of what the run changed, with harnesses that changed alike grouped, then a table with one row per harness and the managed items per harness. `status` stays compact and `--verbose` adds the full lists and native paths.
- `--json` on `status`, `sync`, `doctor`, `doctor audit`, `setup`, `publish`, `hosts` and `skill sources` prints one JSON document on stdout. `cron collect` and `config inventory` print JSON by default. `hook list` prints tab-separated lines. `status --json`, `sync --json` and `sync --dry-run --json` list the items sync skips (in cooldown or blocked) under `skipped`, `[]` when there are none.
- Warnings and notes go to stderr, so a pipe sees only the report.

### Related tools

tackroom does not index or replay sessions. [AgentsView](https://github.com/kenn-io/agentsview) does; run `agentsview serve` on its own.
