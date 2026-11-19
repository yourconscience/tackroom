# Memory

tackroom runs no memory system. It captures nothing from sessions, indexes nothing and never writes a memory file. What it does is delivery: a file you list under `instructions:` goes into each harness's root instructions, so a memory file you keep reaches every session.

```yaml
instructions:
  - ~/agent-memory/MEMORY.md
```

## Where a listed file arrives

Each harness's root file carries `AGENTS.md` followed by the files under `instructions:`, in order ([setup.md](setup.md#extra-instruction-files)).

- Claude Code gets a short `~/.claude/CLAUDE.md` of `@path` import lines, so your edits reach it without a sync.
- Codex, Factory Droid, Pi, OMP, Qwen Code, GitHub Copilot CLI, Grok Build, DeepSeek Harness, Antigravity CLI, Devin, Gemini CLI and Amp get a generated copy. Run `tackroom sync` after you edit a listed file. `tackroom status` reports the copy as drifted until you do, and the periodic job runs that sync for you.
- Cursor and OpenCode get no root file from tackroom, so a listed file does not reach them.
- Kimi Code reads `~/.agents/AGENTS.md` itself and gets no root file, so a listed file does not reach it either.

Where each root file lands is in [harnesses.md](harnesses.md#where-each-surface-lands).

## Lay out the memory

Use the [Agent Memory Repo](https://github.com/AgentMemoryRepo/agentmemoryrepo) format (MIT):

- Keep memory in its own git repository, not in your config root and not in a project.
- `MEMORY.md` is the entry point every session loads. Keep it short.
- Each entry is one line, with optional metadata at the end: `[source: ...; added: YYYY-MM-DD]`.
- `[[path]]` links to topic files. Paths start at the memory root and leave out `.md`. An agent reads a linked file when it needs it.

List `MEMORY.md` under `instructions:`, after the files that sit beside `AGENTS.md`:

```yaml
instructions:
  - SOUL.md                     # beside AGENTS.md in the config root
  - ~/agent-memory/MEMORY.md    # the entry point of your memory repository
```

A harness sees only the text tackroom copies or imports, not the folder it came from. Start `MEMORY.md` with one line that says where the linked files live:

```markdown
# Memory

Linked files are in ~/agent-memory. A [[path]] link is relative to that folder.

- Prefers pnpm for Node work [added: 2026-11-16]

## Index
- [[projects/payments]]
```

Only what you put in `MEMORY.md` is loaded into every session. That is the review step: a fact reaches your agents once it is in that file, and not before.

## Capture with rem

[rem](https://github.com/yourconscience/rem) is a separate tool for review-first capture and consolidation. Install and use it as its repository describes. tackroom only registers its hook, like any other hook ([hooks.md](hooks.md)):

```yaml
hooks:
  - name: rem-session-start
    enabled: true
    event: SessionStart
    command: ~/.local/share/rem/hooks/session-start.sh
    timeout: 15
    agents: [claude, codex]
```

Then run `tackroom sync`. The hook gives each session context at its start, including the head of the vault's `MEMORY.md`, so a harness that runs the hook gets that file twice if you also list it under `instructions:`. Notes you take with `rem add` stay unreviewed, and its dreams land on a branch you merge. tackroom does not install rem, so the script has to exist on every machine that syncs the hook.

## Native harness memory

Some harnesses have a memory of their own. These are each harness's own features: tackroom only writes the key you declare under `settings:` ([setup.md](setup.md#per-harness-settings)), and does not run, read or move that memory.

- Claude Code: `autoMemoryEnabled`, on by default in local sessions, and `autoMemoryDirectory` for where it keeps that memory. Written to `~/.claude/settings.json`. See [Claude Code memory](https://code.claude.com/docs/en/memory).
- Codex: `features.memories`, off by default. Written under `[features]` in `~/.codex/config.toml`. See [Codex memories](https://learn.chatgpt.com/docs/customization/memories).
- OMP: `memory.backend`, `off` by default. Other values such as `local` pick a backend. Written to `~/.omp/agent/config.yml`. See [OMP memory](https://github.com/can1357/oh-my-pi/blob/main/docs/memory.md).

To turn Claude Code's auto memory off on every machine:

```yaml
agents:
  - name: claude
    enabled: true
    detect: claude
    settings:
      autoMemoryEnabled: false
```

Removing a key from `settings:` leaves the value in the native file. To switch a feature back, set the key to its other value and run `tackroom sync`.

## Other memory tools

A memory tool that is an MCP server or a skill is added like any other: see [mcp.md](mcp.md) and [skills.md](skills.md#add-an-external-source). [engram](https://github.com/Gentleman-Programming/engram) and [Basic Memory](https://github.com/basicmachines-co/basic-memory) are examples of MCP memory servers; their own docs give the command to run.

The Agent Memory Repo skill is an external skill source:

```bash
tackroom skill sources add AgentMemoryRepo/agentmemoryrepo --skills agent-memory-repo
```

This pins a commit at least 3 days old and syncs the skill to your harnesses. The skill does its work only when you invoke it.

## Coming from the bundled memory

Releases up to 2.8 shipped memory tiers (`setup --memory basic` or `memsearch`), memory scripts under `memory/` in the config root, and the `rem` and `knowledge-sync` tools. They are gone. `setup --memory` is still accepted and does nothing ([upgrading.md](upgrading.md)).

A config set up with an older release keeps working:

- The `memory-session-start`, `memory-session-end` and `memory-stop` entries in `tackroom.yaml` are ordinary hooks now. `sync` keeps registering them as listed and never adds, edits or removes them.
- While a hook entry in `tackroom.yaml` points at a script under `memory/hooks/` (in the config root or in `~/.agents`), every file under `memory/` stays, whether the hook is enabled or not. The first `sync` or `setup` drops those files from `.tackroom-starter.json` and prints `memory/ in <root> is yours now`. tackroom no longer updates or removes them, and deleting the hook entries later does not change that: remove the files yourself.
- If you delete those hook entries before the first `sync` or `setup` with this release, `sync` removes the scripts under `memory/hooks/` and `memory/lib/` that you never edited. It lists each script you did edit once, as `kept (modified by you)`, and leaves it alone from then on. A hook registration in a harness's own config that still runs a removed or missing memory script, such as the Pi or OMP module an old `memory-session-start` entry generated, is removed with it and backed up first; one that runs a script you edited stays.
- Go sources under `memory/tools/`, the `rem` and `knowledge-sync` binaries in `~/.local/bin` and `memsearch.conf` stay where they are. Nothing builds or updates them.

`rem` and `knowledge-sync` now come from the [rem repository](https://github.com/yourconscience/rem). To stop an old memory hook, set `enabled: false` on it and run `tackroom sync` ([hooks.md](hooks.md#remove-native-registrations)).
