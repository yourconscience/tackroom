# Upgrading

What changes when you move to a new tackroom release, and what to do on every machine that shares your config root.

```bash
tackroom version
```

## Unreleased

- New: `tackroom tmux` and `tackroom zellij`. Each opens a session named `tackroom` in the config root with a shell, `tackroom status` and `tackroom cron collect --table`, and takes `--config PATH` and `--dry-run`, as `tackroom tern` and `tackroom rex` do. A second run shows the existing session. See [terminals.md](terminals.md#tmux) and [terminals.md](terminals.md#zellij)
- Changed: `tackroom setup` on a machine with no `tackroom.yaml` no longer turns memory on. A new config gets the `off` tier: no memory hooks, no `memory/tools/` sources, and no `rem` or `knowledge-sync` build. An existing config keeps its tier. To turn memory on, run `tackroom setup --memory basic`.

## To 2.8

- Removed: Gemini CLI support. Google moved individual accounts from Gemini CLI to Antigravity CLI on 2026-06-18 ([announcement](https://developers.googleblog.com/en/an-important-update-transitioning-gemini-cli-to-antigravity-cli/)); tackroom keeps supporting Antigravity CLI as `agy`. A config that still names `gemini` loads and syncs: the `gemini` agent entry is ignored, and so is `gemini` in the `agents:` list of an MCP server or hook and in `exclude_agents` or `mcp_agents` of an external skill source. A server, hook or source MCP whose list named only `gemini` is turned off rather than sent to every harness. Each run prints one warning. `gemini` is not read as `agy`, because the two use different files: add an `agy` entry yourself if you use Antigravity CLI. A command that rewrites `tackroom.yaml`, such as `tackroom mcp add`, saves it without the `gemini` entries.
- Removed: what earlier syncs wrote for Gemini CLI stays where it is. tackroom does not remove its MCP servers and hooks from `~/.gemini/settings.json`, its skill links in `~/.gemini/skills` or its roles in `~/.gemini/agents`; delete them by hand if you no longer want them. Antigravity CLI also reads `~/.gemini/skills`, and `tackroom doctor` warns when a skill is there and in `~/.gemini/antigravity-cli/skills`. `~/.gemini/GEMINI.md` stays, as Antigravity CLI's root file.
- New: Devin (key `devin`, detected by `devin` on `PATH`), which covers Devin CLI and the Devin Local agent in Devin Desktop. It gets skills (read from `~/.agents/skills`, mirrored into `~/.config/devin/skills` only when the config root is elsewhere), stdio and remote MCP servers in `~/.config/devin/mcp_config.json`, and a root file at `~/.config/devin/AGENTS.md`. tackroom writes it no roles and no hooks. Devin loads the role files in `~/.agents/agents/` itself when that is the config root, with their tool names as written, and it loads Claude Code's hooks by default, so a copy from tackroom would run twice. Devin also reads `~/.claude/CLAUDE.md` by default. With an `instructions:` list in `tackroom.yaml`, a machine with both loads it as a second rule unless you set `read_config_from.claude` to `false` in `~/.config/devin/config.json`; without one, Devin loads the root file once. Checked against Devin CLI 3000.12.57 without signing in. See [Devin](harnesses.md#devin).

## To 2.7.1

Nothing to migrate by hand: `tackroom.yaml` and `tackroom.lock` keep their formats.

- Fixed: `tackroom setup` on a config root that already has `tackroom.yaml` (a rerun, or a clone on another machine) no longer brings back starter files you deleted, such as a starter role, the `tackroom` skill or `plugin.json`. Starter files now go only into a config root with no `tackroom.yaml`. A memory tier that is on, including one `setup --memory basic` turns on, still gets its missing files under `memory/`. A config root holding only a `tackroom.yaml` you wrote by hand no longer gets the starter roles and skill from `setup`.
- Fixed: `setup` no longer rewrites `tackroom.yaml` when its content does not change. Before, every run reformatted it (blank lines dropped), which left a change in a cloned config repository and could stop `git pull --ff-only` there.
- Fixed: `sync`, and the sync `setup` runs, no longer add a `holds:` entry to `tackroom.lock` for an npm or PyPI version that was already older than 72 hours when first looked up. On a new machine every pinned Pi package or MCP server version was new, so its first sync changed the lock. Entries already in the lock are handled as before; one an earlier release wrote with a `hold_until` in the past and no `allowed` can be deleted and committed once every machine runs this release.
- Changed: Pi packages are looked up only on a machine where Pi is detected. A machine without Pi makes no registry lookup for them and records no hold; a decision already in the lock still applies there.
- Changed: release binaries are built with Go 1.27. They were built with Go 1.25, which no longer gets security fixes: `govulncheck` reports 14 standard-library vulnerabilities in the 2.7.0 binary and none in a 1.27.4 build. Building from source still needs Go 1.25 or newer.

- Fixed: `skill sources add --latest` holds a head younger than 72 hours instead of pinning and syncing it. The lock records the hold and no pin, and nothing reaches an agent until the hold ends or you release it. `add` now takes `--allow-fresh` and `--reason`, as `skill update` does.
- Fixed: `skill sources add` runs the audit gate. A source with an unaccepted CRITICAL finding is neither pinned nor linked until you accept it with `tackroom doctor audit accept`.
- Fixed: `skill update` of one source no longer pins other sources that are held or blocked.
- Fixed: a release recorded for a held commit applies to that commit. A branch head that moved afterwards gets its own hold.
- Fixed: `status`, `sync`, `sync --dry-run` and the view list a hold or block created by `skill update`, with an override that works: `tackroom skill update <name> --allow-fresh=<item>`.
- Changed: `tackroom doctor audit` labels its first count as heuristic findings.

## To 2.7

Nothing to migrate by hand: `tackroom.yaml` and `tackroom.lock` keep their formats.

- New: `tackroom setup --overlay NAME` copies `overlays/NAME.yaml`, a complete `tackroom.local.yaml` committed in the config repository, into place before the first sync, for machines of one kind such as cloud VMs: `tackroom setup --from <repository url> --overlay cloud --no-cron`. A different `tackroom.local.yaml` is never overwritten; setup lists it as needing your attention and exits 1. An unknown name stops setup before it syncs anything. See [Machine overlays](setup.md#machine-overlays).
- New: `tackroom hosts fix [HOST...]` runs the fix `tackroom hosts` names (`tackroom setup`, `pull` or `sync` on that machine, over SSH) and prints the machine's new row; `--dry-run` only prints it. A fix that commits and pushes is still printed for you to run. `tackroom hosts setup user@host` sets up another machine from this one with `tackroom setup --from`, and `--install` installs tackroom there first. `hosts --json` adds `fix_runnable` to each host.
- Changed: for a machine that has tackroom but no config, `tackroom hosts` now names `tackroom hosts setup HOST` as the fix, not a bare `tackroom setup` there, which would start an unrelated config.
- New: the Claude Code pane syncs and edits, as the Herdr panel does. When something waits, Sync shows the plan, with what it removes or overwrites, before it applies it, and applies only that plan. Harnesses, MCP servers and hooks have Turn on and Turn off, MCP servers and hooks add or remove a harness, and with more than one machine in `crons.hosts` a Machines section shows each one's health. It needs tackroom 2.6 or newer as the plugin's `tackroom` program; with an older one the pane stays as before and says to update tackroom. Update the Claude Code plugin to get it.

## To 2.6

Nothing to migrate by hand: `tackroom.yaml` and `tackroom.lock` keep their formats. One thing changes on its own: a hook you turned off in the config is now removed from the agents on the next sync (below).

- New: the Herdr panel syncs and edits. Overview has a Sync button that shows the plan, with what it removes or overwrites, before it applies it. Harnesses, MCP servers and hooks have Turn on and Turn off, and MCP servers and hooks add or remove an agent, as the web view's switches and cells do. Copy the two mods from `herdr/tackroom/mods/` again to get it. With an older tackroom the panel stays read-only.
- Fixed: the Herdr panel could stay on "Loading…" (the crons panel on "Collecting…") for good when Herdr created it hidden, as it does at start: its first loads never returned. They now start a moment later.
- New: `tackroom config set <section/name/field> <value>` changes one field, as the web view's switches do, for example `tackroom config set mcp_servers/exa/enabled false`. An entry defined in `tackroom.local.yaml` is changed there. The path must name a field that section's entries have; a misspelled field, a shorter path or `null` is refused.
- New: `sync --dry-run --json` adds `plan`, `destructive` and `plan_digest`, and `tackroom sync --plan-digest DIGEST` syncs only while the plan is still the one with that digest and `tackroom.yaml` and `tackroom.local.yaml` are unchanged. `destructive` lists every removal (skills, roles, MCP servers, hooks, plugins, Pi packages, starter files) and every role overwrite.
- Fixed: turning a hook off, or removing an agent from it, now removes it from that agent on the next sync. Before, `sync` reported "up to date" and the command stayed registered until `tackroom hook remove`. The native file is backed up first, so `tackroom restore latest` puts it back. `status` lists such a hook as `hooks stale`, and `--json` as `agents[].hooks.stale`. This applies to hooks `tackroom.yaml` still declares; a registration of a hook you deleted from the config is left alone, as before.
- Changed: the web view's Sync now also asks you to tick hook and Pi package removals.

## To 2.5.1

Nothing to migrate by hand: `tackroom.yaml` and `tackroom.lock` keep their formats.

- Fixed: a role with `tools: []` was written with no `tools` key, so Claude Code, GitHub Copilot CLI, Gemini CLI, Kimi Code, OMP and Pi gave it every tool. They now get no tools, and the next sync, including the periodic `tackroom pull`, rewrites those copies. `tools: ""` now also means no tools. Codex, Cursor, Factory Droid, OpenCode, Qwen Code and Grok Build cannot express it and keep the harness default; see [Tools](roles.md#tools).
- The Herdr panel moved to `herdr/tackroom/`, a package Herdr's mod gallery can list, with a recipe that adds the button and the panel to the layout you use. The `tackroom` layout in `herdr/layouts/` is gone; if you copied it, it keeps working. To update the panel, copy the two mods from `herdr/tackroom/mods/`, or paste the gallery line in [herdr.md](terminals.md#install) to your agent. Where the notes below say to copy `herdr/mods/tackroom`, copy `herdr/tackroom/mods/tackroom` instead.

## To 2.5

`tackroom.yaml` and `tackroom.lock` keep their formats. Scripts that run `tackroom setup --yes` keep working.

- `tackroom setup` no longer asks anything. It does what `setup --yes` did: imports everything your agents have (copies; a differing native file is backed up before it is replaced), installs the periodic job, and ends with a short summary instead of the full sync report. Unlike `--yes`, it initializes git only on a first setup (no `tackroom.yaml` yet), so a config you kept out of git stays that way; it never changes the memory tier of an existing `tackroom.yaml` without `--memory` (`basic` on a new one); and it exits 1 whenever the summary lists something that needs you, possibly after the sync, commit and job are already done. To review imports and confirm each step as before, run `tackroom setup --review`; `--verbose` prints the full report. `--yes` is still accepted and does nothing, so scripts keep working.
- New: `tackroom setup --from <git url>` sets up another machine in one command. It clones your config repository into the config root, or pulls it when it is already a clone of that URL, then sets up from it. A config root with other content is refused and left as it is. See [Multi-machine](setup.md#multi-machine).
- Changed: a native role is shared, and later replaced, only when the role tackroom writes keeps every field and the instructions of the native file. A role with a field tackroom cannot write (such as `permissionMode` or `sandbox_mode`), with different copies in different agents, or that is a link to a file elsewhere stays each agent's own, and setup adds no starter role of that name, so a fresh setup ends synced. Where the config already has a role of that name, the native file is left in place and listed with how to fix it, and only that file: the rest of that agent still syncs (before, one such file kept the whole agent from syncing). `sync --replace-conflicts` replaces the roles that pass this check, which it did not do before. A role imported without frontmatter no longer gets an invented model, effort or a description holding a machine path.
- If 2.4 left role conflicts on a machine (`status` or `sync` listed a role as "exists but is not tackroom-managed", and that agent got nothing), the first sync after upgrading, including the unattended periodic `tackroom pull`, now syncs that agent. It backs up and links that agent's skill folders and `CLAUDE.md`-style instructions files that are identical to the shared copy, and writes the shared roles into it, including roles a 2.4 setup imported from another agent without the fields tackroom cannot write (such as `sandbox_mode` or `permissionMode`) or, for a Codex role with `"""` instructions, with the raw TOML as its instructions. The conflicting role files themselves stay in place. Right after upgrading, before the next periodic run, run `tackroom sync --dry-run` to see what it would change. To keep a role as one agent's own, rename that agent's file so its name differs from the shared role's. To stop sharing a role, delete `~/.agents/agents/<name>.md` and remove the copies tackroom already wrote into the other agents' folders (they start with a `Generated by tackroom` line); sync does not remove them.
- Fixed: importing a Codex role whose `developer_instructions` is a `"""` multi-line string no longer copies the raw TOML into the instructions.
- Fixed: rerunning `setup` no longer removes the memory hooks from each agent's config only for the sync to add them back.
- Changed: `sync` (and so `setup`) backs up a native file or skill folder that is identical to the shared copy before linking it, as it already did for different ones, so `tackroom restore latest` puts back everything a run replaced.
- Fixed: with many agents, the web view's Skills, Roles and MCP & hooks tables showed no names. The name column now keeps its width and the table scrolls sideways inside its card.
- `setup` lists instructions files it merged from several agents into `AGENTS.md` as needing your attention, and `setup --dry-run` now says what the run would do: whose copy it keeps and which roles stay each agent's own.

## To 2.4

Nothing to migrate by hand: `tackroom.yaml` and `tackroom.lock` keep their formats. If your periodic sync job dates from before 2.0, run `tackroom cron` once (below).

- New: `tackroom hosts` shows tackroom on every machine in `crons.hosts`: its version, whether it is set up and synced, and its config commit against this machine's, with the command that fixes what is wrong. With more than one host, the view's Overview and the Herdr panel's Overview show the same as a Machines block. Copy `herdr/mods/tackroom` again to get it in the panel. See [commands.md](commands.md#check-health).
- Fixed: `tackroom setup` stopped with `config hook memory-session-start targets unknown agent "claude"` when `tackroom.local.yaml` listed an agent, for example one turned off on that machine. It also stopped on any machine with Grok Build installed, reading `~/.grok/config.toml` as JSON. Both now set up; a harness config tackroom cannot read is reported as a warning and its MCP servers are left out of the import.
- If you first installed the periodic sync job with a tackroom before 2.0, run `tackroom cron` once. That job sources `~/.profile` first, and when the file is missing (common with zsh) or fails, `/bin/sh` stops before tackroom runs: the job never synced and its log stayed empty. `tackroom status` and `tackroom doctor` now name this case.
- Deprecated: `tackroom tui`. It still runs, with a notice, and will be removed in 3.0. Use `tackroom view` (on a desktop, a phone or in a side panel) or the [Herdr panel](terminals.md#herdr-panel); in a terminal, `tackroom status`, `tackroom cron collect --table` and `tackroom doctor` print the same facts.

## To 2.3

Nothing to migrate by hand: `tackroom.yaml` and `tackroom.lock` keep their formats.

- `tackroom view` works on a phone and in a narrow side panel: larger text and tap targets, matrices that stack into rows, a web app manifest so it can go on a home screen, and a page that asks for the token when a phone opens it without one. See [On a phone](view.md#on-a-phone).
- The view's Overview lists outside sources as pinned, held or blocked, with the reason and the command that overrides it. The view now applies the freshness hold and the audit gate the way `tackroom status` does, so it no longer offers a sync for a skill that sync would skip.
- New: `tackroom config inventory` prints the view's inventory as JSON.
- The Herdr panel is now a `tackroom` panel with Overview, Skills, Roles, MCP & hooks and Crons tabs. Copy `herdr/mods/tackroom` and the `tackroom` layout again to get it; the old `crons.svg` icon is replaced by `tackroom.svg`. See [terminals.md](terminals.md#herdr-panel).

## To 2.2

Nothing to migrate by hand: `tackroom.yaml` and `tackroom.lock` keep their formats. One thing moves on its own, for Codex users with plugin sources (below).

- New: Codex settings. Keys under `settings:` on the `codex` agent are written into `~/.codex/config.toml`; every other key in that file is kept. See [Per-harness settings](setup.md#per-harness-settings).
- New: `tackroom cron add`, `cron edit` and `cron remove` for jobs tackroom owns on this machine, with the same actions in the web view and the Herdr panel. See [crons.md](crons.md#add-edit-and-remove-owned-jobs).
- `tackroom view` uses the Graphite theme and now has a light theme that follows the OS setting.

Codex now installs an agent-plugins-spec source with `mcp: true` as a native plugin, through `codex plugin`. `tackroom.yaml` and `tackroom.lock` keep their formats. See [Codex plugins](plugins.md#codex-plugins).

- If a source with `mcp: true` already synced into Codex, the next `tackroom sync` moves it. It removes that source's skill links from `~/.codex/skills` and the `[mcp_servers]` rows tackroom wrote for it, backs up `~/.codex/config.toml`, and installs `<name>@tackroom` with `codex plugin add`. Codex then also gets the plugin's `streamable-http` servers.
- A source that sets `skills:`, `skill_dir:` or `skill_dirs:`, that sets no `mcp: true`, or that lists Codex in `exclude_agents` or leaves it out of `mcp_agents` keeps its links and rows.
- `codex` must be on `PATH`. A failed `codex plugin` call is reported for the Codex plugin surface only; the other surfaces still apply.
- To keep one source out of Codex, set `enabled = false` under `[plugins."<name>@tackroom"]` in `~/.codex/config.toml`, or add `codex` to its `exclude_agents`.

## To 2.1

Nothing to migrate: `tackroom.yaml` and `tackroom.lock` keep their formats.

- A download piped into a shell (`curl ... | sh`, `wget ... | bash`, `base64 -d ... | sh`) in an external skill is now a CRITICAL audit finding. A new or changed pin with that pattern is skipped until you accept it with `tackroom doctor audit accept`. Pins already in the lock keep syncing, and your own skills are reported, never blocked.
- The Hermes memory bridge is gone. The next `tackroom sync` deletes `memory/hooks/sync.sh`, `sync-memory-to-vault.sh`, `sync-vault-to-memory.sh` and `memory/lib/sync.py` from your config root if you never edited them. Edited copies are reported and left alone. `tackroom setup` removes the old sync hook commands from native configs.
- `tackroom doctor` no longer fails because an external skill is held or blocked. It passes, counts them and says what to do next.
- Install with Homebrew or npm. The install script is no longer offered.
- New: `tackroom tui`, a read-only terminal view. See [terminals.md](terminals.md#terminal-view-deprecated).

## To 2.0

tackroom 2.0 removes the Hermes and OpenClaw harnesses (with their hooks, MCP adapters, setup defaults and doctor checks), the `hermes` and `openbot` cron sources, the `tackroom skill promote` command, the macOS app, the HarnessKit coexistence notes, and the legacy `tackroom view --port/--host/--no-token/--name` flags.

- `tackroom.lock` gains two keys: `accepted:` (audit decisions) and `holds:` (freshness holds). An older tackroom that rewrites the shared lock drops both, so upgrade every machine that shares the config root before syncing.
- `setup` now installs the periodic sync job (`tackroom pull`) by default, every 15 minutes. It was opt-in at 30 minutes. `--no-cron` skips it.
- If you use Hermes or OpenClaw, tackroom leaves their native files in place and stops managing them. Delete them yourself if you no longer need them.

## The `claude` key

In `tackroom.yaml` and `--agents`, Claude Code is `claude`, since 1.2.0. The former name `claude-code` is still accepted. A tackroom older than 1.2.0 does not know `claude`, so upgrade every machine that shares the config.

## From dotagents

tackroom 1.0 is the dotagents CLI under a new name. An existing config root needs its files renamed once; tackroom refuses to run while `dotagents.yaml`, `dotagents.local.yaml` or `.dotagents-starter.json` still exist without their new names.

```bash
dotagents cron --remove                        # only if you installed the cron entry
cd ~/.agents
git mv dotagents.yaml tackroom.yaml
git mv dotagents.lock tackroom.lock
git mv .dotagents-starter.json .tackroom-starter.json
mv dotagents.local.yaml tackroom.local.yaml    # only if you have one
```

Then:

- In `.gitignore`, replace `dotagents.local.yaml` with `tackroom.local.yaml` so machine-local overrides stay out of git.
- Move `skills/dotagents` to `skills/tackroom`. If you never edited it, copy the [`skills/tackroom` folder](https://github.com/yourconscience/tackroom/tree/main/skills/tackroom) from the tackroom repository over it; otherwise replace `dotagents` with `tackroom` inside it.
- Install tackroom, run `tackroom sync`, then `tackroom cron` if you use it, and remove the old `dotagents` binary.

Role files that dotagents rendered into harness folders are adopted and rewritten on the first sync.
