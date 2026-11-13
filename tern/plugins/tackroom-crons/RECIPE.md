# tackroom crons in Tern

A block for the [Tern terminal](https://stencil.so/tern) that shows every scheduled job tackroom collects, on every host in `crons.hosts`: host health, what needs attention, and each job's schedule and last run, with Run now, Pause and Resume for launchd jobs. It shows the same data as the Crons tab of `tackroom view` and the Herdr panel, and acts through the same `tackroom cron` verbs.

## Install

From a tackroom checkout:

```sh
tern plugin link ./tern/plugins/tackroom-crons
# or, to install a copy:
tern plugin install ./tern/plugins/tackroom-crons
```

Open with **Open tackroom crons** in the palette, or `Ctrl+Alt+C`. To change it, bind the action `plugin.tackroom-crons.open` in Tern's Keyboard settings.

## What you see

- **Header**: "Crons", the time of the last collect (or "collecting…"), then **Collect** and **Web** buttons.
- **Hosts**, one line each: the host's name ("This Mac" for the local one), then a colored dot and count per health (failing, last run failed, no result, healthy, paused).
- **Needs attention**: failing jobs, each row showing the host instead of the schedule.
- **One section per host**: its jobs sorted failing, last run failed, no result, healthy, paused. A row is a health dot, the short name, the schedule in words, and how long ago it last ran ("paused" for a paused job). Launchd jobs get **Run now** and **Pause** (or **Resume** when off).

## How it behaves

- Collects on open, then every 60 seconds. **Collect** refreshes at once and dims while a collect runs.
- An action runs `tackroom cron run|pause|resume --host H --source S <id>`, shows the result in the dock, then collects again.
- **Web** opens the running `tackroom view` on its Crons tab in the default browser, signed in with the token it saved.

## Built from

- One block, `crons`, in the `tackroom-crons` plugin. Block args (optional): `--bin /path/to/tackroom`, `--config /path/to/config.yaml`. A bare `tackroom` resolves to `~/.local/bin/tackroom` or `~/go/bin/tackroom` when it is not on the daemon's PATH.
- Data: `tackroom cron collect`, JSON with `jobs` and `access`, 5-minute timeout.
- Actions: `tackroom cron <verb> [--config cfg] --host H --source S <id>` with `verb` run, pause or resume.
- Web: `open` on `http://<view.addr>/?token=<view.token>#crons`, read from `${XDG_STATE_HOME:-~/.local/state}/tackroom`.

## Watch out

- Needs a tackroom whose `cron collect` carries `health`, `short_name` and `schedule_text`, and has the `cron run|pause|resume` verbs.
- Only launchd jobs have action buttons; crontab, systemd timers and harness schedulers stay read-only, as in the web view.
- A collect probes every host over SSH; on a loaded machine the local probe can hit tackroom's timeout and show an error until the next collect.
- Schedule changes are not in this block yet; use the web view or `tackroom cron schedule`.
