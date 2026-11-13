# tackroom in Tern

Tern is a multiplexing terminal. tackroom integrates with it optionally, in the same shape as the Herdr mod.

## Shipped

- `tackroom doctor` checks Tern: installed, version, runnable (`checkTern`). An absent Tern reports `skipped` (a pass), never a failure.
- `tackroom tern` opens a tackroom workspace in Tern: a "tackroom" session in the config root with a shell pane, a `tackroom status` pane, and a `tackroom cron collect --table` pane. `--dry-run` prints the `tern` commands; `--config PATH` uses a non-default root.

## Planned

- A Tern plugin pane that renders `tackroom cron collect` JSON with Run now / Pause / Resume, acting through the same `tackroom cron run|pause|resume` verbs (one contract with the web view and Herdr mod).
- `tackroom tern install` to copy a layout into Tern's config, backing up first.

## Constraint

Tern panes are terminals. Integration goes through the `tern` CLI and `tackroom` CLI verbs, and never edits Tern config behind the person's back.
