# Web view

`tackroom view` opens a local dashboard over your config: the sync state of every harness, what is on disk, MCP and hook targeting, scheduled jobs and a YAML editor. It listens on loopback only and never runs `sync` on its own.

```bash
tackroom view                                  # start it and open your browser
tackroom view --no-open --addr 127.0.0.1:8765  # start it and print the URL only
tackroom config validate                       # the same check from a shell
tackroom config print                          # shared, local and effective config
```

## Start it

`view` prints `tackroom config UI ready:` followed by the URL on its own line, then opens your default browser. It does not open one with `--no-open` or inside an SSH session (when `SSH_CONNECTION` is set).

- `--addr` is the listen address, default `127.0.0.1:8765`. It must be loopback: `127.0.0.1`, `::1` or `localhost`. Anything else is refused.
- `--config PATH` opens another config file; without it the file is [resolved as for every command](commands.md#config-resolution).
- A second `tackroom view` finds the running one, prints its URL and starts nothing. Without `--addr` it looks where the last view recorded its address.

The seven tabs are Overview, Skills, Roles, MCP & hooks, Unmanaged, Crons and Config. Skills and Roles are matrices of what is on disk per harness. Unmanaged lists items in harness folders that tackroom did not put there and never deletes them. Crons shows the inventory from [crons.md](crons.md#the-web-view-and-actions).

To add entries to the header's Links menu, list them under `ui.links` in either config file:

```yaml
ui:
  links:
    - name: Usage
      url: https://usage.example.com   # an https URL or a path such as /usage
```

## The token

Requests straight to `http://127.0.0.1:PORT` or `localhost` from the same machine need no token, so a bookmark keeps working. A request through a proxy, or with a non-loopback `Host` or forwarding headers, needs the session. The startup URL carries a token, which the server swaps for an `HttpOnly`, `SameSite=Strict` cookie. A browser without that cookie gets a page that asks for the token.

On a machine shared with other users, anyone who can reach its loopback can use the view.

The token is stored in `view.token`, created with mode 0600 on first use, next to a `view.addr` file with the listen address. Both live in `$XDG_STATE_HOME/tackroom` (default `~/.local/state/tackroom`). `--token-file PATH` keeps the token somewhere else, and the address file sits beside it. Because the token persists, the URL survives restarts.

## From another machine

```bash
tackroom view --no-open --ssh-host me@server
```

With `--ssh-host user@host`, or inside an SSH session where it is derived from `SSH_CONNECTION`, the view also prints a ready tunnel command:

```
  from your machine:  ssh -L 8765:localhost:8765 me@server
  then open the URL above locally.
```

For HTTPS over a tailnet you expose the loopback port yourself. `--secure-cookie` applies to requests that come through the proxy, so the same view still works at its local `http://127.0.0.1` URL:

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

A running view checks every 15 seconds whether the `tackroom` binary it started from has been replaced, by `brew upgrade`, `npm install -g` or `go install`. Once the new file has stayed unchanged for one more check, the view restarts itself with `--no-open` and prints `tackroom binary at <path> changed; restarting view`.

The token file keeps the URL and open sessions valid. If the restart fails, the view keeps serving the old version and says so.

## What it can change

- **Config files.** Toggles on Overview and MCP & hooks save to the file that defines the item: `tackroom.local.yaml` when it has the entry, else `tackroom.yaml`. The Config tab edits either file as YAML, with a check step before Save. Edits keep comments and keys tackroom does not know. Each save checks the file's revision, so a change made elsewhere is reported as stale instead of overwritten. Turning a harness off stops syncing it and removes nothing from disk.
- **Harness files, through Sync.** Sync opens a per-harness plan first. Each destructive item must be ticked before it applies: removing a skill, role, plugin or MCP server, or overwriting a role. If the plan or the config changed since the preview, you preview again.
- **Launchd jobs.** The Crons tab runs, pauses and resumes a launchd job, changes the schedule of a job tackroom owns on that machine, and adds, edits and deletes owned jobs ([crons.md](crons.md#the-web-view-and-actions)).

## What it cannot change

- The Effective config layer is read-only. It is what `sync` uses, merged from the two files.
- Saving never runs `sync`. Nothing reaches a harness until you preview and apply.
- Sync never replaces a native file that differs from the shared copy. That stays `tackroom sync --replace-conflicts` ([troubleshooting.md](troubleshooting.md#conflicts)).
- It does not edit skill or role files, delete unmanaged items, install harnesses or add external skill sources.
- It does not listen on a non-loopback address.
