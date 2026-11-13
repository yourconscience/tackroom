# Herdr panel

The [Herdr](https://herdr.dev) app can show tackroom's Crons tab as a native side panel: the same jobs per host, health counts, and Run now, Pause and Resume, plus Add, Edit and Delete for jobs tackroom owns. It reads the output of `tackroom cron collect` and acts through `tackroom cron run|pause|resume|add|edit|remove`, so it never has data or actions the web view lacks.

## Install

Copy the folders from the [`herdr/` directory](https://github.com/yourconscience/tackroom/tree/main/herdr) of the repository into Herdr's config folder, which `herdr/` mirrors:

```bash
cp -R herdr/mods/tackroom herdr/mods/tackroom-toggle ~/.config/herdr-app/mods/
cp herdr/layouts/tackroom.* ~/.config/herdr-app/layouts/
```

Then switch Herdr to the `tackroom` layout: Herdr's main layout (cloned from main 0.1.15) with the panel in the right column and a Crons button after Files and Changes.

To add the panel to a layout of your own, copy only `mods/tackroom` and add its block to that layout's `panel-body` stack. The recipes are [`herdr/RECIPE.md`](https://github.com/yourconscience/tackroom/blob/main/herdr/RECIPE.md) and [`herdr/mods/tackroom/RECIPE.md`](https://github.com/yourconscience/tackroom/blob/main/herdr/mods/tackroom/RECIPE.md).

tackroom never edits Herdr layouts or key bindings for you.

## What the panel shows

- A header with the time of the last collect, a Collect button and a Web button that opens the running `tackroom view` on its Crons tab.
- One line per host with a count per health: failing, last run failed, no result, healthy, paused. A host whose probe failed shows "unreachable" and the error.
- A "needs attention" list, then one section per host, sorted by health. A row opens into its command, log, schedule, last result and problems.
- Run now, Pause and Resume on launchd jobs, as in the web view. Other sources are read-only.
- Add in the header, and Edit and Delete in an open row of a job tackroom owns here. Add and Edit open a dialog with text fields for the name (add only), schedule, command, arguments (one box each, with a button to add more) and an optional effect; Delete asks first. A refusal from tackroom shows in the dialog. A job tackroom does not own, or one for another host, shows the reason instead of the buttons.

The panel collects when it opens and every `refresh` seconds (default 300, at least 60). Props on the block, all optional: `tackroom` (the program, default `tackroom` on the PATH Herdr runs programs with), `config` (a config file other than the default) and `refresh`.

## Limits

- The web view cannot be embedded: Herdr mods are native elements drawn from JavaScript, so Herdr has its own renderer over the same JSON.
- The Web button reads `view.addr` and `view.token` from tackroom's default state folder, so it does not find a view started with another `--token-file`.
- A collect probes every host over SSH. On a loaded machine the local probe can hit tackroom's 2 minute timeout, and the host shows "unreachable" until the next collect.
- After an Add, Edit, Delete or action, the panel runs a full collect and the row updates when that finishes, not at once as in the web view.

## doctor

Inside a Herdr session (`HERDR_ENV=1`), `tackroom doctor` has a `herdr plugins` check that looks for plugin commands pointing at missing files or executables, and outside Herdr it reports `skipped`. See [crons.md](crons.md#herdr-automations) for the herdr-automations check.
