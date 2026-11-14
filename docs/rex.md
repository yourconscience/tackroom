# tackroom in Rex

[Rex](https://www.superlogical.com/rex) is the terminal multiplexer by Superlogical, with sessions, windows, blocks, a `rex` CLI and Lua configuration. tackroom integrates with it optionally.

## Shipped

- `tackroom doctor` checks Rex (`checkRex`): it runs `rex version`. With Rex absent it reports `skipped`, which counts as a pass. With Rex installed but not runnable it warns. Otherwise it prints the version line.
- `tackroom rex` opens a tackroom workspace in Rex: a session labelled `tackroom` in the config root with a shell pane, a `tackroom status` pane, and a `tackroom cron collect --table` pane, then asks the Rex app to show it. The panes run the same `tackroom` that opened them, by its absolute path. Showing the session needs the app open with Remote Control turned on in its settings; otherwise `tackroom rex` prints one line saying to open the session from Rex's sidebar. A second run shows the existing `tackroom` session instead of opening another. `--dry-run` prints the `rex` commands; `--config PATH` takes a config file, as every other command does, and opens the session in its folder. Without it the root follows `$TACKROOM_HOME`, else `~/.agents`. An absent `rex` is an error. Recipe: [`rex/RECIPE.md`](../rex/RECIPE.md).
- Palette actions: `rex/tackroom.lua`, which you `require` from Rex's `init.lua`, adds three actions under "tackroom" in the command palette: Open tackroom Workspace (`tackroom rex`), Open tackroom Crons (`tackroom cron collect --table` in a new tab) and Open tackroom View on Crons (the running `tackroom view` on its Crons tab in the default browser). Install: [`rex/RECIPE.md`](../rex/RECIPE.md#palette-actions).
- Crons with actions: Rex has no panel API, so Run now, Pause and Resume for launchd jobs go through the web view. Open tackroom View on Crons opens its Crons tab ([crons.md](crons.md)). In Rex itself the jobs are the read-only `cron collect` table.

## Constraint

Rex blocks are terminals, and Rex has no plugin or custom panel API. Integration goes through the `rex` CLI, Lua actions the person adds to their own `init.lua`, and `tackroom` CLI verbs. tackroom never reads or writes Rex's configuration.
