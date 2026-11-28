# tackroom in the menu bar

[SwiftBar](https://github.com/swiftbar/SwiftBar) shows the output of a script as an item in the macOS menu bar. `swiftbar/tackroom.5m.sh` is such a script. It runs [`tackroom dash --json`](../docs/terminals.md#as-json) every 5 minutes and shows what [`tackroom dash`](../docs/terminals.md#dash) shows: whether everything is in sync and which scheduled jobs fail. tackroom has no command for it and never edits SwiftBar's settings.

It needs tackroom 3.3 or later, which added `dash --json`, and only what macOS ships: `/bin/sh`, `plutil`, `sed`, `tr`, `head`, `mktemp` and `date`. SwiftBar runs on macOS 12 and later.

## What it shows

In the menu bar, a mark for the dash's level:

| Level | Mark | Number |
| --- | --- | --- |
| `ok`, all clear | a check mark in a circle | none |
| `warn` | a warning triangle | what fails plus what needs attention |
| `fail` | a warning octagon | what fails plus what needs attention |
| tackroom missing or `dash --json` failed | a warning triangle | none |

The marks are SF Symbols, set with SwiftBar's `sfimage`. With `TACKROOM_SWIFTBAR_PLAIN` set to anything, the script uses the text marks `✓`, `▲` and `✖` instead.

In the dropdown:

- The headline, such as `1 failing, 3 need attention`, and the sentences under it in the dash.
- One row for each job that fails or whose last run failed, with its name, its host when it is not this machine, and its result, such as `nightly-index on m1: exit 2`. Under a launchd job, a Run now item.
- Sync now, Open view and Refresh.
- A dimmed last line with the time of the check, in your local time.

When `tackroom` cannot be found or `dash --json` fails, the dropdown is one line that says which, and Refresh.

## Install

1. Install SwiftBar, with Homebrew or from its [releases](https://github.com/swiftbar/SwiftBar/releases):

   ```sh
   brew install swiftbar
   ```

   On its first launch SwiftBar asks for a Plugin Folder. Every file in it becomes a plugin.

2. Put the script in that folder and make it executable. From a tackroom checkout, with `~/Documents/SwiftBar` standing for your Plugin Folder:

   ```sh
   ln -s "$PWD/swiftbar/tackroom.5m.sh" ~/Documents/SwiftBar/tackroom.5m.sh
   # or copy it:
   cp swiftbar/tackroom.5m.sh ~/Documents/SwiftBar/
   chmod +x ~/Documents/SwiftBar/tackroom.5m.sh
   ```

   Without a checkout, download the file:

   ```sh
   curl -fsSL https://raw.githubusercontent.com/yourconscience/tackroom/main/swiftbar/tackroom.5m.sh -o ~/Documents/SwiftBar/tackroom.5m.sh
   chmod +x ~/Documents/SwiftBar/tackroom.5m.sh
   ```

3. SwiftBar picks the file up and runs it. The item appears in the menu bar. Hold Cmd and drag it to where you want it.

## Change the refresh interval

SwiftBar reads the interval from the file name, `{name}.{time}.{ext}`: rename the file. `tackroom.15m.sh` refreshes every 15 minutes and `tackroom.1h.sh` every hour. SwiftBar forgets the item's place in the menu bar when its file is renamed.

Each run collects the sync state, the crons and the machines as the dash does, and probes every host in `crons.hosts` over SSH. With several hosts one run takes a while, so keep the interval in minutes.

## What each menu item does

Actions run in the background, with no Terminal window and no output shown, and then ask SwiftBar to run the script again. To see what a command prints, run it in a shell.

| Item | Runs |
| --- | --- |
| Run now | `tackroom cron run ID`, or `tackroom cron run --host HOST ID` for a job on another machine. It starts the launchd job as [crons.md](../docs/crons.md#run-pause-and-resume-jobs) describes. A job from another scheduler has no Run now, because the actions work on launchd jobs only. The result shows at a later refresh, once the job has finished. |
| Sync now | `tackroom sync`. It asks nothing first, unlike `s` in the dash. |
| Open view | `tackroom view`: starts the web view when none runs and opens it in your default browser. |
| Refresh | Runs the script again. |

## Settings (optional)

```sh
TACKROOM_BIN=/path/to/tackroom      # tackroom, when it is in none of the places below
TACKROOM_SWIFTBAR_PLAIN=1           # text marks instead of SF Symbols
```

SwiftBar does not pass your shell's variables. Set them on a line near the top of your copy of the script, or with a `<swiftbar.environment>[TACKROOM_BIN=/path/to/tackroom]</swiftbar.environment>` comment, which SwiftBar's README describes under "Other Parameters" and which has not been run here.

## Watch out

- SwiftBar starts plugins with a minimal environment, without your shell's `PATH`. The script looks for `tackroom` in `$TACKROOM_BIN`, `~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin` and then `PATH`, and takes the first one it finds. With more than one installed, for example an old copy in `~/.local/bin` and a newer one from Homebrew, that may be the old one. Set `TACKROOM_BIN` to the one you want.
- The script passes no `--config`, so it reads the config tackroom finds without one, `~/.agents/tackroom.yaml`. SwiftBar does not pass your `TACKROOM_HOME`.
- Text from your jobs and notes goes into menu lines, where `|` starts a parameter. The script changes `|` to `¦`, `"` to `″` and a leading `-` to `‐`, and turns control characters into spaces. An action takes the job's id and host as separate parameters, and only when they are letters, digits, `.`, `_`, `@` and `-` and do not start with `-`; otherwise the job has no Run now. When the path to `tackroom` has a space or another unusual character, the script shows no actions and says so.
- A tackroom older than 3.3 does not know `--json`, so the menu shows `tackroom dash --json failed` and the first line of its error, which for an unknown flag is Go's `flag provided but not defined: -json` (read from the 3.2.2 code, not run). Then the script found an older copy first.
- The script uses `plutil -extract KEY raw`, which reads nested keys and the length of an array. It was checked on macOS 27.2.1 only.
- Checked: the script's output for an all-clear answer, failing and warning jobs on this machine and on another host, job names with `|`, quotes and spaces, a missing `tackroom` and a failing one, against a stub `tackroom` that prints a fixed document, with `sh -n` and ShellCheck. Not checked: SwiftBar drawing the menu and running the actions.

## Not planned

- Editing SwiftBar's preferences or its Plugin Folder behind the person's back.
- Pause and Resume in the menu. The [dash](../docs/terminals.md#dash) and the [web view](../docs/view.md) have them.
