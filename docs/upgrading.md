# Upgrading

What changes when you move to a new tackroom release, and what to do on every machine that shares your config root.

```bash
tackroom version
```

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
