# tackroom in Tern

[Tern](https://stencil.so/tern) is the multiplexing terminal by Stencil Labs (`/Applications/Tern.app` on macOS, with a `tern` CLI). tackroom integrates with it optionally. Other tools that also install a `tern` command are not supported.

## Shipped

- `tackroom doctor` checks Tern: installed, version, runnable (`checkTern`). An absent Tern reports `skipped` (a pass), never a failure. A `tern` on PATH whose `--version` is not Tern's `tern VERSION (COMMIT)` also reports `skipped`.
- `tackroom tern` opens a tackroom workspace in Tern: a "tackroom" session in the config root with a shell pane, a `tackroom status` pane, and a `tackroom cron collect --table` pane. The window then shows that session. `--dry-run` prints the `tern` commands; `--config PATH` takes a config file, as every other command does, and opens the session in its folder. Without it the root follows `$TACKROOM_HOME`, else `~/.agents`. Recipe: [`tern/RECIPE.md`](../tern/RECIPE.md).
- A crons panel: the `tackroom-crons` Tern plugin shows `tackroom cron collect` as a block, host health and per-host jobs, with Run now / Pause / Resume for launchd jobs, through the same `tackroom cron run|pause|resume` verbs as the web view and the Herdr panel. Install from a checkout with `tern plugin link ./tern/plugins/tackroom-crons` (or `tern plugin install` for a copy), then open it from the palette or `Ctrl+Alt+C`. Recipe: [`tern/plugins/tackroom-crons/RECIPE.md`](../tern/plugins/tackroom-crons/RECIPE.md).

## Planned

- `tackroom tern install` to copy a layout into Tern's config, backing up first.

## Constraint

Tern panes are terminals. Integration goes through the `tern` CLI, the plugin API, and `tackroom` CLI verbs, and never edits Tern config behind the person's back.
