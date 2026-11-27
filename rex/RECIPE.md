# tackroom in Rex

[Rex](https://www.superlogical.com/rex) is the terminal multiplexer by Superlogical, with sessions, windows, blocks, a `rex` CLI and Lua configuration. tackroom works with it in two ways and never edits Rex's configuration:

- `tackroom rex` shows or hides the tackroom panel in the Rex session it runs in.
- `rex/tackroom.lua`, which you add to your own Rex config, puts the same toggle in Rex's command palette, where you can bind it to a key.

## The panel

The panel is `tackroom dash`, a terminal program with the sync state and your scheduled jobs, with Run now, Pause and Resume for launchd jobs and Sync. Its keys are in [terminals.md](../docs/terminals.md#dash). Rex has no plugin API for custom panes, so the panel is a terminal pane in one of Rex's floating layers: at the right edge of the window, at full height, over the panes there, which keep their size.

Run `tackroom rex` (or `tackroom rex --config PATH` with the path of a non-default `tackroom.yaml`) in a Rex pane:

- The first run opens the panel in that pane's window and focuses it.
- The next run hides it, and the one after shows it again. Hidden, the dash keeps running but skips its timed refreshes, so a hidden panel does not probe your hosts over SSH every 5 minutes. Showing it presses F5 in it, so it refreshes at once; unlike `r`, F5 leaves a question waiting in the dash alone.
- `q` in the dash closes the panel.
- Run from another window of the same session, it closes the panel there and opens it in this one, so a session has one panel.

It creates no session or window and renames nothing. It finds its session and window by `REX_SESSION` and `REX_BLOCK`, which Rex sets in every pane, so outside Rex it is an error. The panel aims for 62 columns, within 30% to 60% of the window's width. The dash runs the same `tackroom` that opened it, by the absolute path it was started as (not resolved through symlinks, so a Homebrew link stays valid across upgrades), so an older `tackroom` earlier on your PATH cannot answer for it. Its pane is labelled `tackroom`, which is how a later run finds the panel.

It calls the Rex server API through the `rex` CLI:

```
rex api call -s <session> session.view
rex block call -s <session> -b <pane> size {}
rex api call -s <session> session.new_layer '{"window_id":"<window>","bounds":[<x>,0,<w>,1],"focus":true,"layout":{"block":{"flavor":"com.superlogical.terminal.shell","label":"tackroom","options":{"command":["<tackroom>","dash"],"cwd":"<root>"}}}}'
rex api call -s <session> session.set_layer_state '{"layer_id":"<panel>","state":"minimized"}'
rex api call -s <session> session.set_layer_state '{"layer_id":"<panel>","state":"normal","focus":true}'
rex send-key -s <session> -b <panel pane> f5
rex api call -s <session> session.close_layer '{"layer_id":"<panel>"}'
```

The first two only read: the session's windows and panes, and the width in columns of a pane of the window, from which the panel's width follows. One of the others runs: `new_layer` opens the panel, `set_layer_state` hides or shows it (showing it then sends F5 to the dash), and `close_layer` takes it out of another window before `new_layer` opens it in this one. `--dry-run` runs the reads and prints the command that would change the panel.

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
   rex config check   # should list 2 more actions and no errors
   rex config reload
   ```

### What each action does

They are under **tackroom** in the command palette. From a shell, `rex do NAME` runs one.

| Action | Palette title | Does |
| --- | --- | --- |
| `tackroom_panel` | Toggle tackroom Panel | Runs `tackroom rex` for the window the palette was opened in: opens, hides or shows the panel. |
| `tackroom_view` | Open tackroom View on Crons | Runs `tackroom view --json`, which starts the view when none runs, and opens its `url` plus `#crons` in the default browser, as the Tern plugin's Web button does. Run now, Pause and Resume for launchd jobs are there. |

### Key binding (optional)

In `init.lua`, after the `require`:

```lua
rex.bind("ctrl+cmd+t", "tackroom_panel")
```

Rex's default key map leaves `ctrl+cmd+t` free; `rex keymap` lists what your config binds.

### Settings (optional)

```lua
local tackroom = require("tackroom")
tackroom.bin = "/path/to/tackroom"         -- default: the first of Rex's PATH, ~/.local/bin, ~/go/bin, /opt/homebrew/bin, /usr/local/bin
tackroom.config = "/path/to/tackroom.yaml" -- passed as --config to every tackroom command
```

## Watch out

- Rex's server runs programs with the minimal PATH of a GUI app, without your shell's `PATH` or `TACKROOM_HOME`. The actions find `tackroom` as above and run `tackroom rex` with `~/.local/bin`, where Rex links its `rex` CLI, in front of that PATH. Set `tackroom.config` when your config is not `~/.agents/tackroom.yaml`.
- With more than one `tackroom` installed, for example an old copy in `~/.local/bin` and a newer one from Homebrew, the actions take the first one found, which may be the old one. Set `tackroom.bin` to the one you want.
- A Rex action has 30 seconds to finish; `tackroom_panel` gives `tackroom rex` 25.
- `tackroom_view` uses the view that keeps its token in the default place, and starts one when none runs; a view started with another `--token-file` is not found.
- `tackroom_view` opens the page with macOS `open`. It needs `tackroom view --json`, from tackroom 3.0; with an older tackroom it fails with "Web needs a newer tackroom; upgrade it with brew or npm".
- `--config` is passed to the dash only when given; the dash runs in the folder that holds the config file.

## Not planned

- Editing Rex config, `init.lua` or key bindings behind the person's back.
- A Rex panel beyond the dash. Rex panes are terminals, so tackroom's view in Rex is a terminal program.
