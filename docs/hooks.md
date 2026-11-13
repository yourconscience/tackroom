# Hooks

Declare a hook once in `tackroom.yaml` and `sync` registers it in every harness that has a hook surface, using that harness's event names. `tackroom hook list` shows what is registered natively, including entries tackroom did not write.

```bash
tackroom sync                        # register the hooks you declared
tackroom hook list                   # every native registration, managed or not
tackroom hook remove --dry-run <query>
```

## Declare a hook

There is no `hook add` command yet: write the entry in `hooks:` of `tackroom.yaml`, or in `tackroom.local.yaml` for one machine. A hook with the same name in the local file replaces the shared one.

```yaml
hooks:
  - name: notify-stop
    enabled: true                     # entries stay off unless enabled
    event: Stop                       # Claude Code event names
    command: ~/.agents/hooks/notify.sh
    timeout: 10                       # seconds; omit for the harness default
    agents: [claude, codex, gemini]   # optional; default: every harness with hooks
```

- `name` is unique and `event` and `command` are required. `timeout` is in seconds and cannot be negative.
- `command` is written as given and run by the harness. tackroom does not create the script, so it has to exist on every machine that syncs the hook.
- `agents` uses [harness keys](harnesses.md#config-keys-and-detection). Without it, harnesses that have no hook surface are skipped silently.
- Event names are Claude Code's. Each harness below gets its own name for the event, or reports it as unsupported.

`tackroom setup` writes hooks of this shape for the memory tier. This is what `setup --memory basic` adds for Claude Code and Codex:

```yaml
hooks:
  - name: memory-session-start
    enabled: true
    event: SessionStart
    command: ~/.agents/memory/hooks/basic-session-start.py
    timeout: 15
    agents: [claude, codex]
  - name: memory-session-end
    enabled: true
    event: SessionEnd
    command: ~/.agents/memory/hooks/basic-session-end.py
    timeout: 30
    agents: [claude, codex]
```

The memory hooks go to every enabled harness with a hook surface except Antigravity CLI ([memory.md](memory.md#tiers)).

## Which harnesses run hooks

Twelve harnesses have a hook surface; OpenCode, DeepSeek Harness and Amp have none, and tackroom writes nothing for them.

| Harness | Written to | Events |
|---|---|---|
| Claude Code | `~/.claude/settings.json` | Any, as given |
| Codex | `~/.codex/hooks.json` | Any, as given |
| Factory Droid | `~/.factory/hooks.json` | Any, as given |
| Qwen Code | `~/.qwen/settings.json` | Any, as given |
| Grok Build | `~/.grok/hooks/tackroom.json` | Any, as given |
| Gemini CLI | `~/.gemini/settings.json` | See below |
| Cursor | `~/.cursor/hooks.json` | See below |
| GitHub Copilot CLI | `~/.copilot/hooks/tackroom.json` | `SessionStart`, `SessionEnd`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `Stop`, `SubagentStop`, `ErrorOccurred`, `PreCompact` |
| Antigravity CLI | `~/.gemini/config/hooks.json` | `SessionStart`, `Stop`, `PreToolUse`, `PostToolUse` |
| Kimi Code | `~/.kimi-code/config.toml` | `SessionStart`, `SessionEnd`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop`, `SubagentStop`, `PreCompact` |
| Pi | `~/.pi/agent/extensions/tackroom-<name>.ts` | `SessionStart`, `SessionEnd` |
| OMP | `~/.omp/agent/hooks/post/tackroom-<name>.ts` | `SessionStart`, `SessionEnd` |

A hook whose event a harness cannot take is skipped for that harness and `tackroom status` lists it under the harness as `hooks unsupported: <name>`. That line does not change the exit code. A hook that names a harness with no hook surface in its `agents:` is reported the same way.

**Gemini CLI** renames `UserPromptSubmit` to `BeforeAgent`, `Stop` to `AfterAgent`, `PreToolUse` to `BeforeTool`, `PostToolUse` to `AfterTool` and `PreCompact` to `PreCompress`. `SessionStart`, `SessionEnd` and `Notification` keep their names. No other event maps.

**Cursor** uses `sessionStart`, `sessionEnd`, `stop`, `preToolUse`, `postToolUse`, `beforeSubmitPrompt` (for `UserPromptSubmit`), `subagentStop` and `preCompact`. If Cursor's third-party configs setting is on, it also runs Claude Code's hooks, so a hook synced to both runs twice there.

**Pi and OMP** run hooks as TypeScript extensions, so tackroom generates one module per hook. The module runs the command with `/bin/sh -c` and sends a Claude Code-style JSON payload (`hook_event_name`, `cwd` and `agent`) on stdin. Without a `timeout` it waits 60 seconds.

For `SessionStart`, an `additionalContext` value in the command's JSON output reaches the model with the next prompt. `Stop` and the tool events would need block-and-continue behavior the module does not reproduce.

Timeouts are seconds in `tackroom.yaml`. Qwen Code and Gemini CLI take milliseconds, and tackroom converts. Codex gives `SessionEnd` hooks at most 3 seconds, so a larger `timeout` is written as 3 there. Codex also needs `hooks = true` under `[features]` in `~/.codex/config.toml`, which `sync` sets.

Factory Droid uses `~/.factory/settings.json` instead when `hooks.json` does not exist and `settings.json` already has a `hooks` block.

## List native registrations

```bash
tackroom hook list [--agents a,b] [query]
```

```
claude	SessionStart	managed	~/.agents/memory/hooks/basic-session-start.py
claude	Stop	unmanaged stale=/path/to/old-tool/hook.sh	/path/to/old-tool/hook.sh
gemini	AfterAgent	managed	~/.agents/hooks/notify.sh
unsupported hook surfaces: amp, dsh, opencode
3 native hook registration(s)
```

Each line is tab-separated: the harness key, the event under the harness's own name, the ownership, and the command.

- `managed` means an enabled hook in `tackroom.yaml` matches the event and command. Anything else is `unmanaged`, whoever wrote it.
- `stale=PATH` appears when the command names a script (`.sh`, `.py`, `.cmd` or `.ts`, under `~`, `$HOME` or an absolute path) that does not exist.
- The command has its whitespace collapsed and is cut at 180 characters.
- `query` is an optional case-insensitive substring of the harness, event, command or file path.

The list covers the enabled harnesses in your config, or the ones named by `--agents`, whether or not they are installed. `tackroom doctor` has a `native hooks` check that warns about stale registrations.

## Remove native registrations

```bash
tackroom hook remove --dry-run <query>   # show the matches, change nothing
tackroom hook remove <query>             # remove them
```

Put flags before the query. `remove` prints the matching registrations, removes each one, and reports how many files it changed. Other entries in those files are kept; for Pi and OMP the generated module is deleted. A query is required, and a query with no match prints `no native hooks match` and exits 0.

`remove` does not check ownership and writes no backup, so preview first. It also removes registrations of hooks that are still declared and enabled; the next `sync` writes those again.

`sync` adds and updates registrations but never removes the native entry of a hook that is no longer declared. It shows up as `unmanaged` in `hook list` until you remove it. The memory hooks are the exception: `setup` removes the native memory registrations it managed before, then syncs the current set.

To retire a hook you declared, delete the entry or set `enabled: false`, then run `hook remove`. Use the same steps for hooks left behind by a tool you uninstalled ([troubleshooting.md](troubleshooting.md#hooks)).
