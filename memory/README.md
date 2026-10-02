# memory

Agent-agnostic session memory and private Hermes-vault synchronization for
tackroom. Session capture writes bounded, redacted digests; it never promotes
facts into durable profile or agent instructions automatically.

## Setup and shipped tools

```bash
tackroom setup --memory basic      # default; Python 3 only
tackroom setup --memory off        # no managed memory hooks
tackroom setup --memory memsearch  # derived search index; requires memsearch
```

A fresh setup scaffolds `memory/hooks`, `memory/lib`, and the Go sources for
`memory/tools/rem` and `memory/tools/knowledge-sync`. `tackroom sync` builds
those source packages and installs `rem` and `knowledge-sync` into `$GOBIN` or
`~/.local/bin`; no prebuilt binaries are shipped. The repository tests both
packages in its root module, while setup materializes each embedded
`go.mod.template` as `go.mod` so a user-owned config root builds independently.

| Tier | Behavior | Dependency |
|---|---|---|
| `off` | no managed memory hooks | none |
| `basic` | session start context plus bounded session-end digests | Python 3 |
| `memsearch` | the same canonical vault plus a disposable search index | Python 3, `memsearch` |

## One capture and consolidation pipeline

All locally captured Amp, Droid/Factory, Hermes, Codex, OMP-compatible, and
Claude fallback payloads are normalized by `lib/basic_memory.py` and written as
the same digest format under `$KNOWLEDGE_DIR/sessions`. Provider-specific code
only classifies payloads or loads a provider-owned session file. The shared
shell helper performs the one bounded, non-overlapping refresh after a new
digest or a successful Hermes bridge sync.

`rem dream` is the only supported consolidation workflow:

```bash
rem add -src claude "prefers pnpm for Node work"
rem dream                  # report-only candidate consolidation
rem dream --apply          # guarded exact-duplicate cleanup
rem search "preferences"  # memsearch collection ai
rem sync                   # guarded knowledge-sync
```

Captured candidates and digests remain inert until a person reviews and
promotes them.

## Managed hook boundary

The CLI only manages hooks exposed by the harness registry:

- Claude Code, Codex, and Droid/Factory: verified start/stop/end events as
  available for the selected tier.
- Hermes: verified native session events, including the existing Hermes vault
  bridge wrappers.
- Amp, OMP, and Pi: no managed hook installation. The dispatcher retains cheap
  compatibility for legacy Amp/OMP payloads, but users should not treat that as
  a managed native integration.

`hooks/sync.sh` remains solely for legacy path migration.

## Layout

- `hooks/` — managed lifecycle wrappers and the shared refresh helper
- `lib/basic_memory.py` — payload normalization, digest capture, and start context
- `lib/sync.py` — Hermes memory ↔ vault bridge (no index implementation)
- `lib/safety.py` — locking, redaction, and safe filesystem helpers
- `tools/rem/`, `tools/knowledge-sync/` — shipped Go package sources
- `tools/memsearch/` — optional memsearch parity guidance
- `tests/` — dependency-free Python behavior tests

`memory/hooks/` and `memory/lib/` are managed in a deployed config root:
`tackroom sync` refreshes them while they are unmodified, removes files a
release stopped shipping, and reports (without touching) anything you edited.
Ownership is recorded in `.tackroom-starter.json` at the config root.
