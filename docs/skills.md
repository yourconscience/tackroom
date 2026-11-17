# Skills

A skill is a directory under `~/.agents/skills/` with a `SKILL.md` that has `name` and `description` frontmatter ([agentskills.io](https://agentskills.io) convention). Skills you write sync everywhere; skills from other people's repos are treated as dependencies: pinned to a commit, audited and in cooldown until they are 72 hours old. This page covers writing, adding and publishing them.

## Write your own

```bash
tackroom skill new review-checklist --description "Pre-merge review checklist"
tackroom sync
tackroom skill list          # per harness: managed links, foreign symlinks, drift, context cost
tackroom skill info <name>   # one skill across harnesses
```

Harnesses that read `~/.agents/skills/` natively (OpenCode, Cursor, Devin, GitHub Copilot CLI, Grok Build, Kimi Code, DeepSeek Harness) get a mirror in their own skill root only when the config root is not `~/.agents`. Qwen Code and Amp read the canonical folder through their own settings (`skills.directories`, `amp.skills.path`), so no mirror is created.

## See where a skill came from

```bash
tackroom skill list [--agents a,b]   # every entry in each harness skill root, with provenance
tackroom skill info <name>           # one skill: its canonical folder and how each harness sees it
```

Both only read. `skill list` prints, for each detected harness, every entry in its skill root with one of these labels, then the context cost of the skill listing: name and description bytes and an estimated token count. A harness that is not on `PATH` prints `not detected`.

| Label | Meaning |
|---|---|
| `managed (local)` | A link to a skill in your config root. |
| `managed (external: acme/agent-skills@b09718b)` | A link to an external source's skill, pinned at that commit. `(unpinned)` replaces the commit while the lock has no pin. |
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

`add` pins the newest commit that is at least 3 days old; `--latest` pins the branch head instead. Then it syncs; `--no-sync` skips that.

- A head younger than 72 hours is in cooldown like any new pin: `add` records the cooldown and no pin, sync skips the source, and `--allow-fresh` on `add` (or a later `sync --allow-fresh=<item>`) skips the cooldown.
- Without `--skills` or `--all`, an attended run shows a numbered list. Other flags: `--branch`, `--name`.
- `--all` keeps no allowlist, so skills upstream adds later in the same folders arrive on `skill update`. A skill in a new folder needs another `add` or a `skill_dirs` edit.
- If `~/.agents/skills/<name>` already exists, `add` adopts it only when it is identical to the source (as `npx skills add` leaves it) and stops otherwise.
- `skill sources` also reads Claude Code's plugin registry and the `npx skills` lock. A source Claude Code already loads as a plugin is added with `exclude_agents: [claude]`, so Claude Code does not see its skills twice.

### What `add` writes

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

## The 72 hour cooldown

```bash
tackroom status                              # lists each item in cooldown, including a skill update in cooldown: reason, cooldown end, override command
tackroom sync --allow-fresh                  # skip the cooldown of all items
tackroom sync --allow-fresh=<item> --reason "checked the diff"
tackroom skill update --allow-fresh
```

A new or changed external skill pin, and a new or changed reference to an exact npm or PyPI version (in an MCP server command or a Pi `packages` entry), is in cooldown until 72 hours after its publish time, the minimum release age. An item in cooldown is skipped and what is already in the harness stays.

- The time comes from npm `time[version]`, the latest PyPI upload, or the git committer date; an unknown time means cooldown.
- Local sources (`file://` URLs and paths) have no publish time and are never in cooldown; the audit gate still applies to them.

To skip the cooldown:

- `--allow-fresh` skips it and records the decision in `tackroom.lock` under `cooldowns:`.
- `--allow-fresh=<item>` takes the item as `status` prints it, for example `skill:lint-skills`. A name that matches nothing in cooldown gets a warning on stderr that lists the items that are, and the run goes on.
- Skipping the cooldown applies to the commit in cooldown only: while a new source is in cooldown, sync keeps it on that commit even if upstream moves, and a newer head is gated as a new pin once the cooldown ends.
- A `skill update` in cooldown keeps the previous pin; sync never moves a pin, so its cooldown is skipped with `tackroom skill update <name> --allow-fresh=<item>`.

What the lock records:

- The lock records an npm or PyPI version while its cooldown is in force, while its publish time is unknown, or once you skip the cooldown.
- A version already older than 72 hours when it is first looked up adds nothing to the lock, so the first sync on a new machine leaves it unchanged.
- Pi packages are looked up only on a machine where Pi is detected; elsewhere a decision already in the lock still applies.

## The audit gate

```bash
tackroom doctor audit                                       # findings per item; exit 1 on an unaccepted CRITICAL in external content
tackroom doctor audit accept skill:<name> <rule> --reason "..."
tackroom doctor audit revoke skill:<name> <rule>
```

New or changed external content with an unaccepted CRITICAL finding is skipped by sync, and the previous pin stays. `skill sources add` applies the same gate: a blocked source is added to `tackroom.yaml` without a pin, and the first sync after `accept` pins it.

CRITICAL means one of these:

- a prompt-injection phrase;
- a secret sent out or a credential file read;
- invisible tag characters;
- a download piped into a shell (rule `remote-exec`).

Some matches count one level lower, so they show as HIGH and do not block:

- Markdown text outside a code fence that warns against the pattern or quotes it: the match sits inside double quotation marks, or one of these words comes before it in the same clause: never, do not, don't, avoid, instead of, for example, such as, flag, detect, warn. A clause ends at a comma, a semicolon or the end of a sentence. So "Do not run `curl ... | sh`" is HIGH, while "Run `curl ... | sh`" and "If you don't have it, run `curl ... | sh`" are CRITICAL.
- A Markdown code fence, as before.
- A script comment or docstring.

Items are named `kind:name` with kind `skill`, `plugin`, `role`, `hook` or `mcp`. `accept` records the decision in `tackroom.lock` under `accepted:`; `revoke` removes it.

Local skills, roles, hooks and MCP entries are reported, never blocked. Nothing is retroactive: a pin already in the lock keeps syncing.

What the gate and the cooldown do not cover:

- A git commit date is a weaker time source than a registry publish time. A commit pushed today with an older date passes at once.
- The npm package `@openai/codex` is exempt from the cooldown.
- A PyPI pre-release version such as `1.0rc1` is not recognised as an exact version, so it is not gated.
- The words that lower a Markdown match do not have to govern it: "Do not worry and run `curl ... | sh`" is HIGH, and so is a command written in double quotation marks. The finding still shows in `doctor audit`.

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

A skill whose bundle is unchanged since the last run is skipped; a changed one gets a new version. `tackroom.lock` records the returned id and version under `published_skills`.

A real upload prints a warning that the registry is US-only with no Zero Data Retention, even with `--yes`; without `--yes` it then shows the plan and asks `Proceed? [y/N]`. Do not publish skills that carry secrets or private notes.
