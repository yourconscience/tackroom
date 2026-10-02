# SPEC: tackroom v1.0

Status: scope decided 2026-10-02. Items marked **[decide]** are still open.

## Goal

Ship v1.0 under the tackroom name. One private `~/.agents` repo gets rendered natively into the coding agents most developers use, and a first-time user gets a working setup in one `tackroom setup` run without editing YAML. tackroom does one job; the memory layer becomes a separate tool. The release ships with a redesigned landing page and a short launch video.

## Evidence (measured 2026-10-02)

Adoption proxy: npm downloads in the last month. Cursor and Droid ship mostly outside npm, so their numbers understate use.

| Harness | npm/month | Reads `~/.agents/skills` | Roles | MCP | Hooks | tackroom |
|---|---|---|---|---|---|---|
| Codex | 89.7M | yes ([docs](https://learn.chatgpt.com/docs/build-skills)) | `~/.codex/agents/*.toml` | yes | yes | managed |
| Claude Code | 55.9M | no, `~/.claude/skills` only ([docs](https://code.claude.com/docs/en/skills)) | `~/.claude/agents` | yes | yes | managed |
| OpenCode | 9.8M | yes | yes | yes | JS plugin only | managed |
| GitHub Copilot CLI | 7.9M | no, `~/.copilot/skills` ([docs](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-config-dir-reference)) | `~/.copilot/agents/*.agent.md` | `~/.copilot/mcp-config.json` | `~/.copilot/hooks/` | not in 1.0 |
| Pi | 3.3M | yes | via pi-subagents | via pi-mcp-adapter | no | managed |
| Gemini CLI | 1.6M | yes ([docs](https://geminicli.com/docs/cli/skills/)) | `~/.gemini/agents/*.md` | `settings.json` | `settings.json` | not in 1.0 |
| Cursor | n/a (own installer) | yes, plus `~/.claude/skills` and `~/.codex/skills` ([docs](https://cursor.com/docs/skills)) | `~/.cursor/agents/*.md` | `~/.cursor/mcp.json` | `~/.cursor/hooks.json` with `sessionStart`/`sessionEnd`/`stop` ([docs](https://cursor.com/docs/hooks)) | **new in 1.0** |
| Qwen Code | 283k | config-driven | yes | yes | yes | managed |
| Amp | 84k | config-driven | no | yes | no | managed |
| Factory Droid | 43k | no | yes | yes | yes | managed |
| Hermes, OMP | small | config / yes | partial | yes | partial | managed |

Five of the top seven harnesses read `~/.agents/skills` themselves, so skill mirroring now matters mainly for Claude Code. MCP, hooks, roles and root instructions still have no shared convention, and those are tackroom's core value.

## Scope

### Harnesses

- **Tier 1** (on the landing page, end-to-end verified each release): Claude Code, Codex, Cursor (new), OpenCode, Pi.
- **Tier 2** (kept, unit-tested, verified on demand): Droid, Qwen Code, Amp, OMP, Hermes.
- **Cursor adapter:** no skill mirror when the config root is `~/.agents`, because Cursor reads `~/.agents/skills` itself. With `--config` or `TACKROOM_HOME` pointing elsewhere, mirror skills into `~/.cursor/skills`, the same way the OpenCode adapter handles a non-default root. It covers roles in `~/.cursor/agents/*.md`, MCP in `~/.cursor/mcp.json`, and hooks in `~/.cursor/hooks.json` (version 1 format). Each surface is enabled only after verification against the real `cursor-agent` in a temp `HOME`.

### Features

| Area | 1.0 |
|---|---|
| `setup`, `status`, `sync`, `doctor`, `config validate/print` | keep |
| External skills (pin, audit, `skill update`) | keep |
| `mcp`, `hook`, `skill new/list/info`, `cron`/`pull` | keep |
| Plugin sync (agent-plugins-spec skills and MCP, consumed by Qwen Code and Pi) | keep unchanged. Codex native projection is still only planned (README, docs/skills.md) and is not in 1.0; AGENTS.md's invariant gets reworded to say so. Cursor plugins are out of 1.0 |
| `view` web UI | keep |
| `publish` to the OpenAI Skills API | keep |
| `config` TUI | cut |
| `inspect` (HarnessKit) and `sessions` (AgentsView) launchers | cut; README lists both as companion tools |
| Compatibility aliases (`render`, `external`, `skillify`, `promote`, `dogfood`, `sync pull`, `sync render`) | cut |
| Memory layer | moves to its own repo (below) |

### Memory split

- New public repo for the memory tools: hook scripts, `lib/`, `rem`, `knowledge-sync`, and the memsearch tier. It has its own release and install (brew tap and `go install`), and it works without tackroom.
- tackroom ships no memory code. Gone: the memory tiers inside tackroom, the `memsearch` command, the build of `rem` and `knowledge-sync`, and `memory/` in the starter inventory.
- Integration is just hooks. The memory tool documents its hook commands, and users list them under `hooks:` in `tackroom.yaml` like any other hook.
- `setup` keeps owning the first-run memory choice (AGENTS.md): `--memory off|on`. With `on`, setup adds the memory tool's hook entries when the tool is on `PATH`, and otherwise prints its install command. The memsearch tier is configured inside the memory tool.
- Existing roots: tackroom stops managing `memory/` (its starter manifest entries are dropped, files stay in place). The maintainer's root then switches its hooks to the installed memory tool.
- **[decide]** Repo and package name. `rem` is taken in Homebrew core (kykim/rem, a Reminders CLI), so the package needs another name even if the `rem` command stays.

### Smooth first run

1. Install with one command: `brew install yourconscience/tap/tackroom`, the curl script, or `npx tackroom setup` from the unscoped npm package `tackroom`, which replaces `@your_conscience/dotagents`.
2. `setup` detects tier 1 and 2 harnesses, offers per-item copy import of existing skills, MCP servers and roles, initializes `~/.agents` as git, and runs the first sync.
3. Coexistence: if `~/.agents` already holds `agents.toml` (Sentry dotagents) or another manager's lock, `setup` stops and explains, and `doctor` warns.
4. Duplicate visibility, measured 2026-10-02 on m1 (Cursor CLI 2026.10.01, `ask` mode, empty workspace). Cursor listed 55 skills with no repeated names. Each tackroom skill appeared once, resolved from `~/.claude/skills`, so Cursor de-duplicates by name. `grill-me` was hidden because it sets `disable-model-invocation: true`. Cursor also showed skills that exist only for other harnesses: Codex's `~/.codex/skills` and Claude Code's account and plugin skills. The adapter therefore needs no skill mirror, and `doctor` should list that cross-harness spillover rather than duplicates.
5. Every `doctor` failure names a next step. Checks with one unambiguous fix (missing sync, stale lock pin, legacy file names, missing binary) print the exact command.

### Release deliverables

- Landing page at `yourconscience.github.io/tackroom`, redesigned for this scope.
- A launch video, 15 to 25 s, made with the brag skill.
- A README hero rewritten for this scope.
- Release mechanics: an npm trusted publisher for `tackroom` (maintainer action), a deprecation notice on `@your_conscience/dotagents`, removal of `Formula/dotagents.rb` from the tap, and `scripts/release.sh v1.0.0` after explicit approval.

## Non-goals

- Codex native plugin projection (still planned).
- GitHub Copilot CLI and Gemini CLI adapters (post-1.0 backlog).
- Project-level config generation (rulesync, ruler territory).
- Windows support.

## Acceptance tests

- For each tier 1 harness, in a temp `HOME`: `setup` detects it, and `sync` writes the surfaces that harness supports natively (per the evidence table) to the documented paths. Skills are mirrored only where the harness doesn't read the config root's skills. A second `sync` is a no-op, and unrelated native config is byte-identical. Hooks are required only where native support is verified (not Pi, and OpenCode only through its JS plugin).
- Plugin projection for Codex, Qwen Code and Pi behaves as before 1.0 (existing tests keep passing).
- Cursor with a non-default config root: skills appear under `~/.cursor/skills`.
- On a machine with only Claude Code and Cursor installed, `setup --yes` finishes and `status` shows both synced, with no YAML edits.
- The tackroom binary has no memory code, and the memory repo's tests pass on their own.
- In a temp `HOME`, `setup --memory on` with a stub memory tool on `PATH` registers its hooks, and running the registered session-end hook writes a digest under a temp `KNOWLEDGE_DIR`.
- `go test ./...`, the npm wrapper test and the release script test pass.
- The landing page renders at phone width in both themes. The video runs 15 to 25 s at -16 LUFS integrated.

## Risks / open questions

- `cursor-agent` is installed and logged in on m1. `cursor-agent mcp list` reads `~/.cursor/mcp.json` and gives an offline check for the MCP surface.
- npm trusted publishing for a package that doesn't exist yet may need one manual first publish. This needs measurement.
- The memory split touches live capture hooks. Maintainer rollout, kept out of the acceptance tests: switch one machine, check a digest lands, then switch the other.

## Codebase notes

- Adapters live in `internal/app/harness.go`, `mcp.go`, `hooks.go`, `agents.go`, `internal/agentrole/`. Follow the OpenCode and Qwen adapters (about 150 to 250 lines each).
- Memory code to move: `memory/`, `internal/app/memsearch.go`, `memory_tools.go`, and the memory tiers in `setup.go`.
- `refuseLegacyRoot` (from the rename) stays until 1.1.

## Outcome / Deviations

- v1.0.0 shipped on 2026-10-02 with the rename, the trims and the unscoped npm package. The maintainer asked to release right away, so the Cursor adapter and the memory split move to 1.1.
- The landing page and the launch video shipped with 1.0. Cursor appears on the page with native skills and planned roles, MCP and hooks.
- The npm job failed on the first run (`403 OIDC permission denied`) and waits on the trusted-publisher settings on npmjs.com. Homebrew and the GitHub release published normally.
