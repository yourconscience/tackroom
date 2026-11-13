# MCP servers

Declare an MCP server once in `tackroom.yaml` and `sync` writes it into each harness's own config file. Stdio servers reach every harness; remote servers reach six of them.

```bash
tackroom mcp add docs --command npx --arg -y --arg @acme/docs-mcp --env 'DOCS_TOKEN=${DOCS_TOKEN}'
tackroom mcp add tracker --url https://mcp.acme.example/tracker --header 'Authorization: Bearer ${TRACKER_TOKEN}'
tackroom sync
```

## Add a server

`mcp add NAME` takes exactly one of `--command` (a stdio server) or `--url` (a remote streamable HTTP server). Adding a name that exists replaces its entry.

- `--arg VALUE` is one command argument; repeat it for each.
- `--env KEY=VALUE` sets an environment variable for a stdio server; repeatable.
- `--header 'Name: value'` sets a header for a remote server; repeatable.
- `--agents a,b` limits the server to those [harness keys](harnesses.md#config-keys-and-detection). Without it, the server targets every configured harness that supports MCP.
- `--config PATH` edits another config file.

The command writes `mcp_servers:` in the shared `tackroom.yaml` and prints `next: tackroom sync`. It changes no harness file itself. You can write the entry by hand instead:

```yaml
mcp_servers:
  - name: docs
    enabled: true
    command: npx
    args: [-y, "@acme/docs-mcp"]
    env:
      DOCS_TOKEN: ${DOCS_TOKEN}
    agents: [claude, codex, gemini]
  - name: tracker
    enabled: true
    url: https://mcp.acme.example/tracker
    headers:
      Authorization: Bearer ${TRACKER_TOKEN}
    agents: [claude, codex, cursor]
```

A server has `command` or `url`, never both. `args` and `env` belong to stdio servers and `headers` to remote ones; the config is rejected otherwise. A `url` must start with `http://` or `https://`. With `enabled: false`, nothing is written. If `agents` is empty, the server goes to every enabled harness that can take it.

`tackroom mcp list` prints the servers in the shared `tackroom.yaml`: enabled or disabled, command or URL, target harnesses, and the names of its env keys and headers, never their values.

## Import a server a harness already has

```bash
tackroom mcp import claude search --agents claude,codex
```

`import` reads the entry named `search` from Claude Code's own config and writes it into `tackroom.yaml`. Without `--agents` it targets every configured harness with MCP. The original stays where it is.

Secrets do not come along. Each env value that is not already a `${VAR}` reference becomes `${KEY}`, and a header that looks secret (`Authorization`, or a name with token, key, secret or cookie in it) becomes a reference such as `Bearer ${WIKI_AUTHORIZATION}`. The command warns which ones it replaced. The source harness keeps its real values.

## Remote servers

`mcp add --url` writes the server to these harnesses, each in its own format:

| Harness | What is written |
|---|---|
| Claude Code | `type: http`, `url` and `headers` |
| Codex | `url`, with headers split as below |
| Pi | `url` and `headers` |
| Cursor | `url` and `headers`, with `${NAME}` rewritten as `${env:NAME}` |
| Gemini CLI | `httpUrl` and `headers` |
| Grok Build | `url` and `headers` |

Factory Droid, OMP, OpenCode, Qwen Code, GitHub Copilot CLI, DeepSeek Harness, Antigravity CLI, Kimi Code and Amp get stdio servers only. A remote server is skipped for them, and `mcp add` says which harnesses it will not reach.

Codex keeps secrets out of `config.toml`. `Authorization: Bearer ${NAME}` becomes `bearer_token_env_var = "NAME"`, a header whose whole value is `${NAME}` goes to `env_http_headers`, and a literal header goes to `http_headers`.

Any other mix of text and a variable, such as `Token ${NAME}`, is rejected when the config loads. Leave Codex out of that server's `agents` list or change the header.

## Secrets

Write `${NAME}` references in `env` and `headers`, not values. tackroom copies the reference into the harness config as written (Cursor and Codex as shown above); it does not read your environment. Set the variable where the harness starts, or put the real value in the harness's own config.

A real value already in a harness config is never replaced by a reference. A sync that finds `DOCS_TOKEN` set in a harness leaves it, and treats the entry as in sync.

`mcp add` warns when `--env` holds a literal value or an `Authorization` header has no `${VAR}`, because `tackroom.yaml` is usually a repo you push. `settings:` blocks work differently: those `${VAR}` references expand at sync time ([setup.md](setup.md#per-harness-settings)).

## Remove a server

```bash
tackroom mcp remove docs
tackroom sync
```

`mcp remove` deletes the entry from `tackroom.yaml` and touches no harness file; it exits 1 if the name is unknown. Until the next `sync`, `tackroom status` shows `mcp stale: docs` under each harness that still has it, and exits 1. `sync` removes the server from:

- each harness where tackroom wrote it earlier. tackroom records that per machine in `mcp-servers.json` under `$XDG_STATE_HOME/tackroom` (default `~/.local/state/tackroom`);
- each harness whose entry still matches the definition at the time of `mcp remove`, which covers servers written before the record existed.

Servers you configured by hand stay. The same cleanup runs when you set `enabled: false` or drop a harness from `agents:`.

Before `sync` removes a server it copies that harness config to `backups/<time>/` in the state folder, and `tackroom restore` puts it back ([troubleshooting.md](troubleshooting.md#conflicts)). `setup` asks before it removes anything; a plain `sync` does not.

## Native shape

Where each harness keeps its servers is in [harnesses.md](harnesses.md#where-each-surface-lands). A few harnesses need extra fields, which tackroom adds:

- Claude Code gets `"type": "stdio"` on stdio entries, Factory Droid `"type": "stdio"` and `"disabled": false`, and GitHub Copilot CLI `"type": "local"` and `"tools": ["*"]` when the entry has no `tools`.
- OpenCode gets a `mcp` block with `type: local`, a single `command` array, `environment` and `enabled: true`.
- DeepSeek Harness gets one `tackroom-mcp-<name>` row per server in `~/.dsh/cordis.patch.yml`, for every profile.
- Codex and Grok Build get `[mcp_servers.<name>]` tables in TOML. Keys tackroom does not manage in a Codex table, such as `startup_timeout_sec`, are kept.

## Freshness hold and plugins

A server command with an exact npm or PyPI version, such as `npx -y @acme/docs-mcp@1.2.0`, is held until the version is 72 hours old. While it is held, `sync` keeps the entry a harness already has and writes nothing new ([skills.md](skills.md#the-72-hour-hold)).

MCP servers can also come from an agent-plugins-spec source with `mcp: true`; tackroom writes the stdio ones, and Codex installs the source as a native plugin instead ([plugins.md](plugins.md#agent-plugins-sources)).
