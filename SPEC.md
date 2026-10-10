# SPEC: a first run that succeeds

Status: requested 2026-10-10 ("please fix 1-3", after the 1.2.0 end-to-end test). PR only; merge needs separate approval. Earlier specs are in git history (`git log -- SPEC.md`).

## Goal

`tackroom setup` succeeds for someone who already uses two or more agents with hand-made config, never loses their content, and can be driven by an agent without prompts.

## Behavior

- **Existing instructions.** With no `~/.agents/AGENTS.md`, setup imports the detected agents' own instructions files (`~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md`, ...). One distinct file is copied as is; several are kept in full under a heading each. With none, no `AGENTS.md` is created and root instructions stay unmanaged.
- **Replace with backup.** A native file or skill folder that differs from the shared copy is a replaceable conflict. `sync --replace-conflicts` moves it to `$XDG_STATE_HOME/tackroom/backups/<UTC time>/` (default `~/.local/state/...`), keeping its path relative to home, then links the shared copy. Setup always does this after listing the paths and asking (`--yes` accepts). An identical copy is relinked without a backup.
- **Per-agent conflicts.** Plain `sync` skips only the agents with conflicts, syncs the rest, and exits 1 with the conflict list and the fix.
- **Unreadable TOML.** Codex and Grok `config.toml` are parsed in full; a parse error marks that agent "config unreadable" and the file is left untouched. Duplicate sections tackroom wrote for one server stay repairable.
- **Secrets.** The Codex reader reads `[mcp_servers.NAME.env]` tables, so import keeps the keys as `${KEY}` references. A `${KEY}` reference in the shared config never replaces a value an agent already has (JSON, TOML), so a working secret is never swapped for a placeholder.
- **Neutral starter.** No `AGENTS.md`, no third-party skills (mattpocock `grilling` removed), a config-root `.gitignore` instead of the repo's, the repo's `.agnix.toml` so a fresh `doctor` passes, and no memory tool sources with `--memory off`.
- **Agents.** `--yes` answers every prompt with its default without reading stdin. `status`, `sync` and `doctor` take `--json`. `<command> --help` exits 0. The tackroom skill documents the unattended recipe and exit codes.

## Out of scope

- External skill sources as a separate step (mattpocock, poteto, superpowers, ...): next PR.
- Importing an MCP server for every agent instead of its source agent: unchanged, needs a product decision.

## Acceptance tests

- A home with differing `CLAUDE.md` and Codex `AGENTS.md`, a skill that differs between Claude and Codex, and a Codex MCP server with a secret env table: `setup --yes --memory off` exits 0 with stdin attached, merges the instructions, backs up the three differing files, links everything, keeps the secret in Codex, and stores `${KEY}` in `tackroom.yaml`.
- A Claude conflict leaves Codex syncing; `--replace-conflicts` resolves it. A broken Codex TOML is reported and untouched.
- A fresh `doctor` passes. `go test ./...` passes.
