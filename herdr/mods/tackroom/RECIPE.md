# tackroom

A side panel with five tabs, named and filled like the web view's: Overview, Skills, Roles, MCP & hooks and Crons. Each tab draws the JSON of a tackroom command, the same data the web view draws. Only the Crons tab acts: Run now, Pause and Resume on launchd jobs, and Add, Edit and Delete for jobs tackroom owns here.

## What you see

- **Header**, 36px: the tackroom icon, "tackroom", the time the open tab's data was loaded (or "loading…"), then two buttons: Refresh, which reloads the open tab's data at once, and Web, which opens the same tab of the running `tackroom view`.
- **Tab strip**: Overview, Skills, Roles, MCP & hooks and Crons, 12px, on `selected` for the open tab and `muted` for the rest. The strip wraps when the panel is too narrow for one line. A 6px dot after a label marks a tab that needs attention: `ansi1` for something failing, conflicting or unreadable, `ansi3` for something drifted, missing, held or out of sync. The dot shows from the first load, whichever tab is open.
- **Overview**: a headline ("All synced", "4 changes waiting" or "2 harnesses out of sync") and, when something is waiting, "Run tackroom sync to apply it, or tackroom sync --dry-run for the plan." With more than one host in `crons.hosts`, a "Machines" section comes next, one row per machine: its name, a health dot, the summary from `tackroom hosts` and, under it in `dim`, "Fix:" and the command, then the health in its tone (healthy, needs attention, failing). Then one row per harness: a state dot, its name, what it holds (what is missing or drifted first, then its skills, roles, MCP servers and hooks) and a state: synced, needs sync, a conflict count, config unreadable or not installed. A row opens into the exact names missing or drifted per surface, its conflicts and root file state. A "Held or blocked" list follows, one row per item sync skips: the item, why and "blocked" or "held"; it opens into when a hold ends and the exact command that overrides it. A line names sources a sync would fetch first. The last row is the Crons summary, and clicking it opens that tab.
- **Skills, Roles, MCP servers, Hooks**: rows with a dot for the worst state among the harnesses, the name, a tag (a skill's origin, `local` or `owner/repo@commit`; a hook's event; "turned off" for a disabled server) and how many of the targeted harnesses have it in place (`2/3`). Rows that need attention come first, then the rest by name. A row opens into its description, origin and path (skills), command and targets (MCP servers and hooks), and its state in each harness in the web view's words: in place, drifted, missing, conflict, not targeted, turned off, not supported, not installed, config unreadable.
- **Crons**: an Add cron button, the hosts, the jobs that need attention and one section per host, as before. A job row opens into its details and its buttons (Run now, Pause or Resume, Edit, Delete). A job tackroom does not own shows "No edit or delete." and the reason in `dim`.
- **The dialog**, a 420px popup of this same mod (`props().form`): a title, then text inputs for the name (Add only), schedule, command, one input per argument with a remove button and "+ Add argument", and an optional effect, each under a label that carries an example and with no placeholder, because Herdr draws placeholders nearly as bright as typed text; Cancel and Add cron or Save. Delete shows the schedule and command and a red Delete button; Enter does not confirm it. An error from tackroom shows in `ansi1` above the buttons and the dialog stays open. Esc closes it.

## How it behaves

- Loads all four sources on open, then every `refresh` seconds (default 300, at least 60). Refresh reloads only the open tab's source; the button is dimmed while it loads.
- Clicking a row opens or closes its details; each tab keeps its own scroll position.
- A launchd job offers Run now and Pause, or Resume when it is disabled or unloaded, as in the web view. Other sources have no buttons. While an action runs its button reads "…" and every action button is dimmed. A notification says whether it worked; then the Crons tab loads again.
- Web opens the open tab of the running `tackroom view` (`#overview`, `#skills`, `#roles`, `#wiring` or `#crons`) in the default browser, signed in with the token it saved.
- Add opens the dialog empty; Edit opens it filled from `declared_schedule`, `declared_command` and `effect`, and sends only the flags that changed. On success the dialog closes, a notification says so and the Crons tab loads again; on failure it loads again too, because a failed apply leaves the config changed.
- Nothing on the other tabs writes. The panel has no Sync button: `sync --dry-run --json` lists what is missing and drifted but not every removal, setting or package change a sync makes, so the panel could not show the whole plan before applying it.

## Built from

- One mod, `tackroom` (`main.js` the panel, `form.js` the dialog), `place: "none"`, put in a layout as a block (the `tackroom` layout in `herdr/layouts` already has it, with a tackroom button): `{ "type": "block", "content": { "mod": "tackroom", "props": { "tackroom": "~/.local/bin/tackroom", "config": "~/.agents/tackroom.yaml", "refresh": 300 } } }`. All props are optional: `tackroom` defaults to `tackroom` on the PATH, `config` to tackroom's default config.
- Data, each with `--config` when the prop is set:
  - Overview: `exec(tackroom, ["sync", "--dry-run", "--json"])`: `agents` (per harness, per surface: `managed`, `missing`, `drifted`; `conflicts`, `error`, `root_instructions`), `changes`, `synced`, `repo_link`, `held`, `not_fetched`. It writes and fetches nothing.
  - Skills, Roles, MCP & hooks: `exec(tackroom, ["config", "inventory"])`: `skills`, `roles`, `mcp`, `hooks` with a `states` map per harness, the document behind `/api/inventory`.
  - Crons: `exec(tackroom, ["cron", "collect"])`: `jobs` and `access`, 5-minute timeout.
  - Overview's Machines: `exec(tackroom, ["hosts", "--json"])`: `hosts`, each with `host`, `health`, `summary` and `fix`, 5-minute timeout. It probes every host in `crons.hosts` over SSH, as a crons collect does.
- Actions: `exec(tackroom, ["cron", verb, "--config", config, "--host", host, "--source", source, id])` with `verb` run, pause or resume. Flags come before the id.
- Add, edit and delete: `exec(tackroom, ["cron", "add" | "edit" | "remove", "--config", config, id, ...flags])`; the id may sit anywhere among the flags. The dialog sends `run("tackroom.reload")` afterwards, which the panel receives from `nextCommand()` and answers with a Crons load.
- Text input: `InputState` and `Input` from `gpui-base`, `openOverlay` with `kind: "dialog"`, `closeOverlay`, `notify`.
- Web: `exec("/bin/sh", ...)` reads `view.addr` and `view.token` from `${XDG_STATE_HOME:-~/.local/state}/tackroom` and runs `open`.
- `herdr` functions: `exec`, `props`, `theme`, `notify`, `openOverlay`, `closeOverlay`, `run`, `nextCommand`. Icon: `tackroom.svg`, tinted.
- Health, short names and schedule words for crons come from the JSON (`health`, `short_name`, `schedule_text`); tackroom works them out once for this panel and the web view. Every colour is a theme slot; none is fixed in the mod.

## Watch out

- Needs a tackroom with `config inventory`, `sync --dry-run --json` and `hosts --json` (without `hosts` the Machines section stays away), and a `cron collect` that carries `health`, `short_name`, `schedule_text`, `editable`, `edit_note` and `declared_command`, and has the `cron run|pause|resume|add|edit|remove` verbs for the buttons.
- `exec` does not expand `~`; the mod expands a leading `~/` itself from `$HOME`.
- Web reads `view.addr` and `view.token` from tackroom's default state folder, so it does not find a view started with another `--token-file`.
- A crons collect probes every host over SSH; on a loaded machine the local probe can hit tackroom's 2-minute timeout and the host shows unreachable until the next collect. The other two sources are local and quick.
- The Skills tab lists what the web view lists, including skills of a source sync has not pinned because it is blocked or held: they show as missing in every harness, while Overview lists the source under "Held or blocked" and counts the harnesses as synced.
- A disabled or untargeted MCP server or hook shows in its tab with the state "turned off" or "not targeted"; the Overview counts only what is in place.
- An owned job shows its declared schedule (`declared_schedule`), not the entries launchd expanded it into. Collected from a host that does not declare it, the expansion still reads as one schedule: sixteen `0 8 N * *` entries for odd days are "Every 2 days 08:00", `0 * * * *; 30 * * * *` is "Every 30 min".
- The reload after the dialog is a `<mod>.<command>` that reaches shown copies of the mod named `tackroom`; a clone under another name still loads on its timer and the Refresh button.
- A change to a job shows when the full collect that follows finishes, not at once as in the web view.

## Done when

- `herdr-app check mods/tackroom` passes. It renders only the first frame, with no data, so check copies of the mod that start with each tab open and the data of the four commands in place, with a few rows open, plus one that forces `form: { mode: "add" }`, `edit` (with a `job`) and `delete` for the dialog.
- Overview lists the same harnesses, counts and held items as `tackroom sync --dry-run`, and the same machines, summaries and fixes as `tackroom hosts`; Skills, Roles and MCP & hooks list the same rows and states as the web view's tabs; Crons lists the same jobs, per host, with the same health counts as the web Crons tab.
- Run now on a launchd job raises its run count after the next collect; Pause turns its row grey with "paused" and offers Resume; Resume brings it back.
- Add creates a job that shows in the panel and in `tackroom view`; Edit changes it; Delete asks, then removes it, its YAML entry and its unit. A job tackroom does not own shows the reason and no Edit or Delete.
