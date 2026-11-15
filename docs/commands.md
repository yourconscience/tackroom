# Commands

Every tackroom command and subcommand, grouped by task, with the exit codes, the config lookup order and how output behaves in a pipe. `tackroom help` lists the commands and `tackroom help --all` adds every form and flag.

```bash
tackroom help --all                  # the full set, as listed below
tackroom <command> --help            # flags of one command (exit code 0)
```

## Set up and sync

- `tackroom setup [--from GIT_URL] [--overlay NAME] [--review] [--verbose] [--memory off|basic|memsearch] [--agents a,b] [--no-cron] [--dry-run] [--json]` creates the config root, imports what your harnesses have by copy, runs the first sync, installs the periodic job and prints a short summary. It asks nothing; `--review` asks before each step, `--verbose` prints the full sync report, and `--from` clones (or pulls) your config repository first, for another machine. `--overlay NAME` copies `overlays/NAME.yaml` from the config repository to `tackroom.local.yaml` before the first sync and never overwrites a different one ([Machine overlays](setup.md#machine-overlays)). `--yes` is accepted and does nothing. See [setup.md](setup.md).
- `tackroom sync [--pull] [--dry-run] [--replace-conflicts] [--agents a,b] [--json] [--allow-fresh[=item,...]] [--reason TEXT]` writes each harness's native config from the repo. `--dry-run` prints the plan, including held and blocked items, and writes nothing; with `--json` it also lists `plan`, one line per harness plus one for starter files a sync would add, update or remove, the `destructive` items (every skill, role, MCP server, hook, plugin, Pi package and starter file the sync removes, and every role it overwrites) and a `plan_digest`. `--pull` runs `tackroom pull` first.
- `tackroom sync --plan-digest DIGEST` syncs only if `sync --dry-run --json` would still print that `plan_digest`, and then syncs from the config it just checked. The digest covers the whole dry-run document and the bytes of `tackroom.yaml` and `tackroom.local.yaml`, so a changed plan or any config edit is refused with exit 1. It does not cover the text inside skill and role files: an edit there that leaves the plan's lines the same still applies. An empty digest is an error. The [Herdr panel](herdr.md)'s Sync uses it.
- `tackroom status [--verbose] [--exit-code] [--json] [--agents a,b]` shows per harness what is synced, drifted, held or blocked. `-v` is short for `--verbose`.
- `tackroom pull` runs `git pull --ff-only` in the config root, then `sync`. A failed pull only warns. This is what the periodic job runs.
- `tackroom restore [<backup> | latest] [--dry-run]` lists the backups of native files, newest first, or puts one back. What it replaces is saved as a new backup, so `restore latest` undoes a restore. See [troubleshooting.md](troubleshooting.md#conflicts).
- `tackroom cron [--interval 15m] [--deps] [--remove]` installs, replaces or removes the periodic sync job. `--deps` installs the weekly dependency job instead. See [setup.md](setup.md#periodic-sync).

## Check health

- `tackroom doctor [--json] [--agents a,b] [--e2e]` checks skills, roles, the lock and materialized copies, native hooks, the periodic job and external sources. `--e2e` runs a real `sync`, `status` and `doctor` in order, so it writes.
- `tackroom doctor audit [--json]` lists audit findings per item.
- `tackroom doctor audit accept <item> <rule> --reason "..."` records an accepted finding in `tackroom.lock`. The item is `kind:name`, for example `skill:foo`.
- `tackroom doctor audit revoke <item> <rule>` removes that decision.
- `tackroom doctor deps` checks the age of exact npm and PyPI versions your config references.
- `tackroom sync deps` updates patch-level dependencies in the config root (Go modules, pnpm, uv), then runs the age check.

- `tackroom hosts [HOST...] [--json]` shows tackroom on each machine in `crons.hosts`, or on the hosts you name; with neither, only `local`, this machine. It probes every host at once, over SSH as `cron collect` does, and reports each one's tackroom version, whether it is set up and synced, and its config root's commit against this machine's: the same, behind, ahead, diverged, or unknown when this machine's git does not have it, plus uncommitted changes. Each machine is `fail` when it cannot be reached, has no tackroom, its `status` fails (for example, no `tackroom.yaml`) or it runs a tackroom a major version behind this machine's; `warn` when its `tackroom.yaml` is not committed (so no other machine can pull it), it is out of sync, behind or diverged, has uncommitted changes or runs an older tackroom than this machine; `ok` otherwise. It prints a table with the command that fixes each problem, or JSON with `--json`, where `fix_runnable` says whether `tackroom hosts fix` runs that command. It exits 0 whatever the machines report.
- `tackroom hosts fix [HOST...] [--dry-run]` runs the fix `tackroom hosts` names for each host you name, or for every host that has one. It runs only tackroom's own verbs on that host: `setup`, `pull` or `sync`, or `git pull --ff-only` in its config root and then `setup`, over SSH as the probe does, or this binary for `local`. Their output follows a line naming the host, then it probes the host again and prints its new row. A fix that commits and pushes on this machine is printed for you to run; tackroom never commits or pushes for you. A host with nothing to fix says so and counts as fine. `--dry-run` prints what it would run and runs nothing.
- `tackroom hosts setup HOST [--from GIT_URL] [--overlay NAME] [--no-cron] [--install] [--dry-run]` sets up a machine that has no tackroom config yet: it runs `tackroom setup --from GIT_URL` there over SSH, with `--overlay` and `--no-cron` passed through. `--from` defaults to the `origin` remote of this machine's config root. It finds tackroom on the host as `hosts` does (PATH, then `~/.local/bin`, `~/go/bin`, `/opt/homebrew/bin` and `/usr/local/bin`). Without one it stops with the two install commands; with `--install` it runs `brew install yourconscience/tap/tackroom`, or `npm install -g tackroom` when the host has npm and no brew. A host that already has a `tackroom.yaml` is left alone: use `hosts fix`. Afterwards it prints the host's row and, when the host is not in `crons.hosts`, the lines to add to `tackroom.local.yaml`. `--dry-run` runs only the read-only probe on the host and prints the commands.

`status`, `doctor`, `pull`, `doctor deps` and `sync deps` take `--skip-package-age`, which skips the registry lookups for package age. The audit gate is in [skills.md](skills.md#the-audit-gate).

## Skills

- `tackroom skill new <name> [--description TEXT]` creates `skills/<name>/SKILL.md`.
- `tackroom skill list [--agents a,b]` lists every entry in each harness skill root with its provenance and context cost.
- `tackroom skill info <name>` shows one skill's origin and how each harness sees it.
- `tackroom skill sources [--json]` lists configured external sources and ones installed outside tackroom.
- `tackroom skill sources search <query>` searches skills.sh.
- `tackroom skill sources add <owner/repo[/path]> [--skills a,b | --all] [--latest] [--no-sync]` pins a source and syncs it.
- `tackroom skill update [name ...] [--allow-fresh[=item,...]]` advances pins.
- `tackroom publish [--target NAME] [--skills a,b] [--dry-run] [--json] [--yes]` pushes allowlisted skills to a remote registry. See [skills.md](skills.md#publish-skills).

## MCP servers and hooks

- `tackroom mcp list` prints the servers in `tackroom.yaml`.
- `tackroom mcp add <name> (--command CMD [--arg V] [--env K=V] | --url URL [--header 'Name: value']) [--agents a,b]` declares a server.
- `tackroom mcp import <harness> <name> [--agents a,b]` copies a server a harness already has into `tackroom.yaml`, with secrets replaced by references.
- `tackroom mcp remove <name>` deletes the entry; the next `sync` removes it from the harnesses. See [mcp.md](mcp.md).
- `tackroom hook list [--agents a,b] [query]` lists native hook registrations and marks which are managed.
- `tackroom hook remove [--dry-run] [--agents a,b] <query>` removes matching registrations. See [hooks.md](hooks.md).

## Config and web view

- `tackroom config validate [--config PATH]` checks the config and prints its file and revision.
- `tackroom config print [--config PATH]` prints the shared, local and effective config.
- `tackroom config inventory [--config PATH]` prints, as JSON, what the web view's Overview, Skills, Roles and MCP & hooks tabs draw: per harness the counts and state, per skill, role, MCP server and hook its state in each harness (`ok`, `drift`, `missing`, `conflict`, `off`, `disabled`, `unsupported`, `absent`, `error`), and each skill's origin. Read-only. The [Herdr panel](herdr.md) draws it.
- `tackroom config set [--config PATH] <section/name/field> <value>` changes one field of an entry, as the web view's switches and cells do: `config set mcp_servers/exa/enabled false`, `config set hooks/notify/agents '["claude","codex"]'`. The path is always `section/name/field`: the section is `agents`, `mcp_servers`, `hooks`, `external_skills` or `publish_targets`, the name is an existing entry and the field is one that section's entries have. Anything else is refused, so it cannot add a misspelled key or replace an entry or a section. The value is JSON when it parses as JSON and text otherwise; `null` is refused. An entry defined in `tackroom.local.yaml` is changed there, otherwise `tackroom.yaml`. It prints the changed lines and does not sync.
- `tackroom view [--addr 127.0.0.1:8765] [--no-open] [--secure-cookie] [--ssh-host user@host] [--token-file PATH]` opens the loopback web UI. See [view.md](view.md).

## Scheduled jobs

- `tackroom cron collect [--hosts local,HOST...] [--table]` is a read-only inventory of scheduled jobs here and over SSH. It prints JSON, or a table with `--table`.
- `tackroom cron apply [--dry-run]` installs, replaces or removes the jobs declared under `crons.jobs`.
- `tackroom cron run|pause|resume [--host HOST] ID` acts on a launchd job.
- `tackroom cron schedule ID "MIN HOUR DOM MON DOW"` sets a new schedule for a job tackroom owns on this machine.
- `tackroom cron add ID --schedule "MIN HOUR DOM MON DOW" --command CMD [--arg ARG ...] [--host NAME] [--effect TEXT] [--local] [--dry-run] [--no-apply]` declares an owned job and applies it.
- `tackroom cron edit ID [--schedule EXPR] [--command CMD [--arg ARG ...]] [--host NAME] [--effect TEXT] [--dry-run] [--no-apply]` changes the given fields of an owned job.
- `tackroom cron remove ID [--dry-run] [--no-apply]` deletes the declaration and the unit.

All seven are in [crons.md](crons.md). The hosts in `crons.hosts` are also the machines `tackroom hosts` reports on ([Check health](#check-health)).

## Terminals

- `tackroom tern [--config PATH] [--dry-run]` opens a tackroom workspace in the Tern terminal. See [tern.md](tern.md).
- `tackroom rex [--config PATH] [--dry-run]` opens a tackroom workspace in the Rex terminal. See [rex.md](rex.md).

## Other

- `tackroom setup memsearch [--vault DIR]` checks for `memsearch`, creates the vault folders and writes `memsearch.conf`. `tackroom memsearch <setup|status>` is the older spelling and prints a rename notice.
- `tackroom version` prints the version; `tackroom --version` does the same.
- `tackroom help [--all]` prints the usage.

## Config resolution

tackroom reads one config file, chosen in this order:

1. `--config /path/to/tackroom.yaml`
2. `$TACKROOM_HOME/tackroom.yaml`
3. `~/.agents/tackroom.yaml`

It never looks in the current project. The folder holding the file is the config root. A `tackroom.local.yaml` next to the file overlays entries for one machine. Every command that reads config takes `--config`; `restore` does not.

The config root cannot be, or sit inside, a linked git worktree. Machine-local state goes under `$XDG_STATE_HOME/tackroom` (default `~/.local/state/tackroom`): backups, the web view token, the record of MCP servers tackroom wrote, and pull status.

## Exit codes

| Command | 0 | 1 |
|---|---|---|
| Any command | Success | Any error, including a bad flag, an unknown command or a missing config |
| `status` | In sync | Out of sync, or a harness config could not be read |
| `sync` | Synced | A conflict was left in place, or a harness config could not be read |
| `sync --dry-run` | Always, whatever it plans | An error |
| `doctor` | Every check passed | Any failed check or any warning |
| `doctor audit` | No unaccepted CRITICAL finding | An unaccepted CRITICAL finding in external content |
| `hosts` | Always, whatever the machines report | An unreadable config or another error |
| `hosts fix` | Every fix it ran left its host `ok` and none is left for you to run; always with `--dry-run` | A fix failed, a host still has a problem after it, or a fix is left for you to run |
| `hosts setup` | Setup ran on the host, or `--dry-run` printed the commands | The host is unreachable, has no tackroom (and no `--install`), already has a config, or a command there failed |

`status --exit-code` is the only form that returns 2: 0 when in sync, 1 on drift, 2 on any other error, such as a missing config. It does not count held or blocked items, hooks reported as unsupported, or the periodic sync warning as drift. `--help` exits 0.

A real `sync` still syncs the other harnesses when one has a conflict, and then exits 1.

## Output

`status`, `sync`, `doctor`, `hosts` and `cron collect --table` use color for the status marks and bold and dim for structure when they write to a terminal. When the output goes to a pipe or a file there are no escape codes at all, and reports wrap at 100 columns instead of the terminal width. Other commands, such as `skill list`, `mcp list` and `hook list`, always print plain text.

- `NO_COLOR` set to any non-empty value drops the color on a terminal; bold and dim stay. `CLICOLOR_FORCE=1` keeps the styling in a pipe.
- `sync` prints a summary of what the run changed, with harnesses that changed alike grouped, then a table with one row per harness and the managed items per harness. `status` stays compact and `--verbose` adds the full lists and native paths.
- `--json` on `status`, `sync`, `doctor`, `doctor audit`, `setup`, `publish`, `hosts` and `skill sources` prints one JSON document on stdout. `cron collect` and `config inventory` print JSON by default. `hook list` prints tab-separated lines.
- Warnings and notes go to stderr, so a pipe sees only the report.

## Related tools

tackroom does not index or replay sessions. [AgentsView](https://github.com/kenn-io/agentsview) does; run `agentsview serve` on its own.
