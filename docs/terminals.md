# Terminals

tackroom has integrations for tmux, zellij, Herdr, Tern and Rex, and a recipe for cmux. Each integration is optional and calls the same `tackroom` commands you can run in any shell. There are three kinds of integration:

- A panel is tackroom's own interface inside the terminal.
- A workspace command opens a session in the terminal with a shell and [`tackroom dash`](#dash).
- In Rex, `tackroom rex` toggles a panel instead: `tackroom dash` floating at the right of the window you are in, with no session of its own.
- A recipe is a snippet you add or run yourself.

| Tool | Panel | Workspace command | Recipe |
| --- | --- | --- | --- |
| <img src="site/marks/tmux.svg" alt="tmux logo" width="20" height="20"> [tmux](#tmux) | - | `tackroom tmux` | [popup](#popup) with `tackroom status` |
| <img src="site/marks/zellij.svg" alt="zellij logo" width="17" height="20"> [zellij](#zellij) | - | `tackroom zellij` | - |
| <img src="site/marks/herdr.svg" alt="Herdr logo" width="20" height="20"> [Herdr](#herdr) | [tackroom panel](#herdr-panel) in the Herdr app; the Herdr TUI has none | - | - |
| <img src="site/marks/tern.png" alt="Tern logo" width="20" height="20"> [Tern](#tern) | crons panel | `tackroom tern` | - |
| <img src="site/marks/rex.png" alt="Rex logo" width="20" height="20"> [Rex](#rex) | [`tackroom rex`](#rex), a floating `tackroom dash` you show and hide | - | palette actions |
| [cmux](#cmux) | - | - | `cmux new-workspace`, not run here |

## Dash

`tackroom dash` is a live screen for a terminal pane. The three workspace commands open it next to a shell, above it in tmux and zellij and below it in Tern, and `tackroom rex` opens it as a panel in Rex. It answers two questions at a glance: is everything in sync, and which scheduled jobs fail.

```bash
tackroom dash [--config PATH]
```

It has the shape of the [Herdr panel](#herdr-panel)'s Overview, drawn in the terminal's own colors. From the top:

- A title bar with the time of the last refresh, or `refreshing…` while one runs.
- The answer: a dot and how many things fail and how many need attention, such as `1 failing, 5 need attention`, or `All clear`, counted as the [Herdr panel](#herdr-panel)'s Overview counts them, so the two agree. Failing: a failing cron, a harness with a conflict or a config that could not be read, a failing machine, and a host the crons collect could not reach that is not already counted as a machine. Needs attention: a cron whose last run failed, a sync waiting (a harness that drifted, or the `~/.agents` link), each machine that needs attention, a source blocked by the audit or a gate, and status, crons or machines that could not be read at all. Under it, in dim: whether the harnesses are in sync or which need a sync, what the failures are when they are one kind, sources that are blocked, cron sources that could not be read, and what is still being checked.
- The counts: skills, roles, MCP servers and hooks in the config, and the scheduled jobs, red when one fails and yellow when a last run failed, in a rounded box of five cells, or one line in a pane narrower than 43 columns.
- The harnesses: how many are in sync, a line for each one that drifted or failed with what is wrong, then the rest as a grid with `✓`, those not detected last and dim. Then how many items are in cooldown or blocked, and the lock check when it did not pass.
- The machines, when `crons.hosts` names more than this one: how they stand, then a line each, with what `tackroom hosts` says is wrong, or the tackroom version and config commit of one that is fine.
- The jobs in three groups, each with its title and count: Failing, Last run failed, and Everything else (just Crons when nothing fails). The groups come from the job health the web view uses: a job with problems is failing, a job whose last exit or last status says it failed has a last run that failed, and a paused job is neither, whatever its last exit. A row has a dot (red, yellow, dim for the rest, hollow for a paused job or one that has not run), the job's name (a launchd label without its reverse-DNS prefix, any other job's id), the schedule in words in a wide pane, the host, the last run (`2m ago`, `3h ago`, `8d ago`) and the result (`exit N` or the last status, or `problem` for a job with problems and no failed exit). The selected row has a bar and, when the terminal says what its background is, a tint of it. The list scrolls and says how many more rows are above or below; with room to spare the groups get a blank line between them.
- At the bottom, the selected job in a rounded card with its id in the border: its schedule, source, state and host, its last run, and its problems, wrapped. The card keeps its place whichever job is selected, so the list does not move with the cursor.
- A message line with a question, the result of the last action or sync, or else the error of a refresh, and the keys.

Rows are cut, never wrapped, a pane wider than 104 columns keeps the content to 100, and a resized pane is drawn again at once. In a narrow pane the jobs lose the schedule, then the last run, then the host, and a long name is shortened in the middle. As the pane gets shorter the blank lines go first, then the counts become one line (`34 skills · 6 roles · 2 MCP · 2 hooks · 20 crons`), then the harnesses and the machines (`Harnesses  11 in sync · 1 need a sync`, `Machines  1 need attention`), then the card becomes two plain lines; then the counts, the harnesses, the detail and the title bar go, and the message line takes the place of the keys whenever it has something to say; last the answer goes and the list shrinks down to the selected job. From 2 lines up, a question waiting for y or n and the selected job are always on screen.

| Key | Action |
| --- | --- |
| `↑` `↓`, `k` `j` | Select a job; `g` and `G`, or Home and End, go to the first and the last |
| Enter | Run the selected job now; for a paused job (disabled or not loaded), only `p` |
| `p` | Pause the selected job, or resume it when it is disabled or not loaded |
| `r` | Refresh everything now, and clear the last result from the message line |
| F5 | Refresh everything now, and leave a question and the last result in place; showing a hidden [Rex panel](#rex) presses it |
| `s` | Run `tackroom sync` in a process of its own, then show what it changed or why it failed; if the dash quits meanwhile, the sync still runs to the end |
| `q` | Quit; while an action or a sync runs, quit when it ends and print its outcome |
| Ctrl-C | Quit at once; a sync already started still runs to the end |

Enter, `p` and `s` ask first in the message line, for example `run ai.knowledge-sync on m1 now? y/n`; any key other than `y` cancels. At `y` the job must still be in the last scan, or nothing runs and the message line says to press `r`. Run now, Pause and Resume work on launchd jobs, on this machine or on a host in `crons.hosts`, and run the same `launchctl` commands as the web view ([crons.md](crons.md#run-pause-and-resume-jobs)), which offers the same ones: Resume alone for a paused job. For any other job, Enter and `p` say they work on launchd jobs only, without asking. A launchd service, a job that runs on load or stays alive rather than on a schedule, is not paused here: the scan leaves a disabled service out, so nothing would be left to resume.

The dash refreshes while it runs: the sync state every 30 seconds, which reads only this machine, and the crons and the machines every 5 minutes and on `r`, the crons also after Run now, Pause or Resume (the job's host). In a Rex pane that is minimized, such as a hidden [Rex panel](#rex), the timed refreshes are skipped; the dash asks Rex at each one, through the `REX_SESSION`, `REX_BLOCK` and `REX_BIN_DIR` it sets in every pane, and refreshes as usual when Rex does not answer within 5 seconds. A crons collect and the machines check probe every host over SSH and can take a while; the screen stays usable meanwhile. A refresh that fails leaves the last data on screen, marked: the line under the answer says when the harnesses or the crons are from. Its error shows in the message line when no question or result is there (`r` clears the last result), and goes when that refresh next succeeds. Warnings the collectors print are kept off the screen; `tackroom status` prints them. A sync from the dash never asks anything on the terminal: it runs with no terminal at all, so when git, ssh or an ssh command of your own would ask for a password or about an unknown host key, the clone or fetch fails instead, and the message line shows the error with the line from git or ssh that says why. Its own output stays off the screen; `tackroom sync` in a shell prints it.

It needs a terminal on stdout; otherwise it prints `tackroom dash needs a terminal; use tackroom status and tackroom cron collect --table` and exits 1. It follows `NO_COLOR`.

## tmux

<img src="site/marks/tmux.svg" alt="tmux logo" width="20" height="20"> [tmux](https://github.com/tmux/tmux) is the terminal multiplexer. `tackroom tmux` opens a tackroom workspace in it.

```bash
tackroom tmux [--config PATH] [--dry-run]
```

It creates a session named `tackroom` in the config root with two panes: [`tackroom dash`](#dash) across the full width on top, with 70% of the height, and a shell below it, which has the focus. Then it shows the session. Outside tmux it attaches to it; inside tmux it switches your client to it; with no terminal on stdin or stdout it prints the `tmux attach-session` command instead.

The dash pane runs the same `tackroom` that opened it, by its absolute path, and keeps a shell open when the dash ends, so an error stays readable.

A second run shows the existing `tackroom` session instead of creating another. `--dry-run` prints the `tmux` commands and runs nothing. `--config PATH` takes a config file, as every other command does, and opens the session in its folder. Without it the root follows `$TACKROOM_HOME`, else `~/.agents`. An absent `tmux` is an error.

Checked with tmux 3.7c on macOS; not checked on Linux.

### Popup

To see `tackroom status` over what you are working on, bind a key in `~/.tmux.conf`:

```
bind-key T display-popup -w 90% -h 80% 'tackroom status'
```

Press the prefix key, then `T`. The popup stays open after the command ends and Escape closes it. Adding `-E` would close it as soon as the command ends, before you can read it. `tackroom` must be on the `PATH` of the tmux server. Checked with tmux 3.7c.

## zellij

<img src="site/marks/zellij.svg" alt="zellij logo" width="17" height="20"> [zellij](https://github.com/zellij-org/zellij) is a terminal workspace with layouts written in KDL. `tackroom zellij` opens a tackroom workspace in it.

```bash
tackroom zellij [--config PATH] [--dry-run]
```

It writes this layout to a temporary file, never into zellij's own config folder, with the config root and the path of the running `tackroom` filled in:

```kdl
layout {
    cwd "/home/you/.agents"
    pane split_direction="horizontal" {
        pane name="dash" size="70%" command="/usr/local/bin/tackroom" { args "dash"; }
        pane name="shell" focus=true
    }
}
```

Run inside zellij, it adds the workspace to the session you are in as a tab named `tackroom`, and each run adds another tab. Run outside zellij, it attaches to the session named `tackroom` if there is one, and otherwise starts that session with the layout.

zellij needs a terminal to create or attach to a session, so with no terminal on stdin or stdout `tackroom zellij` prints the `zellij` command to run instead. The dash pane stays open when the dash ends, with its exit code in the pane frame.

`--dry-run` prints the layout and the `zellij` commands and runs nothing. `--config PATH` takes a config file, as every other command does, and opens the session in its folder. Without it the root follows `$TACKROOM_HOME`, else `~/.agents`. An absent `zellij` is an error.

The first time zellij runs it shows its own welcome wizard, which tackroom does not control; Escape dismisses it without installing anything. Checked with zellij 0.46.0 on macOS; not checked on Linux.

## Herdr

<img src="site/marks/herdr.svg" alt="Herdr logo" width="20" height="20"> [Herdr](https://herdr.dev) is two separate products: the Herdr app, a desktop app, and the Herdr TUI, the original terminal program. Herdr's [FAQ](https://herdr.dev/faq) says the TUI keeps working and that the two do not share agents. tackroom's panel is for the app.

### Herdr app

The Herdr app is herdr-core, which keeps terminals alive when the window closes, plus herdr-app, the desktop app. Its configuration is JSON in `~/.config/herdr-app` (layouts, `keys.json`, `settings.json`) and its mods are JavaScript. tackroom's panel is two of those mods.

#### Herdr panel

The [Herdr](https://herdr.dev) app can show tackroom as a native side panel with five tabs named like the web view's: Overview, Skills, Roles, MCP & hooks and Crons. Each tab draws the JSON of a tackroom command, the same data the web view draws.

The panel changes what the web view changes:

- Sync applies the plan it shows first.
- Harnesses, MCP servers and hooks turn on and off, and MCP servers and hooks add or remove an agent.
- Crons runs, pauses, adds, edits and deletes jobs.

Every change goes through a tackroom command, so the panel never has data or actions the web view lacks.

#### Install

The panel is two Herdr mods, `tackroom` and `tackroom-toggle`, in [`herdr/tackroom/`](https://github.com/yourconscience/tackroom/tree/main/herdr/tackroom), with a [recipe](https://github.com/yourconscience/tackroom/blob/main/herdr/tackroom/RECIPE.md) that adds them to the layout you already use: a tackroom button after Files and Changes, and the panel in the right column.

- It needs tackroom 2.4 or newer on the PATH Herdr runs programs with; otherwise set the block's `tackroom` prop to its path.
- Sync and the on and off buttons need a tackroom with `config set` and `sync --plan-digest`, tackroom 2.6 or newer; with an older one they stay hidden and only the Crons tab changes anything.

From Herdr's mod gallery, paste this line to the agent you use in Herdr:

```
Read ~/.config/herdr-app/AGENTS.md, then add this mod: herdr.dev/mods/yourconscience/tackroom--herdr--tackroom
```

The agent reads the recipe and both mods, tells you what they run, copies them into Herdr's `mods/` folder, adds the button and the panel to your layout (cloning it first when it is one of Herdr's own layouts, which are read-only) and runs `herdr-app check`. The listing appears on [herdr.dev/mods](https://herdr.dev/mods) once the repository has the `herdr-mod` topic.

By hand, from a checkout of this repository:

```bash
cp -R herdr/tackroom/mods/* ~/.config/herdr-app/mods/
```

Then follow "Add to your window" in the recipe: put the `tabs` block in a row with a `tackroom-toggle` block, and add a `tackroom` block to the `panel-body` stack. Use `$HERDR_APP_HOME` in place of `~/.config/herdr-app` if you set it.

tackroom never edits Herdr layouts or key bindings for you.

#### Update

Paste the same line to your agent again, or copy the mods again from an updated checkout with the `cp` above. The layout edit is one-time: an update replaces only the two mod folders. The gallery serves the repository's `main` branch; Herdr pins no version.

If you copied the `tackroom` layout that earlier releases shipped in `herdr/layouts/`, it keeps working, since it already has the button and the panel. To update, copy only the mods.

#### What the panel shows

- **Header.** The tackroom mark and name, a pill with the short hash of your config's commit (from 340 px of panel width; from 440 px the config root comes with it, "~/.agents · 9b13be6"), the tackroom version, a Refresh button that reloads the open tab and a Web button that opens the same tab of `tackroom view`, starting the view when none runs. A count after a tab's name marks a tab that needs attention, in the colour for failing or for needs attention.
- **Overview.** It draws `tackroom sync --dry-run --json`, which writes and fetches nothing, `tackroom hosts --json`, `tackroom config inventory` and `tackroom cron collect`. From the top:
  - the answer, one line such as "2 failing, 1 needs attention" or "All clear", with a filled Sync button at its right while a sync waits;
  - a counts strip with skills, roles, MCP servers, hooks and crons, each opening its tab; a number takes the colour for needs attention or failing when something of that kind is wrong;
  - a harness grid: one row per installed harness with Herdr's own icon for it, its name and one cell each for skills, roles, MCP servers and hooks. A cell is a dot, green when everything on that surface is in place, yellow when something is missing or drifted, red on a conflict or an unreadable config, or a dash where the harness does not take that surface (a middle dot while the inventory has not told yet). Failing harnesses come first, then those that need attention; past six rows the ones in sync fold into "+N more in sync". A row opens into its counts and Turn off;
  - "Failing" and "Needs attention", each with its count and with the fix or the actions on every row: a cron, a harness with a conflict, the sync waiting and the harnesses that need it, a source the audit blocked, and one row for each machine in `crons.hosts` with its summary and the command that fixes it (only with more than one host). A cron row has Run now, Pause and Log, and Details opens the rest;
  - "Outside skills" as chips: how many sources are pinned, one chip for each with audit findings, and how many are in cooldown or blocked. A chip opens the Skills tab;
  - "Everything else": the healthy crons, the config commit and tackroom version the machines share, what is in cooldown with the command that overrides it, and the harnesses not installed or turned off, with Turn on.
- **Buttons and footer.** Actions are quiet bordered buttons, and a row of them wraps on a narrow panel. Under every tab a footer stays in place: the state of the config root on this Mac ("~/.agents · 1 uncommitted" or "~/.agents · clean") and when the open tab's data was loaded.
- **Sync.** When something waits, Overview has a Sync button. It opens the plan in a dialog: each change once, with the harnesses it touches under it, and in red what the sync removes or overwrites. The dialog's button reads Sync and remove when it removes something.
  - It runs `tackroom sync --plan-digest`, so when the plan or the config changed since the plan loaded, nothing is applied, the dialog says so and the panel loads the new plan. The dialog then leaves only Cancel, and Sync on Overview opens the new plan.
  - An edit inside a skill or role file that leaves the plan's lines the same is not noticed.
- **Skills, Roles, MCP & hooks.** They draw `tackroom config inventory`, the document behind the web view's `/api/inventory`.
  - One row per item with its worst state, a tag (a skill's origin, a hook's event) and how many harnesses have it in place. A row opens into the state in each harness, with the web view's words.
  - An MCP server's or hook's row also has Turn off or Turn on, and Add or Remove beside each harness that supports it, as the web view's switches and matrix cells do.
- **Crons.** It draws `tackroom cron collect`.
  - One line per host with a count per health (failing, last run failed, no result, healthy, paused; a host whose probe failed shows "unreachable" and the error), a "needs attention" list, then one section per host, sorted by health.
  - A row opens into its command, effect, log, schedule, last result and problems. Run now, Pause and Resume on launchd jobs, as in the web view; other sources are read-only.
  - Add cron at the top, and Edit and Delete in an open row of a job tackroom owns here. Add and Edit open a dialog with text fields for the name (add only), schedule, command, arguments (one box each, with a button to add more) and an optional effect; Delete asks first.
  - A refusal from tackroom shows in the dialog. A job tackroom does not own, or one for another host, shows the reason instead of the buttons.

The panel loads all four when it opens and every `refresh` seconds (default 300, at least 60). Props on the block, all optional:

| Prop | Meaning |
|---|---|
| `tackroom` | The program, default `tackroom` on the PATH Herdr runs programs with |
| `config` | A config file other than the default |
| `refresh` | Seconds between loads |

Turn on, Turn off, Add and Remove run `tackroom config set`, which changes `tackroom.yaml`, or `tackroom.local.yaml` for an entry defined there, as the web view's switches do. The change then waits in Overview until you Sync; nothing syncs on its own. Turning a hook off, or removing a harness from it, shows in the plan as that hook's removal from the harness.

#### Limits

- The web view cannot be embedded: Herdr mods are native elements drawn from JavaScript, so Herdr has its own renderer over the same data.
- The web view's Unmanaged and Config tabs are not in the panel.
- The Skills tab lists skills of a source sync has not pinned because it is blocked or in cooldown as missing in every harness, as the web view does; Overview shows a blocked source under Needs attention and a source in cooldown as a chip and under Everything else.
- The Web button runs `tackroom view --json`, which uses tackroom's default token file, so it does not find a view started with another `--token-file`; it starts one of its own instead.
- A collect probes every host over SSH. On a loaded machine the local probe can hit tackroom's 2 minute timeout, and the host shows "unreachable" until the next collect.
- After an Add, Edit, Delete or action, the Crons tab runs a full collect and the row updates when that finishes, not at once as in the web view.

### Herdr TUI

The Herdr TUI is `herdr`, the original Herdr: a client and a background server that run in any terminal. It is configured in `~/.config/herdr/config.toml`, and its plugins are folders with a `herdr-plugin.toml`. tackroom has no panel for it, because the panel is made of app mods, which the TUI does not load.

`tackroom doctor` checks the TUI's plugins. Inside a Herdr session (`HERDR_ENV=1`), its `herdr plugins` check runs `herdr plugin list --json` and looks for plugin commands pointing at missing files or executables, and outside Herdr it reports `skipped`.

tackroom has not checked whether terminals in the Herdr app set `HERDR_ENV`. See [crons.md](crons.md#herdr-automations) for the herdr-automations check.

## Tern

<img src="site/marks/tern.png" alt="Tern logo" width="20" height="20"> [Tern](https://stencil.so/tern) is the multiplexing terminal by Stencil Labs (`/Applications/Tern.app` on macOS, with a `tern` CLI). tackroom integrates with it optionally. Other tools that also install a `tern` command are not supported.

### Shipped

- `tackroom doctor` checks Tern: installed, version, runnable (`checkTern`). An absent Tern reports `skipped` (a pass), never a failure. A `tern` on PATH whose `--version` is not Tern's `tern VERSION (COMMIT)` also reports `skipped`.
- `tackroom tern` opens a tackroom workspace in Tern: a "tackroom" session in the config root with a shell pane above a [`tackroom dash`](#dash) pane, in equal halves, because Tern splits only right and down and has no size option. The window then shows that session. Recipe: [`tern/RECIPE.md`](../tern/RECIPE.md).
  - The dash pane runs the same `tackroom` that opened it, by its absolute path.
  - `--dry-run` prints the `tern` commands; `--config PATH` takes a config file, as every other command does, and opens the session in its folder. Without it the root follows `$TACKROOM_HOME`, else `~/.agents`.
- A crons panel: the `tackroom-crons` Tern plugin shows `tackroom cron collect` as a block, host health and per-host jobs, with Run now / Pause / Resume for launchd jobs, through the same `tackroom cron run|pause|resume` verbs as the web view and the Herdr panel.
  - Install from a checkout with `tern plugin link ./tern/plugins/tackroom-crons` (or `tern plugin install` for a copy), then open it from the palette or `Ctrl+Alt+C`. Recipe: [`tern/plugins/tackroom-crons/RECIPE.md`](../tern/plugins/tackroom-crons/RECIPE.md).

### Planned

- `tackroom tern install` to copy a layout into Tern's config, backing up first.

### Constraint

Tern panes are terminals. Integration goes through the `tern` CLI, the plugin API, and `tackroom` CLI verbs, and never edits Tern config behind the person's back.

## Rex

<img src="site/marks/rex.png" alt="Rex logo" width="20" height="20"> [Rex](https://www.superlogical.com/rex) is the terminal multiplexer by Superlogical, with sessions, windows, blocks, a `rex` CLI and Lua configuration. tackroom integrates with it optionally.

### Shipped

- `tackroom doctor` checks Rex (`checkRex`): it runs `rex version`. With Rex absent it reports `skipped`, which counts as a pass. With Rex installed but not runnable it warns. Otherwise it prints the version line.
- `tackroom rex` shows or hides the tackroom panel in the Rex session it runs in: [`tackroom dash`](#dash) in a floating pane at the right edge of the window, at full height. Recipe: [`rex/RECIPE.md`](../rex/RECIPE.md).
  - The first run opens the panel and focuses it. The next run hides it and the one after shows it again; hidden, the dash keeps running but skips its timed refreshes, so a hidden panel does not probe your hosts, and showing it again presses F5 in it so it refreshes at once. `q` in the dash closes the panel.
  - A session has one panel. Run from another window of the session, `tackroom rex` closes the panel there and opens it in this window.
  - The panel aims for 62 columns, within 30% to 60% of the window's width. It floats over the panes on the right instead of resizing them, so nothing reflows when it shows or hides.
  - It creates no session or window and renames nothing. It finds its session and window by `REX_SESSION` and `REX_BLOCK`, which Rex sets in every pane; outside Rex it is an error, as is an absent `rex`.
  - The dash runs the same `tackroom` that opened it, by its absolute path, in the config root. Its pane is labelled `tackroom`, which is how a later run finds the panel.
  - `--dry-run` prints the `rex` command that would change the panel; `--config PATH` takes a config file, as every other command does.
- Palette actions: `rex/tackroom.lua`, which you `require` from Rex's `init.lua`, adds two actions under "tackroom" in the command palette. Install: [`rex/RECIPE.md`](../rex/RECIPE.md#palette-actions).
  - Toggle tackroom Panel (`tackroom rex` for the window the palette was opened in), which you can bind to a key.
  - Open tackroom View on Crons (`tackroom view` on its Crons tab in the default browser, started when none runs).
- Crons with actions: the panel's [dash](#dash) lists the jobs and has Run now, Pause and Resume for launchd jobs. The web view has them too; Open tackroom View on Crons opens its Crons tab ([crons.md](crons.md)).

### Constraint

Rex blocks are terminals, and Rex has no plugin API for custom panes. The panel is a terminal pane in one of Rex's floating layers. Integration goes through the `rex` CLI, Lua actions the person adds to their own `init.lua`, and `tackroom` CLI verbs. tackroom never reads or writes Rex's configuration.

## cmux

[cmux](https://cmux.com) is a terminal app for macOS with a `cmux` CLI. tackroom has no `tackroom cmux` command and does not check for cmux. This recipe is taken from cmux's own documentation ([custom commands](https://cmux.com/docs/custom-commands) and `cmux new-workspace --help`) and has not been run here.

```bash
ROOT="${TACKROOM_HOME:-$HOME/.agents}"
cmux new-workspace --name tackroom --cwd "$ROOT" --focus true --layout '{"direction":"horizontal","split":0.5,"children":[
  {"pane":{"surfaces":[{"type":"terminal","name":"shell"}]}},
  {"direction":"vertical","children":[
    {"pane":{"surfaces":[{"type":"terminal","name":"status","command":"tackroom status"}]}},
    {"pane":{"surfaces":[{"type":"terminal","name":"crons","command":"tackroom cron collect --table"}]}}]}]}'
```

In cmux's layout, `horizontal` puts the children side by side and `vertical` stacks them. Whether a pane stays open after its command ends, and whether `--cwd` applies to every pane, is not known.

By default cmux accepts commands only from processes started inside cmux, so run this from a cmux terminal. From any other terminal it answers `Access denied - only processes started inside cmux can connect`. That default is the `cmuxOnly` socket mode, and cmux's settings list other modes.

## Other terminals

WezTerm, kitty, Ghostty, iTerm2 and Warp can each run the tmux or zellij workspace above: open a tab and run `tackroom tmux` or `tackroom zellij`. tackroom has no integration of its own for them and has not tested them.

## Credits

The marks on this page belong to their owners. Their sources and terms are in [NOTICES.md](site/marks/NOTICES.md).
