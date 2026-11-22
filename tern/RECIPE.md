# tackroom workspace in Tern

[Tern](https://stencil.so/tern) is the multiplexing terminal by Stencil Labs. `tackroom tern` opens a Tern session named "tackroom" in the config root, with two panes:

- a shell in the config root on top,
- `tackroom dash` below it, a live screen with the sync state and the scheduled jobs, with Run now, Pause and Resume for launchd jobs ([terminals.md](../docs/terminals.md#dash)).

## What you get

- One command: `tackroom tern` (or `tackroom tern --config PATH` with the path of a non-default `tackroom.yaml`).
- The dash pane is kept open when the dash ends, so an error stays readable.
- `--dry-run` prints the `tern` commands without touching Tern.

## How it works

It shells out to the `tern` CLI:

```
tern new session tackroom --cwd <root> --keep-open --json
tern split <shell> down --cwd <root> --keep-open --json -- <tackroom> dash
tern focus <shell> --json
```

`<shell>` is the block id `tern new session` prints, and `<tackroom>` is the absolute path of the running `tackroom`, so an older one earlier on the PATH cannot answer for it. Tern splits only right and down and has no size option, so the shell is the upper pane here and the two share the height. Tern does not show a new session by itself, so `@focused` would be the pane you are working in; the last command makes the window show the new session.

## Watch out

- Needs the Tern terminal's `tern` on PATH. Tern's scripting commands (`new`, `split`, `focus`) work with Tern running or from inside a Tern pane (`tern --help`).
- A second run fails with Tern's "a session is already called `tackroom`"; close that session first.
- Needs `tackroom` on the PATH Tern runs programs with, or the dash pane reports command-not-found.
- `--config` is passed to the dash only when given; both panes open in the folder that holds the config file.

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
- A tackroom daemon; the dash collects only while it runs.
