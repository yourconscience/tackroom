---
name: tackroom
description: Set up, inspect, and sync a private user-owned agent configuration across Claude Code, Codex, Cursor, Gemini CLI, Antigravity CLI, GitHub Copilot CLI, Kimi Code, Grok Build, DeepSeek Harness (dsh), Factory Droid, OpenCode, Qwen Code, Pi, OMP, and Amp. Use for tackroom setup, status, sync, doctor, skill, MCP, hook, role, memory-tier, or config-root workflows.
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
tackroom setup [--memory off|basic|memsearch] [--agents ...] [--yes] [--no-cron] [--dry-run] [--json]
tackroom status [--verbose] [--exit-code] [--agents ...]
tackroom sync [--pull] [--dry-run] [--replace-conflicts] [--agents ...] [--allow-fresh[=item,...]] [--reason ...]
tackroom doctor [--e2e] [--agents ...]
tackroom doctor audit [--json]                  # findings per item; exit 1 on an unaccepted CRITICAL in external content
tackroom doctor audit accept <item> <rule> --reason "..."   # record an audit decision; item is kind:name, e.g. skill:foo
tackroom doctor audit revoke <item> <rule>
tackroom config validate
tackroom config print
tackroom config inventory                       # the web view's inventory as JSON: harnesses, skills, roles, MCP servers, hooks
tackroom view [--addr 127.0.0.1:8765] [--no-open] [--secure-cookie] [--ssh-host user@host] [--token-file PATH]
tackroom tui [--config PATH]                    # read-only terminal view of agents, lock, crons and doctor; needs a terminal
tackroom skill new <name> [--description ...]
tackroom skill list [--agents ...]
tackroom skill info <name>
tackroom skill sources [--json]
tackroom skill sources search <query> [--json]
tackroom skill sources add <owner/repo[/path] | git URL> [--skills a,b | --all] [--latest] [--name N] [--no-sync]
tackroom skill update [name ...] [--allow-fresh[=item,...]]
tackroom publish [--target NAME] [--skills a,b] [--dry-run] [--json] [--yes]
tackroom mcp <list|add|import|remove> [options]
tackroom restore [<backup> | latest] [--dry-run]
tackroom tern [--config PATH] [--dry-run]       # open a tackroom workspace in the Tern terminal
tackroom rex [--config PATH] [--dry-run]        # open a tackroom workspace in the Rex terminal
tackroom pull [options]        # git pull --ff-only, then sync; what the periodic job runs
tackroom cron [--interval 15m|--deps|--remove]   # periodic sync job, installed by setup; default 15m
tackroom cron collect [--hosts local,HOST...] [--table] # read-only scheduled-job inventory
tackroom cron apply [--dry-run]       # install crons.jobs entries with schedule+command as launchd, systemd or crontab units
tackroom cron run|pause|resume [--host HOST] ID   # act on a launchd job, here or over SSH
tackroom cron schedule ID "MIN HOUR DOM MON DOW"    # new schedule for an owned job on this machine
tackroom cron add ID --schedule "MIN HOUR DOM MON DOW" --command CMD [--arg ARG ...] [--host NAME] [--effect TEXT] [--local] [--dry-run] [--no-apply]   # declare an owned job, then apply
tackroom cron edit ID [--schedule EXPR] [--command CMD [--arg ARG ...]] [--host NAME] [--effect TEXT] [--dry-run] [--no-apply]   # change fields of an owned job
tackroom cron remove ID [--dry-run] [--no-apply]    # delete an owned job's declaration and unit
```

`sync` prints the skills, agents, MCP servers, hooks, packages and plugins the run changed (harnesses with identical changes grouped), a table with one row per harness, and every managed item per harness, with long lists wrapped to the terminal. `status` stays compact; `--verbose` adds the full lists. Both drop escape codes when output is not a terminal and color when `NO_COLOR` is set.

`view` (browser web UI) is the primary surface, on a desktop, a phone or a narrow
pane: a dashboard with per-agent sync state, outside sources with their pins, holds
and blocks, skill and role matrices, MCP and hook targeting per agent, unmanaged
items in agent folders, and a YAML editor. The CLI is the engine: every view action
calls the same code, and the view has no data or action the CLI lacks. It edits shared YAML or the
machine-local overlay; effective configuration is read-only. Each toggle
applies immediately, and `view` never runs `sync` implicitly. It binds only to
loopback and uses a session cookie plus CSRF and origin protection. `config`
validates or prints the result. AgentsView (`agentsview serve`) is an optional
companion tool that you run on its own.

Run `tackroom help --all` for maintenance commands.

## setup

First-run setup:

1. Creates the canonical config root when absent.
2. Extracts missing public starter assets without overwriting user files.
3. Detects supported harness binaries.
4. Scans native skill, role, and MCP locations.
5. Shows an interactive review screen: share, keep harness-specific, or skip per item; items identical across harnesses are shared automatically. Imports are copy or conversion only; originals remain untouched.
6. In non-interactive runs it falls back to sequential prompts; `--yes` answers every prompt with its default and never reads stdin, `--dry-run` prints candidates and exits without changes, `--json` emits the detection result and exits.
7. Registers the chosen memory tier.
8. Installs the periodic sync job (`tackroom pull` every 15 minutes), unless `--no-cron`.
9. Patches only the required native harness settings.
10. Runs the first sync.

Setup also imports existing instructions files (`~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md`, ...) into `~/.agents/AGENTS.md`, then backs up the originals to `~/.local/state/tackroom/backups/<time>/` and links them.

Extra always-on files (voice, user profile) go under `instructions:` in `tackroom.yaml`, never pasted into `AGENTS.md`. Every managed root doc then carries `AGENTS.md` plus those files: Claude Code via `@` imports, the others as a generated copy marked `Generated by tackroom`. Edit the sources, then run `tackroom sync`; status shows a stale copy as drifted.

### Running setup for a user (agents)

```bash
tackroom setup --dry-run            # show what would be imported; changes nothing
tackroom setup --yes --memory basic # unattended: imports, git init, backs up and links differing files
tackroom status --json              # verify; exit 0 = everything synced
```

Exit codes: `status` and `sync` exit 1 when anything is out of sync or conflicting; `doctor` exits 1 when any check fails or warns. All exit 0 otherwise. Warnings are advisory (for example a package younger than three days, or a registry that could not be reached); to gate on real failures, read `failed` from `doctor --json`. `--json` output on those three is a stable contract: `synced`, `agents[].conflicts`, `agents[].skills|mcp|roles|hooks.{managed,missing,drifted}`, plus `agents[].mcp.stale` when present. When `sync` reports conflicts, show the listed paths to the user before running `tackroom sync --replace-conflicts`.

The public starter contains `tackroom`, six generic roles (`architect` `builder` `general` `researcher` `reviewer` `tester`), and reusable memory scripts. Personal skills, hooks, MCP servers, secrets, and memory data belong only in the private config repository.

Memory tiers:

- `off`: no managed memory hooks.
- `basic` (default): Python-3-only bounded Markdown digests under `$KNOWLEDGE_DIR/sessions/` plus recent context at session start.
- `memsearch`: the indexed SessionStart, Stop, and SessionEnd pipeline; requires `memsearch` on `PATH`.

Verify the complete setup with `tackroom doctor --e2e`.

## status

Reports each configured harness as detected or not detected and compares the four managed surfaces with native state:

- skills
- MCP servers
- hooks
- agent roles

Missing, drifted, conflicting, stale managed, and unrelated external entries are reported separately. `--exit-code` exits 0 in sync, 1 on drift, 2 on error; held and blocked items do not count as drift.

## sync

Reconciles only configured managed entries. Unrelated native content remains untouched.

For symlink-based harnesses, skills point to canonical directories under `~/.agents/skills`. Qwen Code uses `skills.directories: ["~/.agents/skills"]` and consumes the canonical tree without creating a duplicate mirror. OpenCode, Cursor, Gemini CLI, GitHub Copilot CLI, Grok Build and DeepSeek Harness read `~/.agents/skills` themselves; tackroom mirrors into their skill roots only when the config root is elsewhere.

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
- Gemini CLI: `~/.gemini/agents/<name>.md` (local subagents)

Pi always has managed skills. With `pi-subagents` installed, tackroom renders canonical roles into Pi's user agent directory. It patches canonical and Agent Plugin MCP entries into `~/.pi/agent/mcp.json`, which Pi 1.0 and later read natively (older Pi needs `pi-mcp-adapter`). Put pinned package sources under the Pi target's `packages` list to make `sync` reconcile `~/.pi/agent/settings.json`; Pi installs missing declared packages at startup. Tackroom does not install the Pi executable. The role files remain inert without `pi-subagents`. OMP is a separate target.

For MCP servers, sync patches only named canonical entries and preserves unrelated native servers. A server sync wrote earlier and that is no longer wanted for an agent (removed from `tackroom.yaml`, disabled, or dropped from its `agents`) shows as `mcp stale` in `status` and is removed by the next `sync`, after the native config file is copied to `~/.local/state/tackroom/backups/<time>/`. Sync never claims an entry the agent already had; `mcp remove` also claims native entries that still match the definition it removes. Import redacts literal environment values to `${KEY}` references and names the redacted keys; the source agent keeps its values. Other target agents get the literal `${KEY}`, which they may not expand: put the real value in their native config (sync never replaces a value with a reference) or import with `--agents <source>`. List output never prints values.

For hooks, sync registers only declared entries on harnesses with verified hook support. Host-local review and approval state remains outside tackroom.

Claude Code plugins (mods, workflows, monitors, output styles, LSP, `bin/`) stay in Claude's native layout under the config root, listed in `<config-root>/.claude-plugin/marketplace.json`. `sync` registers that directory as a marketplace in `~/.claude/settings.json` (`extraKnownMarketplaces`) and enables each relative-path entry (`enabledPlugins`). Claude Code reads it in place: edit, then `/reload-plugins`. User disables (kept across a marketplace rename) and `defaultEnabled: false` are respected; stale enable keys are pruned and removing the marketplace file removes the registration. A name already registered with another source, or a malformed file, is a conflict and nothing is written. `doctor` runs `claude plugin validate` on it when Claude Code is detected.

Codex gets an agent-plugins-spec external source as a native plugin when it sets `mcp: true`, has a valid root `plugin.json`, targets Codex (`mcp_agents` empty or listing it, `exclude_agents` not listing it), keeps the default `skills/` layout (no `skills:`, `skill_dir:` or `skill_dirs:`) and is not held or blocked. `sync` writes a generated marketplace `tackroom` at `~/.agents/external/.agents/plugins/marketplace.json`, registers it with `codex plugin marketplace add`, installs with `codex plugin add <name>@tackroom` when the plugin is missing or Codex's copy differs, and removes `*@tackroom` plugins that are no longer declared with `codex plugin remove`. Codex then loads that source's skills and MCP servers from the plugin alone, so tackroom links no skills and writes no `[mcp_servers]` rows for it. `enabled = false` under `[plugins."<name>@tackroom"]` in `~/.codex/config.toml` is respected and shown as `plugins off`. `status`, `sync --dry-run` and `doctor` never run `codex`. Tested with codex-cli 0.159.2.

`sync --pull` runs `git pull --ff-only` in the private canonical repository before reconciliation.

`sync --dry-run` prints the plan a real sync would apply, including held and blocked items, and writes nothing (it fetches nothing and makes no registry lookups). `sync --allow-fresh` releases held fresh items, all or a comma list with `--allow-fresh=item,...`, optionally recording `--reason "..."`; held and blocked items do not count as drift.

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

Add a source without editing YAML: `tackroom skill sources add mattpocock/skills --skills tdd,grill-me` (or `--all`; without either, an attended run lists the skills to pick and an unattended one fails with the list). It pins the newest commit at least 3 days old (`--latest` pins the head), writes the entry with `materialize: true`, refuses to overwrite a different `~/.agents/skills/<name>` (an identical copy, as `npx skills add` leaves it, is adopted), then syncs. `tackroom skill sources` lists configured sources and the ones installed outside tackroom (Claude Code plugins, `npx skills`); `search` queries skills.sh. A source that Claude Code already has as a plugin gets `exclude_agents: [claude]`, so Claude does not load its skills twice. Two repos with the same basename need a distinct `name:`; `add` picks `<owner>-<repo>` itself.

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
tackroom mcp add docs --url https://mcp.example.com/mcp --header 'Authorization: Bearer ${DOCS_TOKEN}'  # remote: Claude Code, Codex, Pi, Grok, Cursor, Gemini CLI
tackroom mcp import claude local --agents=codex,droid,pi,omp
tackroom sync
tackroom mcp remove local
tackroom sync                     # removes it from agents where tackroom wrote it or it still matches
```

Use versions verified against the package registry. Keep secrets in environment variables or host-local native config, never canonical YAML.

## doctor --e2e

Runs sync, status, and doctor as one health check. It fails on drift, conflicts, invalid external pins, unsupported managed claims, or other doctor errors.

```bash
tackroom doctor --e2e
```

`doctor audit` lists findings per item (`skill:`, `plugin:`, `role:`, `hook:`, `mcp:`, `package:`) and exits 1 when an external item has an unaccepted CRITICAL finding: a prompt-injection phrase, a secret sent out or a credential file read, invisible tag characters, or a download piped into a shell (rule `remote-exec`). A match inside a Markdown code fence or a script comment counts one level lower. Use `doctor audit accept <item> <rule> --reason "..."` to record a decision (stored in `tackroom.lock` under `accepted:`) and `doctor audit revoke <item> <rule>` to undo one.

## view

Opens the canonical config UI in your browser: the review-first authoring surface, served over a loopback-only HTTP listener embedded in the `tackroom` binary (no Node or separate daemon). Views: Overview (agent cards with sync state, counts and estimated skill-listing tokens, and every outside source as pinned, held or blocked with the command that releases or accepts it), Skills and Roles (matrices of what is on disk per agent), MCP & hooks (toggle an entry or the agents it targets), Unmanaged (foreign skills, conflicts, stale links with the next command), Crons (scheduled jobs and failing services on the hosts in `crons.hosts`, the same data as `tackroom cron collect`, with Run now, Pause and Resume for launchd jobs, a schedule field for owned ones, and Add cron, Edit and Delete for jobs tackroom owns), and Config (edit, check and save the YAML). It authors the shared YAML and the machine-local overlay, shows a read-only effective merge, guards saves by revision, and keeps sync as a separate preview/confirm step in which every destructive item must be ticked. Loopback-only bind, tokenized startup URL bootstrapped into an `HttpOnly`, `SameSite=Strict` session cookie, plus CSRF and origin checks on mutations.

It prints the tokenized URL on its own line and, when running locally, opens it in your default browser. `--no-open` suppresses the browser launch. `--addr` sets the loopback bind (default `127.0.0.1:8765`). `--secure-cookie` serves an HTTPS proxy such as `tailscale serve`: requests through it get `Secure` cookies and https origin checks, while direct loopback requests keep working over http. The token is kept in `view.token` under `$XDG_STATE_HOME/tackroom` (`~/.local/state/tackroom`) or `--token-file`, so the URL survives restarts; `tackroom view` opens an already running view instead of starting another (it finds one at the last recorded address unless `--addr` is given), and a long-running view restarts itself when the `tackroom` binary is upgraded. On a remote host, pass `--ssh-host user@host` (or run inside an SSH session, where it derives the host from `SSH_CONNECTION`) to print a ready `ssh -L` tunnel command instead of auto-opening.

```bash
tackroom view                                        # open the config UI locally
tackroom view --no-open --addr 127.0.0.1:8765        # print the URL, do not open a browser
tackroom view --ssh-host me@box                      # remote: print an ssh -L tunnel command
```


## Capability matrix

| Harness | Skills | Roles | MCP | Hooks |
|---|---|---|---|---|
| Claude Code | yes | yes | yes | yes |
| Codex | yes | yes | yes | yes |
| Factory Droid | yes | yes | yes | yes |
| OpenCode | yes | yes | yes | no |
| Pi | yes | yes, via `pi-subagents` | yes, native since Pi 1.0 | no |
| OMP | yes | yes | yes | no |
| Qwen Code | yes, config-driven | yes | yes | yes |
| Cursor | yes | yes | yes, `~/.cursor/mcp.json` | yes, `~/.cursor/hooks.json` |
| GitHub Copilot CLI | yes | yes | yes, `~/.copilot/mcp-config.json` | yes, `~/.copilot/hooks/tackroom.json` |
| Kimi Code | yes | yes | yes, `~/.kimi-code/mcp.json` | yes, `~/.kimi-code/config.toml` |
| Grok Build | yes | yes | yes, `~/.grok/config.toml` | yes, `~/.grok/hooks/tackroom.json` |
| Gemini CLI | yes | yes | yes, `~/.gemini/settings.json` | yes, `~/.gemini/settings.json` |
| Antigravity CLI | yes | no | yes, `~/.gemini/config/mcp_config.json` | yes, `~/.gemini/config/hooks.json` |
| DeepSeek Harness | yes | no | yes, `tackroom-mcp-*` rows in `~/.dsh/cordis.patch.yml` | no |
| Amp | yes, config-driven | no | yes, `amp.mcpServers` in Amp settings | no |

Do not add a surface without a verified native adapter and focused tests.
