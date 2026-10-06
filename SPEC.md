# SPEC: OpenClaw and DeepSeek Harness targets

Status: requested 2026-10-05 ("support openclaw ... also support deepseek harness"). PR only; merge needs separate approval. Earlier specs are in git history (`git log -- SPEC.md`).

## Goal

`openclaw` and `dsh` are managed harnesses: detected on PATH, offered by setup, and synced for every surface each one can consume natively.

## Behavior

- Skills: both read `~/.agents/skills` natively (OpenClaw "personal agent skills", dsh `user-agents` root), so tackroom mirrors into `~/.openclaw/skills` or `~/.dsh/skills` only when the config root is elsewhere. OpenClaw drops `~/.agents/skills` when `$OPENCLAW_STATE_DIR` is set, so that case mirrors too. dsh honors `$DSH_AGENTS_HOME`.
- MCP, OpenClaw: stdio servers upsert into `mcp.servers.<name>` (`command`, `args`, `env`) in `openclaw.json` (`$OPENCLAW_CONFIG_PATH`, else `$OPENCLAW_STATE_DIR/openclaw.json`, else `~/.openclaw/openclaw.json`). Other keys stay.
- MCP, dsh: each server is an `@deepseek-ai/dsh-mcp-client` row in `$DSH_HOME/cordis.patch.yml` (default `~/.dsh`), the layer every profile applies. New servers get id `tackroom-mcp-<name>` in one `insert` op. A server whose `serverName` already has a row is updated in that row, keeping its id and other config, because dsh rejects two rows with one `serverName`. Other ops and rows, comments and `!!js` values survive. Names outside `[A-Za-z0-9_-]{1,32}` are rejected.
- Setup import: existing OpenClaw `mcp.servers` entries and dsh `dsh-mcp-client` rows are offered for import like other harnesses' servers.
- Root instructions: dsh links `~/.dsh/AGENTS.md` to the config root's `AGENTS.md`. OpenClaw gets none: its workspace `AGENTS.md` is the assistant persona.
- Roles and hooks: not synced for either. No native format matches tackroom's role files or script hooks.

## Acceptance tests

- A sync into a temp home writes both MCP configs, preserves unrelated content, links dsh root instructions, creates no skill mirror, reports synced, and a second sync changes nothing.
- A non-`~/.agents` config root, and OpenClaw with `$OPENCLAW_STATE_DIR`, mirror skills.
- Against real binaries (OpenClaw 2026.9.7, dsh 0.2.0-rc.2): `openclaw mcp list` shows the server, `openclaw skills list` shows a tackroom skill from `agents-skills-personal`, `dsh --dump-config` composes the tackroom row.
- `go test ./...` passes.
