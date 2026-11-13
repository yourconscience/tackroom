# tackroom in Rex

Rex is a terminal multiplexer (sessions, windows, blocks, clients) with a CLI, Lua scripting, a server API, and block plugins. tackroom integrates with it optionally, in the same shape as the Herdr mod.

## Shipped

- `tackroom doctor` checks Rex: installed, version, runnable (`checkRex`). An absent Rex reports `skipped` (a pass), never a failure, because Rex is optional.

## Planned (same contract as the Herdr mod)

- `rex/` — a Rex script or session recipe that opens a tackroom workspace (`rex new` a session with blocks running `tackroom status`, `tackroom cron collect`, and the sync preview).
- A Rex block plugin or Lua script that renders `tackroom cron collect` JSON (`rex ... --json`) with Run now / Pause / Resume, acting through `tackroom cron run|pause|resume`.
- `tackroom rex install` to install the plugin/script, backing up first.

## Constraint

Rex blocks are terminals; the CLI is `rex` with `--json` for machine-readable output. Integration uses Rex's plugin/Lua surface and `tackroom` CLI verbs, and never edits Rex config behind the person's back.
