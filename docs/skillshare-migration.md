# Migrating from skillshare to tackroom

skillshare and tackroom solve the same problem — one place for your agent skills, roles, hooks, MCP servers, and instructions — but tackroom adds dependency discipline (pins, audits, freshness holds), per-harness settings, periodic sync, and a memory tier. This guide maps a skillshare setup onto `tackroom.yaml`.

## Mapping

| skillshare | tackroom |
|---|---|
| `sources.skills` (skill directories) | `~/.agents/skills/` for local skills; `external_skills:` for git sources you pin and audit |
| `sources.agents` (roles) | `~/.agents/agents/` (rendered into each harness's own role format) |
| `targets` (per-harness hooks, MCP, Pi packages) | top-level `hooks:` and `mcp_servers:`, and each agent's `packages:` / `settings:` |
| `audit.block_threshold` | built-in audit gate: `doctor audit`, CRITICAL blocks sync, plus the 72h freshness hold on new pins |
| instructions (AGENTS.md, SOUL.md, profile) | `instructions:` |

## Steps

1. **Keep skills and roles in place.** tackroom reads `~/.agents/skills/` and `~/.agents/agents/` natively, so a skillshare layout there carries over unchanged.
2. **Pin external skills.** For every skill source skillshare pulled from a git URL, add an `external_skills:` entry so tackroom pins the commit and audits it:
   ```yaml
   external_skills:
     - url: https://github.com/latent-spaces/brag
       branch: main
       skill_dirs: [skills/brag]
       materialize: true
   ```
3. **Move hooks** into a top-level `hooks:` block. skillshare's `rem-session-start` and `pr-triage-stop` become:
   ```yaml
   hooks:
     - name: rem-session-start
       enabled: true
       event: SessionStart
       command: ~/.local/share/rem/hooks/session-start.sh
       timeout: 15
       agents: [claude, codex, omp, pi]
   ```
4. **Move MCP servers** into `mcp_servers:`:
   ```yaml
   mcp_servers:
     - name: agentsview
       enabled: true
       command: agentsview
       args: [mcp]
       agents: [claude, codex, pi, omp]
   ```
5. **Move Pi packages** into the `pi` agent's `packages:`, and any per-harness settings into `settings:`:
   ```yaml
   agents:
     - name: pi
       enabled: true
       detect: pi
       packages:
         - npm:pi-web-access@0.47.0
         - npm:pi-subagents@0.84.0
       settings:
         defaultProvider: deepseek
         defaultModel: deepseek-flash
   ```
6. **Run it.** First machine: `tackroom setup`. After that: `tackroom sync` and `tackroom doctor`.

## Verify

- `tackroom status` shows every harness synced with no drift.
- `tackroom doctor` is green: skills frontmatter valid, externals pinned and audited, hooks healthy, and no held or blocked items.
- `tackroom hook list` and `tackroom mcp list` match what skillshare used to write.

## Cleanup

Once `tackroom status` is green, remove skillshare's install (`brew uninstall skillshare`, the desktop app, and `~/.config/skillshare`) so the two tools never write the same harness files.
