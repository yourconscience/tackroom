# Web view

`tackroom view` opens a local dashboard over your config: the sync state of every harness, outside sources with their pins, cooldowns and blocks, what is on disk, MCP and hook targeting, scheduled jobs and a YAML editor. It is where you look and act day to day; every action calls the same code as a CLI command, which stays the surface for scripts and SSH.

```bash
tackroom view                                  # start it in the background if needed, open your browser
tackroom view --no-open                        # the same, print the URL only
tackroom view --json                           # the same, as JSON for scripts and plugins
tackroom view --stop                           # stop it
tackroom view --foreground --no-open           # serve in this terminal until Ctrl-C
tackroom config validate                       # the same check from a shell
tackroom config print                          # shared, local and effective config
```

## Start it

`view` makes sure a view runs, then opens it. With none running it starts one in the background, which exits on its own after 30 minutes without requests. It prints `tackroom config UI ready:` followed by the URL on its own line, then opens your default browser. It does not open one with `--no-open`, with `ui.view.open_browser: false` or inside an SSH session (when `SSH_CONNECTION` is set). The view listens on loopback only and never runs `sync` on its own.

Flags and reruns:

- `--addr` is the listen address, default `ui.view.addr`, else `127.0.0.1:8765`. It must be loopback: `127.0.0.1`, `::1` or `localhost`. Anything else is refused. When another program holds that port and `--addr` was not given, `view` takes a free port, says so and prints that URL; later runs reuse the same port while it stays free. An explicit `--addr` that is taken is an error.
- `--config PATH` opens another config file; without it the file is [resolved as for every command](commands.md#config-resolution).
- A second `tackroom view` finds the running one, prints its URL and starts nothing. Without `--addr` it looks where the last view recorded its address.
- `--foreground` serves in the terminal you run it from until Ctrl-C, as `view` used to, for SSH sessions, `tailscale serve` and debugging. It never exits when idle. Under `nohup` it keeps running after the terminal closes.
- `--secure-cookie` implies `--foreground`: access through a proxy needs a view that does not exit when idle.
- Under a service manager, plain `tackroom view` serves in the foreground too, so a launchd job or systemd unit written for an older tackroom keeps working. tackroom detects launchd from `XPC_SERVICE_NAME` holding a job label (not empty, `0` or an `application.` name) and systemd from `INVOCATION_ID`, and only when stdout is not a terminal: a shell can inherit either variable, a service has no terminal. `--foreground` is the explicit form. `--json` and `--stop` never serve.

## Starting, stopping and the caller contract

Scripts, terminal plugins and editor extensions open the view through the command, never through its state files. Run `tackroom view --json` (with `--config PATH` when you have one), read `url` and open it, adding a tab fragment such as `#crons` if you want one.

`tackroom view` and `tackroom view --json`:

- If a view for this token answers at the recorded address (or at `--addr`), it reports that view and exits 0 at once.
- If none answers, it starts `tackroom view --foreground` as a background process in its own session, with its output in `view.log`, so it outlives the caller. The view gets the caller's environment with `PATH` from your login shell (`$SHELL -l`), plus the login shell's variables the caller does not set, so it finds your harnesses as a terminal does even when the caller is an app with a minimal `PATH`. The caller's own values, such as `HOME`, `XDG_*` and `*_HOME`, win. Without `$SHELL`, or when the shell does not answer within 5 seconds, it gets the caller's environment unchanged. It waits until the view answers, reports it and exits 0. If the view does not answer within 10 seconds, it stops that process and exits 1 with the end of `view.log` on stderr.
- Concurrent calls start one view: the check and the start run under an exclusive lock on `view.lock`, which is removed afterwards.
- `--json` prints one JSON object on stdout and nothing else, and opens no browser:

  ```json
  {"url": "http://127.0.0.1:8765/?token=...", "addr": "127.0.0.1:8765", "pid": 4242, "started": true, "idle_timeout": "30m"}
  ```

  `url` is the signed-in startup URL. `addr` is the address the view listens on, which differs from the configured one after a fallback. `pid` is the view's process. `started` is true only when this call started it. `idle_timeout` is the view's own setting, `"0"` for one that never exits when idle (a `--foreground` view). A view started by an older tackroom reports `pid` 0 and an empty `idle_timeout`. The output is pretty-printed; parse it as JSON.
- Exit codes: 0 when a view answers, 1 when none could be started or a flag or the config is invalid (the reason on stderr).

`tackroom view --stop` stops the view that answers for this token: it sends SIGTERM to the process that view reports, only when `view.pid` records the same one, and waits until it stops answering. Exit 0 means no view answers now: it stopped one, or none was running, and it says which. Exit 1 means a view still answers: it did not report its process (an older tackroom started it), `view.pid` names another process, the signal failed, or it still answered 5 seconds later. It does not read the config, so it also works while the config is invalid.

A background view exits when no HTTP request has reached it for `ui.view.idle_timeout` (default `30m`). Every request resets the timer, and an open page sends one every 60 seconds while it is visible, and also while it is hidden with unsaved Config changes, so a view stays up while you have it open and exits after you close the last tab or leave it hidden. When that request fails, the page says `tackroom view stopped; run tackroom view`. `0` keeps it running until `--stop`; any other value is at least `2m`, so the page's request once a minute always lands in time. It still [restarts on upgrade](#restart-on-upgrade).

State files, in `$XDG_STATE_HOME/tackroom` (default `~/.local/state/tackroom`), or beside `--token-file` and named after it:

| File | Holds |
|---|---|
| `view.token` | The session token, kept across restarts. |
| `view.addr` | The address the last view listened on. |
| `view.pid` | The running view's process ID; removed when it exits. |
| `view.log` | What background views printed, appended at each start. |
| `view.lock` | Present only while a call checks and starts a view. |

### Settings

Under `ui.view` in either config file; a key set in `tackroom.local.yaml` replaces the same key from `tackroom.yaml`. `tackroom config validate` and the Config tab report a bad value.

```yaml
ui:
  view:
    addr: 127.0.0.1:8765   # loopback only; when taken, a free port is used and reported
    idle_timeout: 30m      # 0, or a Go duration of at least 2m (2m, 30m, 2h); 0 keeps it running until --stop
    open_browser: true     # false behaves as --no-open
```

| Key | Default | Meaning |
|---|---|---|
| `ui.view.addr` | `127.0.0.1:8765` | Address a view takes when `--addr` is not given. Unlike `--addr`, a taken port falls back to a free one, which `view.addr` records. |
| `ui.view.idle_timeout` | `30m` | How long a background view waits without a request before it exits. `0` never exits. Any other value must be at least `2m`; `30`, `30s` and negative values are refused. |
| `ui.view.open_browser` | `true` | `false` makes `tackroom view` print the URL without opening a browser, as `--no-open` does. |

## Find your way around

The seven tabs are Overview, Skills, Roles, MCP & hooks, Unmanaged, Crons and Config.

| Tab | Shows |
|---|---|
| Overview | Every outside source: pinned, in cooldown or blocked, with the reason and the command that skips the cooldown or accepts the finding, the same items `tackroom status` lists. |
| Skills, Roles | Matrices of what is on disk per harness. |
| MCP & hooks | MCP and hook targeting per harness. |
| Unmanaged | Items in harness folders that tackroom did not put there. It never deletes them. |
| Crons | The inventory from [crons.md](crons.md#run-pause-and-resume-jobs). |
| Config | Either config file as YAML ([What it can change](#what-it-can-change)). |

With more than one host in `crons.hosts`, Overview also has a Machines block right under the counts: one row per machine with its health, a one-line summary, its tackroom version, its config commit against this machine's and, when something is wrong, the command that fixes it, the same as `tackroom hosts`. It appears once the hosts answer and is cached for a minute.

### Add links to the header

To add entries to the header's Links menu, list them under `ui.links` in either config file; a `ui.links` list in `tackroom.local.yaml` replaces the shared one:

```yaml
ui:
  links:
    - name: Usage
      url: https://usage.example.com   # an https URL or a path such as /usage
```

## When it asks for a token

Requests straight to `http://127.0.0.1:PORT` or `localhost` from the same machine need no token, so a bookmark keeps working. A request through a proxy, or with a non-loopback `Host` or forwarding headers, needs the session. The startup URL carries a token, which the server swaps for an `HttpOnly`, `SameSite=Strict` cookie. A browser without that cookie gets a page that asks for the token.

On a machine shared with other users, anyone who can reach its loopback can use the view.

The token is stored in `view.token`, created with mode 0600 on first use, next to a `view.addr` file with the listen address. Both live in `$XDG_STATE_HOME/tackroom` (default `~/.local/state/tackroom`). `--token-file PATH` keeps the token somewhere else, and the other [state files](#starting-stopping-and-the-caller-contract) sit beside it. Because the token persists, the URL survives restarts.

## Open it from another machine

```bash
tackroom view --no-open --ssh-host me@server
```

With `--ssh-host user@host`, or inside an SSH session where it is derived from `SSH_CONNECTION`, the view also prints a ready tunnel command:

```
  from your machine:  ssh -L 8765:localhost:8765 me@server
  then open the URL above locally.
```

A view started over SSH keeps running after you log out, until it is idle for `ui.view.idle_timeout`; use `--foreground` to tie it to the session.

For HTTPS over a tailnet you expose the loopback port yourself. `--secure-cookie` applies to requests that come through the proxy, so the same view still works at its local `http://127.0.0.1` URL. It implies `--foreground`, so the view does not exit while nobody uses it:

```bash
tackroom view --no-open --secure-cookie --addr 127.0.0.1:8765
tailscale serve --bg --set-path /tackroom http://127.0.0.1:8765
tailscale serve status                         # prints the https URL on your tailnet
```

## On a phone

The view is a web app you can add to a phone's home screen; there is no native app. With the two commands above running and the phone on the same tailnet:

1. Send the startup URL to the phone with `https://<machine>.<tailnet>.ts.net/tackroom/` in place of `http://127.0.0.1:8765/`, keeping `?token=...`, and open it.
2. Add it to the home screen: Share, then Add to Home Screen in Safari; Add to Home screen in Chrome's menu.
3. The icon opens the view in its own window. If it asks for the token, paste the value from `view.token`.

The manifest and icons are served without the token; they carry no config data. There is no service worker and no offline copy: every screen needs the running view.

## Restart on upgrade

A running view checks every 15 seconds whether the `tackroom` binary it started from has been replaced, by `brew upgrade`, `npm install -g` or `go install`. Once the new file has stayed unchanged for one more check, the view restarts itself in place with `--foreground` and `--no-open`, each added once, and prints `tackroom binary at <path> changed; restarting view`.

The token file keeps the URL and open sessions valid. A background view keeps its idle timeout across the restart. If the restart fails, the view keeps serving the old version and says so.

## What it can change

- **Config files.** Toggles on Overview and MCP & hooks save to the file that defines the item: `tackroom.local.yaml` when it has the entry, else `tackroom.yaml`. The Config tab edits either file as YAML, with a check step before Save.
  - Edits keep comments and keys tackroom does not know.
  - Each save checks the file's revision, so a change made elsewhere is reported as stale instead of overwritten.
  - Turning a harness off stops syncing it and removes nothing from disk.
- **Harness files, through Sync.** Sync opens a per-harness plan first. Each destructive item must be ticked before it applies: removing a skill, role, plugin, MCP server, hook or Pi package, or overwriting a role. If the plan or the config changed since the preview, you preview again.
- **Launchd jobs.** The Crons tab runs, pauses and resumes a launchd job, changes the schedule of a job tackroom owns on that machine, and adds, edits and deletes owned jobs ([crons.md](crons.md#run-pause-and-resume-jobs)).

## What it cannot change

- The Effective config layer is read-only. It is what `sync` uses, merged from the two files.
- Saving never runs `sync`. Nothing reaches a harness until you preview and apply.
- Sync never replaces a native file that differs from the shared copy. That stays `tackroom sync --replace-conflicts` ([troubleshooting.md](troubleshooting.md#conflicts)).
- It does not edit skill or role files, delete unmanaged items, install harnesses or add external skill sources.
- It does not listen on a non-loopback address.
