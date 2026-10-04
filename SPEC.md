# SPEC: `claude` as the Claude Code harness name

Status: requested 2026-10-04 ("i think claude is right name"). PR only; merge needs separate approval. Earlier specs are in git history (`git log -- SPEC.md`).

## Goal

The Claude Code harness is named `claude`, like `codex`, `copilot` and `grok`, instead of `claude-code`.

## Behavior

- `claude` is the canonical name in `tackroom.yaml` (`agents[].name`, `mcp_servers[].agents`, `hooks[].agents`), on the CLI (`--agents`, `mcp import`), in status output, and in configs written by `setup`.
- `claude-code` stays accepted everywhere a harness name is read and maps to `claude`. A config naming both is a duplicate-agent error.
- Role file names are not harness names and are not aliased.
- Role frontmatter already uses `claude:` for per-harness overrides; unchanged.

## Compatibility

- Old configs keep working without edits.
- A command that rewrites the config (setup, `mcp add`, the view UI) writes `claude`. A tackroom older than 1.2.0 treats `claude` as an unknown, skills-only harness, so machines sharing a config should upgrade together. The README says so.

## Acceptance tests

- A config written entirely with `claude-code` syncs Claude Code's MCP, hooks and roles, with `--agents claude-code`, and loads with canonical `claude`.
- `claude` and `claude-code` in one config fail as duplicates.
- `go test ./...` passes.
