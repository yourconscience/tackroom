# tackroom in Herdr

Goal: the Herdr app and `tackroom view` show the same things and offer the same actions, so it doesn't matter which window is in front.

## Constraint

Herdr mods are native: GPUI elements described from JavaScript, with no HTML, CSS or web view (`~/.config/herdr-app/AGENTS.md`, "What you can and can't do"). The web view can't be embedded. Herdr gets its own renderer instead, and both renderers read one contract.

## Shape

```
tackroom cron collect            ->  JSON  ->  web Crons tab  (GET /api/crons)
                                           ->  Herdr mod      (exec)
tackroom cron run|pause|resume   <-  Herdr mod      (exec)
  (doCronAction, shared)         <-  web Crons tab  (POST /api/crons/action, also schedule)
```

- **One contract.** The mod runs the same `cron collect` the view serves and calls the same action code through CLI verbs. Neither side has data or actions the other can't reach.
- **No session or token in the mod.** `exec` runs as the person, with their PATH and SSH config, so the mod needs no HTTP auth. The view's token is read only to open the browser.
- **Machines.** `exec` takes `on: <machine>` and runs through herdr-core there, so a later version can show a machine Herdr knows even when it is not in `crons.hosts`.

## Phases

1. **Crons panel** (this change): `herdr/mods/tackroom`, a side panel with host health, needs attention, per-host job lists, Run now / Pause / Resume, and a button that opens the web view. `herdr/` is laid out like `~/.config/herdr-app` and also holds `layouts/tackroom.json`, Herdr's main layout with the panel and a Crons button (`mods/tackroom-toggle`). Installed by copying: the mod into a layout of your own (its `RECIPE.md`), or the whole folder for the layout (`herdr/RECIPE.md`).
2. **One implementation of the derived fields.** Health, short name and schedule words are computed in `app.js` and copied into the mod today. Move them into the collect JSON (`health`, `short_name`, `schedule_text`) so both renderers only draw. Then add schedule editing to the panel and a `tackroom herdr install` that copies the mod and adds the block to the shown layout.
3. **The other tabs.** Overview status, skills and MCP wiring from the same JSON the view's `/api/inventory` and `/api/status` serve, behind CLI verbs, as more panels in the same mod.

## Not planned

- Writing Herdr layouts or `keys.json` behind the person's back. Install edits a layout only when asked, and keeps a backup.
- A tackroom daemon for the mod. Collect runs on demand and on the panel's timer.
