# tackroom in Rex

[Rex](https://www.superlogical.com/rex) is the terminal multiplexer by Superlogical, with sessions, windows, blocks, a `rex` CLI and Lua configuration. tackroom works with it in two ways and never edits Rex's configuration:

- `tackroom rex` opens a tackroom workspace: a Rex session labelled `tackroom`.
- `rex/tackroom.lua`, which you add to your own Rex config, puts three tackroom actions in Rex's command palette.

Rex has no plugin or panel API: its blocks are terminals. So the workspace runs `tackroom dash`, a terminal program that shows the sync state and your scheduled jobs and has Run now, Pause and Resume for launchd jobs. The web view's Crons tab has them too, and the **Open tackroom View on Crons** action opens it.

## The workspace

`tackroom rex` (or `tackroom rex --config PATH` with the path of a non-default `tackroom.yaml`) opens a session labelled `tackroom` in the config root, with two panes:

- `tackroom dash` across the full width on top, with 70% of the height,
- a shell in the config root below it, which keeps the focus.

The dash refreshes itself and redraws when the pane is resized; its keys are in [terminals.md](../docs/terminals.md#dash). Its pane is labelled `dash` and kept open when the dash ends, so an error stays readable. It runs the same `tackroom` that opened it, by the absolute path it was started as (not resolved through symlinks, so a Homebrew link stays valid across upgrades), so an older `tackroom` earlier on your PATH cannot answer for it. `--dry-run` prints the `rex` commands without touching Rex.

It shells out to the `rex` CLI:

```
rex ls --json
rex new tackroom -c <root> --window tackroom --json
rex split -s <session> -b <shell> --split above --ratio 70 -c <root> --label dash --keep-open --focus=false --json -- <tackroom> dash
rex do session.select session_id=<session>
```

`<tackroom>` is the absolute path of the running `tackroom`. `<session>` and `<shell>` are `.session_id` and `.initial_windows[0].block_ids[0]` from `rex new`. When `rex ls` already lists a session labelled `tackroom`, nothing is created and only the last command runs, with that session's id.

`rex new` does not make the Rex app show the new session; the last command asks it to. That is an action of the app, which it takes only while it is open with Remote Control turned on in its settings. When it refuses, `tackroom rex` prints one line saying to open the `tackroom` session from Rex's sidebar, with Rex's reason, and still exits 0.

## Palette actions

### Install

1. Put `tackroom.lua` beside Rex's `init.lua`, which is `$XDG_CONFIG_HOME/rex/init.lua`, else `~/.config/rex/init.lua` (or the file `REX_CONFIG` names). Rex's `require` loads Lua files from that folder. From a tackroom checkout:

   ```sh
   ln -s "$PWD/rex/tackroom.lua" "${XDG_CONFIG_HOME:-$HOME/.config}/rex/tackroom.lua"
   # or copy it:
   cp rex/tackroom.lua "${XDG_CONFIG_HOME:-$HOME/.config}/rex/"
   ```

2. Add one line to `init.lua`:

   ```lua
   require("tackroom")
   ```

3. Check and load it:

   ```sh
   rex config check   # should list 3 more actions and no errors
   rex config reload
   ```

### What each action does

They are under **tackroom** in the command palette. From a shell, `rex do NAME` runs one.

| Action | Palette title | Does |
| --- | --- | --- |
| `tackroom_workspace` | Open tackroom Workspace | Runs `tackroom rex`, then asks the app it ran in to show the `tackroom` session. Run from the palette, that works with Remote Control off. |
| `tackroom_crons` | Open tackroom Crons | Opens `tackroom cron collect --table` in a new tab labelled `crons`, kept open after the table prints. Read-only. |
| `tackroom_view` | Open tackroom View on Crons | Runs `tackroom view --json`, which starts the view when none runs, and opens its `url` plus `#crons` in the default browser, as the Tern plugin's Web button does. Run now, Pause and Resume for launchd jobs are there. |

### Key binding (optional)

In `init.lua`, after the `require`:

```lua
rex.bind("ctrl+alt+c", "tackroom_crons")
```

Pick a key Rex does not already use.

### Settings (optional)

```lua
local tackroom = require("tackroom")
tackroom.bin = "/path/to/tackroom"         -- default: the first of Rex's PATH, ~/.local/bin, ~/go/bin, /opt/homebrew/bin, /usr/local/bin
tackroom.config = "/path/to/tackroom.yaml" -- passed as --config to every tackroom command
```

## Watch out

- Rex's server runs programs with the minimal PATH of a GUI app, without your shell's `PATH` or `TACKROOM_HOME`. The actions find `tackroom` as above and run `tackroom rex` with `~/.local/bin`, where Rex links its `rex` CLI, in front of that PATH. Set `tackroom.config` when your config is not `~/.agents/tackroom.yaml`.
- With more than one `tackroom` installed, for example an old copy in `~/.local/bin` and a newer one from Homebrew, the actions take the first one found, which may be the old one. Set `tackroom.bin` to the one you want.
- A Rex action has 30 seconds to finish; `tackroom_workspace` gives `tackroom rex` 25.
- `tackroom_view` uses the view that keeps its token in the default place, and starts one when none runs; a view started with another `--token-file` is not found.
- `tackroom_view` opens the page with macOS `open`. It needs `tackroom view --json`, from tackroom 3.0; with an older tackroom it fails with "Web needs a newer tackroom; upgrade it with brew or npm".
- `--config` is passed to the dash only when given; both panes open in the folder that holds the config file.

## Not planned

- Editing Rex config, `init.lua` or key bindings behind the person's back.
- A Rex panel. Rex blocks are terminals, so tackroom's view in Rex is the dash, a terminal program.
