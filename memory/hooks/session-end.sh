#!/usr/bin/env bash
set -eu

. "$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/common.sh"

if [ "${MEMSEARCH_SKIP_CLAUDE_HOOKS:-}" = "1" ]; then
  echo '{}'
  exit 0
fi

load_memory_config
prepare_memory_index_env

payload="$(mktemp)"
trap 'rm -f "$payload"' EXIT
cat >"$payload"

kind="$(classify_payload "$payload")"

case "$kind" in
  amp-json)
    dispatch_basic_digest "$payload" amp
    ;;
  factory-jsonl)
    dispatch_basic_digest "$payload" droid
    ;;
  codex|omp)
    dispatch_basic_digest "$payload" "$kind"
    ;;
  *)
    plugin_dir="$(resolve_claude_memory_plugin || true)"
    if [ -n "$plugin_dir" ]; then
      bash "$plugin_dir/hooks/session-end.sh" <"$payload" >/dev/null 2>&1 || true
      index_memory_top_level || true
      printf '{"continue":true,"suppressOutput":true}\n'
    else
      # No memsearch claude-code plugin (e.g. memsearch 0.2.x): fall back to the
      # local basic_memory digest so Claude capture keeps working.
      dispatch_basic_digest "$payload"
    fi
    ;;
esac
