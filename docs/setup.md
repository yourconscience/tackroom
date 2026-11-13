# Setup

`tackroom setup` creates your config repo, imports what your harnesses already have, runs the first sync and installs the periodic sync job. This page also covers multi-machine use, the web view in brief, per-harness settings and root instructions.

```bash
tackroom setup                        # detect harnesses, review imports, first sync
tackroom setup --dry-run              # print import candidates, change nothing
tackroom setup --yes --no-cron        # unattended: default answer to every prompt, no periodic job
```

Other flags: `--memory off|basic|memsearch` (default `basic`, see [memory.md](memory.md)), `--agents a,b` to limit the run to named harnesses, `--json` for the detection result, `--config PATH`.

Harnesses are named by these keys in `tackroom.yaml`, in `--agents` and in `agents:` lists: Claude Code `claude`, Codex `codex`, Factory Droid `droid`, OMP `omp`, OpenCode `opencode`, Pi `pi`, Qwen Code `qwen-code`, Cursor `cursor`, Gemini CLI `gemini`, GitHub Copilot CLI `copilot`, Grok Build `grok`, DeepSeek Harness `dsh`, Antigravity CLI `agy`, Kimi Code `kimi`, Amp `amp`. The former key `claude-code` still works. What each harness gets, and the executable that detects it, is in [harnesses.md](harnesses.md).

## What setup does

1. Creates the config root and copies in the starter content once: the `tackroom` skill, six roles, `tackroom.yaml`, `tackroom.lock`, `.gitignore`, `plugin.json`, `.agnix.toml` and the memory code layer under `memory/`. No instructions file and no third-party skills ship. `--memory off` leaves out the Go sources under `memory/tools/` and registers no memory hooks.
2. Detects harnesses by their executable on `PATH` and records them in `tackroom.yaml`. Amp is not auto-detected; add it with `--agents amp`.
3. Scans each harness for skills, roles and MCP servers you already have and shows a review screen. Import is copy-only; originals stay where they are.
4. Imports existing root instructions into `AGENTS.md`: one file is copied as is, several different ones are kept in full under a heading each for you to merge. With none, no `AGENTS.md` is created and every harness keeps its own file.
5. Before the first sync touches a harness that already has content, shows what would be removed or overwritten and asks per harness `[y/N]`. Declining, or `--yes`, keeps that harness's files.
6. Offers `git init` for a new config root, then runs the first sync. Native files that differ from the shared copy are moved to a backup and linked, after you agree.
7. Installs the periodic sync job unless you pass `--no-cron` (see [Periodic sync](#periodic-sync)).

The config root is `--config`'s folder, else `$TACKROOM_HOME`, else `~/.agents`. The checkout of tackroom itself is never your config.

`--yes` answers every prompt with its default and never reads stdin, so a script can run setup: it imports everything, initializes git, backs up and links differing files, and keeps existing content wherever the safe answer is no.

## The review screen

Each row is one skill, role or MCP server found in a harness. Items identical across harnesses are shared automatically. For the rest, `space` cycles share, keep or skip; `←` and `→` pick which harness's copy becomes canonical when copies differ; `a` shares all, `s` skips all, `enter` applies and `q` aborts. Non-interactive runs fall back to sequential prompts.

## Multi-machine

```bash
cd ~/.agents
git remote add origin <your-private-repository>
git push -u origin main
# on the next machine: clone it to ~/.agents, install tackroom, run tackroom setup
```

`tackroom sync --pull` pulls the repo first, then reconciles. Machines without a Go toolchain skip building the memory tools; everything else syncs normally.

## The web view

```bash
tackroom view                         # opens the loopback web UI
```

The view is a local dashboard over your config with a per-harness sync plan. It listens on loopback only, and saving never runs `sync`. Tabs, the token file, remote access and what it can change are in [view.md](view.md).

## Per-harness settings

`settings:` on an agent is a map of dotted key to value, written into that harness's own settings file. Supported: `claude` (`~/.claude/settings.json`), `pi` (`~/.pi/agent/settings.json`), `omp` (`~/.omp/agent/config.yml`). Other harnesses report `settings unsupported` and ignore the block.

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
- Keys tackroom manages through another surface are refused: Pi `packages`; Claude Code `hooks`, `enabledPlugins`, `extraKnownMarketplaces`, `mcpServers`.
- The file is backed up before the first modifying write; `tackroom restore` lists and restores it.

## Root instructions

`~/.agents/AGENTS.md` is your one root instruction file. Sync links it into each harness's own path:

- Amp `~/.config/amp/AGENTS.md`, Claude Code `~/.claude/CLAUDE.md`, Codex `~/.codex/AGENTS.md`, Factory Droid `~/.factory/AGENTS.md`
- Pi `~/.pi/agent/AGENTS.md`, OMP `~/.omp/agent/AGENTS.md`, Qwen Code `~/.qwen/QWEN.md`
- GitHub Copilot CLI `~/.copilot/copilot-instructions.md`, Grok Build `~/.grok/AGENTS.md`, DeepSeek Harness `~/.dsh/AGENTS.md` (`$COPILOT_HOME`, `$GROK_HOME` and `$DSH_HOME` replace the folders when set)
- Gemini CLI and Antigravity CLI share `~/.gemini/GEMINI.md`
- Cursor and OpenCode get no root file from tackroom. Kimi Code reads `~/.agents/AGENTS.md` itself, so none is written for it either

Without `AGENTS.md`, tackroom leaves every harness's instructions file alone. `tackroom status` reports drift. A real file with different content is never replaced without your confirmation, and then only after a backup (`setup`, or `sync --replace-conflicts`). An identical copy is relinked.

### Extra instruction files

List voice, profile or other always-on notes under `instructions:`:

```yaml
instructions:
  - SOUL.md                  # relative to the config root
  - ~/notes/profile.md       # or anywhere else
```

Each harness's root doc then carries `AGENTS.md` followed by those files, in order. Claude Code expands `@path` imports, so its `CLAUDE.md` becomes a short generated file of import lines and edits show up live. Every other harness reads one plain file, so it gets a generated copy: run `tackroom sync` after editing, and `status` reports the copy as drifted until you do.

Generated files start with a `Generated by tackroom` comment that names their sources; edit the sources, not the copy. A listed file missing on one machine is skipped and named in `status`. Set `instructions: []` in `tackroom.local.yaml` to go back to plain links on that machine.

## Managed starter files

`setup` copies the starter content once. The memory code layer under `memory/hooks/` and `memory/lib/` stays managed: on every `sync`, tackroom scaffolds missing files, refreshes files it wrote that you have not modified, removes files a release stopped shipping, and reports without touching anything you edited.

Ownership is recorded in `.tackroom-starter.json` at the config root; commit it with `tackroom.yaml`. Everything else in the starter set (`tackroom.yaml`, `agents/*.md`, `skills/`, `.gitignore`, `AGENTS.md`) is yours: tackroom only creates it when missing.

## Periodic sync

```bash
tackroom cron                         # install or replace the job (every 15 minutes)
tackroom cron --interval 1h           # 5m, 15m, 30m, 1h, 6h, 12h, daily, weekly
tackroom cron --remove
```

`setup` installs `tackroom-pull`, which runs `tackroom pull` (`git pull --ff-only`, then `sync`). A failed pull prints one warning and the sync still runs. Re-run `tackroom cron` after a `PATH` change, since the job runs with the `PATH` of the shell that installed it.

While the job is missing, paused, broken or its last run failed, `status` prints a warning and `doctor` has a `periodic sync` check. `setup --no-cron` records `periodic_sync: false` in `tackroom.local.yaml`, which silences that warning; installing the job again clears it.

Without the job, edited root instruction files reach Codex, Pi and OMP only on a manual `tackroom sync`. [crons.md](crons.md#built-in-jobs) describes the job.
