# Comparison

tackroom is a user-level dotfiles manager for coding agents. It syncs skills, MCP servers, hooks, roles and root instructions across the harnesses you use, and pins and audits what you install from strangers.

## At a glance

| | **tackroom** | [skillshare](https://github.com/runkids/skillshare) | [HarnessKit](https://github.com/RealZST/HarnessKit) | [dotagents](https://github.com/getsentry/dotagents) | [rulesync](https://github.com/dyoshikawa/rulesync) | [ruler](https://github.com/intellectronica/ruler) | [openskills](https://github.com/numman-ali/openskills) |
|---|---|---|---|---|---|---|---|
| Scope | user | user, and per project | user, and per project | user, and per project | project, and a global mode | project | user + project |
| Skills | ✓ | ✓ | ✓ | ✓ | ✓ | ~ | ✓ |
| MCP | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | - |
| Hooks | ✓ | ✓ | ~ 10 of 17 agents | ~ 3 of 8 agents | ✓ | - | - |
| Roles | ✓ native | ✓ | - lists, does not write | ✓ subagents | ✓ | ~ | - |
| Root instructions | ✓ | ✓ | - lists, does not write | - | ✓ | ✓ | - |
| Memory | ~ delivers files you list | ✓ shared Markdown notes | ~ lists files; a Kit can carry them | - | - | - | - |
| Pin + audit externals | ✓ lock + audit | ~ audit; commit pins in project mode | ~ trust score; no lockfile | ~ trust allowlist, ref pins; no audit | - | - | - |
| Cooldown (72h minimum release age) | ✓ | - | - | ~ opt-in, git sources only | - | - | - |
| Per-harness settings | ✓ | - | - | - | - | - | - |
| Periodic sync | ✓ | - | - | - | - | - | - |
| Desktop app | - (web view) | ✓ | ✓ | - | - | - | - |
| Terminal panels | ✓ Herdr panel · Tern crons panel and workspace command · Rex workspace command and palette actions · tmux and zellij workspace commands | ✓ Herdr panel | - | - | - | - | - |
| Harness depth | deep | broad | broad | broad | broad | broad | narrow |

`✓` full, `~` partial, `-` none. The other tools' cells follow their public READMEs and documentation as read on 2026-11-13, HarnessKit's its [docs](https://harness-kit.dev/docs/) and dotagents' its README and docs as read on 2026-11-16, and skillshare's memory cell its [v0.24.0 release notes](https://github.com/runkids/skillshare/releases/tag/v0.24.0) as read on 2026-11-16; check them before you rely on one.

## skillshare vs tackroom

skillshare is the closest tool. It syncs skills, agents, MCP servers, hooks and plugins at user level, with a desktop app, a web dashboard and a merge, copy or symlink mode per target. It audits skills against a configurable block threshold.

For pins, it writes `.skillshare/skills.lock.json` in project mode ([how the lockfile works](https://skillshare.runkids.cc/docs/understand/project-skills#lockfile)). In its global mode it records sources and file hashes in a local `.metadata.json`, which its documentation calls personal machine state.

tackroom goes further in three places:

- external skills are pinned in `tackroom.lock`, which lives in your config repo and is shared by every machine;
- new pins and new exact package versions wait out a 72-hour cooldown, the minimum release age, and an item still inside it is skipped until the cooldown ends or you pass `--allow-fresh`;
- each harness accepts declared settings keys, and a periodic job keeps machines in sync.

skillshare's documentation describes none of these. See [Moving from skillshare](#moving-from-skillshare) to move over.

Memory goes the other way. Since v0.24.0 (2026-10-03), skillshare keeps shared Markdown notes in `extras/memory`, with commands and a dashboard page to browse and search them, and you connect the agents that read them. tackroom runs no memory system: it delivers the files you list under `instructions:`, and capture is left to a separate tool such as [rem](https://github.com/yourconscience/rem). See [memory.md](memory.md).

## HarnessKit vs tackroom

[HarnessKit](https://harness-kit.dev/) is an app: a desktop app, a web UI and an `hk` CLI. It shows the extensions, rules, memory and subagent files of 17 agents in one place, and copies skills, MCP servers and some hooks from one agent to another. It adds a marketplace, a 0 to 100 trust score from 20 static rules, and per-project scope. tackroom has none of those in that form.

It changes each agent's files in place and keeps no config repo of its own. Its documentation describes no lockfile, no minimum release age for new versions, no scheduled sync and no way to sync two machines. A [Kit](https://harness-kit.dev/docs/guides/kits) carries a setup instead: a snapshot of skills, MCP servers, rules and memory files exported as a zip, applied to projects only. The [`hk` CLI](https://harness-kit.dev/docs/reference/cli) lists, audits, enables and disables, and does not install.

tackroom is the other shape: one config repo you edit and commit, written into 15 agents, with pins in a lockfile, a 72-hour cooldown, an audit gate, per-harness settings, roles and root instructions it writes, and a periodic job that keeps machines in sync. The two can run on one machine; HarnessKit then shows what tackroom wrote.

## dotagents vs tackroom

[dotagents](https://github.com/getsentry/dotagents) is Sentry's tool, and it works per repository as well as per user. It declares skills, MCP servers, hooks, subagents and plugins in `agents.toml`, either globally under `~/.agents/` or committed in a repository, so a collaborator runs `dotagents --project install` and gets the same setup. tackroom has no project manifest and no team workflow, because it manages only your own user-level setup.

dotagents supports eight agents, has a `[trust]` allowlist of GitHub orgs, repos and git domains, pins a source to a ref such as `@v1.0.0`, and has an optional `minimum_release_age`, in minutes and off unless you set it, for git sources. Its `agents.lock` records the resolved commit, which its documentation calls informational only. Its documentation does not describe an audit of skill contents, root instructions, per-harness settings or a periodic sync.

tackroom pins commits in a lock shared by every machine, audits what it pins, and applies a 72-hour cooldown by default, to exact npm and PyPI versions as well as git sources. For git sources both tools go by the commit date, which the committer sets, so it is a weaker clock than a registry publish time; [skills.md](skills.md#the-audit-gate) lists what the cooldown does not cover. tackroom itself was called dotagents until 2026-10-02 and is unrelated to Sentry's project.

## The rest

rulesync and ruler generate config per repository; rulesync also has a `--global` mode, and ruler a global config used when a repository has no `.ruler/` folder. openskills installs skills only. None of them treats a personal setup as a dependency-managed dotfiles repo. For what tackroom refuses to do, read [philosophy.md](philosophy.md).

## Moving from skillshare

skillshare and tackroom solve the same problem: one place for your agent skills, roles, hooks, MCP servers and instructions. This section maps a skillshare setup onto `tackroom.yaml` and the `~/.agents` folders; [skillshare vs tackroom](#skillshare-vs-tackroom) says where the two differ.

### Mapping

| skillshare | tackroom |
|---|---|
| Skills source folder (default `~/.config/skillshare/skills`) | `~/.agents/skills/` for skills you wrote |
| Skills installed from a repo (`skills:` entries with a `source`) | `external_skills:`, pinned in `tackroom.lock` |
| Agents source folder | `~/.agents/agents/`, rendered into each harness's role format |
| `targets` | `agents:` entries, one per harness; `setup` detects them |
| `mcp.servers` | `mcp_servers:` |
| Hooks managed with `skillshare hooks` | `hooks:` |
| `audit.block_threshold` | `doctor audit`: an unaccepted CRITICAL finding in external content blocks sync, and new pins wait 72 hours |
| An extra holding a shared `AGENTS.md` or similar | `~/.agents/AGENTS.md`, plus `instructions:` for more files |

### Steps

1. **Preview.** `tackroom setup --dry-run` lists what it would import and changes nothing.
2. **Copy what you wrote.** Skills are `skills/<name>/SKILL.md` and roles are `agents/<name>.md` directly under the config root:
   ```bash
   mkdir -p ~/.agents/skills ~/.agents/agents
   cp -R ~/.config/skillshare/skills/<your-skill> ~/.agents/skills/
   cp ~/.config/skillshare/agents/<your-role>.md ~/.agents/agents/
   ```
3. **Pin external skills.** For each repo skillshare installed from, add it to tackroom so the commit is pinned and audited instead of copied:
   ```bash
   tackroom skill sources add owner/repo --skills a,b
   ```
4. **Move hooks** into a top-level `hooks:` block:
   ```yaml
   hooks:
     - name: notify-stop
       enabled: true
       event: Stop                       # Claude Code event names
       command: ~/.agents/hooks/notify.sh
       timeout: 10                       # seconds
       agents: [claude, codex]           # optional; default: every harness with hooks
   ```
5. **Move MCP servers** with `tackroom mcp add NAME --command ... [--arg ...]` or `--url ...`, or write them in `mcp_servers:`. To adopt one a harness already has, run `tackroom mcp import <harness> <name>`.
6. **Move Pi packages and per-harness settings** under the harness's own entry:
   ```yaml
   agents:
     - name: pi
       enabled: true
       detect: pi
       packages:
         - npm:<package>@<exact-version>
       settings:
         <dotted.key>: <value>
   ```
7. **Run it.** On the first machine, `tackroom setup`. After that, `tackroom sync` and `tackroom doctor`.

Skills that skillshare linked into a harness are relinked to the canonical copy on the first sync. A real folder with different content is never replaced without your confirmation, and then only after a backup.

### Verify

- `tackroom status` shows every harness synced with no drift.
- `tackroom doctor` has no failures, and any held or blocked item has a reason in `status`.
- `tackroom hook list` and `tackroom mcp list` match what skillshare used to write.

### Clean up

Once `tackroom status` is clean, uninstall skillshare the way you installed it and remove its config folder, so the two tools never write the same harness files.
