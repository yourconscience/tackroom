# tackroom in Rex

[Rex](https://www.superlogical.com/rex) is the terminal multiplexer by Superlogical, with sessions, windows, blocks, a `rex` CLI and Lua configuration. tackroom works with it in two ways and never edits Rex's configuration:

- `tackroom rex` opens a tackroom workspace: a Rex session labelled `tackroom`.
- `rex/tackroom.lua`, which you add to your own Rex config, puts three tackroom actions in Rex's command palette.

Rex has no plugin or panel API: its blocks are terminals. So Rex shows your scheduled jobs as `tackroom cron collect --table`, and Run now, Pause and Resume go through the web view's Crons tab, which the **Open tackroom View on Crons** action opens.

## The workspace

`tackroom rex` (or `tackroom rex --config PATH` with the path of a non-default `tackroom.yaml`) opens a session labelled `tackroom` in the config root, with three panes:

- a shell in the config root,
- `tackroom status` on the right,
- `tackroom cron collect --table` below the status pane.

Each pane is kept open after its command ends, so the output stays readable. The panes run the same `tackroom` that opened them, by the absolute path it was started as (not resolved through symlinks, so a Homebrew link stays valid across upgrades), so an older `tackroom` earlier on your PATH cannot answer for it. `--dry-run` prints the `rex` commands without touching Rex.

It shells out to the `rex` CLI:

```
rex ls --json
rex new tackroom -c <root> --window tackroom --json
rex split -s <session> -b <shell> --split right -c <root> --keep-open --focus=false --json -- <tackroom> status
rex split -s <session> -b <status> --split below -c <root> --keep-open --focus=false --json -- <tackroom> cron collect --table
rex do session.select session_id=<session>
```

`<tackroom>` is the absolute path of the running `tackroom`. `<session>` and `<shell>` are `.session_id` and `.initial_windows[0].block_ids[0]` from `rex new`; `<status>` is `.block_ids[0]` from the first split. When `rex ls` already lists a session labelled `tackroom`, nothing is created and only the last command runs, with that session's id.

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
| `tackroom_view` | Open tackroom View on Crons | Opens the running `tackroom view` on its Crons tab in the default browser, signed in with the token it saved: `http://<view.addr>/?token=<view.token>#crons`, read from `${XDG_STATE_HOME:-~/.local/state}/tackroom`, as the Tern plugin's Web button does. Run now, Pause and Resume for launchd jobs are there. |

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
- `tackroom_view` needs a running `tackroom view` that keeps its token in the default place. The address file stays after the view stops, so a stopped view shows up as a connection error in the browser.
- `tackroom_view` opens the page with macOS `open`.
- `--config` is passed to the status and crons commands only when given; every pane opens in the folder that holds the config file.

## Not planned

- Editing Rex config, `init.lua` or key bindings behind the person's back.
- A Rex panel. Rex blocks are terminals, so job actions stay in the web view.
