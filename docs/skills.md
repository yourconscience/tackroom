# Skills

A skill is a directory under `~/.agents/skills/` containing a `SKILL.md` with name and description frontmatter ([agentskills.io](https://agentskills.io) convention). Create one and it appears in every configured harness:

```bash
tackroom skill new review-checklist --description "Pre-merge review checklist"
tackroom sync
```

## External skills: pinned, audited

Pulling skills from other people's repos is installing prompt code from strangers — tackroom treats it like a dependency, not a download. Adding one is a separate step, never part of setup:

```bash
tackroom skill sources                                # configured sources, plus ones installed outside tackroom
tackroom skill sources search grill                   # find sources on skills.sh
tackroom skill sources add mattpocock/skills --skills tdd,grill-me
tackroom skill sources add obra/superpowers --all
tackroom skill sources add cursor/plugins/pstack --skills how,why   # a path inside a repo
```

`add` pins the newest commit that is at least 3 days old (`--latest` pins the branch head), so a freshly pushed commit never reaches every agent the moment it lands. Pick skills with `--skills` or `--all`; an attended run without either shows a numbered list. `--all` keeps no allowlist, so skills upstream adds later in the same directories arrive on `skill update`; a skill in a new directory needs another `add` or a `skill_dirs` edit. If `~/.agents/skills/<name>` already exists, `add` adopts it only when it is identical to the source (as `npx skills add` leaves it) and stops otherwise. Then it syncs (`--no-sync` skips that).

`tackroom skill sources` also reads Claude Code's plugin registry and the `npx skills` lock. A source Claude Code already loads as a plugin (superpowers, say) is added with `exclude_agents: [claude]`, so the other agents get it and Claude does not see its skills twice.

`add` writes an entry like this to `tackroom.yaml`; you can also write it by hand:

```yaml
external_skills:
  - url: https://github.com/mattpocock/skills
    branch: main
    skill_dirs: [skills/productivity/grilling]
    materialize: true
```

- `name` is optional and defaults to the repo name. Two repos that share it (`mattpocock/skills`, `anthropics/skills`) need distinct names; `add` uses `<owner>-<repo>` for the second.
- `exclude_agents: [claude]` keeps the source's skills away from the listed agents.
- `tackroom.lock` pins the source to an exact commit; agents only ever see the pinned tree.
- `materialize: true` copies the selected directories into `~/.agents/skills/<name>` so the content is versioned in your repo and diffable on update.
- `tackroom skill update [name ...]` is the only thing that advances a pin; updates are deliberate, never implicit.
- `tackroom doctor` scans external sources for risky patterns (exfiltration, shell abuse) and detects drift between the lock and the materialized copies.

Two gates apply to outside content only. A new or changed reference to an exact npm or PyPI version (in an MCP server command or a Pi `packages` entry), or a new or changed external skill pin, is held until 72 hours after its publish time (npm `time[version]`, PyPI latest upload, git committer date); an unknown publish time means held. A held item is skipped and what is already in the harness stays. `sync --allow-fresh` (also `skill update --allow-fresh`) releases and records it, and `status` lists each held or blocked item with the reason, the hold end and the exact override command.

New or changed external content with an unaccepted CRITICAL finding is skipped by sync and the previous pin stays. `doctor audit` lists findings per item (`skill:`, `plugin:`, `role:`, `hook:`, `mcp:`, `package:`); `doctor audit accept <item> <rule> --reason "..."` records the decision in `tackroom.lock` under `accepted:`, and `revoke` removes one. Local skills, roles, hooks and MCP entries are reported, never blocked. Nothing is retroactive: a pin already in the lock keeps syncing, and a git commit date counts as a weaker time source than a registry publish time.

## Plugins

External sources with an [agent-plugins-spec](https://agent-plugins.org) `plugin.json` get their skills and MCP servers discovered automatically. Native plugin projection (Codex `.codex-plugin/`) is planned.

Qwen Code reads the canonical skill directory through `skills.directories` in `~/.qwen/settings.json`. This leaves its personal `~/.qwen/skills/` inventory untouched and avoids a duplicate symlink mirror.
