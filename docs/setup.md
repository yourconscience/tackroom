# Setup

`tackroom setup` sets up a machine in one step and asks nothing. It creates your config repo, imports what your harnesses already have, runs the first sync and installs the periodic sync job. This page also covers multi-machine use, the web view in brief, per-harness settings and root instructions.

```bash
tackroom setup                        # first machine: no questions, reasonable defaults
tackroom setup --from <git url>       # another machine: clone your config repo, then set up
tackroom setup --review               # review imports and confirm each step instead
tackroom setup --dry-run              # what the run would import, whose copy it keeps; changes nothing
```

Other flags: `--overlay NAME` to start this machine from a committed `tackroom.local.yaml` (see [Machine overlays](#machine-overlays)), `--agents a,b` to limit the run to named harnesses, `--no-cron` to skip the periodic job, `--verbose` for every step and the full sync report, `--json` for the detection result, `--config PATH`. `--yes` is still accepted and does nothing: it is the default now. `--memory` is still accepted and does nothing too: tackroom no longer sets up memory ([memory.md](memory.md)).

Harnesses are named by these keys in `tackroom.yaml`, in `--agents` and in `agents:` lists: Claude Code `claude`, Codex `codex`, Factory Droid `droid`, OMP `omp`, OpenCode `opencode`, Pi `pi`, Qwen Code `qwen-code`, Cursor `cursor`, Devin `devin`, GitHub Copilot CLI `copilot`, Grok Build `grok`, DeepSeek Harness `dsh`, Antigravity CLI `agy`, Kimi Code `kimi`, Amp `amp`. The former key `claude-code` still works. Gemini CLI's key `gemini` is ignored with a warning ([upgrading.md](upgrading.md)). What each harness gets, and the executable that detects it, is in [harnesses.md](harnesses.md).

## What setup does

1. Creates the config root and, when it has no `tackroom.yaml` yet, copies in the starter content: the `tackroom` skill, six roles, `tackroom.yaml`, `tackroom.lock`, `.gitignore`, `plugin.json` and `.agnix.toml`. No instructions file and no third-party skills ship. A config root that already has `tackroom.yaml` (a rerun, or a clone on another machine) gets no starter files, so a starter role, skill or `plugin.json` you deleted stays deleted.
2. Detects harnesses by their executable on `PATH` and records them in `tackroom.yaml`. Amp is not auto-detected; add it with `--agents amp`. A `tackroom.yaml` whose content does not change is not rewritten, so its formatting stays as you wrote it.
3. Copies every skill, role and MCP server your harnesses already have into the config root. When two harnesses have different copies of a skill, the first detected harness's copy is kept and the summary says which. A role is shared only when every harness's copy would come back unchanged from the shared one: same fields, same values, same instructions, nothing added. A role with a field tackroom cannot write (for example `permissionMode` or `sandbox_mode`), without the frontmatter a shared role needs, with different copies in different harnesses, or that is a link to a file elsewhere stays each harness's own, and no starter role of that name is added. Import is copy-only and adds nothing to what it copies; nothing is moved yet.
4. Imports existing root instructions into `AGENTS.md`: one file is copied as is, several different ones are kept in full under a `## From <agent> (<file>)` heading each. Every agent then reads all of them, so each `setup` run lists them under what needs your attention until you merge them and delete those headings. With none, no `AGENTS.md` is created and every harness keeps its own file.
5. Runs the first sync. Every native file or skill folder it replaces, identical to the shared copy or not, is first moved to a backup under `~/.local/state/tackroom/backups/`, so `tackroom restore latest` puts them all back. A role is replaced only when the rewritten role is the same role, and never when it is a link; otherwise that one role file is left in place and listed as needing attention, and everything else still syncs into that harness. Removing or overwriting content an earlier tackroom run wrote is left for `tackroom sync`: the summary lists it per harness with that command.
6. Initializes git and makes the first commit on a first setup: no `tackroom.yaml` before this run and the folder not already under git. A config that already has `tackroom.yaml` and no git (for example after you declined git with `--review`) is left without it.
7. Installs the periodic sync job unless you pass `--no-cron` (see [Periodic sync](#periodic-sync)).

It ends with a short summary: the agents it found, what it imported, which copy it kept where copies differed, where the backups are, and either `Everything is synced.` or what needs you, each with the command that fixes it. Running `setup` again on a machine that is set up prints `Already set up; everything is synced.` or what changed. Setup exits 1 when anything needs your attention; by then the sync, the first commit and the periodic job may already be done, so fix the listed items and run `setup` again.

The config root is `--config`'s folder, else `$TACKROOM_HOME`, else `~/.agents`. The checkout of tackroom itself is never your config.

## Review instead of defaults

`setup --review` shows the review screen for imports, then asks before initializing git, before replacing differing native files, and before removing or overwriting what a harness already has (`[y/N]` per harness; declining keeps that harness's files). Without a terminal it falls back to sequential prompts that show `[y/N]`: only a typed `y` counts as yes.

## The review screen

Each row is one skill, role or MCP server found in a harness. Items identical across harnesses are shared automatically. For the rest, `space` cycles share, keep or skip; `←` and `→` pick which harness's copy becomes canonical when copies differ; `a` shares all, `s` skips all, `enter` applies and `q` aborts.

## Multi-machine

On the first machine, push the config root to a private repository:

```bash
cd ~/.agents
git remote add origin <your-private-repository>
git push -u origin main
```

On the next machine, install tackroom and run:

```bash
tackroom setup --from <your-private-repository>
```

`--from` clones the repository into the config root when that folder is missing or empty, or runs `git pull --ff-only` when it is already a clone of the same repository (ssh and https forms of one repository match, and so do `url.<base>.insteadOf` aliases), then sets up from it: its `tackroom.yaml` is used as it is and no starter files are added. A folder with other content, or a clone of another repository, is refused and left untouched. A git repository with no origin is refused too, with the two commands that connect it if it is a copy of that repository. A repository without `tackroom.yaml` is set up like a first machine, and the summary says so. What this machine's harnesses already have is imported as on a first machine; an item the repository already has keeps the repository's copy.

`tackroom sync --pull` pulls the repo first, then reconciles. List the machines under `crons.hosts` in `tackroom.local.yaml` (SSH aliases, plus `local` for this one) and `tackroom hosts` shows each one's tackroom version, sync state and config commit, with the command that fixes what is wrong.

### Machine overlays

A machine of a known kind, such as a cloud VM, needs the same `tackroom.local.yaml` every time. Commit it once in the config repository as `overlays/<name>.yaml`, a complete `tackroom.local.yaml`, and pass `--overlay <name>`. An entry in the local file replaces the shared entry of the same name, so write each entry in full:

```yaml
# overlays/cloud.yaml
mcp_servers:
  - name: browser
    enabled: false
    command: npx
    args: [-y, "@acme/browser-mcp"]
hooks:
  - name: notify-stop
    enabled: false
    event: Stop
    command: ~/.agents/hooks/notify.sh
```

A cloud VM's setup script then runs:

```bash
npm install -g tackroom
tackroom setup --from <repository url> --overlay cloud --no-cron
```

- Setup copies `overlays/<name>.yaml` to `tackroom.local.yaml` in the config root after the clone or pull and before the first sync, so the first sync already leaves those entries out. It works without `--from` too.
- A `tackroom.local.yaml` that already holds the overlay is left as it is. One that differs is never overwritten: setup syncs with it, lists it under what needs your attention and exits 1.
- `periodic_sync` is left out of that comparison. `--no-cron` records `periodic_sync: false` in the copied file, and running the same command again reports nothing to do.
- An unknown name stops setup before it syncs anything, and lists the overlays the repository has. With `--from` that is after the clone or pull. The name is a file name under `overlays/`, never a path.
- `--dry-run` and `--json` list the copy as a planned action.
- Only `setup --overlay` reads `overlays/`. `sync` and `config validate` ignore it, and the starter `.gitignore` does not exclude it, so it is committed with the rest.
You can also set up another machine from this one: `tackroom hosts setup user@host` runs `tackroom setup --from` there over SSH, from this config root's `origin` unless you pass `--from`, and `--install` first installs tackroom there with Homebrew or npm. Later, `tackroom hosts fix` runs the fix `tackroom hosts` names, such as `tackroom pull` on a machine that is behind.

## The web view

```bash
tackroom view                         # opens the loopback web UI
```

The view is a local dashboard over your config with a per-harness sync plan. It listens on loopback only, and saving never runs `sync`. Tabs, the token file, remote access and what it can change are in [view.md](view.md).

## Per-harness settings

`settings:` on an agent is a map of dotted key to value, written into that harness's own settings file. Supported: `claude` (`~/.claude/settings.json`), `codex` (`~/.codex/config.toml`), `pi` (`~/.pi/agent/settings.json`), `omp` (`~/.omp/agent/config.yml`). Other harnesses report `settings unsupported` and ignore the block.

```yaml
agents:
  - name: claude
    enabled: true
    detect: claude
    settings:
      model: opus
      api_token: ${API_TOKEN}   # must be a single ${VAR} reference
```

- Only declared keys are written; every other key in the file is kept. Removing a key from `settings:` leaves the native value in place.
- A key whose last segment ends in `token`, `secret`, `password`, `passwd`, `apikey`, `authorization` or `credential(s)` (any casing) must be a single `${VAR}` reference.
- `${VAR}` expands at sync time. An unset variable fails that harness and writes nothing; expanded values never appear in output.
- Keys tackroom manages through another surface are refused: Pi `packages`; Claude Code `hooks`, `enabledPlugins`, `extraKnownMarketplaces`, `mcpServers`; Codex `mcp_servers`, `plugins`, `marketplaces`, `features.hooks`, `codex_hooks`.
- Codex's `config.toml` is edited as text, so comments, key order and unrelated tables stay. A top-level key such as `model` is set above the first table, and `tui.notifications` goes under `[tui]`, which is added when missing. Sync refuses with an error and writes nothing when the file does not parse, when a key segment would need TOML quoting (`projects."/p"`), or when a declared key already exists as an inline table, dotted key, array of tables or multi-line value.
- A model you pick in Codex's own picker is saved as `model` in `config.toml`, so with `model` declared the next sync overwrites that choice.
- The file is backed up before the first modifying write; `tackroom restore` lists and restores it.

## Root instructions

`~/.agents/AGENTS.md` is your one root instruction file. Sync links it into each harness's own path:

- Amp `~/.config/amp/AGENTS.md`, Claude Code `~/.claude/CLAUDE.md`, Codex `~/.codex/AGENTS.md`, Factory Droid `~/.factory/AGENTS.md`
- Pi `~/.pi/agent/AGENTS.md`, OMP `~/.omp/agent/AGENTS.md`, Qwen Code `~/.qwen/QWEN.md`
- GitHub Copilot CLI `~/.copilot/copilot-instructions.md`, Grok Build `~/.grok/AGENTS.md`, DeepSeek Harness `~/.dsh/AGENTS.md` (`$COPILOT_HOME`, `$GROK_HOME` and `$DSH_HOME` replace the folders when set)
- Antigravity CLI `~/.gemini/GEMINI.md`, Devin `~/.config/devin/AGENTS.md` (Devin also reads `~/.claude/CLAUDE.md` by default; see [harnesses.md](harnesses.md#devin))
- Cursor and OpenCode get no root file from tackroom. Kimi Code reads `~/.agents/AGENTS.md` itself, so none is written for it either

Without `AGENTS.md`, tackroom leaves every harness's instructions file alone. `tackroom status` reports drift. A real file with different content is replaced only by `setup` or `sync --replace-conflicts`, and only after a backup. An identical copy is backed up, then linked.

### Extra instruction files

List voice, profile, memory ([memory.md](memory.md)) or other always-on notes under `instructions:`:

```yaml
instructions:
  - SOUL.md                  # relative to the config root
  - ~/notes/profile.md       # or anywhere else
```

Each harness's root doc then carries `AGENTS.md` followed by those files, in order. Claude Code expands `@path` imports, so its `CLAUDE.md` becomes a short generated file of import lines and edits show up live. Every other harness reads one plain file, so it gets a generated copy: run `tackroom sync` after editing, and `status` reports the copy as drifted until you do.

Generated files start with a `Generated by tackroom` comment that names their sources; edit the sources, not the copy. A listed file missing on one machine is skipped and named in `status`. Set `instructions: []` in `tackroom.local.yaml` to go back to plain links on that machine.

## Managed starter files

`setup` copies the starter content into a new config root only. The starter set (`tackroom.yaml`, `tackroom.lock`, `agents/*.md`, `skills/`, `plugin.json`, `.agnix.toml`, `.gitignore`) is yours: only `setup` on a config root without `tackroom.yaml` creates it, so a starter file you delete stays deleted. `AGENTS.md` is not a starter file; setup creates it only to import your harnesses' own instructions.

Releases up to 2.8 also managed memory scripts under `memory/` and recorded them in `.tackroom-starter.json`. What `sync` does with them now is in [memory.md](memory.md#coming-from-the-bundled-memory).

## Periodic sync

```bash
tackroom cron                         # install or replace the job (every 15 minutes)
tackroom cron --interval 1h           # 5m, 15m, 30m, 1h, 6h, 12h, daily, weekly
tackroom cron --remove
```

`setup` installs `tackroom-pull`, which runs `tackroom pull` (`git pull --ff-only`, then `sync`). A failed pull prints one warning and the sync still runs. Re-run `tackroom cron` after a `PATH` change, since the job runs with the `PATH` of the shell that installed it.

While the job is missing, paused, broken or its last run failed, `status` prints a warning and `doctor` has a `periodic sync` check. `setup --no-cron` records `periodic_sync: false` in `tackroom.local.yaml`, which silences that warning; installing the job again clears it.

Without the job, edited root instruction files reach Codex, Pi and OMP only on a manual `tackroom sync`. [crons.md](crons.md#built-in-jobs) describes the job.
