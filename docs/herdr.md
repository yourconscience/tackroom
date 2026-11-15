# Herdr panel

The [Herdr](https://herdr.dev) app can show tackroom as a native side panel with five tabs named like the web view's: Overview, Skills, Roles, MCP & hooks and Crons. Each tab draws the JSON of a tackroom command, the same data the web view draws, and the panel changes what the web view changes: Sync applies the plan it shows first, harnesses, MCP servers and hooks turn on and off, MCP servers and hooks add or remove an agent, and Crons runs, pauses, adds, edits and deletes jobs. Every change goes through a tackroom command, so the panel never has data or actions the web view lacks.

## Install

The panel is two Herdr mods, `tackroom` and `tackroom-toggle`, in [`herdr/tackroom/`](https://github.com/yourconscience/tackroom/tree/main/herdr/tackroom), with a [recipe](https://github.com/yourconscience/tackroom/blob/main/herdr/tackroom/RECIPE.md) that adds them to the layout you already use: a tackroom button after Files and Changes, and the panel in the right column. It needs tackroom 2.4 or newer on the PATH Herdr runs programs with; otherwise set the block's `tackroom` prop to its path. Sync and the on and off buttons need a tackroom with `config set` and `sync --plan-digest`, tackroom 2.6 or newer; with an older one they stay hidden and only the Crons tab changes anything.

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

## Update

Paste the same line to your agent again, or copy the mods again from an updated checkout with the `cp` above. The layout edit is one-time: an update replaces only the two mod folders. The gallery serves the repository's `main` branch; Herdr pins no version.

If you copied the `tackroom` layout that earlier releases shipped in `herdr/layouts/`, it keeps working, since it already has the button and the panel. To update, copy only the mods.

## What the panel shows

- A header with the time the open tab's data was loaded, a Refresh button that reloads it and a Web button that opens the same tab of the running `tackroom view`. A dot after a tab's name marks a tab that needs attention.
- **Overview**: whether everything is synced or how many changes wait, a Machines section with one row per machine in `crons.hosts` (its health, a summary and the command that fixes it; only with more than one host), one row per harness (what is missing or drifted, then how many skills, roles, MCP servers and hooks it has, and its state) that opens into the exact names, a "Held or blocked" list of what sync skips with the command that overrides it, and a line for the Crons tab. An open harness row has Turn off; a harness turned off in the config is listed with Turn on. It draws `tackroom sync --dry-run --json`, which writes and fetches nothing, and `tackroom hosts --json`.
- **Sync**: when something waits, Overview has a Sync button. It opens the plan in a dialog: each change once, with the harnesses it touches under it, and in red what the sync removes or overwrites. The dialog's button reads Sync and remove when it removes something. It runs `tackroom sync --plan-digest`, so when the plan or the config changed since the plan loaded, nothing is applied, the dialog says so and the panel loads the new plan; the dialog then leaves only Cancel, and Sync on Overview opens the new plan. An edit inside a skill or role file that leaves the plan's lines the same is not noticed.
- **Skills, Roles, MCP & hooks**: one row per item with its worst state, a tag (a skill's origin, a hook's event) and how many harnesses have it in place. A row opens into the state in each harness, with the web view's words. An MCP server's or hook's row also has Turn off or Turn on, and Add or Remove beside each harness that supports it, as the web view's switches and matrix cells do. They draw `tackroom config inventory`, the document behind the web view's `/api/inventory`.
- **Crons**: one line per host with a count per health (failing, last run failed, no result, healthy, paused; a host whose probe failed shows "unreachable" and the error), a "needs attention" list, then one section per host, sorted by health. A row opens into its command, effect, log, schedule, last result and problems. Run now, Pause and Resume on launchd jobs, as in the web view; other sources are read-only. Add cron at the top, and Edit and Delete in an open row of a job tackroom owns here. Add and Edit open a dialog with text fields for the name (add only), schedule, command, arguments (one box each, with a button to add more) and an optional effect; Delete asks first. A refusal from tackroom shows in the dialog. A job tackroom does not own, or one for another host, shows the reason instead of the buttons. It draws `tackroom cron collect`.

The panel loads all four when it opens and every `refresh` seconds (default 300, at least 60). Props on the block, all optional: `tackroom` (the program, default `tackroom` on the PATH Herdr runs programs with), `config` (a config file other than the default) and `refresh`.

Turn on, Turn off, Add and Remove run `tackroom config set`, which changes `tackroom.yaml`, or `tackroom.local.yaml` for an entry defined there, as the web view's switches do. The change then waits in Overview until you Sync; nothing syncs on its own. Turning a hook off, or removing a harness from it, shows in the plan as that hook's removal from the harness.

## Limits

- The web view cannot be embedded: Herdr mods are native elements drawn from JavaScript, so Herdr has its own renderer over the same data.
- The web view's Unmanaged and Config tabs are not in the panel.
- The Skills tab lists skills of a source sync has not pinned because it is blocked or held as missing in every harness, as the web view does; Overview lists the source under "Held or blocked".
- The Web button reads `view.addr` and `view.token` from tackroom's default state folder, so it does not find a view started with another `--token-file`.
- A collect probes every host over SSH. On a loaded machine the local probe can hit tackroom's 2 minute timeout, and the host shows "unreachable" until the next collect.
- After an Add, Edit, Delete or action, the Crons tab runs a full collect and the row updates when that finishes, not at once as in the web view.

## doctor

Inside a Herdr session (`HERDR_ENV=1`), `tackroom doctor` has a `herdr plugins` check that looks for plugin commands pointing at missing files or executables, and outside Herdr it reports `skipped`. See [crons.md](crons.md#herdr-automations) for the herdr-automations check.
