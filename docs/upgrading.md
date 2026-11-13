# Upgrading to 2.0

tackroom 2.0 removes the Hermes and OpenClaw harnesses (with their hooks, MCP adapters, setup defaults and doctor checks), the `hermes` and `openbot` cron sources, the `tackroom skill promote` command, the macOS app, the HarnessKit coexistence notes, and the legacy `tackroom view --port/--host/--no-token/--name` flags.

What changes:

- `tackroom.lock` gains two keys: `accepted:` (audit decisions) and `holds:` (freshness holds). An older tackroom that rewrites the shared lock drops both, so upgrade every machine that shares the config root before syncing.
- `setup` now installs the periodic sync job (`tackroom pull`) by default, every 15 minutes instead of opt-in at 30; `--no-cron` skips it.
- If you use Hermes or OpenClaw, tackroom leaves their native files in place and simply stops managing them; delete them yourself if you no longer need them.

# Upgrading from dotagents

tackroom 1.0 is the dotagents CLI under a new name. An existing config root needs its files renamed once; tackroom refuses to run until they are.

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
- Move `skills/dotagents` to `skills/tackroom`. If you never edited it, copy this repo's `skills/tackroom` over it; otherwise replace `dotagents` with `tackroom` inside it.
- Install tackroom, run `tackroom sync`, then `tackroom cron` if you use it, and remove the old `dotagents` binary.

Role files that dotagents rendered into harness folders are adopted and rewritten on the first sync.
