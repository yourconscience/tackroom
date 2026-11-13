# tackroom workspace in Tern

`tackroom tern` opens a Tern session named "tackroom" in the config root, with three panes:

- a shell in the config root,
- `tackroom status` on the right,
- `tackroom cron collect --table` below the status pane.

## What you get

- One command: `tackroom tern` (or `tackroom tern --config PATH` for a non-default config root).
- Each pane is kept open after its one-shot command finishes, so the output stays readable.
- `--dry-run` prints the `tern` commands without touching Tern.

## How it works

It shells out to the `tern` CLI:

```
tern new session tackroom --cwd <root> --keep-open
tern split @focused right --cwd <root> --keep-open -- tackroom status
tern split @focused down --cwd <root> --keep-open -- tackroom cron collect --table
```

## Watch out

- Needs `tern` on PATH; the Tern daemon starts itself if it isn't running.
- Needs `tackroom` on the PATH Tern runs programs with, or the status and crons panes report command-not-found.
- `--config` is passed to the status and crons commands only when given; the shell pane always opens in the config root.

## Not planned

- Editing Tern config or layouts behind the person's back.
- A tackroom daemon; `tackroom status` and `cron collect` run on demand.
