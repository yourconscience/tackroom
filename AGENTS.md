# Project scope

This repository is the public `tackroom` CLI distribution. It must not contain a maintainer's personal skills, hooks, MCP servers, secrets, memory data, or machine-specific configuration.

The CLI manages a separate user-owned configuration root:

1. `--config /path/to/tackroom.yaml`, when supplied.
2. `$TACKROOM_HOME/tackroom.yaml`, when set.
3. `~/.agents/tackroom.yaml` otherwise.

Never restore project-directory config discovery or make the public checkout the user's canonical configuration.

# Architecture invariants

- tackroom syncs exactly five surfaces: skills, MCP servers, hooks, agent roles, and plugins.
- Plugins follow the agent-plugins-spec v1.0.0 format. For harnesses with native plugin systems (Codex), tackroom projects spec-format plugins into the native format. Claude Code plugins stay in Claude's native layout: the config root's `.claude-plugin/marketplace.json` is registered as a directory marketplace and its relative-path plugins are enabled in `~/.claude/settings.json`.
- `setup` owns first-run scaffolding, harness detection, optional copy/convert import, memory-tier selection, and the first sync. There is no separate `init` command.
- Native import is copy-only. Never move or delete the source content.
- Existing unrelated native harness configuration must remain untouched.
- External Git skills are commit-pinned in `tackroom.lock`; materialized files remain owned by that lock entry.
- Harness adapters must represent verified native capabilities. Do not invent unsupported MCP, hook, role, or skill surfaces.

# Public starter inventory

The distribution intentionally contains:

- `skills/tackroom/`
- `skills/grilling/`
- `agents/architect.md`
- `agents/builder.md`
- `agents/general.md`
- `agents/researcher.md`
- `agents/reviewer.md`
- `agents/tester.md`
- reusable memory infrastructure under `memory/`

New starter content requires an explicit product decision and an inventory test update.

# Development

- Go version and module layout are declared by `go.mod`. The memory tools under `memory/tools/` are packages of that root module; `setup` scaffolds a standalone `go.mod` beside each copied tool so a user-owned config root can build them without the source checkout.
- Install the development CLI with `go install ./cmd/tackroom`.
- Use focused package tests while iterating; run `go test ./...` before submission.
- Use Python 3's standard library for the dependency-free basic memory tier.
- Keep tests isolated with temporary homes and paths. Tests must never mutate live harness configuration.
- Prefer surgical changes and existing adapters over parallel implementations.
- For behavior changes, add a focused test for the user-visible contract and plausible failure modes.

# Documentation

Keep `README.md`, `skills/tackroom/SKILL.md`, CLI help, the public template, and release-site copy consistent. Examples must use conventional paths or environment variables, not one maintainer's filesystem.

# Git and review

- Preserve unrelated worktree changes.
- Use short, single-line commit messages without bot attribution trailers.
- Do not commit secrets, local overlays, external caches, generated memory, or native harness state.
- Pull requests must pass focused behavior tests and the full repository suite before merge.
