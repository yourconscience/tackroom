# Philosophy

What tackroom believes about managing a coding-agent setup, and what it refuses to do. Each principle names something you can check.

## Your setup is a repo you own
Your skills, roles, hooks and instructions live in a folder of yours: `--config`, else `$TACKROOM_HOME`, else `~/.agents`. tackroom's own checkout is never read as config, and a config root inside a linked git worktree is refused.

Check: `tackroom config validate` prints the file it used; `tackroom setup` puts a new config root under git.

## One config for every harness, plus what makes each one itself
You declare skills, MCP servers, hooks, roles and plugins once in `tackroom.yaml`, and every harness gets them in its own format: roles as TOML for Codex and Markdown elsewhere, hooks under each harness's event names, Gemini CLI with its own tool names.

The harnesses with features worth keeping get their own block next to the shared one: `settings:` keys written into the native settings files of Claude Code, Codex, Pi and OMP, Claude Code plugins loaded in place, a pinned `packages` list for Pi. Where a harness has no surface, tackroom writes nothing: Antigravity CLI gets no roles, OpenCode no hooks.

Check: [harnesses.md](harnesses.md) is the matrix of who gets what; `tackroom hook list` ends with `unsupported hook surfaces:` for the harnesses you have; [roles.md](roles.md) lists every path.

## Import copies; unrelated config is left alone
Import copies what your harnesses already have and never moves or deletes the original. Sync writes only entries it marks as managed and only the settings keys you declare. When it must replace a native file, it asks first and keeps the original in a backup.

Check: `tackroom setup --dry-run` changes nothing; the Unmanaged tab of `tackroom view` lists what tackroom does not own.

## Strangers' skills are dependencies
An external skill is pinned to a commit in `tackroom.lock`, audited, and held until it is 72 hours old. The same hold covers exact npm and PyPI versions. An existing pin moves only on `tackroom skill update`.

Check: `tackroom skill sources add` pins a commit at least 3 days old; `tackroom doctor audit` exits 1 on an unaccepted CRITICAL finding; `sync --allow-fresh` is the way past a hold.

## Every change can be previewed and undone
`sync --dry-run`, `setup --dry-run`, `cron apply --dry-run` and `hook remove --dry-run` print the plan and write nothing. Before tackroom replaces a native file it moves it to a backup.

Check: `tackroom restore` lists the backups; `tackroom restore latest` puts the newest back.

## Memory is reviewed, not rewritten
Capture and de-duplication are machine jobs. Promoting a fact into your instructions is yours: `rem dream` writes a report and never edits a durable file.

Check: captured candidates sit inert in `$KNOWLEDGE_DIR/ai/` until you act on the report in `$KNOWLEDGE_DIR/reviews/`. See [memory.md](memory.md).

## You work in the view; the CLI is the engine
Day to day you look and act in `tackroom view`: on a desktop, on a phone over your tailnet, or in a narrow pane beside your agents. The CLI is the engine and the surface for scripts and SSH. The view, the Herdr panel, the skill and the Claude Code and Pi plugins call the same commands; none has data or an action the CLI lacks, and saving in the view never runs a sync.

Check: the view's Overview lists the same held and blocked sources as `tackroom status`; its Crons tab draws `tackroom cron collect`; after a save in the view, `tackroom status` reports the change as not synced until you apply Sync.

## A small, fixed scope
tackroom syncs five surfaces: skills, MCP servers, hooks, roles and plugins. It also manages root instruction files, Pi packages and the settings keys you declare. Scheduled jobs are inventoried, and jobs you declare become native units, but they are not a sixth surface.

Check: `tackroom help --all` lists the whole command set.

## What tackroom is not

- Not a per-project rules generator. It manages your user-level setup; [comparison.md](comparison.md) covers the tools that work per repository.
- Not a scheduler. Declared jobs run in launchd, systemd or cron, and tackroom runs no daemon.
- Not a skills marketplace. `skill sources search` queries skills.sh; you choose what to add.
- Not an editor of your harnesses' own settings. It writes the keys you declare and leaves the rest.
- Not self-updating memory. Nothing promotes itself into your instructions.
- Not hosted, and not instrumented. Its network calls are git, npm and PyPI lookups for the hold, skills.sh search and the registry you configure for `publish`. It sends no telemetry.
