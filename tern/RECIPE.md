# tackroom workspace in Tern

[Tern](https://stencil.so/tern) is the multiplexing terminal by Stencil Labs. `tackroom tern` opens a Tern session named "tackroom" in the config root, with three panes:

- a shell in the config root,
- `tackroom status` on the right,
- `tackroom cron collect --table` below the status pane.

## What you get

- One command: `tackroom tern` (or `tackroom tern --config PATH` with the path of a non-default `tackroom.yaml`).
- Each pane is kept open after its one-shot command finishes, so the output stays readable.
- `--dry-run` prints the `tern` commands without touching Tern.

## How it works

It shells out to the `tern` CLI:

```
tern new session tackroom --cwd <root> --keep-open --json
tern split <shell> right --cwd <root> --keep-open --json -- tackroom status
tern split <status> down --cwd <root> --keep-open --json -- tackroom cron collect --table
tern focus <shell> --json
```

`<shell>` and `<status>` are the block ids the earlier commands print. Tern does not show a new session by itself, so `@focused` would be the pane you are working in; the last command makes the window show the new session.

## Watch out

- Needs the Tern terminal's `tern` on PATH. Tern's scripting commands (`new`, `split`, `focus`) work with Tern running or from inside a Tern pane (`tern --help`).
- A second run fails with Tern's "a session is already called `tackroom`"; close that session first.
- Needs `tackroom` on the PATH Tern runs programs with, or the status and crons panes report command-not-found.
- `--config` is passed to the status and crons commands only when given; every pane opens in the folder that holds the config file.

## Crons panel

The `tackroom-crons` plugin adds a block that shows every cron job tackroom collects, with Run now / Pause / Resume for launchd jobs. From a tackroom checkout:

```
tern plugin link ./tern/plugins/tackroom-crons
# or, to install a copy:
tern plugin install ./tern/plugins/tackroom-crons
```

Open it with **Open tackroom crons** in the palette, or `Ctrl+Alt+C`. Details: [`plugins/tackroom-crons/RECIPE.md`](plugins/tackroom-crons/RECIPE.md).

## Not planned

- Editing Tern config or layouts behind the person's back.
- A tackroom daemon; `tackroom status` and `cron collect` run on demand.
