# Scheduled jobs

`tackroom cron` lists every scheduled job on your machines, runs and pauses launchd jobs, and installs jobs you declare as native units of the host's own scheduler. tackroom runs no scheduler daemon of its own.

```bash
tackroom cron collect [--hosts local,HOST...] [--table]   # read-only inventory, JSON by default
tackroom cron run|pause|resume [--host HOST] ID           # act on a launchd job, here or over SSH
tackroom cron schedule ID "MIN HOUR DOM MON DOW"          # new schedule for a job tackroom owns here
tackroom cron apply [--dry-run]                           # install, replace or remove declared jobs
tackroom cron add|edit|remove ID [flags]                  # declare, change or delete an owned job, then apply
tackroom cron [--interval 15m] [--deps] [--remove]        # tackroom's own sync job
```

`run`, `pause`, `resume` and `schedule` also take `--source` (default `launchd`) and `--config PATH`.

## Inventory: `cron collect`

For each host, `collect` runs one POSIX `sh` probe, locally or through `ssh -o BatchMode=yes HOST sh -s`, and parses the printed scheduler state locally. Nothing is installed on, copied to or written on the scanned host. Hosts come from `--hosts`, else `crons.hosts` in `tackroom.local.yaml`, else `crons.hosts` in `tackroom.yaml`, else `local`. SSH aliases resolve through your own `~/.ssh/config`.

Scope is the user tackroom runs as (or the SSH user): their LaunchAgents, crontab and user timers. System daemons, other users' crontabs and system timers are out of scope.

| Source | Read from | Included |
| --- | --- | --- |
| `launchd` | `~/Library/LaunchAgents/*.plist` and `launchctl print` | Jobs with `StartCalendarInterval` or `StartInterval`. `KeepAlive`, `RunAtLoad` and other on-demand agents are services: listed only when their last exit is non-zero, with `state: service`. |
| `crontab` | `crontab -l` | Every schedule line; `@reboot` is left out. |
| `systemd` | `systemctl --user` timers and their units | User timers systemd has loaded. |
| `codex` | Codex's automations database (needs a local `sqlite3`) | Every automation. |
| `claude-desktop` | The Claude desktop app's `scheduled-tasks.json` | Every scheduled task. |
| `herdr-automations` | `automations.yaml` and `history.jsonl` of the [herdr-automations](https://github.com/DnzzL/herdr-automations) plugin | Every automation. When its scheduler process is not running, every enabled automation is `dead`. |
| `claude-cloud` | nothing | Reported as `not_scanned`: cloud routines have no on-disk state. |

### Checks

- launchd ignores keys it does not recognize. A `StartCalendarInterval` written with `hour` and `minute` is an empty interval, which launchd fires every minute. `collect` flags an entry with no recognized key, any key that matches a known one only in a different case, and unknown keys.
- An absolute path in a launchd command, a crontab line or a systemd unit that does not exist is flagged. Redirect targets are skipped.
- A launchd `KeepAlive` service with 10 or more runs and a non-zero last exit is flagged as a crash loop. The threshold is a heuristic.
- A launchd job killed by the system (for example `OS_REASON_CODESIGNING` after its binary was overwritten in place) is flagged.
- A scheduled job whose recorded last run is older than the two most recent times it should have fired is flagged as "enabled, but has not run". This runs only for Claude desktop and herdr-automations, where the scheduler records its own last run.

### The record

Each job is one JSON record. Main fields:

| Field | Meaning |
| --- | --- |
| `host`, `source`, `id`, `name` | Where it lives and its scheduler-native id. A crontab id is `crontab-` plus a short hash of the line. |
| `schedule`, `tz`, `command`, `log` | As the scheduler stores them. |
| `runs`, `last_exit`, `last_status`, `last_run` | What the scheduler records; `last_run` may come from a log's mtime. |
| `state` | `scheduled`, `service`, `disabled`, `paused`, `not_loaded`, `dead` or `missing`. |
| `health` | `ok`, `warn`, `fail`, `idle` or `paused`, computed once for every view. |
| `created_by`, `connectors`, `effect` | From the manifest below, or inferred. |
| `declared_schedule`, `declared_command` | The `schedule` and `command` list a `crons.jobs` entry declares, as written. |
| `editable`, `edit_note` | `editable` is true for a job `cron edit` and `cron remove` can change from this machine; otherwise `edit_note` says why not (a job tackroom did not install, one on another host, a note-only entry). |
| `problems`, `inferred` | Findings, and the names of fields tackroom derived rather than read. |

### Access states

`collect` also reports one access entry per host and source, so an empty list is never ambiguous.

| State | Meaning |
| --- | --- |
| `found` | Read successfully, at least one job. |
| `empty` | Read successfully, no jobs, or the scheduler is not installed. |
| `unreadable` | The state exists but could not be read: a permission error, a database that will not open, a GUI launchd domain not available to the SSH session. |
| `not_scanned` | Not attempted: wrong OS for the source, host unreachable, or a documented gap. |

## The web view and actions

`tackroom view` has a Crons tab with the same inventory. It collects on first open, caches for a minute and collects again on request. Jobs that need attention come first, then one table per host with an access chip per source. A job needs attention when it has problems or is `dead` or `missing`; a paused job's old error does not count until it is turned on again.

A launchd job's row offers Run now, Pause and Resume; an owned job on the machine the view runs on also takes a new schedule. The tab has an Add cron button, and an open row of a job tackroom owns here has Edit and Delete. Any other row has neither, and says why (`edit_note`). The CLI verbs do the same for scripts and other clients, such as the [Herdr panel](herdr.md).

- Run is `launchctl kickstart gui/$UID/<label>`. Pause is `launchctl disable` then `bootout`, so the job stays off across logins and `collect` reports it `disabled`. Resume is `launchctl enable`, then `bootstrap` of the plist when it is not loaded. On another host the same script runs over SSH.
- An action addresses a job by host, source and id as the last scan reported it, never by a label or path from the request. The view answers `stale_crons` for a job that is not in its last scan.
- An action answers `{"output", "job"}`, where `job` is the job's record from a fresh scan of its host. The web view redraws that one row from it at once, so Pause turns into Resume without waiting; the full collect still runs in the background and does not overwrite a row changed after it began.
- A schedule change rewrites `crons.jobs.<id>.schedule` in the file that declares the job (`tackroom.local.yaml` when it has the id, else `tackroom.yaml`), then runs `cron apply`. A paused job must be resumed first.
- Only launchd jobs have actions. crontab, systemd timers and the other sources are read-only.

## In Claude Code and Pi

The same view is available inside Claude Code and Pi, from the [`claude/`](https://github.com/yourconscience/tackroom/tree/main/claude) and [`pi/`](https://github.com/yourconscience/tackroom/tree/main/pi) folders of the repository. Both run `tackroom` from your `PATH`, show which harnesses are out of sync, list the jobs from `cron collect`, and act through `tackroom cron run|pause|resume`. `/tackroom` opens them.

```bash
claude plugin install tackroom --marketplace yourconscience/tackroom
pi install git:github.com/yourconscience/tackroom@v2.0.0
```

The Claude Code plugin takes two options in `/config`: `tackroom`, the program to run, and `config`, the full path of a config file other than the default. On a Pi agent with a `packages` list in `tackroom.yaml`, add the `git:` entry there instead, because sync writes that list as a whole. The same install steps are in [plugins.md](plugins.md#tackroom-in-claude-code-and-pi).

## The `crons:` manifest

Who created a job and what it touches cannot be derived from a command line, so you declare it once. `collect` merges declared fields into the record with the same scheduler-native id, narrowed by `host` and `source` when set.

```yaml
crons:
  hosts: [local, laptop, vps]
  jobs:
    com.example.backup:            # scheduler-native id
      host: laptop                 # optional
      source: launchd              # optional
      created_by: human
      connectors: [git, ssh]
      effect: nightly commit pushed to the backup remote
```

A declared job that `collect` cannot find is reported as `state: missing`, but only when every place it could live was read: the host was reached and its `source` came back `found` or `empty`, or, with no `source`, no source on that host came back `unreadable`.

## Owned jobs: `cron apply`

A job with a `schedule` and a `command` is owned: tackroom writes it as a native unit and removes it when the declaration goes. Agent prompts on a schedule are not owned jobs; use herdr-automations for those.

```yaml
crons:
  scheduler: auto          # auto | launchd | systemd | crontab
  jobs:
    knowledge-sync:
      host: m1             # matched against crons.self; omit to run on every machine
      schedule: "*/30 * * * *"
      command: [~/.local/bin/knowledge-sync]
      created_by: human
```

- `crons.self` in each machine's `tackroom.local.yaml` names that machine. A job is in scope when its `host` is unset or equals `crons.self`; if any owned job has a `host` and `crons.self` is unset, `apply` stops before changing anything. `crons.scheduler` may also be set locally; the local value wins.
- Job ids match `^[a-z0-9][a-z0-9-]{0,62}$`, because the id becomes part of file names and unit labels.
- `~` and `$HOME` in `command` expand at render time, so units hold absolute paths.

`apply` loads the config, takes a lock at `~/.local/state/tackroom/cron/apply.lock`, reads every owned unit on the machine, and prints a plan per id: install, replace or remove. A missing or invalid config stops it; it never reads one as an empty manifest. `--dry-run` stops after the plan and prints the units.

Owned units are recognized by prefix: `dev.tackroom.<id>` for launchd, `tackroom-<id>.timer` for systemd, a `# tackroom:<id>` marker for crontab. Nothing without the prefix is read as owned or touched.

| `scheduler` | Unit | While the machine is asleep or off |
| --- | --- | --- |
| `launchd` (default on macOS) | `~/Library/LaunchAgents/dev.tackroom.<id>.plist` with `StartCalendarInterval` | Runs once on wake. |
| `systemd` (default on Linux) | `tackroom-<id>.timer` with `OnCalendar=` and `Persistent=true`, plus a oneshot service | Runs once when the user manager starts again. A server needs `loginctl enable-linger $USER`; `apply` warns when it is off. |
| `crontab` | One marked line | Skipped. Only when set explicitly. |

`auto` never falls back to crontab: when `systemctl --user` does not answer, `apply` stops and asks for an explicit `scheduler`. Output goes to `~/.local/state/tackroom/cron/<id>.log`; systemd keeps it in the journal.

The schedule is a 5-field cron expression and is translated, never passed through. A launchd job lists restricted fields as keys, an expression that expands past 64 intervals is refused, and `StartInterval` is never used because it drops firings during sleep. An expression that restricts both day of month and day of week is refused, since cron runs it when either matches and the other backends cannot say that.

`collect` maps an owned unit back to its manifest id by the prefix and marks it `created_by: tackroom` unless the manifest says otherwise.

## Add, edit and remove owned jobs

```bash
tackroom cron add ID --schedule "MIN HOUR DOM MON DOW" --command CMD [--arg ARG ...] [--host NAME] [--effect TEXT] [--local]
tackroom cron edit ID [--schedule EXPR] [--command CMD [--arg ARG ...]] [--host NAME] [--effect TEXT]
tackroom cron remove ID
```

All three take `--dry-run`, `--no-apply` and `--config PATH`; the id may come before, between or after the flags. `--dry-run` prints the YAML change and the `apply` plan and writes nothing. Without `--no-apply` each runs `cron apply` after writing, which stays the only writer of native units.

- A command is `--command` plus one `--arg` per argument, in order, stored as a YAML list: `--command /usr/bin/git --arg -C --arg /srv/notes --arg pull` becomes `command: [/usr/bin/git, -C, /srv/notes, pull]`. `edit --command` replaces the whole list.
- `add` writes `crons.jobs.<id>` to `tackroom.yaml`, or to `tackroom.local.yaml` with `--local`. `edit` changes only the fields you pass, in the file that declares the job (`tackroom.local.yaml` when it has the id). `--host ""` and `--effect ""` remove the field. `remove` deletes the declaration, from both files when both declare it, with the comment lines directly above it. When that was the last job, the `jobs:` and `crons:` keys that held it go too, so an `add` followed by a `remove` leaves the file as it was; a `crons:` block with another key or any comment keeps its place.
- Only the lines of the touched entry are rewritten. Every other byte of the file stays as it was, and comments inside the touched entry are kept but may be re-spaced. A file whose `crons` section is in flow style, or has no block `jobs` mapping to extend, is re-encoded whole instead. The write is atomic and keeps the file's mode. No backup copy is made, the same as for a schedule change; the config root is meant to be in git.
- Refused before anything is written: an invalid or reserved id, a schedule the `apply` parser rejects, an empty command, a command not found on `PATH` here (when this machine will install it and `--no-apply` is not given), a `host` while `crons.self` is unset, `add` of an id that exists, `edit` or `remove` of an id that is not declared, and an id that is declared without both `schedule` and `command`. The last kind are inventory annotations; edit them by hand.
- If `apply` fails after the write, the declaration stays saved and the error says so; fix the cause and run `tackroom cron apply`.

tackroom creates, changes and deletes only jobs it owns. Any launchd, systemd, crontab or other job without tackroom's prefix is never touched by these commands, and an id that `collect` reports but that is not declared is refused with an error that says so.

The view serves the same operations as `POST /api/crons/jobs`, with the token, origin and CSRF checks of `/api/crons/action`. The body is JSON: `action` (`add`, `edit` or `remove`) and `id`, plus `schedule`, `command` (a list, executable first), `host`, `effect`, `local`, `dry_run` and `no_apply` as above; unknown fields are refused. The answer carries `files` (the YAML files written), `diff`, `plan` (the lines `apply` printed) and `job`, the job's fresh record from a scan of this machine so a client can refresh one row (`null` after a removal or a dry run). The web view's Add cron, Edit and Delete post to it; the form takes the name (add only), schedule, command, one argument per line and an optional effect, sends only the fields an edit changed, and shows a refusal in the form. Delete asks first. Errors use the usual `{"error": {"code", "message"}}` shape: `invalid_request` (400) for a malformed body, `cron_action_failed` (422) for a refusal or a failed apply. A job for another host is refused with a message to edit `tackroom.yaml` and let that machine run `cron apply`.

## Built-in jobs

`tackroom cron` with no subcommand installs the job `setup` installs:

- `tackroom-pull` runs `tackroom pull` (`git pull --ff-only`, then `sync`), every 15 minutes by default.
- `--deps` installs `tackroom-deps` instead, weekly: `tackroom sync deps` updates patch-level dependencies in the config root (Go modules, pnpm, uv) and checks package age.
- `--remove` removes the job; `--interval` takes `5m`, `15m`, `30m`, `1h`, `6h`, `12h`, `daily` or `weekly`.

They are installed through the same planner as `apply`, on the selected scheduler and off every other, so a self-update cannot end up installed twice. `apply` never prunes these ids and the manifest cannot declare them. The first `tackroom cron` also removes an older crontab line that ran `tackroom pull`, `sync deps` or `deps update` for the same job.

While the pull job is missing, paused, broken or its last run failed, `status` prints a warning and `doctor` has a `periodic sync` check. `periodic_sync: false` (set by `setup --no-cron`) silences it.

## herdr-automations

[herdr-automations](https://github.com/DnzzL/herdr-automations) is a Herdr plugin that schedules agent prompts. tackroom reads it and never writes `automations.yaml`:

- `collect` reads the plugin's config and history on every scanned host where the config folder exists.
- The view shows its records in the Crons tab, and a host card shows whether its scheduler is running.
- On a machine with an `automations.yaml`, `tackroom doctor` reports a scheduler that is not installed as a login service, and agent names in the file that this machine has no binary for.

## What it does not do

- It runs no scheduler daemon. Owned jobs are native units that the host's scheduler fires.
- Scheduled jobs are not a sixth sync surface. `collect` reads state; `apply` writes only units with tackroom's prefix; neither writes harness configuration.
- `collect` never changes a job. The view and the action verbs change only a launchd job someone acts on, and `add`, `edit` and `remove` change only the declaration of an owned job.
- It does not ship job output or history anywhere. Everything stays on your machines.
