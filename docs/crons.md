# Scheduled job inventory (`tackroom crons`)

Status: design. `tackroom crons scan` is the first piece; the manifest and Healthchecks sync follow.

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

`tackroom cron` already exists: it installs tackroom's own self-update crontab entry. The inventory lives under `tackroom crons` so neither command changes meaning. `cron` manages one job tackroom owns; `crons` observes every job on every host.

## 1. scan

`tackroom crons scan [--hosts local,HOST...] [--table]` is read-only. For each host it runs one POSIX `sh` probe (locally, or through `ssh -o BatchMode=yes HOST sh -s`), the probe prints raw scheduler state, and tackroom parses it locally. Nothing is copied to, installed on, or written on the scanned host. SQLite files are streamed back and opened from a local temporary copy, because opening a WAL database with `mode=ro` in place can still create a `-shm` file.

Hosts come from `--hosts`, else `crons.hosts` in `tackroom.yaml`, else `local`. SSH aliases resolve through the user's own `~/.ssh/config`.

### Sources

| Source | Read from | Included |
| --- | --- | --- |
| `launchd` | `~/Library/LaunchAgents/*.plist` via `plutil -convert json`, state via `launchctl print gui/$UID/<label>` | `StartCalendarInterval` or `StartInterval` jobs. `RunAtLoad`-only and `KeepAlive` daemons are services, not schedules, and are left out. |
| `crontab` | `crontab -l` | Every non-comment, non-variable line. |
| `systemd` | `systemctl --user list-timers --all --output=json`, `systemctl --user show` on each timer and its unit | User timers. |
| `hermes` | `~/.hermes/cron/jobs.json`, `executions.db`, `ticker_heartbeat` | Every job. An enabled job whose scheduler heartbeat is stale is reported `dead`. |
| `codex` | `~/.codex/sqlite/codex-dev.db`, tables `automations` and `automation_runs` | Every automation. |
| `claude-desktop` | `scheduled-tasks.json` under the Claude desktop app's `claude-code-sessions` directory, prompts in `~/.claude/scheduled-tasks/<id>/SKILL.md`, runs from session files that carry `scheduledTaskId` | Every task. |

Documented gaps, reported as `not_scanned` rather than silently absent:

- Claude cloud routines: no on-disk state; listable only from inside a Claude session.
- OpenBot routines: the database does not open from an SSH session.

### launchd checks

`launchd` ignores keys it does not recognize. A `StartCalendarInterval` written with `hour` and `minute` instead of `Hour` and `Minute` is an empty calendar interval, which launchd treats as "every minute". The scan flags:

- a `StartCalendarInterval` entry with no recognized key (`Minute`, `Hour`, `Day`, `Weekday`, `Month`): fires every minute;
- any key that matches a known launchd key only case-insensitively;
- a `ProgramArguments[0]` or `Program` path that does not exist.

### Record

Every job becomes one JSON record:

| Field | Meaning | Provenance |
| --- | --- | --- |
| `host` | Host alias as scanned | declared (by the scan target) |
| `source` | `launchd`, `crontab`, `systemd`, `hermes`, `codex`, `claude-desktop` | declared |
| `id` | Scheduler-native id: launchd label, timer unit, harness job id, crontab line number | declared |
| `name` | Human name where the scheduler has one, else `id` | declared |
| `schedule` | Cron expression, interval, `OnCalendar`, or RRULE as the scheduler stores it | declared |
| `tz` | Host time zone from `/etc/localtime`, unless the scheduler stores its own | inferred |
| `command` | Program and arguments, script, or prompt reference | declared |
| `log` | `StandardOutPath`, a `>>` redirect in a crontab line, the journal, or a harness output directory | declared or inferred |
| `runs` | Run count (launchd `runs` since load; harness run tables) | declared |
| `last_exit` | Last exit code where the scheduler records one | declared |
| `last_status` | Harness status string (`ok`, `error`, `blocked_config`, ...) | declared |
| `last_run` | Last start time; from log mtime where the scheduler keeps none | declared or inferred |
| `state` | `scheduled`, `disabled`, `paused`, `not_loaded`, `dead`, `missing` | inferred |
| `created_by` | `human`, `agent`, or a harness name | from the manifest |
| `connectors` | Services the job reads or writes | from the manifest; a harness delivery target is inferred |
| `effect` | Expected observable outcome | from the manifest |
| `problems` | Findings such as miscased plist keys or a missing program | inferred |

`provenance` on each record lists which fields were inferred, so a view can show them differently. The inference rules stay small and explicit; they never guess connectors from free-form command text.

### Access states

A scan reports one access entry per host and source, independent of the job list:

| State | Meaning |
| --- | --- |
| `found` | Read successfully; at least one job. |
| `empty` | Searched and read successfully; zero jobs, or the scheduler is not installed. `detail` says which. |
| `unreadable` | The state exists but could not be read: a locked Keychain over SSH, a GUI launchd domain that is not available to the SSH session, a database that will not open, a permission error. `detail` carries the error. |
| `not_scanned` | Not attempted: wrong OS for the source, host unreachable, or a documented gap. |

An empty job list means nothing on its own; only `empty` means "looked and there are none".

## 2. The `crons:` manifest

Authorship and connectors cannot be derived reliably from a command line, so they are declared once in `tackroom.yaml`. The scan merges declared fields into matching records and reports declared jobs it did not find as `state: missing`.

```yaml
crons:
  hosts: [local, laptop, vps]
  jobs:
    com.example.backup:            # scheduler-native id
      host: laptop                 # optional; matches any scanned host when omitted
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

`tackroom crons sync` (later) pushes the scan into a Healthchecks instance the user runs (BSD-3, SQLite).

- **Checks.** One check per record via `POST /api/v3/checks/` with `unique: ["name"]`, so repeated syncs update instead of duplicating. `name` is `host/source/id`. `tags` carry `host:<h>`, `source:<s>` and one tag per connector. `schedule` and `tz` come from the scan when the schedule is a cron expression; interval jobs use `timeout`. `desc` carries `effect`.
- **Own launchd and crontab jobs.** Wrapped in [`runitor`](https://github.com/bdd/runitor), which pings start, exit code and output. Jobs that read private notes or other sensitive data use `-no-output-in-ping` so their output never reaches the monitor. Wrapping edits plists and crontab lines, so it is a separate, reviewed step per job.
- **Agent-harness jobs.** Their schedulers cannot be wrapped. tackroom backfills runs from their own run records (`executions.db`, `automation_runs`, scheduled-task sessions) as pings with a stable `rid` per run, so a replayed sync does not double-count.
- **Alerts.** A Healthchecks webhook integration posts to an existing Telegram bot. The built-in Telegram integration needs a public HTTPS endpoint; a webhook does not, so the instance can stay reachable only over a private network or SSH tunnel.

Exit codes and log mtimes catch jobs that stopped running, not jobs that run and do nothing. Jobs with an external effect should check that effect, for example with Healthchecks `success_kw` on the ping body.

## Non-goals

- tackroom does not become a scheduler. `tackroom cron` keeps its one entry.
- Scheduled jobs are not a sixth sync surface. `crons` reads harness state and writes only to the user's Healthchecks instance; it never writes harness configuration.
- Scan never fixes, unloads or edits a job. It reports.
- No hosted monitoring service, and no job output leaving the user's own infrastructure.
