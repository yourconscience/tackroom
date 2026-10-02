# Setup walkthrough

## What `tackroom setup` does

1. Creates `~/.agents` if it does not exist and copies in the starter content (two skills, six roles, memory hooks, a minimal `tackroom.yaml`).
2. Detects which harnesses are installed and records their native paths.
3. Scans each harness for skills, roles, and MCP servers you already have and shows a review screen: one row per item, share/keep/skip per row, items identical across harnesses shared automatically. Copy-only, originals untouched. Non-interactive runs fall back to sequential prompts; `--yes` imports everything without prompting, `--dry-run` prints the candidates and exits without changes, `--json` emits the detection result for scripting and exits.
4. Before its first sync touches a harness that already has content, shows exactly what would be removed or overwritten there and asks per harness. Declining keeps that harness's files.
5. Offers to `git init` the new repository, and runs the first sync.

The review screen in step 3 looks like this — `space` cycles share/keep/skip per row, `enter` applies:

```
tackroom setup — review 3 item(s)  (2 identical, shared automatically)

  skill   grilling   claude-code✓ codex✓ droid·   [share]
  skill   my-notes   claude-code✓ codex· droid✓   [share] (differ) from claude-code
  role    reviewer   claude-code✓ codex✓ droid✓   [skip]

↑↓ move  space cycle action  ←→ pick source  a share-all  s skip-all  enter apply  q abort
```

## Multi-machine

```bash
cd ~/.agents
git remote add origin <your-private-repository>
git push -u origin main
# on the next machine: clone it to ~/.agents, install tackroom, run tackroom setup
```

Subsequent syncs: `tackroom sync --pull` pulls the repo first, then reconciles. Machines without a Go toolchain skip the memory-tools build step; everything else syncs normally.

## Authoring the canonical YAML

After setup, use the authoring surface rather than editing native harness
files:

```bash
tackroom config
tackroom view --no-open --addr 127.0.0.1:8765
tackroom config validate
tackroom config print
```

The shared file and `tackroom.local.yaml` remain separate layers. The
effective view is read-only, and saving YAML never runs `sync`. The web UI
binds to loopback, requires a session cookie and CSRF header, and keeps sync
behind an explicit preview/apply confirmation.

For temporary HTTPS access from a tailnet, the operator owns the route:

```bash
tackroom view --no-open --secure-cookie --addr 127.0.0.1:8765
tailscale serve --bg --set-path /tackroom http://127.0.0.1:8765
```

## Managed starter files

`setup` copies the starter content into the config root once. The memory code layer under `memory/hooks/` and `memory/lib/` stays managed afterwards: on every `sync`, tackroom

- scaffolds files that are missing,
- refreshes files it wrote that you have not modified, so an upgraded CLI does not leave an old hook layer behind,
- removes files a release stopped shipping,
- and reports, without touching, anything you edited yourself.

Ownership is tracked in `.tackroom-starter.json` at the config root (commit it alongside `tackroom.yaml`). Only content tackroom wrote is ever refreshed or removed, so a customized layer is safe.

Everything else in the starter set — `AGENTS.md`, `tackroom.yaml`, `agents/*.md`, `skills/` — is your content: tackroom only creates those when they are missing.

## Memory tier

Choose during setup or reconfigure later:

```bash
tackroom setup --memory basic      # default
tackroom setup --memory off
tackroom setup --memory memsearch
```

| Tier | Behavior | Dependency |
|---|---|---|
| `off` | no managed memory hooks | none |
| `basic` | appends a bounded digest of each session to `$KNOWLEDGE_DIR/sessions/YYYY-MM-DD.md` and injects recent digests as context at session start | Python 3 |
| `memsearch` | full-text indexed search over the knowledge vault | `memsearch` on PATH (`uv tool install memsearch`) |

## Root instructions

`~/.agents/AGENTS.md` is your single root instruction file. During sync, tackroom links it into each harness's native memory path — `~/.config/amp/AGENTS.md` for Amp, `~/.claude/CLAUDE.md` for Claude Code, `~/.codex/AGENTS.md` for Codex, `~/.factory/AGENTS.md` for Droid, and `~/.qwen/QWEN.md` for Qwen Code — so an edit in one place reaches every agent. `tackroom status` reports drift, and a file that exists but is not a symlink is never touched without your confirmation.
