# Plugins

tackroom handles two kinds of plugin: Claude Code plugins, which stay in Claude's native layout and load in place from your config root, and agent-plugins-spec sources, whose skills and MCP servers sync like any other source's, or reach Codex as a native plugin. It also ships its own sync and jobs panel as a Claude Code plugin and a Pi package.

```bash
tackroom sync        # registers the marketplace and enables its plugins
tackroom status      # the plugins column counts what is enabled
```

## Claude Code plugins

A Claude Code plugin carries what the other surfaces cannot: mods, workflows, monitors, output styles, LSP servers and `bin/` tools. Put each plugin in its native layout under the config root and list it in `.claude-plugin/marketplace.json` there:

```json
{
  "name": "my-agents",
  "owner": { "name": "you" },
  "plugins": [{ "name": "my-mods", "source": "./plugins/my-mods" }]
}
```

`sync` writes two things into `~/.claude/settings.json`. It registers the config root under `extraKnownMarketplaces` as a `directory` source, and it sets `enabledPlugins["<plugin>@<marketplace>"]` to `true` for each relative-path entry.

Claude Code reads a directory marketplace in place, so there is no install step and no plugin cache. Edit a plugin, run `/reload-plugins`, and the change is live.

What `sync` leaves alone or cleans up:

- A plugin you turn off in `/plugin` stays off, also across a marketplace rename.
- An entry with `defaultEnabled: false`, in the marketplace or in the plugin's own `.claude-plugin/plugin.json`, is left for you to enable. `status` lists it as `plugins off`.
- An entry whose `source` is the config root itself (`"."`) or lives elsewhere, such as a GitHub repo, is skipped.
- A bare plugin name resolves under `metadata.pluginRoot`.
- Enable keys for plugins the marketplace no longer lists are removed, and deleting the marketplace file removes the registration.
- A marketplace name already registered with another source, or a malformed `marketplace.json` or settings key, is reported as a conflict and nothing is written.

`tackroom doctor` runs `claude plugin validate` on the config root when Claude Code is detected, and skips the check when there is no marketplace file.

## Agent plugins sources

A source that follows [agent-plugins-spec](https://agent-plugins.org) is added like any external skill source. Its skills reach every harness, and `mcp: true` registers the MCP servers from its `mcp.json`.

```yaml
external_skills:
  - name: acme-tools
    url: https://github.com/acme/agent-skills
    branch: main
    mcp: true                      # register the plugin's MCP servers
    mcp_agents: [claude, codex]    # optional; default: every harness with MCP
    exclude_agents: [claude]       # optional: no skills for these harnesses
```

`tackroom skill sources add acme/agent-skills` writes the pinned entry for you ([skills.md](skills.md#add-an-external-source)). The source needs a `plugin.json` at its root.

The manifest names the schema `https://agent-plugins.org/schemas/1.0.0/plugin.schema.json` and a `name` of lowercase letters, digits, `.` and `-`, at most 64 characters. A missing or invalid manifest means `mcp: true` is ignored with a warning.

In `mcp.json`, tackroom takes stdio servers only:

- `streamable-http` and `sse` servers, and servers with `cwd`, are skipped with a warning.
- A server name matches `[a-z0-9][a-z0-9_-]*`. The command is a bare name or a `./` path inside the plugin.
- `${PLUGIN_ROOT}` and `${PLUGIN_DATA}` expand in `args` and `env`, and both are also set as environment variables. The data folder is `~/.agents/external-data/<source name>`.
- A server with the same name as one in your own `mcp_servers:` is skipped, and so are servers that two plugin sources both declare.

The pin, the 72 hour hold and the audit gate apply as for any external source ([skills.md](skills.md#the-72-hour-hold)). The audit names the source `plugin:<name>`, also scans its stdio MCP servers and the raw `mcp.json` (every transport), `plugin.json`, `.codex-plugin/plugin.json` and `hooks/hooks.json`, and `tackroom.lock` records the plugin name and version. Servers a plugin adds are written and listed like your own ([mcp.md](mcp.md)).

### Codex plugins

Codex loads a plugin from its own cache, so tackroom drives Codex's CLI (`codex plugin marketplace add`, `codex plugin add`, `codex plugin remove`) instead of writing that cache. A source is installed into Codex as a native plugin when all of these hold:

- Codex is detected and the source sets `mcp: true`.
- The cached source has a valid `plugin.json` at its root.
- Codex is an MCP target (`mcp_agents` is empty or lists `codex`) and `exclude_agents` does not list `codex`.
- The source keeps the default skill layout: no `skills:`, `skill_dir:` or `skill_dirs:`, because a plugin carries all of its `skills/` folder.
- The pin is not held or blocked.

For such a source Codex gets the skills and every MCP server, `streamable-http` ones included, from the plugin alone. `sync` links no skills and writes no `[mcp_servers]` rows for it into Codex. Other harnesses are unchanged, and a source without `mcp: true` keeps its skill links.

`sync` writes `~/.agents/external/.agents/plugins/marketplace.json`, a generated marketplace named `tackroom` with one entry per plugin, and runs `codex plugin marketplace add ~/.agents/external` once. It then runs `codex plugin add <name>@tackroom` when the plugin is not installed or Codex's copy differs from the cached source. It never writes Codex's plugin cache and never touches your own `~/.agents/plugins/marketplace.json`.

```bash
tackroom status      # plugins (1)   acme@tackroom
```

- **Turn one off.** Set `enabled = false` under `[plugins."<name>@tackroom"]` in `~/.codex/config.toml`. `sync` leaves it alone and `status` lists it as `plugins off`. Codex then has none of that source's skills or servers. To stop installing it for good, add `codex` to the source's `exclude_agents`, drop `codex` from `mcp_agents`, or set `mcp: false`.
- **Remove one.** Deleting the source, or any setting above, makes the next `sync` run `codex plugin remove`, and `codex plugin marketplace remove tackroom` with the last plugin. The generated `marketplace.json` goes too. Only plugins ending in `@tackroom` are touched; plugins from other marketplaces never are.
- **Uninstalled in Codex.** A plugin you remove with `codex plugin remove` comes back on the next `sync`, because the config declares it. Use `enabled = false` instead.
- **Same plugin name in two sources.** Neither is installed into Codex, with a warning.
- **A `tackroom` marketplace from another source.** Reported as a conflict, and nothing is written for plugins.
- **Failures.** If `codex` is not on `PATH` or a `codex plugin` call fails, `sync` reports an error for the Codex plugin surface; the other surfaces of that sync still apply. `status`, `sync --dry-run`, `doctor` and the views never run `codex`: they read `~/.codex/config.toml`, the generated marketplace and Codex's plugin cache.

`sync` backs up `~/.codex/config.toml` before the first `codex` call of a run, and runs `codex` with `CODEX_HOME` set to `~/.codex`. Tested with codex-cli 0.159.2; tackroom does not check the version. Whether a Codex session loads the plugin's skills and tools is Codex's behavior, not checked by `sync`.

## tackroom in Claude Code and Pi

The sync status and scheduled jobs from [Scheduled jobs](crons.md) are also available inside Claude Code and Pi, as a plugin and a package from the [`claude/`](https://github.com/yourconscience/tackroom/tree/main/claude) and [`pi/`](https://github.com/yourconscience/tackroom/tree/main/pi) folders of the repository.

```bash
claude plugin install tackroom --marketplace yourconscience/tackroom
pi install git:github.com/yourconscience/tackroom@v2.0.0
```

Both run `tackroom` from your `PATH`, show which harnesses are out of sync, list the jobs from `tackroom cron collect`, and act through `tackroom cron run|pause|resume`, the same verbs as the web view and the [Herdr panel](herdr.md).

- **Claude Code.** `/tackroom` opens a pane with the sync status, health counts per host, the jobs that need attention and every job per host. A job opens into its details with Run now, Pause and Resume. The plugin takes two options in `/config`: `tackroom`, the program to run, and `config`, the full path of a config file other than the default.
- **Pi.** `/tackroom` lists the jobs with the sync status in the title; picking one shows its details and the same actions. Pi uses `tackroom` from the `PATH` and follows `$TACKROOM_HOME`.

On a Pi entry with a `packages` list in `tackroom.yaml`, add the `git:` line to that list instead of running `pi install`, because `sync` writes the list as a whole ([harnesses.md](harnesses.md#pi)).
