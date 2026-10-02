# Skills

A skill is a directory under `~/.agents/skills/` containing a `SKILL.md` with name and description frontmatter ([agentskills.io](https://agentskills.io) convention). Create one and it appears in every configured harness:

```bash
tackroom skill new review-checklist --description "Pre-merge review checklist"
tackroom sync
```

Already wrote a skill inside one harness? Promote it to canonical so every agent gets it:

```bash
tackroom skill promote my-skill        # finds it in a native skill root, copies it under ~/.agents
```

## External skills: pinned, audited

Pulling skills from other people's repos is installing prompt code from strangers — tackroom treats it like a dependency, not a download. Declare a source in `tackroom.yaml`:

```yaml
external_skills:
  - url: https://github.com/mattpocock/skills
    branch: main
    skill_dirs: [skills/productivity/grilling]
    materialize: true
```

- `tackroom.lock` pins the source to an exact commit; agents only ever see the pinned tree.
- `materialize: true` copies the selected directories into `~/.agents/skills/<name>` so the content is versioned in your repo and diffable on update.
- `tackroom skill update [name ...]` is the only thing that advances a pin — updates are deliberate, never implicit.
- `tackroom doctor` scans external sources for risky patterns (exfiltration, shell abuse) and detects drift between the lock and the materialized copies.

## Plugins

External sources with an [agent-plugins-spec](https://agent-plugins.org) `plugin.json` get their skills and MCP servers discovered automatically. Native plugin projection (Codex `.codex-plugin/`) is planned.

Qwen Code reads the canonical skill directory through `skills.directories` in `~/.qwen/settings.json`. This leaves its personal `~/.qwen/skills/` inventory untouched and avoids a duplicate symlink mirror.
