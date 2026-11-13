# tackroom in Rex

Rex is a terminal multiplexer (sessions, windows, blocks, clients) with a CLI, Lua scripting and a server API. tackroom integrates with it optionally, in the same shape as the Herdr mod.

## Shipped

- `tackroom doctor` checks Rex: installed, version, runnable (`checkRex`). An absent Rex reports `skipped` (a pass), never a failure, because Rex is optional.

## Planned

- `tackroom rex`, the same workspace as `tackroom tern`: a session with a shell, `tackroom status` and `tackroom cron collect --table`, built with `rex new` and `rex split`.
- Run now / Pause / Resume as Lua custom actions (`rex.action` in `init.lua`) that call `tackroom cron run|pause|resume`.

## Constraint

Rex blocks are terminals and Rex 0.1.0 has no plugin surface for other block types, so there is no native Crons panel to build yet. Integration goes through the `rex` CLI (`--json` for machine-readable output), Lua in `init.lua` and `tackroom` CLI verbs, and never edits Rex config behind the person's back.
