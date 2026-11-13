# How tackroom differs

Different tools solve different parts of agent-config fragmentation.

| Tool | Scope | What it does |
|---|---|---|
| [rulesync](https://github.com/dyoshikawa/rulesync), [ruler](https://github.com/intellectronica/ruler) | per-project | Generate agent config inside a repo |
| [openskills](https://github.com/numman-ali/openskills) | user-level | Install skills |
| [dot-agents](https://github.com/dot-agents/dot-agents) | user-level | Symlink rules and MCP config once |
| [dotagent](https://github.com/johnlindquist/dotagent) | one-shot | Convert between agent config formats |
| [skillshare](https://github.com/runkids/skillshare) | user-level | Sync skills, roles, hooks, and MCP via a config, with a desktop app |
| `AGENTS.md` | per-project | Project-level agent instructions |
| **tackroom** | user-level | Sync skills, MCP, hooks, roles, root instructions, and memory across harnesses continuously |

## Detailed comparison

tackroom' own harness coverage is listed below; competitor breadth is described qualitatively, since their exact tool counts drift over time.

| | tackroom | [rulesync](https://github.com/dyoshikawa/rulesync) | [ruler](https://github.com/intellectronica/ruler) | [openskills](https://github.com/numman-ali/openskills) |
|---|---|---|---|---|
| Scope | user-level | project-level | project-level | user + project |
| Skills sync | yes | yes | experimental | yes |
| MCP sync | yes | yes | yes | no |
| Hooks sync | yes | yes | no | no |
| Agent roles | rendered native | yes | experimental | no |
| Root instructions | symlinked per harness | generated files | generated files | no |
| Memory tooling | built in (`rem`) | no | no | no |
| Pinned + audited externals | lock file + audit | no pinning | no pinning | tracks source, no pin |
| Harness coverage | 9 deep | broad | broad | narrow |

## Positioning

Project-level generators win on tool breadth: if you need every team member's repo to emit config for 30 tools, use rulesync or ruler. tackroom is the complement, not the competitor: one personal setup, carried deeply into the harnesses you actually live in, following you across machines like dotfiles, with dependency discipline (pins, audits) applied to the prompt code you install from strangers.

## skillshare vs tackroom

skillshare is the closest in spirit: a user-level sync of skills, roles, hooks, and MCP, with a desktop app. Honest differences:

**Where skillshare is simpler**

- A native desktop app for browsing and editing; tackroom uses a loopback web view and CLI verbs.
- One `merge` mode; tackroom's reconciliation is richer (drift, held and blocked items, dry-run), which is more power but more to learn.

**Where tackroom is stronger**

- **Dependency discipline.** External skills are pinned to a commit and audited (`doctor audit`); a new pin is held 72h; a CRITICAL finding blocks sync. skillshare's audit sets a block threshold but does not pin.
- **Per-harness settings.** Declare settings keys per agent, secrets as `${ENV}` references, and new settings files are written `0600`.
- **Periodic sync.** `tackroom pull` keeps every machine current on a schedule, with held and blocked items surfaced in `status`.
- **Memory.** A review-first memory tier (`rem`) ships with it rather than as a separate tool.
- **Integrations.** Web view, plus Herdr, Tern, and Rex panels/layouts.

If you want GUI-first, merge-into-what-exists tooling, skillshare is good. If you want pins, audits, freshness holds, per-harness settings, and a memory tier on top of the same sync, that is tackroom — see the [migration guide](skillshare-migration.md).
