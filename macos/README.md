# Tackroom for Mac (experimental)

A menu bar app and window around `tackroom view`, [AgentsView](https://github.com/kenn-io/agentsview) and [HarnessKit](https://github.com/RealZST/HarnessKit). The app never writes agent files. Edits go to `tackroom.yaml` through `tackroom view`, and agents change only when you apply a sync.

- **Menu bar**: sync state per agent, today's AI spend from AgentsView, quick actions.
- **Overview**: what drifted in each agent, and which local servers the app is using. The sidebar shows how many agents need sync.
- **Sync**: preview what `tackroom sync` would change, confirm each destructive item, apply.
- **Skills**: every tackroom skill with its origin, token cost, one state symbol per agent, and invoked and loaded counts. Select one for its description, path, per-agent state, usage, and a SKILL.md preview. Reveal it in Finder, open SKILL.md, or update an external skill's source repo. The agent switches turn a whole agent on or off (`agents[].enabled`). The per-agent matrix is read-only: tackroom has no per-skill, per-agent targeting yet.
- **Skill usage**: invoked and loaded counts per skill for 7, 30 or 90 days, on all machines or one. Skills with neither signal are candidates to disable. See [What the usage numbers mean](#what-the-usage-numbers-mean).
- **MCP servers**: managed servers with their command, env keys (never values) and a chip per agent. Switch a server on or off, click a chip to send it to an agent or stop, edit the command, arguments and one env key, add a server, or press Test to start it and list its tools. Servers that only an agent's own config has (found by `hk list --json`) sit under "Not managed by tackroom" with an import action. Every save shows the redacted diff and a "Preview sync…" button.
- **Foreign items**: things in agent folders that tackroom does not manage. Skills come from tackroom's own scan; MCP servers, hooks and plugins come from `hk list --json` when HarnessKit is installed. The sidebar shows the count.
- **Config, Sessions, HarnessKit**: the three web UIs, embedded.
- Notifies when an agent that was synced drifts, and can launch at login.

## Build

Needs macOS 14 or later and a Swift 6 toolchain. The Command Line Tools are enough; Xcode is not required.

```bash
./bundle.sh             # builds build/Tackroom.app, ad-hoc signed
./bundle.sh --install   # also copies it to ~/Applications
./test.sh               # unit tests
```

Two headless modes run without opening a window. Run them from `swift build` output or from inside the app bundle:

```bash
.build/debug/Tackroom --check   # every view's data path, read-only
.build/debug/Tackroom --e2e     # the config write path, against a throwaway copy
```

`--check` connects to the same servers as the app, then prints what each view would show: agent states, a sync preview (never applied), skill usage with both signals, the unused list, the skills inventory, MCP servers and the ones tackroom does not manage. It fails if the usage view calls a skill unused while the SKILL.md search shows reads for it (it checks `ai-engineering-radar`, `decide`, `redesign-skill` and `tern`), or if an MCP server's agent list read from the YAML disagrees with the typed config.

`--e2e` copies the canonical `tackroom.yaml` into a temp directory and starts a separate `tackroom view` on a free port with `--config` pointing at the copy. Through the same model code the window uses, it toggles an MCP server, changes its agents, adds and replaces an env key, adds a server, toggles an agent, and imports an unmanaged server, re-reading `/api/state` after each step to check that only the intended entry changed. It checks that a stale revision comes back as a readable 409, previews a sync against the copy (never applied), then stops the server and deletes the directory. It checksums the real config before and after and fails if they differ. It prints `ok` or `FAIL` per step and exits non-zero on any failure.

## What it runs

- Its own `tackroom view` on `127.0.0.1:8791`, with a stable token in `~/Library/Application Support/Tackroom/view.token`. Another `tackroom view` (for example one behind `tailscale serve`) can keep running; saves are revision-guarded.
- The AgentsView daemon it finds through `~/.agentsview/daemon.<pid>.json`. The token comes from `AGENTSVIEW_AUTH_TOKEN` or `auth_token` in `~/.agentsview/config.toml`, the same sources the AgentsView web UI asks for. It also runs `agentsview session search` for the SKILL.md loads.
- `hk serve` on `127.0.0.1:7070` when `hk` is installed and nothing answers there yet. HarnessKit writes agent files directly, so the HarnessKit tab says to preview a sync afterwards.
- On request only: `tackroom skill update <repo>` (Update on an external skill) and `tackroom mcp import <agent> <name> --agents … --config …` (Import into tackroom). Both ask first. The import writes only `tackroom.yaml`, for the agents that already have the server, and replaces env values with `${KEY}` references, so secrets are not copied. Test on an MCP server starts its command for up to 10 s; the config's env values are masked, so a server that needs them can fail there.

The app looks for these CLIs in `~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`, `~/go/bin` and `PATH`. It stops the servers it started when it quits. Logs go to `~/Library/Logs/Tackroom/`.

Notifications and launch at login need the bundled app. The app is ad-hoc signed and not notarized, so it is meant to be built on the Mac that runs it.

## Config edits

Edits use `PATCH /api/config` on the shared layer with the revision the page was loaded from. If the file changed since, the server answers 409 `stale_revision`; the app reloads and asks you to make the change again.

- An MCP server's agent list keeps the spelling the file already uses: an entry that says `claude-code` stays `claude-code`. The typed config only says `claude`, so the app reads the entry's own spelling from the YAML.
- An empty `agents` list means every agent to tackroom, so the app refuses to remove an entry's last agent. Turn the server off instead.
- tackroom's PATCH cannot delete an entry. Turn a server off, or run `tackroom mcp remove <name>`.
- The server's save diff and `/api/state` `raw_yaml` carry env values in clear. The app reads `raw_yaml` once for agent spellings and keeps no copy. It masks every env value in the diffs it shows and keeps two lines of context around each change.
- A save re-marshals the whole file, so blank lines can disappear from the diff's unchanged parts.

## What the usage numbers mean

Measured on 2026-10-06 with AgentsView v0.42.0, 30-day window. Numbers will drift; the mechanisms will not.

- **Invoked** is `GET /api/v1/analytics/skills`. It counts explicit calls: Claude's Skill tool, slash commands, Hermes. Namespaced names such as `plugin:skill` merge into the bare skill name. Codex, Pi and OMP load skills by reading `skills/<name>/SKILL.md`, which this endpoint ignores: `&agent=codex` returned 0.
- **Loaded** is the number of distinct sessions that read a skill's SKILL.md, from `agentsview session search 'skills/[a-z0-9-]+/SKILL\.md' --regex --in tool_input --since <7|30|90>d --limit 500 --json`, paged with `--cursor`. A hit is any tool call whose input names the path, so it also catches `grep`, `sed` and edits of the skill itself. Of 535 hits: 323 shell commands, 116 read-tool calls, 75 edits or writes, 21 other. Treat Loaded as "touched", and Invoked as "called".
- The last search page reports `next_cursor: 0`, not null. The app stops on a cursor that does not move forward, and fails with a message instead of undercounting if a window holds more than 20,000 hits.
- **By agent** adds, per agent, the invocation count and the number of sessions that loaded the SKILL.md.
- **Last used** is the later of AgentsView's last call and the newest SKILL.md read.
- **Unused** means both signals are zero. If either one cannot be read, the view shows an error instead of guessing.
- AgentsView merges sessions from more than one machine (`GET /api/v1/machines` returned `m1.local` and `m4`; 134 of 186 invocations came from m4). The machine picker passes `machine` to the analytics call and `--machine` to the search.
- AgentsView leaves out one-shot sessions by default. With `include_one_shot=true` the analytics call returned 255 invocations instead of 186. The search also skips subagent sessions: with `--include-one-shot --include-children --include-automated`, session loads per skill and agent rose from 202 to 279. At 30 days no skill changed from used to unused, but a skill used only in such sessions would show as unused.
