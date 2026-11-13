# Skills

A skill is a directory under `~/.agents/skills/` with a `SKILL.md` that has `name` and `description` frontmatter ([agentskills.io](https://agentskills.io) convention). Skills you write sync everywhere; skills from other people's repos are treated as dependencies: pinned to a commit, audited and held until they are 72 hours old.

## Write your own

```bash
tackroom skill new review-checklist --description "Pre-merge review checklist"
tackroom sync
tackroom skill list          # per harness: managed links, foreign symlinks, drift, context cost
tackroom skill info <name>   # one skill across harnesses
```

Harnesses that read `~/.agents/skills/` natively (OpenCode, Cursor, Gemini CLI, GitHub Copilot CLI, Grok Build, Kimi Code, DeepSeek Harness) get a mirror in their own skill root only when the config root is not `~/.agents`. Qwen Code and Amp read the canonical folder through their own settings (`skills.directories`, `amp.skills.path`), so no mirror is created.

## Where a skill came from

```bash
tackroom skill list [--agents a,b]   # every entry in each harness skill root, with provenance
tackroom skill info <name>           # one skill: its canonical folder and how each harness sees it
```

Both only read. `skill list` prints, for each detected harness, every entry in its skill root with one of these labels, then the context cost of the skill listing: name and description bytes and an estimated token count. A harness that is not on `PATH` prints `not detected`.

| Label | Meaning |
|---|---|
| `managed (local)` | A link to a skill in your config root. |
| `managed (external: acme/agent-skills@774ddc2)` | A link to an external source's skill, pinned at that commit. `(unpinned)` replaces the commit while the lock has no pin. |
| `missing (not linked)` | Expected but absent. `sync` creates the link. |
| `drifted symlink -> TARGET` | The link points somewhere other than the canonical folder. |
| `stale managed (...)` | A link into the store for a skill that is no longer canonical. `sync` removes it. |
| `conflict (real dir, differs from canonical)` | A real folder with the same name and different content. That harness is left alone until `sync --replace-conflicts`. |
| `unmanaged dir` | A real folder tackroom did not put there. |
| `foreign symlink -> TARGET` | A link tackroom did not create. |
| `external cache link -> TARGET` | A link into the external source cache that this harness is not expected to have. |
| `broken symlink -> TARGET` | A link whose target no longer exists. |

Amp and Qwen Code read the canonical folder through their own settings, so they print `managed (N): names` and an `integration:` line instead of one row per skill.

## Add an external source

Pulling skills from other people's repos is installing prompt code from strangers, so it is a separate step and never part of setup.

```bash
tackroom skill sources                                # configured sources, plus ones installed outside tackroom
tackroom skill sources search <query>                 # find sources on skills.sh
tackroom skill sources add owner/repo --skills a,b    # pick skills
tackroom skill sources add owner/repo --all
tackroom skill sources add owner/repo/path/in/repo --skills a   # a folder inside a repo
```

`add` pins the newest commit that is at least 3 days old; `--latest` pins the branch head instead. Without `--skills` or `--all`, an attended run shows a numbered list. Then it syncs; `--no-sync` skips that. Other flags: `--branch`, `--name`.

- `--all` keeps no allowlist, so skills upstream adds later in the same folders arrive on `skill update`. A skill in a new folder needs another `add` or a `skill_dirs` edit.
- If `~/.agents/skills/<name>` already exists, `add` adopts it only when it is identical to the source (as `npx skills add` leaves it) and stops otherwise.
- `skill sources` also reads Claude Code's plugin registry and the `npx skills` lock. A source Claude Code already loads as a plugin is added with `exclude_agents: [claude]`, so Claude Code does not see its skills twice.

## What `add` writes

```yaml
external_skills:
  - url: https://github.com/owner/repo
    branch: main
    skill_dirs: [skills/review]
    skills: [review-checklist]
    materialize: true
```

- `name` is optional and defaults to the repo name. Two repos with the same name need distinct names; `add` uses `<owner>-<repo>` for the second.
- `exclude_agents: [claude]` keeps the source's skills away from the listed harnesses.
- `materialize: true` copies the selected skills into `~/.agents/skills/`, so the content is versioned in your repo and diffable on update.
- `tackroom.lock` pins the source to an exact commit. Harnesses only ever see the pinned tree.
- `tackroom skill update [name ...]` is the only command that advances a pin. Updates are deliberate, never implicit.
- `tackroom doctor` scans external sources for risky patterns and detects drift between the lock and the materialized copies.

## The 72 hour hold

```bash
tackroom status                              # lists each held item: reason, hold end, override command
tackroom sync --allow-fresh                  # release all held items
tackroom sync --allow-fresh=<item> --reason "checked the diff"
tackroom skill update --allow-fresh
```

A new or changed external skill pin, and a new or changed reference to an exact npm or PyPI version (in an MCP server command or a Pi `packages` entry), is held until 72 hours after its publish time. The time comes from npm `time[version]`, the latest PyPI upload, or the git committer date; an unknown time means held.

A held item is skipped and what is already in the harness stays. `--allow-fresh` releases it and records the decision in `tackroom.lock` under `holds:`.

## The audit gate

```bash
tackroom doctor audit                                       # findings per item; exit 1 on an unaccepted CRITICAL in external content
tackroom doctor audit accept skill:<name> <rule> --reason "..."
tackroom doctor audit revoke skill:<name> <rule>
```

New or changed external content with an unaccepted CRITICAL finding is skipped by sync, and the previous pin stays. Items are named `kind:name` with kind `skill`, `plugin`, `role`, `hook` or `mcp`. `accept` records the decision in `tackroom.lock` under `accepted:`; `revoke` removes it.

Local skills, roles, hooks and MCP entries are reported, never blocked. Nothing is retroactive: a pin already in the lock keeps syncing. A git commit date is a weaker time source than a registry publish time.

## Plugins

An external source with an [agent-plugins-spec](https://agent-plugins.org) `plugin.json` is audited as `plugin:<name>`, and its name and version are recorded in the lock. Claude Code plugins in your config root, the spec sources and the Claude Code and Pi panels are covered in [plugins.md](plugins.md).

## Publish skills

```bash
tackroom publish --dry-run                   # the plan: create, update or skip per skill
tackroom publish [--target NAME] [--skills a,b] [--yes] [--json]
```

`publish` is the outward counterpart of external sources: it uploads skills from your config root to a remote skill registry, the OpenAI skills API. Declare an opt-in target with an explicit allowlist, because only the skills you name are ever uploaded:

```yaml
publish_targets:
  - name: openai
    kind: openai-skills           # the only kind today
    enabled: true                 # a target does nothing until this is set
    skills: [review-checklist]
    api_key_env: OPENAI_API_KEY   # the env var that holds the key; this is the default
    version_strategy: new-version # or set-default, which also makes the new version the default
```

A skill whose bundle is unchanged since the last run is skipped; a changed one gets a new version. `tackroom.lock` records the returned id and version under `published_skills`. A real upload prints a warning that the registry is US-only with no Zero Data Retention, even with `--yes`; without `--yes` it then shows the plan and asks `Proceed? [y/N]`. Do not publish skills that carry secrets or private notes.
