# Terminal view

> **Deprecated.** `tackroom tui` prints a notice on every run and will be removed in 3.0. Use [`tackroom view`](view.md), which works on a desktop, on a phone and in a side panel, or the [Herdr panel](herdr.md). In a terminal or over SSH, `tackroom status`, `tackroom cron collect --table` and `tackroom doctor` print the same facts.

`tackroom tui` is a full-screen, read-only view of what `status`, the lock, `cron collect` and `doctor` already report. It changes nothing, so it is safe to leave open while you work.

```bash
tackroom tui [--config PATH]
```

It needs a terminal on both stdin and stdout; otherwise it prints `tackroom tui needs a terminal; use tackroom status` and exits non-zero. It follows `NO_COLOR` and the terminal's own colors. Rows are cut with `…` instead of wrapped, so a wider terminal shows more.

## Tabs

| Tab | Shows |
| --- | --- |
| Agents | One row per enabled harness: product name, config key, sync state (synced, drifted, not detected, config unreadable) and managed counts. The facts of `tackroom status`. |
| Lock | Every external skill source and every held package: name, source, pinned commit, state (pinned, held with its age against the 72 hour hold, blocked, or why the pin is off) and audit result (pass, findings, critical). The facts of `tackroom.lock`, the freshness hold and `doctor audit`. |
| Crons | The jobs of `tackroom cron collect`: host, source, id, schedule, state, last run and result. |
| Doctor | The checks of `tackroom doctor` with their marks, and the summary line. |

Each tab loads in the background and shows `loading…` until it is ready. A tab that fails to load shows the error in place and the other tabs keep working.

## Keys

| Key | Action |
| --- | --- |
| `tab`, `shift+tab`, `1` to `4` | Switch tab |
| `j`, `k`, `↓`, `↑` | Move down and up |
| `r` | Reload every tab |
| `?` | Show or hide the key help |
| `q`, `ctrl+c` | Quit |

The Doctor tab runs the same checks as `tackroom doctor`, including the package age lookup, which asks the npm and PyPI registries about the packages your MCP servers and Pi packages reference. `r` runs the checks again.
