# Scheduled job inventory (`tackroom cron collect`)

Status: `tackroom cron collect` and the Crons tab in `tackroom view` are the first piece; the manifest and Healthchecks sync follow.

## Problem

Scheduled work is spread across launchd, crontab, systemd timers and each agent harness's own scheduler, on more than one machine. Some jobs were created by hand, others by agents. There is no single place that answers: what runs, when, on which host, did the last run succeed, and what does it touch.

Existing tools cover one slice each:

| Tool | Gap |
| --- | --- |
| Cronicle, Dkron, Windmill | Replace the scheduler; every job has to move inside them. |
| LaunchControl, Cockpit, crontab-ui | One host and one scheduler. |
| Harness dashboards (for example Hermes) | Only that harness's jobs. |
| Cronitor | Free tier is 5 monitors, and job output leaves the machine. |
| Uptime Kuma (push monitors) | Heartbeat only; does not understand schedules. |
| Healthchecks | Sees only jobs that ping it, but has history, alerts, tags and an upsert API. |

So the design has two layers: tackroom collects (the provider-agnostic part nothing else does) and a self-hosted Healthchecks shows history and sends alerts.

## Naming

`tackroom cron` already exists: with flags only (`--interval`, `--deps`, `--remove`) it installs tackroom's own self-update crontab entry, and it keeps doing that. The inventory is its `collect` subcommand, so all scheduled-job commands live under one word: plain `cron` manages the one job tackroom owns, `cron collect` observes every job on every host, and `cron sync` (section 3) comes later.

## 1. collect

`tackroom cron collect [--hosts local,HOST...] [--table]` is read-only. For each host it runs one POSIX `sh` probe (locally, or through `ssh -o BatchMode=yes HOST sh -s`), the probe prints raw scheduler state, and tackroom parses it locally. Nothing is copied to, installed on, or written on the scanned host. SQLite files are streamed back and opened from a local temporary copy, because opening a WAL database with `mode=ro` in place can still create a `-shm` file.

Hosts come from `--hosts`, else `crons.hosts` in `tackroom.local.yaml`, else `crons.hosts` in `tackroom.yaml`, else `local`. Hosts differ per machine, so the local overlay is the usual place for them. SSH aliases resolve through the user's own `~/.ssh/config`.

### Sources

| Source | Read from | Included |
| --- | --- | --- |
| `launchd` | `~/Library/LaunchAgents/*.plist` via `plutil -convert json -o -` (to stdout, so the file is never rewritten), state via `launchctl print gui/$UID/<label>` | `StartCalendarInterval` or `StartInterval` jobs. `KeepAlive`, `RunAtLoad` and other on-demand agents are services, not schedules; they appear only when their last exit is non-zero, with `state: service`. |
| `crontab` | `crontab -l` | Every schedule line; `@reboot` is a boot hook and is left out. |
| `systemd` | `systemctl --user list-timers --all --output=json`, `systemctl --user show` on each timer and its unit | User timers that systemd has loaded. A timer unit file that was installed but never loaded is not listed. |
| `hermes` | `~/.hermes/cron/jobs.json`, `executions.db`, `ticker_heartbeat` | Every job. An enabled job whose scheduler heartbeat is older than 10 minutes is reported `dead`. |
| `codex` | `~/.codex/sqlite/codex-dev.db`, tables `automations` and `automation_runs` | Every automation. |
| `claude-desktop` | `scheduled-tasks.json` under the Claude desktop app's `claude-code-sessions` directory, prompts in `~/.claude/scheduled-tasks/<id>/SKILL.md`, runs from session files that carry `scheduledTaskId` | Every task. |

Scope is the user tackroom runs as (or the SSH user): their LaunchAgents, crontab and user timers. System daemons in `/Library/LaunchDaemons`, other users' crontabs and system timers are out of scope; collecting them needs root, which a read-only probe does not ask for.

Documented gaps, reported as `not_scanned` rather than silently absent:

- Claude cloud routines: no on-disk state; listable only from inside a Claude session.
- OpenBot routines: the database does not open from an SSH session.
- Login items an app registers through `SMAppService` (`launchctl print` shows them as submitted by `smd`): there is no plist in `~/Library/LaunchAgents` to read.

### launchd checks

`launchd` ignores keys it does not recognize. A `StartCalendarInterval` written with `hour` and `minute` instead of `Hour` and `Minute` is an empty calendar interval, which launchd treats as "every minute". The scan flags:

- a `StartCalendarInterval` entry with no recognized key (`Minute`, `Hour`, `Day`, `Weekday`, `Month`): fires every minute;
- any key that matches a known launchd key only case-insensitively;
- an absolute path in `ProgramArguments`, `Program` or `WorkingDirectory` that does not exist.

The same missing-path check runs on crontab and systemd commands, skipping `>`/`>>` targets a job creates itself.

For a service, a `KeepAlive` agent with 10 or more runs and a non-zero last exit is flagged as a crash loop. The threshold is a heuristic.

### Missed runs

A scheduled job whose recorded last run is older than the two most recent times its cron expression should have fired is flagged as "enabled, but has not run". The check runs only where the scheduler records its own last run (Hermes, Claude desktop). A last run inferred from a log mtime is skipped, because a job that runs but writes nothing would look stale.

### Record

Every job becomes one JSON record:

| Field | Meaning | Provenance |
| --- | --- | --- |
| `host` | Host alias as scanned | declared (by the scan target) |
| `source` | `launchd`, `crontab`, `systemd`, `hermes`, `codex`, `claude-desktop` | declared |
| `id` | Scheduler-native id: launchd label, timer unit, harness job id; for crontab, `crontab-` plus a short hash of the line, so it survives reordering | declared |
| `name` | Human name where the scheduler has one, else `id` | declared |
| `schedule` | Cron expression, interval, `OnCalendar`, or RRULE as the scheduler stores it | declared |
| `tz` | Host time zone from `/etc/localtime`; `CRON_TZ` for crontab; the UTC offset of Hermes `next_run_at` | inferred |
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

`tackroom view` has a Crons tab with the same inventory. It runs the collector on first open, caches the result for a minute, and collects again on request. The tab lists the jobs that need attention first, then one table per host with an access chip per source, so an unreachable host or an unreadable source shows as such instead of as an empty list. A job needs attention when it has problems or is `dead` or `missing`; a disabled or paused job's old error does not count until the job is turned on again. Selecting a row shows its command, log, time zone, problems and manifest fields. The tab only reads; it has no actions that change a job.

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

## Non-goals

- tackroom does not become a scheduler. Plain `tackroom cron` keeps its one entry.
- Scheduled jobs are not a sixth sync surface. `cron collect` and `cron sync` read harness state and write only to the user's Healthchecks instance; it never writes harness configuration.
- Neither `cron collect` nor the view fixes, unloads or edits a job. They report.
- No hosted monitoring service, and no job output leaving the user's own infrastructure.
