# Plugins

tackroom handles two kinds of plugin: Claude Code plugins, which stay in Claude's native layout and load in place from your config root, and agent-plugins-spec sources, whose skills and MCP servers sync like any other source's. It also ships its own sync and jobs panel as a Claude Code plugin and a Pi package.

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

The pin, the 72 hour hold and the audit gate apply as for any external source ([skills.md](skills.md#the-72-hour-hold)). The audit names the source `plugin:<name>`, also scans its MCP servers, and `tackroom.lock` records the plugin name and version. Servers a plugin adds are written and listed like your own ([mcp.md](mcp.md)).

tackroom does not write plugins into Codex today. Codex installs its own with `codex plugin add`.

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
