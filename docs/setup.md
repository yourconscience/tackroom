# Setup

`tackroom setup` sets up a machine in one step and asks nothing. It creates your config repo, imports what your harnesses already have, runs the first sync and installs the periodic sync job. You run it on each machine you use; this page also covers settings per harness, root instructions, upgrades and uninstalling.

```bash
tackroom setup                        # first machine: no questions, reasonable defaults
tackroom setup --from <git url>       # another machine: clone your config repo, then set up
tackroom setup --review               # review imports and confirm each step instead
tackroom setup --dry-run              # what the run would import, whose copy it keeps; changes nothing
```

## Set up your first machine

Install tackroom, then run `tackroom setup`. It ends with a short summary: the agents it found, what it imported, which copy it kept where copies differed, where the backups are, and either `Everything is synced.` or what needs you, each with the command that fixes it.

Every native file it replaces is backed up first, so `tackroom restore latest` puts them all back. Each step is described in [What setup does](#what-setup-does).

- Setup exits 1 when anything needs your attention. By then the sync, the first commit and the periodic job may already be done, so fix the listed items and run `setup` again.
- Running `setup` again on a machine that is set up prints `Already set up; everything is synced.` or what changed.
- The config root is `--config`'s folder, else `$TACKROOM_HOME`, else `~/.agents`. The checkout of tackroom itself is never your config.
- Harnesses are named by keys such as `claude` and `codex`, and `--agents a,b` limits a run to some of them. Every key is in [harnesses.md](harnesses.md#config-keys-and-detection).

## Review each step instead

`setup --review` shows the review screen for imports, then asks:

- before initializing git;
- before replacing differing native files;
- before removing or overwriting what a harness already has (`[y/N]` per harness; declining keeps that harness's files).

Without a terminal it falls back to sequential prompts that show `[y/N]`: only a typed `y` counts as yes.

On the review screen, each row is one skill, role or MCP server found in a harness. Items identical across harnesses are shared automatically. For the rest:

| Key | Action |
|---|---|
| `space` | Cycle share, keep or skip |
| `←` and `→` | Pick which harness's copy becomes canonical when copies differ |
| `a` | Share all |
| `s` | Skip all |
| `enter` | Apply |
| `q` | Abort |

## Set up another machine

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

`--from` puts the repository in the config root, then sets up from it:

- A missing or empty config root gets a clone. One that is already a clone of the same repository gets `git pull --ff-only`. The ssh and https forms of one repository match, and so do `url.<base>.insteadOf` aliases.
- The repository's `tackroom.yaml` is used as it is and no starter files are added. A repository without `tackroom.yaml` is set up like a first machine, and the summary says so.
- What this machine's harnesses already have is imported as on a first machine. An item the repository already has keeps the repository's copy.
- A folder with other content, or a clone of another repository, is refused and left untouched. A git repository with no origin is refused too, with the two commands that connect it if it is a copy of that repository.

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

### Set up a machine over SSH

You can also set up another machine from this one. `tackroom hosts setup user@host` runs `tackroom setup --from` there over SSH, from this config root's `origin` unless you pass `--from`, and `--install` first installs tackroom there with Homebrew or npm. Its other flags are in [commands.md](commands.md#check-health-in-detail).

### Check every machine

List the machines under `crons.hosts` in `tackroom.local.yaml`: SSH aliases, plus `local` for this one. Then:

- `tackroom hosts` shows each one's tackroom version, sync state and config commit, with the command that fixes what is wrong.
- `tackroom hosts fix` runs the fix `tackroom hosts` names, such as `tackroom pull` on a machine that is behind.

On any one machine, `tackroom sync --pull` pulls the repo first, then reconciles.

## Open the web view

```bash
tackroom view                         # opens the loopback web UI
```

The view is a local dashboard over your config with a per-harness sync plan. It listens on loopback only, and saving never runs `sync`. Tabs, the token file, remote access and what it can change are in [view.md](view.md).

## Per-harness settings

`settings:` on an agent is a map of dotted key to value, written into that harness's own settings file. Four harnesses support it; other harnesses report `settings unsupported` and ignore the block.

| Harness key | Settings file |
|---|---|
| `claude` | `~/.claude/settings.json` |
| `codex` | `~/.codex/config.toml` |
| `pi` | `~/.pi/agent/settings.json` |
| `omp` | `~/.omp/agent/config.yml` |

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
- The file is backed up before the first modifying write; `tackroom restore` lists and restores it.
- Keys tackroom manages through another surface are refused: Pi `packages`; Claude Code `hooks`, `enabledPlugins`, `extraKnownMarketplaces`, `mcpServers`; Codex `mcp_servers`, `plugins`, `marketplaces`, `features.hooks`, `codex_hooks`.

Secrets:

- A key whose last segment ends in `token`, `secret`, `password`, `passwd`, `apikey`, `authorization` or `credential(s)` (any casing) must be a single `${VAR}` reference.
- `${VAR}` expands at sync time. An unset variable fails that harness and writes nothing; expanded values never appear in output.

Codex:

- Codex's `config.toml` is edited as text, so comments, key order and unrelated tables stay. A top-level key such as `model` is set above the first table, and `tui.notifications` goes under `[tui]`, which is added when missing.
- Sync refuses with an error and writes nothing when the file does not parse, when a key segment would need TOML quoting (`projects."/p"`), or when a declared key already exists as an inline table, dotted key, array of tables or multi-line value.
- A model you pick in Codex's own picker is saved as `model` in `config.toml`, so with `model` declared the next sync overwrites that choice.

## Share your root instructions

`~/.agents/AGENTS.md` is your one root instruction file. Sync links it into each harness's own root file, such as `~/.claude/CLAUDE.md` or `~/.codex/AGENTS.md`; the path for every harness is in [harnesses.md](harnesses.md#where-each-surface-lands). Cursor and OpenCode get no root file from tackroom, and Kimi Code reads `~/.agents/AGENTS.md` itself.

- Without `AGENTS.md`, tackroom leaves every harness's instructions file alone.
- `tackroom status` reports drift.
- A real file with different content is replaced only by `setup` or `sync --replace-conflicts`, and only after a backup. An identical copy is backed up, then linked.

### Extra instruction files

List voice, profile, memory ([memory.md](memory.md)) or other always-on notes under `instructions:`:

```yaml
instructions:
  - SOUL.md                  # relative to the config root
  - ~/notes/profile.md       # or anywhere else
```

Each harness's root doc then carries `AGENTS.md` followed by those files, in order. Claude Code expands `@path` imports, so its `CLAUDE.md` becomes a short generated file of import lines and edits show up live. Every other harness reads one plain file, so it gets a generated copy: run `tackroom sync` after editing, and `status` reports the copy as drifted until you do.

Generated files start with a `Generated by tackroom` comment that names their sources; edit the sources, not the copy. A listed file missing on one machine is skipped and named in `status`. Set `instructions: []` in `tackroom.local.yaml` to go back to plain links on that machine.

## Sync on a schedule

```bash
tackroom cron                         # install or replace the job (every 15 minutes)
tackroom cron --interval 1h           # 5m, 15m, 30m, 1h, 6h, 12h, daily, weekly
tackroom cron --remove
```

`setup` installs `tackroom-pull`, which runs `tackroom pull` (`git pull --ff-only`, then `sync`). A failed pull prints one warning and the sync still runs. Re-run `tackroom cron` after a `PATH` change, since the job runs with the `PATH` of the shell that installed it.

Without the job, edited root instruction files reach Codex, Pi and OMP only on a manual `tackroom sync`.

While the job is missing, paused, broken or its last run failed, `status` prints a warning and `doctor` has a `periodic sync` check. `setup --no-cron` records `periodic_sync: false` in `tackroom.local.yaml`, which silences that warning; installing the job again clears it. [crons.md](crons.md#built-in-jobs) describes the job.

## Upgrade

Install the new release with the tool you installed tackroom with, then check the version:

```bash
brew upgrade yourconscience/tap/tackroom   # installed with Homebrew
npm install -g tackroom                    # installed with npm
tackroom version
```

Do this on every machine that shares your config root. `tackroom hosts` shows the tackroom version of each machine in `crons.hosts` and warns about one that runs an older tackroom than this machine ([Check health](commands.md#check-health)).

Then read the [release notes](upgrading.md) of every release since the one you had. Each entry says what changes on its own and what to run once, if anything, such as `tackroom cron` for a periodic job installed before 2.0.

A running `tackroom view` restarts itself on the new binary ([view.md](view.md#restart-on-upgrade)). The Claude Code plugin and the Herdr mods update separately, and an entry says when a release needs that.

## Uninstall

tackroom has no uninstall command. These steps undo a setup with the commands it has. Do them in order, since the first three need the config root, and commit and push the config root first if you want to keep it.

1. **Stop the scheduled jobs.** `tackroom cron --remove` removes `tackroom-pull`, and `tackroom cron --deps --remove` removes `tackroom-deps` if you installed it. Remove each job you declared under `crons.jobs` with `tackroom cron remove ID`.
2. **Take your hooks and MCP servers out of the harnesses.** Set `enabled: false` on every entry under `hooks:` and `mcp_servers:`, by hand or with `tackroom config set hooks/<name>/enabled false`, then run `tackroom sync`. If you registered Claude Code plugins, delete `.claude-plugin/marketplace.json` first, and the same sync removes the registration ([plugins.md](plugins.md)).
3. **Put your own files back.** `tackroom restore` lists the backups, newest first. Restore the one `setup` made, which its summary named: `tackroom restore <backup> --dry-run`, then without `--dry-run`. Skip the backups the sync in step 2 made, since they hold the entries you removed.
4. **Delete the links and generated files that are left.** These two commands list them. Check each path and delete the ones tackroom created; keep a file of your own, since the second command matches any file that contains the `Generated by tackroom` line.
   ```bash
   ROOT="${TACKROOM_HOME:-$HOME/.agents}"
   # skill links and root files that point into the config root
   find -H ~/.[!.]* -maxdepth 3 -lname "$ROOT/*" -not -path "$ROOT/*"
   # role files and root files tackroom wrote, in every harness
   find -H ~/.[!.]* -maxdepth 4 -type f -size -1024k -not -path "$ROOT/*" -not -path "$HOME/.local/state/*" -exec grep -l "Generated by tackroom" {} +
   ```
   The second command finds every file that carries the `Generated by tackroom` line: the role files of each harness, and, with an `instructions:` list, the root files such as `~/.claude/CLAUDE.md` and `~/.codex/AGENTS.md`, which are generated copies. Delete those root files too, unless step 3 put your own back: Claude Code's copy imports files from the config root, which step 5 deletes. Both commands search the hidden folders of your home directory, following one that is itself a link, such as a linked `~/.config`. If `tackroom.yaml` sets a `skill_root` or `agent_root` anywhere else, run them on that folder too.
5. **Delete the config root and the state folder.** The config root is `$TACKROOM_HOME`, else `~/.agents`. The state folder is `$XDG_STATE_HOME/tackroom` (default `~/.local/state/tackroom`) and holds the backups, so delete it last. Job logs are in `~/.local/state/tackroom/cron` even when `$XDG_STATE_HOME` is set.
6. **Remove the binary** with the package manager you installed it with.

These stay, because no tackroom command removes them:

- `hooks = true` under `[features]` in `~/.codex/config.toml`, and an empty `~/.codex/hooks.json`.
- Empty `mcpServers` maps in MCP files such as `~/.claude.json` and `~/.pi/agent/mcp.json`, an empty `mcp` map in `~/.config/opencode/opencode.json`, and, after Claude Code plugins, empty `enabledPlugins` and `extraKnownMarketplaces` maps in `~/.claude/settings.json`.
- The skills folder setup added to Qwen Code's `skills.directories` and Amp's `amp.skills.path`.
- Keys you declared under `settings:`. Removing a key leaves the native value in place.
- Empty folders tackroom created, such as `~/.claude/agents`.

## Reference

### Flags

| Flag | Effect |
|---|---|
| `--from <git url>` | Clone or pull your config repository first ([Set up another machine](#set-up-another-machine)) |
| `--overlay NAME` | Start this machine from a committed `tackroom.local.yaml` ([Machine overlays](#machine-overlays)) |
| `--review` | Ask before each step ([Review each step instead](#review-each-step-instead)) |
| `--dry-run` | Show what the run would import and whose copy it keeps; change nothing |
| `--agents a,b` | Limit the run to the named harnesses |
| `--no-cron` | Skip the periodic job |
| `--verbose` | Print every step and the full sync report |
| `--json` | Print the detection result |
| `--config PATH` | Use another config file |
| `--yes` | Accepted and does nothing: it is the default now |
| `--memory` | Accepted and does nothing: tackroom no longer sets up memory ([memory.md](memory.md)) |

### What setup does

1. **Creates the config root** and, when it has no `tackroom.yaml` yet, copies in the starter content: the `tackroom` skill, six roles, `tackroom.yaml`, `tackroom.lock`, `.gitignore`, `plugin.json` and `.agnix.toml`. No instructions file and no third-party skills ship.
   - A config root that already has `tackroom.yaml` (a rerun, or a clone on another machine) gets no starter files, so a starter role, skill or `plugin.json` you deleted stays deleted.
2. **Detects harnesses** by their executable on `PATH` and records them in `tackroom.yaml`. Amp is not auto-detected; add it with `--agents amp`.
   - A `tackroom.yaml` whose content does not change is not rewritten, so its formatting stays as you wrote it.
3. **Copies every skill, role and MCP server** your harnesses already have into the config root. Import is copy-only and adds nothing to what it copies; nothing is moved yet.
   - When two harnesses have different copies of a skill, the first detected harness's copy is kept and the summary says which.
   - A role is shared only when every harness's copy would come back unchanged from the shared one: same fields, same values, same instructions, nothing added.
   - A role stays each harness's own, and no starter role of that name is added, when it has a field tackroom cannot write (for example `permissionMode` or `sandbox_mode`), lacks the frontmatter a shared role needs, has different copies in different harnesses, or is a link to a file elsewhere.
4. **Imports existing root instructions** into `AGENTS.md`. With none, no `AGENTS.md` is created and every harness keeps its own file.
   - One file is copied as is. Several different ones are kept in full under a `## From <agent> (<file>)` heading each.
   - Every agent then reads all of them, so each `setup` run lists them under what needs your attention until you merge them and delete those headings.
5. **Runs the first sync.** Every native file or skill folder it replaces, identical to the shared copy or not, is first moved to a backup under `~/.local/state/tackroom/backups/`, so `tackroom restore latest` puts them all back.
   - A role is replaced only when the rewritten role is the same role, and never when it is a link. Otherwise that one role file is left in place and listed as needing attention, and everything else still syncs into that harness.
   - Removing or overwriting content an earlier tackroom run wrote is left for `tackroom sync`: the summary lists it per harness with that command.
6. **Initializes git** and makes the first commit on a first setup: no `tackroom.yaml` before this run and the folder not already under git. A config that already has `tackroom.yaml` and no git (for example after you declined git with `--review`) is left without it.
7. **Installs the periodic sync job** unless you pass `--no-cron` (see [Sync on a schedule](#sync-on-a-schedule)).

### Starter files

`setup` copies the starter content into a new config root only: `tackroom.yaml`, `tackroom.lock`, `agents/*.md`, `skills/`, `plugin.json`, `.agnix.toml` and `.gitignore`. The starter set is yours. Only `setup` on a config root without `tackroom.yaml` creates it, so a starter file you delete stays deleted.

`AGENTS.md` is not a starter file; setup creates it only to import your harnesses' own instructions.

Releases up to 2.8 also managed memory scripts under `memory/` and recorded them in `.tackroom-starter.json`. What `sync` does with them now is in [memory.md](memory.md#coming-from-the-bundled-memory).
