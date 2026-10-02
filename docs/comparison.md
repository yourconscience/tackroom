# How tackroom differs

Different tools solve different parts of agent-config fragmentation.

| Tool | Scope | What it does |
|---|---|---|
| [rulesync](https://github.com/dyoshikawa/rulesync), [ruler](https://github.com/intellectronica/ruler) | per-project | Generate agent config inside a repo |
| [openskills](https://github.com/numman-ali/openskills) | user-level | Install skills |
| [dot-agents](https://github.com/dot-agents/dot-agents) | user-level | Symlink rules and MCP config once |
| [dotagent](https://github.com/johnlindquist/dotagent) | one-shot | Convert between agent config formats |
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
