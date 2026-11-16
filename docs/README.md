# tackroom docs

Guides and reference for the tackroom CLI, in reading order. Philosophy says what tackroom is for; setup gets you running; the rest stand alone.

## Start here

- [Philosophy](philosophy.md): what tackroom believes and refuses to do.
- [Setup](setup.md): first run, `--review`, more machines with `--from` and `--overlay`, settings and root instructions.
- [Commands](commands.md): every command by task, exit codes, config lookup order and output rules.
- [Harnesses](harnesses.md): what each of the 15 agents gets, the key that names it and where its files land.

## Guides

- [Skills](skills.md): write your own, add external sources, provenance, the 72 hour hold, the audit gate and publishing.
- [Roles](roles.md): the role file format, where each harness gets it, model tiers and overrides.
- [MCP servers](mcp.md): add, import and remove servers, remote servers, secrets and which harnesses get what.
- [Hooks](hooks.md): declare a hook, the events each harness takes, and list or remove native registrations.
- [Plugins](plugins.md): Claude Code plugins, agent-plugins-spec sources and the Claude Code and Pi panels.
- [Memory](memory.md): the three tiers, the `rem` capture and review workflow, and why it is review-first.
- [Scheduled jobs](crons.md): inventory every job on every host, run and pause launchd jobs, declare your own.
- [Web view](view.md): the loopback dashboard, its token, remote access and what it can change.
- [Terminals](terminals.md): the tmux, zellij, Tern and Rex workspaces, the Herdr panel, a cmux recipe, and the deprecated `tackroom tui`.

## Background

- [Comparison](comparison.md): how tackroom differs from skillshare, rulesync, ruler and openskills.
- [Migrating from skillshare](skillshare-migration.md): map a skillshare setup onto tackroom.
- [Upgrading](upgrading.md): what changed in each release since 2.0, and the rename from dotagents.
- [Troubleshooting](troubleshooting.md): conflicts, held and blocked items, hooks, Pi, periodic sync.
