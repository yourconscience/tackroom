#!/usr/bin/env bash
set -eu

. "$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/common.sh"

if [ "${MEMSEARCH_SKIP_CLAUDE_HOOKS:-}" = "1" ]; then
  echo '{}'
  exit 0
fi

load_memory_config
if [ "${DOTAGENTS_MEMORY_SOURCE:-}" = "codex" ] || [ "${DOTAGENTS_MEMORY_SOURCE:-}" = "omp" ]; then
  exec python3 "$MEMORY_DIR/hooks/basic-session-start.py"
fi
plugin_dir="$(resolve_claude_memory_plugin || true)"
if [ -z "$plugin_dir" ]; then
  # No memsearch claude-code plugin: fall back to the local basic_memory
  # implementation so recent session digests are injected as additionalContext.
  exec python3 "$MEMORY_DIR/hooks/basic-session-start.py"
fi

prepare_memory_index_env
exec bash "$plugin_dir/hooks/session-start.sh"
