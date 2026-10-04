# SPEC: Cursor, GitHub Copilot CLI and Grok Build adapters

Status: requested 2026-10-04 ("support cursor, copilot and grok in next pr"). PR only; merge needs separate approval. Earlier specs are in git history (`git log -- SPEC.md`).

## Goal

`tackroom sync` renders skills, MCP servers, hooks, roles and root instructions into Cursor, GitHub Copilot CLI and Grok Build, using each harness's documented native paths. Evidence for the choice: `research/2026-10-04-tackroom-harness-support.html` in the maintainer's vault (Copilot CLI 7.4M npm downloads in 30 days; Cursor in all 9 compared config managers; Grok Build open source, 27k stars).

## Native surfaces

| | Cursor (`cursor`, detect `cursor-agent`) | Copilot CLI (`copilot`, detect `copilot`) | Grok Build (`grok`, detect `grok`) |
|---|---|---|---|
| Skills | reads `~/.agents/skills` itself; mirror to `~/.cursor/skills` only when the config root is not `~/.agents` | reads `~/.agents/skills` itself; mirror to `~/.copilot/skills` otherwise | reads `~/.agents/skills` itself; mirror to `~/.grok/skills` otherwise |
| Roles | `~/.cursor/agents/<name>.md`: `name`, `description`, `model` (default `inherit`), optional `readonly` | `~/.copilot/agents/<name>.agent.md`: `name`, `description`, `tools` (Copilot aliases), `model` | `~/.grok/agents/<name>.md`: `name`, `description`, `model`, `effort`, `tools` (Claude names), `color` |
| MCP | `~/.cursor/mcp.json` `mcpServers` | `~/.copilot/mcp-config.json` `mcpServers`, `type: local`, `tools: ["*"]` | `~/.grok/config.toml` `[mcp_servers.<name>]` (same shape as Codex) |
| Hooks | `~/.cursor/hooks.json`, `version: 1`, camelCase events per Cursor's Claude mapping | `~/.copilot/hooks/tackroom.json`, `version: 1`, Claude-style event names (Copilot sends Claude-style payloads for them) | `~/.grok/hooks/tackroom.json`, Claude settings format |
| Root instructions | none (no global file) | `~/.copilot/copilot-instructions.md` -> `AGENTS.md` | `~/.grok/AGENTS.md` -> `AGENTS.md` |

Sources: Cursor docs (hooks, third-party hooks, subagents, skills), GitHub docs (`cli-config-dir-reference`, `hooks-reference`, `add-mcp-servers`, `add-skills`, `custom-agents-configuration`), Grok Build source (`xai-org/grok-build`: skill, agent, hook and MCP discovery; `claude_alias.rs`; `Effort`).

Event mapping:
- Cursor: SessionStart, SessionEnd, Stop, PreToolUse, PostToolUse, UserPromptSubmit (`beforeSubmitPrompt`), SubagentStop, PreCompact. Other events are reported unsupported.
- Copilot: SessionStart, SessionEnd, UserPromptSubmit, PreToolUse, PostToolUse, PostToolUseFailure, Stop, SubagentStop, ErrorOccurred, PreCompact. Other events are reported unsupported.
- Grok: canonical Claude names pass through.

## Non-goals

- Antigravity CLI, Gemini CLI, DeepSeek Harness (separate decisions).
- Cursor or Copilot plugin projection.
- Detecting Cursor's "third-party configs" setting. When it is on, Cursor also runs Claude Code hooks, so a hook synced to both runs twice; the docs say so.

## Acceptance tests

- Role renderers produce byte-stable output for all three, including tool mapping and model fallback (Claude tier names are not passed to other vendors).
- In a temp `HOME`, sync writes each surface to the paths above, a second sync is a no-op, and unrelated keys in the native files are preserved.
- With the config root at `~/.agents`, no skill mirror is created; with another root, skills are mirrored.
- Unsupported hook events are reported, not written.
- `tackroom hook list` reads the new native hook files; `hook remove` keeps their JSON valid.
- `go test ./...` passes.
- Real-binary checks where available, each reported as run or not run: Cursor on m1 (`cursor-agent`), Copilot CLI from npm in a temp prefix, Grok Build verified from source only unless installed.

## Risks

- Hook payloads differ per harness; a script written for Claude Code may need changes for Cursor's native camelCase payloads.
- `grok` and `copilot` are generic binary names; detection only checks `PATH`.

## Outcome / Deviations

- Shipped as specified. `readsAgentsSkillsRoot` (renamed from `openCodeReadsAgentsSkills`) now serves OpenCode, Cursor, Copilot CLI and Grok Build.
- Copilot MCP entries get `tools: ["*"]` only when missing, in a small patch wrapper; putting a slice in the target `defaults` would panic in `validateNativeDefaults`, which compares with `!=`.
- `hook list` reads every `*.json` file in `~/.copilot/hooks/` and `~/.grok/hooks/`, not only `tackroom.json`. Copilot's inline `hooks` in `settings.json` (JSONC) are not listed. The flat hook remover now writes JSON for `.json` files instead of YAML.
- The landing page adds GitHub Copilot and Grok marks from lobe-icons 1.95.1 (MIT, already credited) and marks Cursor as supported.
- Real-binary checks:
  - Cursor `cursor-agent` 2026.10.01 on m1 in a temp `HOME`: tackroom sync wrote `~/.cursor/{mcp.json,hooks.json,agents/reviewer.md}`; `cursor-agent mcp list` showed `local: not loaded (needs approval)`. It ran from a herdr pane, because over SSH `cursor-agent` refuses to start with a locked login keychain.
  - Copilot CLI 1.0.90 from npm in a scratch prefix and temp `HOME`: `copilot mcp get local` showed type local, tools `*`, source User; `copilot skill list` showed `sample` as a personal skill read from `~/.agents/skills`; `copilot instruction list` showed the `copilot-instructions.md` link. Roles and hooks have no offline listing command and were not exercised.
  - Grok Build was not installed; its adapter is verified against its source only.
