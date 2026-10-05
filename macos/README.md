# Tackroom for Mac (experimental)

A menu bar app and window around `tackroom view`, [AgentsView](https://github.com/kenn-io/agentsview) and [HarnessKit](https://github.com/RealZST/HarnessKit). Config edits and syncs still go through `tackroom view`. The app never writes agent config itself.

- **Menu bar**: sync state per agent, today's AI spend from AgentsView, quick actions.
- **Overview**: what drifted in each agent, and which local servers the app is using.
- **Sync**: preview what `tackroom sync` would change, confirm each destructive item, apply.
- **Skill usage**: calls, sessions and last use per tackroom skill, from AgentsView session history. Skills with no calls in the window show up as candidates to disable.
- **Foreign items**: things in agent folders that tackroom does not manage. Skills come from tackroom's own scan; MCP servers, hooks and plugins come from `hk list --json` when HarnessKit is installed.
- **Config, Sessions, HarnessKit**: the three web UIs, embedded.
- Notifies when an agent that was synced drifts, and can launch at login.

## Build

Needs macOS 14 or later and a Swift 6 toolchain. The Command Line Tools are enough; Xcode is not required.

```bash
./bundle.sh             # builds build/Tackroom.app, ad-hoc signed
./bundle.sh --install   # also copies it to ~/Applications
./test.sh               # unit tests
```

`Tackroom.app/Contents/MacOS/Tackroom --check` runs every view's data path without UI and prints the results. It previews a sync but never applies one.

## What it runs

- Its own `tackroom view` on `127.0.0.1:8791`, with a stable token in `~/Library/Application Support/Tackroom/view.token`. Another `tackroom view` (for example one behind `tailscale serve`) can keep running; saves are revision-guarded.
- The AgentsView daemon it finds through `~/.agentsview/daemon.<pid>.json`. The token comes from `AGENTSVIEW_AUTH_TOKEN` or `auth_token` in `~/.agentsview/config.toml`, the same sources the AgentsView web UI asks for.
- `hk serve` on `127.0.0.1:7070` when `hk` is installed and nothing answers there yet. HarnessKit writes agent files directly, so the HarnessKit tab says to preview a sync afterwards.

The app looks for these CLIs in `~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`, `~/go/bin` and `PATH`. It stops the servers it started when it quits. Logs go to `~/Library/Logs/Tackroom/`.

Notifications and launch at login need the bundled app. The app is ad-hoc signed and not notarized, so it is meant to be built on the Mac that runs it.
