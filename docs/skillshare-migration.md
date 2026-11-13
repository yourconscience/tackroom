# Migrating from skillshare

skillshare and tackroom solve the same problem: one place for your agent skills, roles, hooks, MCP servers and instructions. This guide maps a skillshare setup onto `tackroom.yaml` and the `~/.agents` folders; [comparison.md](comparison.md) says where the two differ.

## Mapping

| skillshare | tackroom |
|---|---|
| Skills source folder (default `~/.config/skillshare/skills`) | `~/.agents/skills/` for skills you wrote |
| Skills installed from a repo (`skills:` entries with a `source`) | `external_skills:`, pinned in `tackroom.lock` |
| Agents source folder | `~/.agents/agents/`, rendered into each harness's role format |
| `targets` | `agents:` entries, one per harness; `setup` detects them |
| `mcp.servers` | `mcp_servers:` |
| Hooks managed with `skillshare hooks` | `hooks:` |
| `audit.block_threshold` | `doctor audit`: an unaccepted CRITICAL finding in external content blocks sync, and new pins wait 72 hours |
| An extra holding a shared `AGENTS.md` or similar | `~/.agents/AGENTS.md`, plus `instructions:` for more files |

## Steps

1. **Preview.** `tackroom setup --dry-run` lists what it would import and changes nothing.
2. **Copy what you wrote.** Skills are `skills/<name>/SKILL.md` and roles are `agents/<name>.md` directly under the config root:
   ```bash
   mkdir -p ~/.agents/skills ~/.agents/agents
   cp -R ~/.config/skillshare/skills/<your-skill> ~/.agents/skills/
   cp ~/.config/skillshare/agents/<your-role>.md ~/.agents/agents/
   ```
3. **Pin external skills.** For each repo skillshare installed from, add it to tackroom so the commit is pinned and audited instead of copied:
   ```bash
   tackroom skill sources add owner/repo --skills a,b
   ```
4. **Move hooks** into a top-level `hooks:` block:
   ```yaml
   hooks:
     - name: notify-stop
       enabled: true
       event: Stop                       # Claude Code event names
       command: ~/.agents/hooks/notify.sh
       timeout: 10                       # seconds
       agents: [claude, codex]           # optional; default: every harness with hooks
   ```
5. **Move MCP servers** with `tackroom mcp add NAME --command ... [--arg ...]` or `--url ...`, or write them in `mcp_servers:`. To adopt one a harness already has, run `tackroom mcp import <harness> <name>`.
6. **Move Pi packages and per-harness settings** under the harness's own entry:
   ```yaml
   agents:
     - name: pi
       enabled: true
       detect: pi
       packages:
         - npm:<package>@<exact-version>
       settings:
         <dotted.key>: <value>
   ```
7. **Run it.** On the first machine, `tackroom setup`. After that, `tackroom sync` and `tackroom doctor`.

Skills that skillshare linked into a harness are relinked to the canonical copy on the first sync. A real folder with different content is never replaced without your confirmation, and then only after a backup.

## Verify

- `tackroom status` shows every harness synced with no drift.
- `tackroom doctor` has no failures, and any held or blocked item has a reason in `status`.
- `tackroom hook list` and `tackroom mcp list` match what skillshare used to write.

## Clean up

Once `tackroom status` is clean, uninstall skillshare the way you installed it and remove its config folder, so the two tools never write the same harness files.
