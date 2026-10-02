# Memory

tackroom ships a review-first memory system: capture is automatic and cheap, consolidation produces reports, and only you promote facts into durable instructions.

## Tiers

Pick during `setup` (`--memory off|basic|memsearch`):

| Tier | Behavior | Dependency |
|---|---|---|
| `off` | no managed memory hooks | none |
| `basic` | appends a bounded digest of each session to `$KNOWLEDGE_DIR/sessions/YYYY-MM-DD.md` and injects recent digests as context at session start | Python 3 |
| `memsearch` | full-text indexed search over the knowledge vault | `memsearch` on PATH (`uv tool install memsearch`) |

On Hermes, the `memsearch` tier bridges all three stores rather than replacing
Hermes memory: the vault is imported into `~/.hermes/memories/` at session
start, the finalized session is captured into the vault, and Hermes durable
memory is exported back to the vault at finalize. Hermes continues to use its
built-in memory injector and memory tool; memsearch provides the larger indexed
history. Run `tackroom doctor --e2e` and `hermes hooks doctor` after setup.

Memory data lives in your knowledge directory (default `~/Workspace/knowledge`, configurable via `KNOWLEDGE_DIR`), never in the tool repository.

## Tools

`sync` builds every Go tool under your repo's `memory/tools/` into `$GOBIN` or `~/.local/bin` (skipped without a Go toolchain; rebuilds only on source changes). Two ship as reference implementations in this repository:

- **`rem`** — the capture/consolidation CLI (see below).
- **`knowledge-sync`** — commit + fetch + merge + push for the vault. Refuses to run when the worktree is checked out on a branch other than the sync target, uses an flock, and aborts cleanly on merge conflicts by leaving a `sync-conflict-*` branch.

## The rem workflow

```bash
rem add -src claude "prefers pnpm for Node work"   # capture a candidate fact anywhere
rem search "quota preferences"                     # semantic search via memsearch
rem dream                                          # consolidation report
rem dream --apply                                  # collapse exact-duplicate records
rem sync                                           # flush the vault through knowledge-sync
```

- `add` appends `- candidate: <fact>` to `$KNOWLEDGE_DIR/ai/YYYY-MM-DD.md`, deduplicating against existing candidates across days. Candidates are inert: nothing reads them as truth until promoted.
- `dream` clusters candidates lexically, counts distinct source days, flags mixed-polarity clusters as conflicts, and writes a proposal report to `$KNOWLEDGE_DIR/reviews/rem-dream-YYYY-MM-DD.md`. It never edits durable targets.
- `--apply` performs the only unattended-safe write: collapsing normalized-exact duplicate `## Sync` sections in `sessions/knowledge.md`, keep-first, with a timestamped backup and a git commit. It refuses on a dirty tree or a non-main checkout.

Run `rem dream` on a schedule (cron/LaunchAgent, ~2x per week) and review the reports when they accumulate candidates.

## Why review-first

Automatically rewriting memory is how agents corrupt their own instructions: low-authority observations get promoted into preferences or standing rules, contradictions silently resolve to whichever fact was written last, and near-duplicate merges drop the constraints that made a fact true. So tackroom draws a hard line: capture and dedup are machine jobs; promotion of meaning requires an explicit human decision on a concrete patch.
