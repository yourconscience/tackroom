# Rex

Rex is a terminal multiplexer with sessions, windows, blocks, a CLI and a Lua `do` command. tackroom's only Rex integration today is a `doctor` check; Rex stays optional.

```bash
tackroom doctor            # includes a "rex" check
```

## What works

- The `rex` check runs `rex version`. With Rex absent it reports `skipped`, which counts as a pass. With Rex installed but not runnable it warns. Otherwise it prints the version line.
- tackroom never reads or writes Rex's configuration.

## Show jobs in a Rex block

There is no Rex panel. A Rex block is a terminal, so run the inventory in one:

```bash
tackroom status
tackroom cron collect --table
```

## Not built

- `tackroom rex`, a counterpart to `tackroom tern` ([tern.md](tern.md)) that would open a ready-made workspace.
- Run now, Pause and Resume as Rex actions. The verbs they would call exist already: `tackroom cron run|pause|resume` ([crons.md](crons.md)).
