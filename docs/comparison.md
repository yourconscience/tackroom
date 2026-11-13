# tackroom vs the field

tackroom is a user-level dotfiles manager for coding agents: it syncs skills, MCP servers, hooks, roles, root instructions, and memory across the harnesses you use, and pins + audits anything you install from strangers.

## Comparison

| | **tackroom** | [skillshare](https://github.com/runkids/skillshare) | [rulesync](https://github.com/dyoshikawa/rulesync) | [ruler](https://github.com/intellectronica/ruler) | [openskills](https://github.com/numman-ali/openskills) |
|---|---|---|---|---|---|
| Scope | user | user | project | project | user + project |
| Skills | ✓ | ✓ | ✓ | ~ | ✓ |
| MCP | ✓ | ✓ | ✓ | ✓ | — |
| Hooks | ✓ | ✓ | ✓ | — | — |
| Roles | ✓ native | ✓ | ✓ | ~ | — |
| Root instructions | ✓ | ✓ | ✓ | ✓ | — |
| Memory tier | ✓ (`rem`) | — | — | — | — |
| Pin + audit externals | ✓ lock + audit | ~ audit only | — | — | ~ no pin |
| Freshness hold (72h) | ✓ | — | — | — | — |
| Per-harness settings | ✓ | — | — | — | — |
| Periodic sync | ✓ | — | — | — | — |
| Desktop app | — (web view) | ✓ | — | — | — |
| Terminal panels | ✓ Herdr Crons panel · Tern workspace command · Rex doctor check only | — | — | — | — |
| Harness depth | deep | broad | broad | broad | narrow |

✓ full · ~ partial · — none

## skillshare vs tackroom

skillshare is the closest tool: same user-level sync, plus a desktop app and one simple `merge` mode. tackroom is the stricter, deeper version — pins, audits, freshness holds, per-harness settings, periodic sync, and a `rem` memory tier. skillshare audits but does not pin, so a source can change under you.

The rest is project-level or single-surface: rulesync and ruler generate config per repo; openskills ships skills only. tackroom is the only one that treats a personal setup as a dependency-managed dotfiles repo. [Migrate from skillshare →](skillshare-migration.md)
