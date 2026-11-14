# Upgrading

What changes when you move to a new tackroom release, and what to do on every machine that shares your config root.

```bash
tackroom version
```

## Unreleased

- New: `tackroom hosts` shows tackroom on every machine in `crons.hosts`: its version, whether it is set up and synced, and its config commit against this machine's, with the command that fixes what is wrong. With more than one host, the view's Overview and the Herdr panel's Overview show the same as a Machines block. Copy `herdr/mods/tackroom` again to get it in the panel. See [commands.md](commands.md#check-health).
- Fixed: `tackroom setup` stopped with `config hook memory-session-start targets unknown agent "claude"` when `tackroom.local.yaml` listed an agent, for example one turned off on that machine. It also stopped on any machine with Grok Build installed, reading `~/.grok/config.toml` as JSON. Both now set up; a harness config tackroom cannot read is reported as a warning and its MCP servers are left out of the import.
- Deprecated: `tackroom tui`. It still runs, with a notice, and will be removed in 3.0. Use `tackroom view` (on a desktop, a phone or in a side panel) or the [Herdr panel](herdr.md); in a terminal, `tackroom status`, `tackroom cron collect --table` and `tackroom doctor` print the same facts.

## To 2.3

Nothing to migrate by hand: `tackroom.yaml` and `tackroom.lock` keep their formats.

- `tackroom view` works on a phone and in a narrow side panel: larger text and tap targets, matrices that stack into rows, a web app manifest so it can go on a home screen, and a page that asks for the token when a phone opens it without one. See [On a phone](view.md#on-a-phone).
- The view's Overview lists outside sources as pinned, held or blocked, with the reason and the command that overrides it. The view now applies the freshness hold and the audit gate the way `tackroom status` does, so it no longer offers a sync for a skill that sync would skip.
- New: `tackroom config inventory` prints the view's inventory as JSON.
- The Herdr panel is now a `tackroom` panel with Overview, Skills, Roles, MCP & hooks and Crons tabs. Copy `herdr/mods/tackroom` and the `tackroom` layout again to get it; the old `crons.svg` icon is replaced by `tackroom.svg`. See [herdr.md](herdr.md).

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
- New: `tackroom tui`, a read-only terminal view. See [tui.md](tui.md).

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
