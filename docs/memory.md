# Memory

tackroom ships a review-first memory system: capture is automatic and cheap, consolidation produces reports, and only you promote facts into durable instructions. Memory data lives in your knowledge directory, never in the tool repository.

```bash
tackroom setup --memory basic        # default
tackroom setup --memory off
tackroom setup --memory memsearch    # needs memsearch on PATH (uv tool install memsearch)
```

## Tiers

| Tier | What it does | Needs |
|---|---|---|
| `off` | Registers no memory hooks and scaffolds no Go tool sources. | nothing |
| `basic` | Appends a bounded digest of each session to `$KNOWLEDGE_DIR/sessions/YYYY-MM-DD.md` at session end and injects recent digests as context at session start. | Python 3 |
| `memsearch` | Everything in `basic`, plus a disposable semantic search index over the vault, queried with `rem search`. | Python 3, `memsearch` |

The knowledge directory is `$KNOWLEDGE_DIR`, default `~/Workspace/knowledge`. The index is derived: markdown is the only source of truth, and any machine can rebuild its index.

- The hooks are `memory-session-start` and `memory-session-end` (`memsearch` adds `memory-stop`). They go to every enabled harness with a hook surface except Antigravity CLI, whose hook reply format the memory scripts cannot satisfy; setup prints a warning for it.
- The `memsearch` tier also writes a `memsearch.conf` in the config root (git-ignored) and creates the vault's `sessions/`, `notes/` and `profile/` folders.

## Tools

`tackroom sync` builds every Go tool under the config root's `memory/tools/` into `$GOBIN` or `~/.local/bin`. It skips this without a Go toolchain and rebuilds only when sources change. Two ship with tackroom:

- `rem` is the capture and consolidation CLI.
- `knowledge-sync` commits dirty vault changes, fetches, merges and pushes. It takes a lock, refuses to run when the checkout is not on the sync branch, and on a merge conflict aborts the merge and leaves a `sync-conflict-<time>` branch. It reads `KNOWLEDGE_REPO` (default `~/Workspace/knowledge`), `KNOWLEDGE_REMOTE` (default `origin`) and `KNOWLEDGE_BRANCH` (default `main`).

## The rem workflow

```bash
rem add -src claude "prefers pnpm for Node work"   # capture a candidate fact
rem search "quota preferences"                     # semantic search via memsearch, collection ai
rem dream                                          # consolidation report
rem dream --apply                                  # collapse exact-duplicate records
rem sync                                           # flush the vault through knowledge-sync
```

- `add` appends `- candidate: <fact>` to `$KNOWLEDGE_DIR/ai/YYYY-MM-DD.md` and skips a fact already captured on any day. Candidates are inert: nothing reads them as truth until you promote them.
- `dream` clusters candidates lexically, counts the distinct days each cluster appears on, flags clusters with both positive and negated phrasing as conflicts, and writes a proposal report to `$KNOWLEDGE_DIR/reviews/rem-dream-YYYY-MM-DD.md`. It never edits durable targets.
- `dream --apply` does the one write that is safe unattended: it collapses identical `## Sync` sections in `sessions/knowledge.md`, keeping the first, with a timestamped backup and a git commit. It refuses on a dirty tree or when the vault is not on `main`.

## Why review-first

Automatically rewriting memory is how agents corrupt their own instructions: low-authority observations get promoted into preferences or standing rules, contradictions silently resolve to whichever fact was written last, and near-duplicate merges drop the constraints that made a fact true. So capture and dedup are machine jobs, and promoting a meaning needs a human decision on a concrete patch.
