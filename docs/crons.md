# Scheduled job inventory (`tackroom cron collect`)

Status: `tackroom cron collect`, the Crons tab in `tackroom view` with its actions, `cron apply` (section 4) and the herdr-automations source (section 5) are built; Healthchecks sync follows.

## Problem

Scheduled work is spread across launchd, crontab, systemd timers and each agent harness's own scheduler, on more than one machine. Some jobs were created by hand, others by agents. There is no single place that answers: what runs, when, on which host, did the last run succeed, and what does it touch.

Existing tools cover one slice each:

| Tool | Gap |
| --- | --- |
| Cronicle, Dkron, Windmill | Replace the scheduler; every job has to move inside them. |
| LaunchControl, Cockpit, crontab-ui | One host and one scheduler. |
| Harness dashboards (for example Codex automations) | Only that harness's jobs. |
| Cronitor | Free tier is 5 monitors, and job output leaves the machine. |
| Uptime Kuma (push monitors) | Heartbeat only; does not understand schedules. |
| Healthchecks | Sees only jobs that ping it, but has history, alerts, tags and an upsert API. |

So the design has two layers: tackroom collects (the provider-agnostic part nothing else does) and a self-hosted Healthchecks shows history and sends alerts.

## Naming

`tackroom cron` already exists: with flags only (`--interval`, `--deps`, `--remove`) it installs tackroom's own self-update crontab entry, and it keeps doing that. The inventory is its `collect` subcommand, so all scheduled-job commands live under one word: plain `cron` manages tackroom's own self-update entry, `cron collect` observes every job on every host, `cron apply` (section 4) installs declared jobs, and `cron sync` (section 3) comes later.

## 1. collect

`tackroom cron collect [--hosts local,HOST...] [--table]` is read-only. For each host it runs one POSIX `sh` probe (locally, or through `ssh -o BatchMode=yes HOST sh -s`), the probe prints raw scheduler state, and tackroom parses it locally. Nothing is copied to, installed on, or written on the scanned host. SQLite files are streamed back and opened from a local temporary copy, because opening a WAL database with `mode=ro` in place can still create a `-shm` file.

Hosts come from `--hosts`, else `crons.hosts` in `tackroom.local.yaml`, else `crons.hosts` in `tackroom.yaml`, else `local`. Hosts differ per machine, so the local overlay is the usual place for them. SSH aliases resolve through the user's own `~/.ssh/config`.

### Sources

| Source | Read from | Included |
| --- | --- | --- |
| `launchd` | `~/Library/LaunchAgents/*.plist` via `plutil -convert json -o -` (to stdout, so the file is never rewritten), state via `launchctl print gui/$UID/<label>` | `StartCalendarInterval` or `StartInterval` jobs. `KeepAlive`, `RunAtLoad` and other on-demand agents are services, not schedules; they appear only when their last exit is non-zero, with `state: service`. |
| `crontab` | `crontab -l` | Every schedule line; `@reboot` is a boot hook and is left out. |
| `systemd` | `systemctl --user list-timers --all --output=json`, `systemctl --user show` on each timer and its unit | User timers that systemd has loaded. A timer unit file that was installed but never loaded is not listed. |
| `codex` | `~/.codex/sqlite/codex-dev.db`, tables `automations` and `automation_runs` | Every automation. |
| `claude-desktop` | `scheduled-tasks.json` under the Claude desktop app's `claude-code-sessions` directory, prompts in `~/.claude/scheduled-tasks/<id>/SKILL.md`, runs from session files that carry `scheduledTaskId` | Every task. |
| `herdr-automations` | `automations.yaml` in `$HERDR_PLUGIN_CONFIG_DIR`, else `~/.config/herdr/plugins/config/dnzzl.automations/`; `history.jsonl` in `$HERDR_PLUGIN_STATE_DIR`, else `~/.local/state/herdr/plugins/dnzzl.automations/`; `daemon.pid` in the same state folder | Every automation. `id` is its `name`; `command` is `agent` and `model` plus the first prompt line; `disabled: true` or a top-level `paused: true` gives `disabled`/`paused`. `last_status` and `last_run` come from the last terminal history event (`done`, `failed`, `missed`, `skipped`, `cancelled`, `invalid`); `failed`, `missed` and `invalid` are problems. When the pid in `daemon.pid` is not a live `herdr-automations` process, every enabled automation is `dead`. |

Scope is the user tackroom runs as (or the SSH user): their LaunchAgents, crontab and user timers. System daemons in `/Library/LaunchDaemons`, other users' crontabs and system timers are out of scope; collecting them needs root, which a read-only probe does not ask for.

Documented gaps, reported as `not_scanned` rather than silently absent:

- Claude cloud routines: no on-disk state; listable only from inside a Claude session.
- Login items an app registers through `SMAppService` (`launchctl print` shows them as submitted by `smd`): there is no plist in `~/Library/LaunchAgents` to read.

### launchd checks

`launchd` ignores keys it does not recognize. A `StartCalendarInterval` written with `hour` and `minute` instead of `Hour` and `Minute` is an empty calendar interval, which launchd treats as "every minute". The scan flags:

- a `StartCalendarInterval` entry with no recognized key (`Minute`, `Hour`, `Day`, `Weekday`, `Month`): fires every minute;
- any key that matches a known launchd key only case-insensitively;
- an absolute path in `ProgramArguments`, `Program` or `WorkingDirectory` that does not exist.

The same missing-path check runs on crontab and systemd commands, skipping `>`/`>>` targets a job creates itself.

For a service, a `KeepAlive` agent with 10 or more runs and a non-zero last exit is flagged as a crash loop. The threshold is a heuristic.

### Missed runs

A scheduled job whose recorded last run is older than the two most recent times its cron expression should have fired is flagged as "enabled, but has not run". The check runs only where the scheduler records its own last run (Claude desktop). A last run inferred from a log mtime is skipped, because a job that runs but writes nothing would look stale.

### Record

Every job becomes one JSON record:

| Field | Meaning | Provenance |
| --- | --- | --- |
| `host` | Host alias as scanned | declared (by the scan target) |
| `source` | `launchd`, `crontab`, `systemd`, `codex`, `claude-desktop`, `herdr-automations` | declared |
| `id` | Scheduler-native id: launchd label, timer unit, harness job id; for crontab, `crontab-` plus a short hash of the line, so it survives reordering | declared |
| `name` | Human name where the scheduler has one, else `id` | declared |
| `schedule` | Cron expression, interval, `OnCalendar`, or RRULE as the scheduler stores it | declared |
| `tz` | Host time zone from `/etc/localtime`; `CRON_TZ` for crontab | inferred |
| `command` | Program and arguments, script, or prompt reference | declared |
| `log` | `StandardOutPath`, a `>>` redirect in a crontab line, the journal, or a harness output directory | declared or inferred |
| `runs` | Run count (launchd `runs` since load; harness run tables) | declared |
| `last_exit` | Last exit code where the scheduler records one | declared |
| `last_status` | Harness status string (`ok`, `error`, `blocked_config`, ...) | declared |
| `last_run` | Last start time; from log mtime where the scheduler keeps none | declared or inferred |
| `state` | `scheduled`, `service` (a failing on-demand launchd agent), `disabled`, `paused`, `not_loaded`, `dead`, `missing` | inferred |
| `created_by` | `human`, `agent`, or a harness name; `system` for timers shipped under `/usr` or `/lib` | from the manifest, or inferred |
| `connectors` | Services the job reads or writes | from the manifest; a harness delivery target is inferred |
| `effect` | Expected observable outcome | from the manifest |
| `problems` | Findings such as miscased plist keys or a missing program | inferred |
| `inferred` | Names of the fields above that tackroom derived rather than read or had declared, for example `tz`, `last_run`, `created_by` | inferred |

`inferred` on each record lists which fields tackroom derived rather than read or had declared, so a view can show them differently. The inference rules stay small and explicit; they never guess connectors from free-form command text.

### Access states

A scan reports one access entry per host and source, independent of the job list:

| State | Meaning |
| --- | --- |
| `found` | Read successfully; at least one job. |
| `empty` | Searched and read successfully; zero jobs, or the scheduler is not installed. `detail` says which. |
| `unreadable` | The state exists but could not be read: a locked Keychain over SSH, a GUI launchd domain that is not available to the SSH session, a database that will not open, a permission error. `detail` carries the error. |
| `not_scanned` | Not attempted: wrong OS for the source, host unreachable, or a documented gap. |

An empty job list means nothing on its own; only `empty` means "looked and there are none".

## View

`tackroom view` has a Crons tab with the same inventory. It runs the collector on first open, caches the result for a minute, and collects again on request. The tab lists the jobs that need attention first, then one table per host with an access chip per source, so an unreachable host or an unreadable source shows as such instead of as an empty list. A job needs attention when it has problems or is `dead` or `missing`; a disabled or paused job's old error does not count until the job is turned on again. Selecting a row shows its command, log, time zone, problems and manifest fields, and the actions below.

### View actions

A launchd job's row offers **Run now**, **Pause** and **Resume**; an owned job on the machine the view runs on also takes a new **schedule**. The same actions are CLI verbs, for scripts and for other clients such as a Herdr mod:

```
tackroom cron run|pause|resume [--host HOST] [--config PATH] ID
tackroom cron schedule [--config PATH] ID "MIN HOUR DOM MON DOW"
```

- An action addresses a job by host, source and id as the last scan reported it, and uses the label and plist path from that scan, never ones sent with the request. The view acts on its cached scan and answers `stale_crons` for a job that is not in it; the CLI collects the job's host first.
- Run is `launchctl kickstart gui/$UID/<label>`. Pause is `launchctl disable` then `bootout`, so the job stays off across logins and `collect` reports it `disabled`. Resume is `launchctl enable` then `bootstrap` of the job's plist when it is not loaded; a plist with `RunAtLoad` runs once as it loads, and launchd's run count starts again. On another host the same script runs through `ssh -o BatchMode=yes HOST sh -s`, like the probe.
- A schedule change rewrites `crons.jobs.<id>.schedule` in the file that declares the job (`tackroom.local.yaml` when it has the id, else the shared config), then runs `cron apply`, which replaces the unit. The expression is checked the way `apply` checks it before anything is written. A paused job must be resumed first, because `apply` cannot bootstrap a disabled label.
- Only launchd jobs have actions so far. crontab, systemd timers and the harness schedulers stay read-only.

## 2. The `crons:` manifest

Authorship and connectors cannot be derived reliably from a command line, so they are declared once in `tackroom.yaml`. Collection merges declared fields into matching records. An entry matches records by scheduler-native id, narrowed by `host` and `source` when they are set; without them it applies to every record with that id on any host, which is the explicit rule for ids that repeat. A declared job that is not found is reported as `state: missing` only when every place it could live was read: the host was reached, and its `source` (when set) came back `found` or `empty`, or (when unset) no source on that host came back `unreadable`. Otherwise it is left out, and the access entries show what could not be read.

```yaml
crons:
  hosts: [local, laptop, vps]
  jobs:
    com.example.backup:            # scheduler-native id
      host: laptop                 # optional; matches any scanned host when omitted
      source: launchd              # optional; matches any source when omitted
      created_by: human
      connectors: [git, ssh]
      effect: nightly commit pushed to the backup remote
    weekly-digest:
      host: vps
      created_by: agent
      connectors: [telegram]
      effect: digest message in the team channel
```

Jobs that tackroom itself installs later can carry `created_by` from the start; existing jobs are annotated once by hand. Unknown keys elsewhere in `tackroom.yaml` are preserved by the config round-trip, so the section needs no migration.

## 3. sync to self-hosted Healthchecks

`tackroom cron sync` (later) pushes the collected inventory into a Healthchecks instance the user runs (BSD-3, SQLite).

- **Checks.** One check per record via `POST /api/v3/checks/` with `unique: ["name"]`, so repeated syncs update instead of duplicating. `name` is `host/source/id`. `tags` carry `host:<h>`, `source:<s>` and one tag per connector. `schedule` and `tz` come from the scan when the schedule is a cron expression; interval jobs use `timeout`. `desc` carries `effect`.
- **Own launchd and crontab jobs.** Wrapped in [`runitor`](https://github.com/bdd/runitor), which pings start, exit code and output. Jobs that read private notes or other sensitive data use `-no-output-in-ping` so their output never reaches the monitor. Wrapping edits plists and crontab lines, so it is a separate, reviewed step per job.
- **Agent-harness jobs.** Their schedulers cannot be wrapped. tackroom backfills runs from their own run records (`executions.db`, `automation_runs`, scheduled-task sessions) as pings with a stable `rid` per run, so a replayed sync does not double-count.
- **Alerts.** A Healthchecks webhook integration posts to an existing Telegram bot. The built-in Telegram integration needs a public HTTPS endpoint; a webhook does not, so the instance can stay reachable only over a private network or SSH tunnel.

Exit codes and log mtimes catch jobs that stopped running, not jobs that run and do nothing. Jobs with an external effect should check that effect, for example with Healthchecks `success_kw` on the ping body.

## 4. Owned jobs: `cron apply`

A job declared with a `schedule` and a `command` is owned: tackroom writes it as a native unit of the host's own scheduler and removes it when the declaration goes. tackroom runs no daemon of its own; launchd, systemd or cron fires the job. Agent prompts on a schedule are not owned jobs; they belong in herdr-automations (section 5).

```yaml
crons:
  scheduler: auto          # auto | launchd | systemd | crontab
  jobs:
    knowledge-sync:
      host: m1             # matched against crons.self; omitted means every machine
      schedule: "*/30 * * * *"
      command: [~/.local/bin/knowledge-sync]
      created_by: human
```

### Identity and scope

- `crons.self` in each machine's `tackroom.local.yaml` names that machine (`self: m1`), so one shared manifest says which machine runs what. `crons.scheduler` may also be set in the local overlay; the overlay wins.
- A job is in scope on this machine when its `host` is unset or equals `crons.self`. If any owned job has a `host` and `crons.self` is unset, `apply` stops before changing anything. Inventory-only entries (no `schedule` or `command`) may name any host.
- Job ids match `^[a-z0-9][a-z0-9-]{0,62}$`. Any other id is refused, because the id becomes part of file names and unit labels.
- The manifest loader keeps `scheduler`, `self`, `schedule` and `command`; today's decode types drop them, so they are extended, and the local overlay merges `self` and `scheduler`.

### What `apply` does

`tackroom cron apply [--dry-run]` reconciles this machine's owned units with the manifest:

1. Load the config. A missing, unreadable or invalid config stops `apply` with an error; it is never read as an empty manifest, so it never prunes because of a load failure.
2. Take a lock (`~/.local/state/tackroom/cron/apply.lock`); a second `apply` waits for it, then plans from the manifest as it is then.
3. Read every owned unit on this machine from all three backends, recognized by its prefix: `dev.tackroom.<id>` for launchd, `tackroom-<id>.timer`/`.service` for systemd, a `# tackroom:<id>` marker for crontab. Nothing without the prefix is read as owned or touched.
4. Plan, per id: install (declared, absent), replace (declared, content differs, or present on a backend other than the selected one), remove (present, not declared). Print the plan as a diff. `--dry-run` stops here.
5. Execute. `~` and `$HOME` in `command` and in paths are expanded at render time, so units hold absolute paths. The log folder is created before any unit is loaded.
   - launchd: write the plist to a temporary file and rename it into place; `launchctl bootout gui/$UID/<label>` when it is loaded, then `launchctl bootstrap gui/$UID <plist path>`.
   - systemd: write both units, `systemctl --user daemon-reload`, then `enable --now` the timer; a replaced timer is restarted. A removed one is `disable --now`d before its files go.
   - crontab: rewrite the marked line in one `crontab -` call.
6. Report each step. If any step fails, `apply` exits non-zero and says what is installed now.

### Backends

| `scheduler` | Unit | Missed while asleep or off |
| --- | --- | --- |
| `launchd` (default on macOS) | `~/Library/LaunchAgents/dev.tackroom.<id>.plist` with `StartCalendarInterval` | Runs once on wake. launchd.plist(5): unlike cron, launchd starts a calendar job the next time the computer wakes, coalescing missed intervals into one run. |
| `systemd` (default on Linux) | `tackroom-<id>.timer` with `OnCalendar=` and `Persistent=true`, plus a `Type=oneshot` service | Runs once when the user manager starts again (`Persistent=true`). Without lingering the user manager stops at logout and starts at login, so a server needs `loginctl enable-linger $USER`; `apply` checks `Linger` and warns, and does not enable it. |
| `crontab` | One line with the marker | Skipped. Only when set explicitly. |

`auto` picks launchd on macOS and systemd on Linux. It never falls back to crontab: when `systemctl --user` does not answer, `apply` stops and asks for an explicit `scheduler`.

The schedule is always a 5-field cron expression and is translated, never passed through:

- launchd: each restricted field becomes a key (`Minute`, `Hour`, `Day`, `Month`, `Weekday`) and lists expand into an array of dictionaries. An expression that would expand past 64 dictionaries is refused. `StartInterval` is never used, because it drops firings during sleep.
- An expression that restricts both day of month and day of week is refused: cron runs it when either matches, and the other backends cannot say that.
- systemd: `0 7 * * 2,5` becomes `OnCalendar=Tue,Fri *-*-* 07:00:00`.

Output goes to `~/.local/state/tackroom/cron/<id>.log` (launchd `StandardOutPath`/`StandardErrorPath`, a redirect for crontab); systemd keeps it in the journal.

### Collect sees owned jobs as owned

`cron collect` maps an owned unit back to its manifest id by the prefix (`dev.tackroom.knowledge-sync` and `tackroom-knowledge-sync.timer` are both `knowledge-sync`) and treats the `local` host as `crons.self`, so the declaration's fields merge into the record and a declared job is not reported `missing` because its native id has a prefix. Owned records get `created_by: tackroom` unless the manifest says otherwise.

tackroom writes every plist key itself, so the case-sensitive key trap in section 1 cannot happen in an owned job. One trap stays outside its reach: launchd kills a job whose binary was overwritten in place (`last exit reason = OS_REASON_CODESIGNING`). `cron collect` flags it; install binaries by writing a new file and renaming it over the old one.

### tackroom's own entries

The two jobs plain `tackroom cron` installs today become built-in owned jobs on the selected backend: `tackroom-pull` (every 30 minutes by default) and, with `--deps`, `tackroom-deps` (weekly). `tackroom cron [--interval] [--deps] [--remove]` installs, replaces or removes that one built-in job through the same planner as `apply`, on the selected backend and off every other; it no longer writes crontab directly, so the self-update cannot end up installed twice. `apply` never prunes the built-in ids, and the manifest cannot declare them. The first plain `tackroom cron` lists the old crontab lines in its plan and removes them as it installs the built-in job; `apply` leaves them, so the self-update is never removed without a replacement. A line counts as old when its command, after an optional `. "$HOME/.profile" 2>/dev/null;` prefix, is any executable whose base name is `tackroom` (quoted or not), followed by `pull`, `sync deps` or the legacy `deps update`, and an optional `--config <path>`. The match is on that whole shape, not a substring of today's resolved command, so a line with another binary path or `--config` value is found too, and a different command that merely contains it is not.

## 5. herdr-automations

[herdr-automations](https://github.com/DnzzL/herdr-automations) (MIT) schedules agent prompts: a prompt, a cron line and a fresh git worktree per run, in Herdr or in the Herdr app (`backend: hc`). Its own daemon runs as a launchd or systemd service and reads the wall clock every 30 seconds, so a run due while the machine slept fires on wake within `catch_up_minutes`. It is the place for scheduled agent work; owned jobs (section 4) are for plain commands. The two do not overlap: tackroom does not write `automations.yaml`.

tackroom supports it in three places:

- **collect**: the `herdr-automations` source (section 1) on every scanned host where the config folder exists.
- **view**: its records sit in the Crons tab like any other source, and a host card shows whether its scheduler is running.
- **doctor**: on a machine with an `automations.yaml`, `tackroom doctor` reports a scheduler that is not installed as a service, and agent names in the file that this machine has no binary for.

Version note: v0.11.1 to v0.11.3 all shipped on 2026-10-30, and `backend: hc` arrived in that series.

## Non-goals

- tackroom runs no scheduler daemon. Owned jobs are native units that the host's scheduler fires (section 4).
- Scheduled jobs are not a sixth sync surface. `cron collect` and `cron sync` read harness state and write only to the user's Healthchecks instance, and `cron apply` writes only units with its own prefix; none of them writes harness configuration, `automations.yaml` included.
- `cron collect` never changes a job. The view and the action verbs change only a launchd job someone acts on, and never fix one by themselves.
- No hosted monitoring service, and no job output leaving the user's own infrastructure.
