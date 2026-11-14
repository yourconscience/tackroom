# tackroom docs

Guides and reference for the tackroom CLI, in reading order. Start with setup; the rest stand alone.

## Start here

- [Setup](setup.md): first run, the review screen, multiple machines, settings and root instructions.
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
- [Terminal view](tui.md): `tackroom tui`, a read-only full-screen view of agents, the lock, scheduled jobs and doctor checks.

## Terminals

- [Herdr panel](herdr.md): Overview, Skills, Roles, MCP & hooks and Crons as a native Herdr side panel.
- [Tern](tern.md): a ready-made tackroom workspace in the Tern terminal.
- [Rex](rex.md): a ready-made tackroom workspace and palette actions in the Rex terminal.

## Background

- [Philosophy](philosophy.md): what tackroom believes and refuses to do.
- [Comparison](comparison.md): how tackroom differs from skillshare, rulesync, ruler and openskills.
- [Migrating from skillshare](skillshare-migration.md): map a skillshare setup onto tackroom.
- [Upgrading](upgrading.md): what changed in 2.1 and 2.0, and the rename from dotagents.
- [Troubleshooting](troubleshooting.md): conflicts, held and blocked items, hooks, Pi, periodic sync.
