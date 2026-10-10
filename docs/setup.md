# Setup walkthrough

## What `tackroom setup` does

1. Creates `~/.agents` if it does not exist and copies in the starter content: the `tackroom` skill, six roles, memory hooks (skipped with `--memory off`), a minimal `tackroom.yaml` and a `.gitignore`. It ships no instructions file and no third-party skills.
2. Detects which harnesses are installed and records their native paths.
3. Scans each harness for skills, roles, and MCP servers you already have and shows a review screen: one row per item, share/keep/skip per row, items identical across harnesses shared automatically. Copy-only, originals untouched. Non-interactive runs fall back to sequential prompts; `--yes` imports everything without prompting, `--dry-run` prints the candidates and exits without changes, `--json` emits the detection result for scripting and exits.
4. Imports the instructions you already have (`~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md` and so on) into `~/.agents/AGENTS.md`: one file is copied as is, several different ones are kept in full under a heading each for you to merge. Without any, no `AGENTS.md` is created and every agent keeps its own file.
5. Before its first sync touches a harness that already has content, shows exactly what would be removed or overwritten there and asks per harness. Declining keeps that harness's files. Native files that differ from the shared copy (your old instructions, a skill imported from another agent) are listed and, once you agree, moved to `~/.local/state/tackroom/backups/<time>/` and linked.
6. Offers to `git init` the new repository, and runs the first sync.

`--yes` answers every prompt with its default and never reads stdin, so an agent or script can run setup unattended: it imports everything, initializes git, backs up and links differing files, and keeps existing content wherever the safe answer is no.

The review screen in step 3 looks like this — `space` cycles share/keep/skip per row, `enter` applies:

```
tackroom setup — review 3 item(s)  (2 identical, shared automatically)

  skill   pr-review  claude✓      codex✓ droid·   [share]
  skill   my-notes   claude✓      codex· droid✓   [share] (differ) from claude
  role    reviewer   claude✓      codex✓ droid✓   [skip]

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
tackroom view --no-open --addr 127.0.0.1:8765
tackroom config validate
tackroom config print
```

`view` opens a dashboard: per-agent sync state, skill and role matrices, MCP
and hook targeting per agent, unmanaged items in agent folders, and a YAML
editor. The shared file and `tackroom.local.yaml` remain separate layers. The
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

Everything else in the starter set — `tackroom.yaml`, `agents/*.md`, `skills/`, `.gitignore` — is your content: tackroom only creates those when they are missing. `AGENTS.md` is yours too; setup only creates it from instructions you already had.

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

`~/.agents/AGENTS.md` is your single root instruction file. During sync, tackroom links it into each harness's native memory path — `~/.config/amp/AGENTS.md` for Amp, `~/.claude/CLAUDE.md` for Claude Code, `~/.codex/AGENTS.md` for Codex, `~/.factory/AGENTS.md` for Droid, `~/.qwen/QWEN.md` for Qwen Code, `~/.copilot/copilot-instructions.md` for GitHub Copilot CLI, `~/.grok/AGENTS.md` for Grok Build, and `~/.dsh/AGENTS.md` for DeepSeek Harness (Cursor has no global instructions file, and OpenClaw's workspace `AGENTS.md` is its assistant persona, so tackroom leaves it alone; `$COPILOT_HOME`, `$GROK_HOME` and `$DSH_HOME` replace `~/.copilot`, `~/.grok` and `~/.dsh` when set) — so an edit in one place reaches every agent. Without `~/.agents/AGENTS.md`, tackroom leaves every agent's instructions file alone. `tackroom status` reports drift; a real file with different content is never replaced without your confirmation, and then only after a backup (`setup`, or `sync --replace-conflicts`). An identical copy is simply relinked.
