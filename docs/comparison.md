# tackroom vs the field

tackroom is a user-level dotfiles manager for coding agents. It syncs skills, MCP servers, hooks, roles and root instructions across the harnesses you use, and pins and audits what you install from strangers.

## Comparison

| | **tackroom** | [skillshare](https://github.com/runkids/skillshare) | [rulesync](https://github.com/dyoshikawa/rulesync) | [ruler](https://github.com/intellectronica/ruler) | [openskills](https://github.com/numman-ali/openskills) |
|---|---|---|---|---|---|
| Scope | user | user, and per project | project, and a global mode | project | user + project |
| Skills | ✓ | ✓ | ✓ | ~ | ✓ |
| MCP | ✓ | ✓ | ✓ | ✓ | - |
| Hooks | ✓ | ✓ | ✓ | - | - |
| Roles | ✓ native | ✓ | ✓ | ~ | - |
| Root instructions | ✓ | ✓ | ✓ | ✓ | - |
| Memory tier | ✓ (`rem`) | ~ shared notes | - | - | - |
| Pin + audit externals | ✓ lock + audit | ~ audit; commit pins in project mode | - | - | - |
| Freshness hold (72h) | ✓ | - | - | - | - |
| Per-harness settings | ✓ | - | - | - | - |
| Periodic sync | ✓ | - | - | - | - |
| Desktop app | - (web view) | ✓ | - | - | - |
| Terminal panels | ✓ Herdr Crons panel · Tern crons panel and workspace command · Rex doctor check only | ✓ Herdr panel | - | - | - |
| Harness depth | deep | broad | broad | broad | narrow |

`✓` full, `~` partial, `-` none. The other tools' cells follow their public READMEs and documentation as read on 2026-11-13; check them before you rely on one.

## skillshare vs tackroom

skillshare is the closest tool. It syncs skills, agents, MCP servers, hooks and plugins at user level, with a desktop app, a web dashboard and a merge, copy or symlink mode per target. It audits skills against a configurable block threshold.

For pins, it writes `.skillshare/skills.lock.json` in project mode ([how the lockfile works](https://skillshare.runkids.cc/docs/understand/project-skills#lockfile)). In its global mode it records sources and file hashes in a local `.metadata.json`, which its documentation calls personal machine state.

tackroom goes further in four places:

- external skills are pinned in `tackroom.lock`, which lives in your config repo and is shared by every machine;
- new pins and new exact package versions are held for 72 hours, and a held item is skipped until you release it;
- each harness accepts declared settings keys, and a periodic job keeps machines in sync;
- a review-first memory tier (`rem`) captures facts without promoting them.

skillshare's documentation describes none of these. See [skillshare-migration.md](skillshare-migration.md) to move over.

## The rest

rulesync and ruler generate config per repository; rulesync also has a `--global` mode, and ruler a global config used when a repository has no `.ruler/` folder. openskills installs skills only. None of them treats a personal setup as a dependency-managed dotfiles repo. For what tackroom refuses to do, read [philosophy.md](philosophy.md).
