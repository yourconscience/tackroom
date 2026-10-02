# knowledge-sync

Small Go helper that keeps the local knowledge vault synchronized with its configured git remote.

Runtime is intentionally separate from this source tree:

- source: `memory/tools/knowledge-sync/`
- installed executable: `~/.local/bin/knowledge-sync`
- user LaunchAgent: `~/Library/LaunchAgents/ai.knowledge-sync.plist`

The LaunchAgent should point at the stable installed executable, not at this source directory. Build and install with:

```bash
GOWORK=off go build -o ~/.local/bin/knowledge-sync .
```

`GOWORK=off` keeps this small standalone module independent from the parent tackroom Go workspace.

Configuration is read from environment variables, with defaults in `main.go`:

- `KNOWLEDGE_REPO`, default `$KNOWLEDGE_DIR` or `~/Workspace/knowledge`
- `KNOWLEDGE_REMOTE`, default `origin`. If your vault pushes to a non-`origin` remote, set this env var to that remote name. The default was previously `vps`; existing installs that relied on that must now set `KNOWLEDGE_REMOTE=vps` in their LaunchAgent/env, since the tool no longer assumes a `vps` remote.
- `KNOWLEDGE_BRANCH`, default `main`

The helper uses a lock file under the knowledge repo git directory, commits dirty vault changes, fetches/merges from the remote branch, and pushes back to the same remote branch.

## Pinned commit identity (R6)

Every commit pins an explicit author/committer identity so unattended runs
(the LaunchAgent has no `GIT_AUTHOR_*` env and the vault may carry no git config)
can **never** fall back to a host-detected `user@hostname` identity -- the
historical source of the "your name and email address were configured
automatically based on your username and hostname" warning and the resulting
`EX_CONFIG` job status.

Identity is resolved in this order, then passed as `git -c user.name=... -c
user.email=...` on the commit:

1. `KNOWLEDGE_GIT_AUTHOR_NAME` / `KNOWLEDGE_GIT_AUTHOR_EMAIL` (dedicated pin)
2. `GIT_AUTHOR_NAME` / `GIT_AUTHOR_EMAIL` (standard git env)
3. repo-local or global `git config user.name` / `user.email`

If none resolve, the tool refuses to commit rather than writing a host-detected
identity. To pin the identity for the unattended LaunchAgent, do **one** of:

```bash
# a) repo-local config on the vault (survives launchd's minimal env)
git -C ~/Workspace/knowledge config user.name  "Your Name"
git -C ~/Workspace/knowledge config user.email "you@example.com"

# b) or set the dedicated env in ~/Library/LaunchAgents/ai.knowledge-sync.plist
#    KNOWLEDGE_GIT_AUTHOR_NAME / KNOWLEDGE_GIT_AUTHOR_EMAIL
```

## Reindex after sync (R2)

After a successful pull/merge/push, `knowledge-sync` triggers a bounded,
best-effort refresh of the derived `memsearch` index (collection `ai`) so the
index tracks whatever the sync pulled in -- replacing a separate reindex cron.
It mirrors the capture-side trigger (`memory/hooks/common.sh`
`refresh_index_async`) exactly -- same `mkdir` lock at
`${MEMSEARCH_STATE_DIR:-~/.memsearch/state}/reindex.lock`, same
notes+profile+sessions scope -- so the two never index concurrently. It never
blocks the sync (the git work is already complete), skips when a refresh is
already running, and is a no-op when `memsearch` is not installed. See
[../memsearch/README.md](../memsearch/README.md) for the full shared reindex
contract (`MEMSEARCH_REINDEX_TIMEOUT`, default 120s).
