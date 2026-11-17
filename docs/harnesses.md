# Harnesses

tackroom writes your setup into 16 coding agents, called harnesses, each in its own native format. This page is the capability matrix: what each harness gets today, the key that names it in `tackroom.yaml`, and where its files land. Look here before you rely on a surface in one harness.

```bash
tackroom status                       # per harness: synced, drifted, or not detected
tackroom sync --agents codex,gemini   # limit a run to some harnesses, by key
```

## Capability matrix

| Harness | Skills | Roles | MCP | Hooks | Plugins | Root file | Settings |
|---|---|---|---|---|---|---|---|
| Claude Code | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| Codex | ✓ | ✓ | ✓ | ✓ | ~ | ✓ | ✓ |
| Factory Droid | ✓ | ✓ | ~ | ✓ | - | ✓ | - |
| OMP | ✓ | ✓ | ~ | ~ | - | ✓ | ✓ |
| OpenCode | ✓ | ✓ | ~ | - | - | - | - |
| Pi | ✓ | ~ | ✓ | ~ | - | ✓ | ✓ |
| Qwen Code | ✓ | ✓ | ~ | ✓ | - | ✓ | - |
| Cursor | ✓ | ✓ | ✓ | ✓ | - | - | - |
| Devin | ✓ | - | ✓ | - | - | ✓ | - |
| Gemini CLI | ✓ | ✓ | ✓ | ✓ | - | ✓ | - |
| GitHub Copilot CLI | ✓ | ✓ | ~ | ✓ | - | ✓ | - |
| Grok Build | ✓ | ✓ | ✓ | ✓ | - | ✓ | - |
| DeepSeek Harness | ✓ | - | ~ | - | - | ✓ | - |
| Antigravity CLI | ✓ | - | ~ | ✓ | - | ✓ | - |
| Kimi Code | ✓ | ✓ | ~ | ✓ | - | - | - |
| Amp | ✓ | - | ~ | - | - | ✓ | - |

`✓` full, `~` partial, `-` none. A cell claims only what the adapter writes today; tackroom does not invent files a harness cannot read.

What each `~` means:

- **Roles, Pi.** The files are written to `~/.pi/agent/agents/`, but Pi uses them only with the `pi-subagents` package. Declare it under `packages:` (see [Pi](#pi)).
- **MCP.** Stdio servers only. Remote servers (`mcp add --url`) reach the seven harnesses marked `✓`: Claude Code, Codex, Pi, Cursor, Devin, Gemini CLI and Grok Build. See [mcp.md](mcp.md).
- **Hooks, OMP and Pi.** Only `SessionStart` and `SessionEnd` map. Other events are reported as unsupported. See [hooks.md](hooks.md#which-harnesses-run-hooks).
- **Plugins, Codex.** Only agent-plugins-spec sources with `mcp: true`, installed through `codex plugin add` ([plugins.md](plugins.md#codex-plugins)). Codex has no plugin layout for your own plugins in the config root.

What the Plugins and Settings columns mean:

- **Plugins** is `✓` where tackroom registers a harness's own plugin format, which is Claude Code ([plugins.md](plugins.md)). Codex is `~`: tackroom installs agent-plugins-spec sources with `mcp: true` as native plugins by running `codex plugin`, tested with codex-cli 0.159.2.
- **Plugins elsewhere.** In every other harness, skills and, with `mcp: true`, MCP servers from an agent-plugins-spec source arrive through the Skills and MCP columns.
- **Settings** means a `settings:` block under the agent entry is written into the harness's own settings file ([setup.md](setup.md#per-harness-settings)). Supported: Claude Code, Codex, Pi and OMP; the files are in the table below. Other harnesses report `settings unsupported` and ignore the block.

## Config keys and detection

The key is what you write in `tackroom.yaml`, in `--agents`, and in the `agents:` list of an MCP server or hook. A harness is detected by its executable on `PATH`; one that is not found is skipped and reported as not detected. The skill root is where `sync` links skills when the harness does not read `~/.agents/skills` itself.

| Harness | Key | Executable | Skill root |
|---|---|---|---|
| Claude Code | `claude` | `claude` | `~/.claude/skills` |
| Codex | `codex` | `codex` | `~/.codex/skills` |
| Factory Droid | `droid` | `droid` | `~/.factory/skills` |
| OMP | `omp` | `omp` | `~/.omp/agent/skills` |
| OpenCode | `opencode` | `opencode` | `~/.config/opencode/skills` |
| Pi | `pi` | `pi` | `~/.pi/agent/skills` |
| Qwen Code | `qwen-code` | `qwen` | `~/.qwen/skills` |
| Cursor | `cursor` | `cursor-agent` | `~/.cursor/skills` |
| Devin | `devin` | `devin` | `~/.config/devin/skills` |
| Gemini CLI | `gemini` | `gemini` | `~/.gemini/skills` |
| GitHub Copilot CLI | `copilot` | `copilot` | `~/.copilot/skills` |
| Grok Build | `grok` | `grok` | `~/.grok/skills` |
| DeepSeek Harness | `dsh` | `dsh` | `~/.dsh/skills` |
| Antigravity CLI | `agy` | `agy` | `~/.gemini/antigravity-cli/skills` |
| Kimi Code | `kimi` | `kimi` | `~/.kimi-code/skills` |
| Amp | `amp` | `amp` | `~/.agents/skills` |

- `setup` detects every harness above except Amp; add that with `--agents amp`.
- Pi and OMP are separate harnesses: `pi` is not counted as detected when its `--version` output starts with `omp/`.
- Claude Code is `claude`. The former key `claude-code` is still accepted and read as `claude`. A tackroom older than 1.2.0 does not know `claude`, so upgrade every machine that shares the config ([release notes](upgrading.md#the-claude-key)).

## Where each surface lands

Roles follow each harness's `agent_root`; the paths are in [roles.md](roles.md#where-each-harness-gets-its-role). Skills are linked into the skill root above. The other surfaces:

| Harness | MCP servers | Hooks | Root file | Settings |
|---|---|---|---|---|
| Claude Code | `~/.claude.json` | `~/.claude/settings.json` | `~/.claude/CLAUDE.md` | `~/.claude/settings.json` |
| Codex | `~/.codex/config.toml` | `~/.codex/hooks.json` | `~/.codex/AGENTS.md` | `~/.codex/config.toml` |
| Factory Droid | `~/.factory/mcp.json` | `~/.factory/hooks.json` | `~/.factory/AGENTS.md` | none |
| OMP | `~/.omp/agent/mcp.json` | `~/.omp/agent/hooks/post/tackroom-<name>.ts` | `~/.omp/agent/AGENTS.md` | `~/.omp/agent/config.yml` |
| OpenCode | `~/.config/opencode/opencode.json` | none | none | none |
| Pi | `~/.pi/agent/mcp.json` | `~/.pi/agent/extensions/tackroom-<name>.ts` | `~/.pi/agent/AGENTS.md` | `~/.pi/agent/settings.json` |
| Qwen Code | `~/.qwen/settings.json` | `~/.qwen/settings.json` | `~/.qwen/QWEN.md` | none |
| Cursor | `~/.cursor/mcp.json` | `~/.cursor/hooks.json` | none | none |
| Devin | `~/.config/devin/mcp_config.json` | none | `~/.config/devin/AGENTS.md` | none |
| Gemini CLI | `~/.gemini/settings.json` | `~/.gemini/settings.json` | `~/.gemini/GEMINI.md` | none |
| GitHub Copilot CLI | `~/.copilot/mcp-config.json` | `~/.copilot/hooks/tackroom.json` | `~/.copilot/copilot-instructions.md` | none |
| Grok Build | `~/.grok/config.toml` | `~/.grok/hooks/tackroom.json` | `~/.grok/AGENTS.md` | none |
| DeepSeek Harness | `~/.dsh/cordis.patch.yml` | none | `~/.dsh/AGENTS.md` | none |
| Antigravity CLI | `~/.gemini/config/mcp_config.json` | `~/.gemini/config/hooks.json` | `~/.gemini/GEMINI.md` | none |
| Kimi Code | `~/.kimi-code/mcp.json` | `~/.kimi-code/config.toml` | none | none |
| Amp | `~/.config/amp/settings.json` | none | `~/.config/amp/AGENTS.md` | none |

Some folders move with an environment variable: `$COPILOT_HOME`, `$GROK_HOME`, `$DSH_HOME` and `$KIMI_CODE_HOME` replace the folder of their harness, and `$XDG_CONFIG_HOME/opencode` replaces `~/.config/opencode`.

The root files are links to `~/.agents/AGENTS.md`, or generated files when you list extra `instructions:` ([setup.md](setup.md#share-your-root-instructions)). Where the table says none, nothing is written for that surface.

## Notes per harness

### Skills

OpenCode, Cursor, Devin, Gemini CLI, GitHub Copilot CLI, Grok Build, Kimi Code and DeepSeek Harness read `~/.agents/skills` themselves. tackroom mirrors skills into their own skill root only when your config root is not `~/.agents`.

Qwen Code and Amp are config-driven: `setup` adds the skills folder to `skills.directories` in `~/.qwen/settings.json` and to `amp.skills.path` in Amp's settings, so no mirror is needed. More in [skills.md](skills.md#write-your-own).

### Pi

Pi 1.0 and later read MCP servers from `~/.pi/agent/mcp.json`; an older Pi needs `pi-mcp-adapter`. tackroom does not install Pi itself. A Pi entry can declare a pinned `packages` list; `sync` writes it into `~/.pi/agent/settings.json` as a whole and Pi installs missing packages on its next start. Leave `packages` out to keep Pi's own list untouched.

```yaml
agents:
  - name: pi
    enabled: true
    detect: pi
    skill_root: ~/.pi/agent/skills
    agent_root: ~/.pi/agent/agents
    packages:
      - npm:pi-subagents@<exact-version>
```

An exact npm version in `packages` is in cooldown until it is 72 hours old ([skills.md](skills.md#the-72-hour-cooldown)).

### Grok Build and Cursor

Grok Build also reads Claude Code's skills, roles, hooks and MCP servers and drops duplicates by name. If Cursor's third-party configs setting is on, Cursor also runs Claude Code's hooks, so a hook synced to both runs twice there.

### DeepSeek Harness

MCP servers are written as `tackroom-mcp-<name>` rows in `~/.dsh/cordis.patch.yml`, a file every `dsh` profile applies. tackroom renders no roles or hooks for it. Its root file links to `~/.dsh/AGENTS.md`.

### Antigravity CLI

Skills go to `~/.gemini/antigravity-cli/skills` (this CLI only, not Gemini CLI's `~/.gemini/skills`). MCP servers and hooks go to `~/.gemini/config/`, which Gemini CLI does not read. Only stdio MCP servers are written, roles are not rendered yet, and hooks cover `SessionStart`, `Stop`, `PreToolUse` and `PostToolUse`. It shares `~/.gemini/GEMINI.md` with Gemini CLI.

### Devin

Devin means Devin CLI and the Devin Local agent in Devin Desktop (formerly Windsurf), which share one configuration in `~/.config/devin`. It is detected by `devin` on `PATH`, so Devin Desktop without the CLI installed is not detected.

The adapter was checked against Devin CLI 3000.12.57 with `devin skills`, `devin mcp`, `devin rules` and `devin doctor`, without signing in. Nothing here was checked in a model session or in Devin Desktop.

- **Skills.** Devin reads `~/.agents/skills` itself. tackroom mirrors skills into `~/.config/devin/skills` only when your config root is not `~/.agents`.
- **MCP.** Stdio and remote servers go to `mcpServers` in `~/.config/devin/mcp_config.json`. A remote server gets `url` and `headers` and no `transport`, so Devin tries Streamable HTTP first and falls back to SSE. Devin fills in a `${NAME}` in a header from its environment, as it does `${env:NAME}`. Whether it does the same in a stdio server's `env` is not verified.
- **Root file.** `~/.config/devin/AGENTS.md`, the global rules file Devin documents.
- **Roles.** Not rendered, but Devin loads them itself when your config root is `~/.agents`: it reads `~/.agents/agents/*.md` as custom subagents, beside the `~/.config/devin/agents/` its docs name. With the config root elsewhere, Devin gets no roles.
  - `devin doctor` lists them and warns that it ignores the `color` key.
  - A role's `tools` reach Devin as written, with Claude Code's names. Devin's docs list `read`, `edit`, `grep`, `glob` and `exec` for that field, and whether Devin matches `Read` or `Bash` to them was not checked, so do not rely on a role's tool limits in Devin.
- **Hooks.** Not written. By default Devin loads Claude Code's hooks from `~/.claude/settings.json` (setting `read_config_from.claude`), so a second copy written by tackroom would run each hook twice on a machine that has both.

By default Devin also imports Claude Code's rules and MCP servers: it reads `~/.claude/CLAUDE.md` as a global rule and the servers in `~/.claude.json`. A server with the same name as one in `~/.config/devin/mcp_config.json` is listed once, from Devin's own file.

What happens to the root file depends on your config:

- Without an `instructions:` list, both files are links to the same `AGENTS.md`: Devin loads it once, and `devin rules list` reports `~/.claude/CLAUDE.md` as a conflict.
- With an `instructions:` list, `~/.claude/CLAUDE.md` is a generated file of `@path` import lines, and Devin loads it as a second rule next to its own generated copy.

To turn the import off, set this in `~/.config/devin/config.json`:

```json
{
  "read_config_from": {
    "claude": false
  }
}
```

That also stops Devin loading Claude Code's hooks, so Devin then runs none of your tackroom hooks. On a machine with Codex, Devin also reads `~/.codex/skills`, where tackroom links your skills for Codex. A skill that reaches Devin from both folders is listed once.

### Kimi Code

Kimi Code reads `~/.agents/skills` and `~/.agents/AGENTS.md` itself, so no root file is written for it and files listed under `instructions:` do not reach it. MCP servers go to `~/.kimi-code/mcp.json` and hooks to `[[hooks]]` tables in `~/.kimi-code/config.toml`.

### Amp

Amp gets skills, stdio MCP servers and a root file. tackroom writes no roles or hooks for it. A `.amp/settings.json` in your config root takes precedence over `~/.config/amp/settings.json` when it exists, and `settings.jsonc` is used when that is the file you have.
