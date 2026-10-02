# rem

The sole supported capture/consolidation CLI for the knowledge vault. A fresh
`tackroom setup` copies this Go package source, materializes the embedded
`go.mod.template` as `go.mod`, and `tackroom sync` builds and installs it to
`$GOBIN` or `~/.local/bin`. In this repository the package remains part of the
root module, so `go test ./...` covers it without a nested module boundary.

```text
rem add [-src harness] "<fact>"   capture an inert candidate in ai/YYYY-MM-DD.md
rem search "<query>"              semantic search via memsearch (collection ai)
rem dream [--apply]               report candidates; optionally collapse exact
                                  duplicate sync sections with backup + commit
rem sync                          run the guarded knowledge-sync binary
```

`rem dream` is the only consolidation command. Its normal mode writes a review
report and does not promote facts. `--apply` performs only its documented,
unattended-safe exact-duplicate cleanup; it does not infer or promote facts.
Session digests written by memory hooks remain context records, not durable
profile claims.

Environment:

- `KNOWLEDGE_DIR` — vault root (default `~/Workspace/knowledge`)
- `REM_SYNC_BIN` — alternate `knowledge-sync` binary
- `REM_COLLECTION` — collection for `rem search` (default `ai`)

Tests are included in the root module and run with `go test ./...`.
