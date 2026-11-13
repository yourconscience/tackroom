# Troubleshooting

Start with `tackroom doctor`, then `tackroom status`. This page lists the messages and states you are most likely to meet and what to do about each.

```bash
tackroom doctor                  # health checks, one line each
tackroom status --verbose        # full per-harness lists and native paths
tackroom sync --dry-run          # what a real sync would change, including held and blocked items
```

`doctor` validates skill frontmatter, roles, the lock and materialized copies, native hook registrations and the periodic sync job, and audits external sources. `--json` works on `status`, `sync` and `doctor`.

## Conflicts

**"exists but is not a symlink" or "differs from canonical content".** A real file or skill folder with different content sits where tackroom wants a link, for example `~/.claude/CLAUDE.md`. Sync skips that harness and syncs the rest. Merge anything you want to keep into the shared copy under `~/.agents`, then run:

```bash
tackroom sync --replace-conflicts    # moves the native files to a backup, then links the shared copy
tackroom restore                     # list backups, newest first
tackroom restore latest --dry-run    # preview putting the newest one back
```

Backups live in `$XDG_STATE_HOME/tackroom/backups/<time>/` (default `~/.local/state/tackroom/backups/`).

**A sync proposed removals you did not expect.** Setup-driven syncs preview removals and role overwrites per harness and default to no. Answer `n`, then inspect with `tackroom status`.

**"refusing to run with repo root ... linked git worktree".** The config root cannot be, or live inside, a linked git worktree, because the links it creates would dangle when the worktree is removed. Point `$TACKROOM_HOME` or `--config` at the main checkout.

**"config unreadable" for one harness.** Its native config (JSON, YAML or TOML) does not parse. tackroom leaves that file alone and syncs the others. Fix the reported line and rerun `tackroom sync`.

## External content

**An item is "held".** A new or changed external skill pin, or exact npm or PyPI version, waits 72 hours after its publish time. `tackroom status` shows the reason, the hold end and the override; `tackroom sync --allow-fresh` (or `--allow-fresh=<item> --reason "..."`) releases it. See [skills.md](skills.md#the-72-hour-hold).

**An item is "blocked".** Sync skips new or changed external content with an unaccepted CRITICAL audit finding and keeps the previous pin. Run `tackroom doctor audit`, then `accept` the finding or fix the source. See [skills.md](skills.md#the-audit-gate).

## Hooks

**A removed tool left stale native hooks.** Review them, preview the cleanup, then remove:

```bash
tackroom hook list <tool-name>
tackroom hook remove --dry-run <tool-name>
tackroom hook remove <tool-name>
tackroom sync && tackroom doctor
```

Unrelated hook entries are preserved.

## Pi

**Pi MCP entries do not appear.** Keep the server targeted at `pi`, run `tackroom sync`, then restart Pi or run `/reload`. Pi 1.0 and later read `~/.pi/agent/mcp.json` natively; an older Pi needs `pi-mcp-adapter`, which you can declare under the Pi agent's `packages`. tackroom writes only its own named entries in that file.

**Pi packages are listed but not installed.** tackroom manages the `packages` declaration in `~/.pi/agent/settings.json`, not the Pi executable or its npm runtime. Install Pi, run `tackroom sync`, then start Pi once so it installs the declared packages.

## Periodic sync and memory

**"periodic sync is off".** The `tackroom-pull` job is missing, paused, broken or its last run failed. Reinstall it with `tackroom cron`. `setup --no-cron` records `periodic_sync: false` in `tackroom.local.yaml`, which silences the warning on purpose.

**Memory tools are skipped during sync.** No Go toolchain on `PATH`. Install Go, or copy prebuilt `rem` and `knowledge-sync` binaries to `$GOBIN` or `~/.local/bin`; sync rebuilds them only when their sources change.
