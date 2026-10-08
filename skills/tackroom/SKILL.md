---
name: tackroom
description: Set up, inspect, and sync a private user-owned agent configuration across Claude Code, Codex, Cursor, GitHub Copilot CLI, Grok Build, OpenClaw, DeepSeek Harness (dsh), Hermes, Droid, OpenCode, Qwen Code, Pi, and OMP. Use for tackroom setup, status, sync, doctor, skill, MCP, hook, role, memory-tier, or config-root workflows.
---

# tackroom

`tackroom` is a public Go CLI. The user's canonical configuration is a separate private directory or repository.

Config precedence:

1. `--config /path/to/tackroom.yaml`
2. `$TACKROOM_HOME/tackroom.yaml`
3. `~/.agents/tackroom.yaml`

Never infer configuration from the current project. Never replace a user's config root with a public checkout.

## Commands

```bash
tackroom setup [--memory off|basic|memsearch] [--agents ...] [--yes] [--dry-run] [--json]
tackroom status [--verbose] [--agents ...]
tackroom sync [--pull] [--agents ...]
tackroom doctor [--e2e] [--agents ...]
tackroom config validate
tackroom config print
tackroom view [--addr 127.0.0.1:8765] [--no-open] [--secure-cookie] [--ssh-host user@host]
tackroom skill new <name> [--description ...]
tackroom skill list [--agents ...]
tackroom skill info <name>
tackroom skill update [name ...]
tackroom skill promote <name-or-path> [--dry-run]
tackroom publish [--target NAME] [--skills a,b] [--dry-run] [--json] [--yes]
tackroom mcp <list|add|import|remove> [options]
```

`view` (browser web UI) is the canonical authoring surface: a dashboard with
per-agent sync state, skill and role matrices, MCP and hook targeting per agent,
unmanaged items in agent folders, and a YAML editor. It edits shared YAML or the
machine-local overlay; effective configuration is read-only. Each toggle
applies immediately, and `view` never runs `sync` implicitly. It binds only to
loopback and uses a session cookie plus CSRF and origin protection. `config`
validates or prints the result. HarnessKit (`hk serve`) and AgentsView
(`agentsview serve`) are optional companion tools that you run on their own.

Run `tackroom help --all` for maintenance commands.

## setup

First-run setup:

1. Creates the canonical config root when absent.
2. Extracts missing public starter assets without overwriting user files.
3. Detects supported harness binaries.
4. Scans native skill, role, and MCP locations.
5. Shows an interactive review screen: share, keep harness-specific, or skip per item; items identical across harnesses are shared automatically. Imports are copy or conversion only; originals remain untouched.
6. In non-interactive runs it falls back to sequential prompts; `--yes` imports everything without prompting, `--dry-run` prints candidates and exits without changes, `--json` emits the detection result and exits.
7. Registers the chosen memory tier.
8. Patches only the required native harness settings.
9. Runs the first sync.

The public starter contains `tackroom`, the pinned `grilling` example, six generic roles (`architect` `builder` `general` `researcher` `reviewer` `tester`), and reusable memory scripts. Personal skills, hooks, MCP servers, secrets, and memory data belong only in the private config repository.

Memory tiers:

- `off`: no managed memory hooks.
- `basic` (default): Python-3-only bounded Markdown digests under `$KNOWLEDGE_DIR/sessions/` plus recent context at session start.
- `memsearch`: the indexed SessionStart, Stop, and SessionEnd pipeline; requires `memsearch` on `PATH`.

For Hermes, the `memsearch` tier also keeps the built-in memory files and the
knowledge vault in sync: `sync-vault-to-memory.sh` runs at session start,
`session-end.sh` captures the session digest at finalize, and
`sync-memory-to-vault.sh` exports durable memory at finalize. The generic
`session-start.sh` is not registered for Hermes because its Claude-style
context response is not consumed by Hermes; Hermes injects the synchronized
built-in memory natively instead.

Verify the complete setup with `tackroom doctor --e2e`, then run
`hermes hooks doctor`. Hermes hook approval is host-local and may require one
interactive approval after setup.

## status

Reports each configured harness as detected or not detected and compares the four managed surfaces with native state:

- skills
- MCP servers
- hooks
- agent roles

Missing, drifted, conflicting, stale managed, and unrelated external entries are reported separately.

## sync

Reconciles only configured managed entries. Unrelated native content remains untouched.

For symlink-based harnesses, skills point to canonical directories under `~/.agents/skills`. Hermes uses `skills.external_dirs: ["~/.agents/skills"]`; Qwen Code uses `skills.directories: ["~/.agents/skills"]`. Both consume the canonical tree without creating a duplicate mirror. OpenCode, Cursor, GitHub Copilot CLI, Grok Build, OpenClaw and DeepSeek Harness read `~/.agents/skills` themselves; tackroom mirrors into their skill roots only when the config root is elsewhere.

Agent roles are canonical Markdown files under `~/.agents/agents/` and render to:

- Claude Code: `~/.claude/agents/<name>.md`
- Codex: `~/.codex/agents/<name>.toml`
- Factory Droid: `~/.factory/droids/<name>.md`
- OpenCode: `~/.config/opencode/agents/<name>.md`
- Pi with `pi-subagents`: `~/.pi/agent/agents/<name>.md`
- OMP: `~/.omp/agent/agents/<name>.md`
- Qwen Code: `~/.qwen/agents/<name>.md`
- Cursor: `~/.cursor/agents/<name>.md`
- GitHub Copilot CLI: `~/.copilot/agents/<name>.agent.md`
- Grok Build: `~/.grok/agents/<name>.md`

Pi always has managed skills. With `pi-subagents` installed, tackroom renders canonical roles into Pi's user agent directory. With `pi-mcp-adapter` installed, it patches canonical and Agent Plugin MCP entries into `~/.pi/agent/mcp.json`. Put pinned package sources under the Pi target's `packages` list to make `sync` reconcile `~/.pi/agent/settings.json`; Pi installs missing declared packages at startup. Tackroom does not install the Pi executable. The role and MCP files remain inert when their packages are absent. OMP is a separate target.

For MCP servers, sync patches only named canonical entries and preserves unrelated native servers. Import redacts literal environment values to `${KEY}` references; list output never prints values.

For hooks, sync registers only declared entries on harnesses with verified hook support. Host-local review and approval state remains outside tackroom; Hermes keys first-use consent by the exact event and command and reports script mtime drift through `hermes hooks doctor`.

Claude Code plugins (mods, workflows, monitors, output styles, LSP, `bin/`) stay in Claude's native layout under the config root, listed in `<config-root>/.claude-plugin/marketplace.json`. `sync` registers that directory as a marketplace in `~/.claude/settings.json` (`extraKnownMarketplaces`) and enables each relative-path entry (`enabledPlugins`). Claude Code reads it in place: edit, then `/reload-plugins`. User disables and `defaultEnabled: false` are respected; stale enable keys for that marketplace are pruned. `doctor` runs `claude plugin validate` on it.

`sync --pull` runs `git pull --ff-only` in the private canonical repository before reconciliation.

## External skills

Declare external Git sources in the private `tackroom.yaml`:

```yaml
external_skills:
  - url: https://github.com/example/shared-skills
    branch: main
    skill_dirs: [engineering/alpha, productivity/beta]
    materialize: true
```

`tackroom.lock` records exact commits and materialized ownership. `sync` repairs drift to the pin; `skill update` explicitly advances it. `doctor` audits external source content and fails on materialization drift.

## publish

`publish` is the outward analogue of external skills: it pushes canonical skills to a remote skill registry (OpenAI `/v1/skills`) and pins the returned id/version in `tackroom.lock` under `published_skills`. Declare opt-in targets with an explicit skill allowlist in `tackroom.yaml`:

```yaml
publish_targets:
  - name: openai
    kind: openai-skills
    enabled: true
    skills: [jobs, tech-search]
    api_key_env: OPENAI_API_KEY   # key is read from this env var, never inlined
```

`tackroom publish` uploads only allowlisted skills whose bundle content changed since the last run (content-hash idempotency: create, skip if unchanged, new version if changed). It is inert until a target sets `enabled: true`. Use `--dry-run` to preview, `--target`/`--skills` to narrow, `--json` for machine output, `--yes`/`-y` to skip the confirmation prompt. A real upload always prints a US-only / no-Zero-Data-Retention warning first; do not publish skills carrying secrets or private vault content.

## MCP management

```bash
tackroom mcp list
tackroom mcp add local --command uvx --arg pkg@1.2.3 --env KEY=value
tackroom mcp import claude local --agents=codex,hermes,droid,pi,omp
tackroom sync
tackroom mcp remove local
```

Use versions verified against the package registry. Keep secrets in environment variables or host-local native config, never canonical YAML.

## doctor --e2e

Runs sync, status, and doctor as one health check. It fails on drift, conflicts, invalid external pins, unsupported managed claims, or other doctor errors.

```bash
tackroom doctor --e2e
```

## view

Opens the canonical config UI in your browser: the review-first authoring surface, served over a loopback-only HTTP listener embedded in the `tackroom` binary (no HarnessKit, Node, or separate daemon). Views: Overview (agent cards with sync state, counts and estimated skill-listing tokens), Skills and Roles (matrices of what is on disk per agent), MCP & hooks (toggle an entry or the agents it targets), Unmanaged (foreign skills, conflicts, stale links with the next command), and Config (edit, check and save the YAML). It authors the shared YAML and the machine-local overlay, shows a read-only effective merge, guards saves by revision, and keeps sync as a separate preview/confirm step in which every destructive item must be ticked. Loopback-only bind, tokenized startup URL bootstrapped into an `HttpOnly`, `SameSite=Strict` session cookie, plus CSRF and origin checks on mutations.

It prints the tokenized URL on its own line and, when running locally, opens it in your default browser. `--no-open` suppresses the browser launch. `--addr` sets the loopback bind (default `127.0.0.1:8765`). `--secure-cookie` marks the session cookie `Secure` for HTTPS loopback access (e.g. behind a Tailscale HTTPS proxy). On a remote host, pass `--ssh-host user@host` (or run inside an SSH session, where it derives the host from `SSH_CONNECTION`) to print a ready `ssh -L` tunnel command instead of auto-opening.

```bash
tackroom view                                        # open the config UI locally
tackroom view --no-open --addr 127.0.0.1:8765        # print the URL, do not open a browser
tackroom view --ssh-host me@box                      # remote: print an ssh -L tunnel command
```

Legacy HarnessKit flags on `view` (`--port`, `--host`, `--no-token`) are rejected with a one-line pointer to running HarnessKit itself (`hk serve`); they do not launch it.

## Capability matrix

| Harness | Skills | Roles | MCP | Hooks |
|---|---|---|---|---|
| Claude Code | yes | yes | yes | yes |
| Codex | yes | yes | yes | yes |
| Factory Droid | yes | yes | yes | yes |
| Hermes | yes, config-driven | no | yes | yes |
| OpenCode | yes | yes | yes | no |
| Pi | yes | yes, via `pi-subagents` | yes, via `pi-mcp-adapter` | no |
| OMP | yes | yes | yes | no |
| Qwen Code | yes, config-driven | yes | yes | yes |
| Cursor | yes | yes | yes, `~/.cursor/mcp.json` | yes, `~/.cursor/hooks.json` |
| GitHub Copilot CLI | yes | yes | yes, `~/.copilot/mcp-config.json` | yes, `~/.copilot/hooks/tackroom.json` |
| Grok Build | yes | yes | yes, `~/.grok/config.toml` | yes, `~/.grok/hooks/tackroom.json` |
| OpenClaw | yes | no | yes, `~/.openclaw/openclaw.json` `mcp.servers` | no |
| DeepSeek Harness | yes | no | yes, `tackroom-mcp-*` rows in `~/.dsh/cordis.patch.yml` | no |

Do not add a surface without a verified native adapter and focused tests.
