# tackroom crons

A side panel that shows every scheduled job tackroom collects, on every host in `crons.hosts`: how each host is doing, what needs attention, and each job's schedule and last run. A job opens into its details with Run now, Pause and Resume, and, for a job tackroom owns here, Edit and Delete. An Add button in the header declares a new one. It shows the same data as the Crons tab of `tackroom view` and acts through the same commands.

## What you see

- **Header**, 36px, a bottom border: a clock icon, "Crons", the time of the last collect (or "collecting…"), then three buttons: Add, Collect and Web.
- **Hosts**, one line each: the host's name ("This Mac" for the local one) in a 84px column, then a colored dot and count per health: failing (`ansi1`), last run failed (`ansi3`), no result (`dim`), healthy (`ansi2`), paused (`muted`). The counts wrap. A host whose probe failed shows "unreachable" and the error in `ansi1`.
- **Needs attention**: jobs with problems, or `dead` or `missing`, and not paused. Each row shows the host instead of the schedule.
- **One section per host**: its jobs sorted failing, last run failed, no result, healthy, paused. A row is a health dot, the short name (`com.example.backup` shows as `backup`), the schedule in words (at most 120px, truncated) and how long ago it last ran ("paused" for a paused job).
- **Details**, under an open row: problems in `ansi1`, then Health, ID, Command, Log, Schedule, Last run and Result, then the action buttons. A job tackroom owns here (the `editable` field of the JSON) adds Edit and Delete; any other job shows "No edit or delete." and the reason (`edit_note`) in `dim`.
- **The dialog**, a 420px popup of this same mod (`props().form`): a title, then text inputs for the name (Add only), schedule, command, one input per argument with a remove button and "+ Add argument", and an optional effect; Cancel and Add cron or Save. Delete shows the schedule and command and a red Delete button; Enter does not confirm it. An error from tackroom shows in `ansi1` above the buttons and the dialog stays open. Esc closes it.

## How it behaves

- Collects on open, then every `refresh` seconds (default 300, at least 60). Collect collects at once; the button is dimmed while a collect runs.
- Clicking a row opens or closes its details.
- A launchd job offers Run now and Pause, or Resume when it is disabled or unloaded, as in the web view. Other sources have no buttons. While an action runs its button reads "…" and every action button is dimmed. A notification says whether it worked; then the panel collects again.
- Web opens the running `tackroom view` on its Crons tab in the default browser, signed in with the token it saved.
- Add opens the dialog empty; Edit opens it filled from `declared_schedule`, `declared_command` and `effect`, and sends only the flags that changed. On success the dialog closes, a notification says so and the panel collects again; on failure the panel collects again too, because a failed apply leaves the config changed.

## Built from

- One mod, `tackroom` (`main.js` the panel, `form.js` the dialog), `place: "none"`, put in a layout as a block (the `tackroom` layout in `herdr/layouts` already has it, with a Crons button): `{ "type": "block", "content": { "mod": "tackroom", "props": { "tackroom": "~/.local/bin/tackroom", "config": "~/.agents/crons.yaml", "refresh": 300 } } }`. All props are optional: `tackroom` defaults to `tackroom` on the PATH, `config` to tackroom's default config.
- Data: `exec(tackroom, ["cron", "collect", "--config", config])`, JSON with `jobs` and `access`, 5-minute timeout.
- Actions: `exec(tackroom, ["cron", verb, "--config", config, "--host", host, "--source", source, id])` with `verb` run, pause or resume. Flags come before the id.
- Add, edit and delete: `exec(tackroom, ["cron", "add" | "edit" | "remove", "--config", config, id, ...flags])`; the id may sit anywhere among the flags. The dialog sends `run("tackroom.reload")` afterwards, which the panel receives from `nextCommand()` and answers with a collect.
- Text input: `InputState` and `Input` from `gpui-base`, `openOverlay` with `kind: "dialog"`, `closeOverlay`, `notify`.
- Web: `exec("/bin/sh", ...)` reads `view.addr` and `view.token` from `${XDG_STATE_HOME:-~/.local/state}/tackroom` and runs `open`.
- `herdr` functions: `exec`, `props`, `theme`, `notify`, `openOverlay`, `closeOverlay`, `run`, `nextCommand`. Icon: `crons.svg`, tinted.
- Health, short names and schedule words come from the JSON (`health`, `short_name`, `schedule_text`); tackroom works them out once for this panel and the web view.

## Watch out

- Needs a tackroom whose `cron collect` carries `health`, `short_name`, `schedule_text`, `editable`, `edit_note` and `declared_command`, and has the `cron run|pause|resume|add|edit|remove` verbs for the buttons.
- `exec` does not expand `~`; the mod expands a leading `~/` itself from `$HOME`.
- Web reads `view.addr` and `view.token` from tackroom's default state folder, so it does not find a view started with another `--token-file`.
- A collect probes every host over SSH; on a loaded machine the local probe can hit tackroom's 2-minute timeout and the host shows unreachable until the next collect.
- An owned job shows its declared schedule (`declared_schedule`), not the entries launchd expanded it into. Collected from a host that does not declare it, the expansion still reads as one schedule: sixteen `0 8 N * *` entries for odd days are "Every 2 days 08:00", `0 * * * *; 30 * * * *` is "Every 30 min".
- The reload after the dialog is a `<mod>.<command>` that reaches shown copies of the mod named `tackroom`; a clone under another name still collects on its timer and the Collect button.
- A change shows when the full collect that follows finishes, not at once as in the web view.

## Done when

- `herdr-app check mods/tackroom` passes. It renders only the panel; the dialog appears only with `props().form`, so check a copy of the mod that forces `form: { mode: "add" }`, `edit` (with a `job`) and `delete`.
- The panel lists the same jobs, per host, with the same health counts as the web Crons tab.
- Run now on a launchd job raises its run count after the next collect; Pause turns its row grey with "paused" and offers Resume; Resume brings it back.
- Add creates a job that shows in the panel and in `tackroom view`; Edit changes it; Delete asks, then removes it, its YAML entry and its unit. A job tackroom does not own shows the reason and no Edit or Delete.
