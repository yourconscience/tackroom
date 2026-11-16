# tackroom vs the field

tackroom is a user-level dotfiles manager for coding agents. It syncs skills, MCP servers, hooks, roles and root instructions across the harnesses you use, and pins and audits what you install from strangers.

## Comparison

| | **tackroom** | [skillshare](https://github.com/runkids/skillshare) | [HarnessKit](https://github.com/RealZST/HarnessKit) | [rulesync](https://github.com/dyoshikawa/rulesync) | [ruler](https://github.com/intellectronica/ruler) | [openskills](https://github.com/numman-ali/openskills) |
|---|---|---|---|---|---|---|
| Scope | user | user, and per project | user, and per project | project, and a global mode | project | user + project |
| Skills | ✓ | ✓ | ✓ | ✓ | ~ | ✓ |
| MCP | ✓ | ✓ | ✓ | ✓ | ✓ | - |
| Hooks | ✓ | ✓ | ~ 10 of 17 agents | ✓ | - | - |
| Roles | ✓ native | ✓ | - lists, does not write | ✓ | ~ | - |
| Root instructions | ✓ | ✓ | - lists, does not write | ✓ | ✓ | - |
| Memory | ~ delivers files you list | ✓ shared Markdown notes | ~ lists files; a Kit can carry them | - | - | - |
| Pin + audit externals | ✓ lock + audit | ~ audit; commit pins in project mode | ~ trust score; no lockfile | - | - | - |
| Freshness hold (72h) | ✓ | - | - | - | - | - |
| Per-harness settings | ✓ | - | - | - | - | - |
| Periodic sync | ✓ | - | - | - | - | - |
| Desktop app | - (web view) | ✓ | ✓ | - | - | - |
| Terminal panels | ✓ Herdr panel · Tern crons panel and workspace command · Rex workspace command and palette actions | ✓ Herdr panel | - | - | - | - |
| Harness depth | deep | broad | broad | broad | broad | narrow |

`✓` full, `~` partial, `-` none. The other tools' cells follow their public READMEs and documentation as read on 2026-11-13, HarnessKit's its [docs](https://harness-kit.dev/docs/) as read on 2026-11-16, and skillshare's memory cell its [v0.24.0 release notes](https://github.com/runkids/skillshare/releases/tag/v0.24.0) as read on 2026-11-16; check them before you rely on one.

## skillshare vs tackroom

skillshare is the closest tool. It syncs skills, agents, MCP servers, hooks and plugins at user level, with a desktop app, a web dashboard and a merge, copy or symlink mode per target. It audits skills against a configurable block threshold.

For pins, it writes `.skillshare/skills.lock.json` in project mode ([how the lockfile works](https://skillshare.runkids.cc/docs/understand/project-skills#lockfile)). In its global mode it records sources and file hashes in a local `.metadata.json`, which its documentation calls personal machine state.

tackroom goes further in three places:

- external skills are pinned in `tackroom.lock`, which lives in your config repo and is shared by every machine;
- new pins and new exact package versions are held for 72 hours, and a held item is skipped until you release it;
- each harness accepts declared settings keys, and a periodic job keeps machines in sync.

skillshare's documentation describes none of these. See [skillshare-migration.md](skillshare-migration.md) to move over.

Memory goes the other way. Since v0.24.0 (2026-10-03), skillshare keeps shared Markdown notes in `extras/memory`, with commands and a dashboard page to browse and search them, and you connect the agents that read them. tackroom runs no memory system: it delivers the files you list under `instructions:`, and capture is left to a separate tool such as [rem](https://github.com/yourconscience/rem). See [memory.md](memory.md).

## HarnessKit vs tackroom

[HarnessKit](https://harness-kit.dev/) is an app: a desktop app, a web UI and an `hk` CLI. It shows the extensions, rules, memory and subagent files of 17 agents in one place, and copies skills, MCP servers and some hooks from one agent to another. It adds a marketplace, a 0 to 100 trust score from 20 static rules, and per-project scope. tackroom has none of those in that form.

It changes each agent's files in place and keeps no config repo of its own. Its documentation describes no lockfile, no hold on new versions, no scheduled sync and no way to sync two machines. A [Kit](https://harness-kit.dev/docs/guides/kits) carries a setup instead: a snapshot of skills, MCP servers, rules and memory files exported as a zip, applied to projects only. The [`hk` CLI](https://harness-kit.dev/docs/reference/cli) lists, audits, enables and disables, and does not install.

tackroom is the other shape: one config repo you edit and commit, written into 15 agents, with pins in a lockfile, a 72 hour hold, an audit gate, per-harness settings, roles and root instructions it writes, and a periodic job that keeps machines in sync. The two can run on one machine; HarnessKit then shows what tackroom wrote.

## The rest

rulesync and ruler generate config per repository; rulesync also has a `--global` mode, and ruler a global config used when a repository has no `.ruler/` folder. openskills installs skills only. None of them treats a personal setup as a dependency-managed dotfiles repo. For what tackroom refuses to do, read [philosophy.md](philosophy.md).
