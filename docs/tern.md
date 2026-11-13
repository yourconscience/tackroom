# tackroom in Tern

Tern is a multiplexing terminal. tackroom integrates with it optionally, in the same shape as the Herdr mod: a doctor check plus an optional layout/recipe, shipped in the repo.

## Shipped

- `tackroom doctor` checks Tern: installed, version, runnable (`checkTern`). An absent Tern reports `skipped` (a pass), never a failure, because Tern is optional.

## Planned (same contract as the Herdr mod)

- `tern/` — a Tern session layout/recipe that opens a tackroom workspace: one pane for `tackroom status`, one for `tackroom cron collect`, one for the sync preview.
- A Tern plugin pane that renders `tackroom cron collect` JSON with Run now / Pause / Resume, acting through the same `tackroom cron run|pause|resume` verbs the web view and Herdr mod use.
- `tackroom tern install` to copy the layout into Tern's config, backing up first.

## Constraint

Tern panes are terminals. Integration goes through Tern's plugin/layout system and the `tackroom` CLI verbs, not a web view, and never edits Tern config behind the person's back.
