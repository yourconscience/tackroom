# tackroom docs

Guides and reference for the tackroom CLI, in reading order. Philosophy says what tackroom is for; setup gets you running; the rest stand alone.

## Start here

- [Philosophy](philosophy.md): what tackroom believes and refuses to do.
- [Setup](setup.md): first run, `--review`, more machines with `--from` and `--overlay`, settings, root instructions, upgrades with the release notes, and uninstalling.
- [Commands](commands.md): every command by task, exit codes, config lookup order and output rules.
- [Harnesses](harnesses.md): what each of the 16 agents gets, the key that names it and where its files land.

## Guides

- [Skills](skills.md): write your own, add external sources, provenance, the 72 hour cooldown, the audit gate and publishing.
- [Roles](roles.md): the role file format, where each harness gets it, model tiers and overrides.
- [MCP servers](mcp.md): add, import and remove servers, remote servers, secrets and which harnesses get what.
- [Hooks](hooks.md): declare a hook, the events each harness takes, and list or remove native registrations.
- [Plugins](plugins.md): Claude Code plugins, agent-plugins-spec sources and the Claude Code and Pi panels.
- [Memory](memory.md): deliver a memory file to every harness, native memory settings, and separate tools such as rem.
- [Crons](crons.md): inventory every job on every host, run and pause launchd jobs, declare your own.
- [Web view](view.md): the loopback dashboard, its token, remote access and what it can change.
- [Terminals](terminals.md): the tmux, zellij, Tern and Rex workspaces, the Herdr panel and a cmux recipe.

## More

- [Comparison](comparison.md): how tackroom differs from skillshare, HarnessKit, dotagents, rulesync, ruler and openskills, and how to move from skillshare.
- [Troubleshooting](troubleshooting.md): conflicts, items in cooldown and blocked items, hooks, Pi, periodic sync.
