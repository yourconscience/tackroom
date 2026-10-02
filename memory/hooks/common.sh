#!/usr/bin/env bash

MEMORY_HOOK_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
MEMORY_DIR="$(dirname "$MEMORY_HOOK_DIR")"

load_memory_config() {
  CONF="${KNOWLEDGE_CONF:-$(dirname "$MEMORY_DIR")/memsearch.conf}"
  KNOWLEDGE_DIR_ENV="${KNOWLEDGE_DIR:-}"
  SESSIONS_DIR_ENV="${SESSIONS_DIR:-}"
  NOTES_DIR_ENV="${NOTES_DIR:-}"
  PROFILE_DIR_ENV="${PROFILE_DIR:-}"
  MEMSEARCH_STATE_DIR_ENV="${MEMSEARCH_STATE_DIR:-}"
  MEMSEARCH_COLLECTION_ENV="${MEMSEARCH_COLLECTION:-}"
  if [ -f "$CONF" ]; then
    # shellcheck source=/dev/null
    . "$CONF"
  fi

  export KNOWLEDGE_DIR="${KNOWLEDGE_DIR_ENV:-${KNOWLEDGE_DIR:-$HOME/Workspace/knowledge}}"
  export SESSIONS_DIR="${SESSIONS_DIR_ENV:-${SESSIONS_DIR:-$KNOWLEDGE_DIR/sessions}}"
  export NOTES_DIR="${NOTES_DIR_ENV:-${NOTES_DIR:-$KNOWLEDGE_DIR/notes}}"
  export PROFILE_DIR="${PROFILE_DIR_ENV:-${PROFILE_DIR:-$KNOWLEDGE_DIR/profile}}"
  export MEMSEARCH_STATE_DIR="${MEMSEARCH_STATE_DIR_ENV:-${MEMSEARCH_STATE_DIR:-$HOME/.memsearch/state}}"
  export MEMSEARCH_COLLECTION="${MEMSEARCH_COLLECTION_ENV:-${MEMSEARCH_COLLECTION:-ai}}"

}

resolve_claude_memory_plugin() {
  plugin_dir="${MEMSEARCH_PLUGIN_DIR:-}"
  if [ -z "$plugin_dir" ]; then
    memsearch_bin="$(command -v memsearch 2>/dev/null || true)"
    if [ -n "$memsearch_bin" ]; then
      plugin_dir="$(dirname "$memsearch_bin")/plugins/claude-code"
    fi
  fi
  if [ -z "$plugin_dir" ] || [ ! -d "$plugin_dir" ]; then
    plugin_dir="$(python3 -c 'import memsearch; import os; print(os.path.join(os.path.dirname(memsearch.__file__), "plugins", "claude-code"))' 2>/dev/null || true)"
  fi
  [ -d "$plugin_dir" ] && printf '%s\n' "$plugin_dir"
}

prepare_memory_index_env() {
  export MEMSEARCH_MEMORY_DIR="${SESSIONS_DIR}"
  export MEMSEARCH_COLLECTION_NAME="${MEMSEARCH_COLLECTION}"
  mkdir -p "$MEMSEARCH_MEMORY_DIR" "$MEMSEARCH_STATE_DIR"
}

index_memory_top_level() {
  set -- "$NOTES_DIR" "$PROFILE_DIR"
  for path in "$SESSIONS_DIR"/*.md "$SESSIONS_DIR"/*.markdown; do
    [ -f "$path" ] && set -- "$@" "$path"
  done
  memsearch index "$@" --collection "$MEMSEARCH_COLLECTION" >/dev/null 2>&1
}

# Best-effort vault reindex fired after a session digest is written. It never
# blocks the hook (all work is backgrounded), is bounded by a watchdog so a
# hung memsearch can't run forever, and refuses to overlap a refresh that is
# already running via an atomic mkdir lock. No-op when memsearch is absent.
refresh_index_async() {
  command -v memsearch >/dev/null 2>&1 || return 0
  prepare_memory_index_env

  reindex_lock="${MEMSEARCH_STATE_DIR%/}/reindex.lock"
  # mkdir is atomic: it fails when a refresh already holds the lock, so we never
  # spawn overlapping reindexers.
  if ! mkdir "$reindex_lock" 2>/dev/null; then
    # The lock exists. Reclaim it only if its owner is gone (e.g. the refresher
    # was SIGKILLed mid-run), so a dead process can't suppress reindex forever.
    owner="$(cat "$reindex_lock/pid" 2>/dev/null || true)"
    if [ -n "$owner" ] && kill -0 "$owner" 2>/dev/null; then
      return 0
    fi
    rm -f "$reindex_lock/pid" 2>/dev/null || true
    rmdir "$reindex_lock" 2>/dev/null || true
    mkdir "$reindex_lock" 2>/dev/null || return 0
  fi

  (
    trap 'rm -f "$reindex_lock/pid" 2>/dev/null || true; rmdir "$reindex_lock" 2>/dev/null || true' EXIT
    set -- "$NOTES_DIR" "$PROFILE_DIR"
    for path in "$SESSIONS_DIR"/*.md "$SESSIONS_DIR"/*.markdown; do
      [ -f "$path" ] && set -- "$@" "$path"
    done
    memsearch index "$@" --collection "$MEMSEARCH_COLLECTION" >/dev/null 2>&1 &
    index_pid=$!
    ( sleep "${MEMSEARCH_REINDEX_TIMEOUT:-120}"; kill "$index_pid" 2>/dev/null || true ) &
    watchdog_pid=$!
    wait "$index_pid" 2>/dev/null || true
    kill "$watchdog_pid" 2>/dev/null || true
    wait "$watchdog_pid" 2>/dev/null || true
  ) >/dev/null 2>&1 &
  # Record the worker's PID (portable across bash 3.2, unlike $BASHPID) so a
  # later call can tell a live refresh from a lock stranded by a killed one.
  printf '%s\n' "$!" >"$reindex_lock/pid" 2>/dev/null || true

  return 0
}

# Classify a hook payload file into a dispatch kind. Codex and OMP capture route
# through the local basic_memory digest (never the Claude plugin), detected from
# an explicit source hint or the payload's own agent/platform marker.
classify_payload() {
  MEMORY_SOURCE_HINT="${TACKROOM_MEMORY_SOURCE:-}" python3 - "$1" <<'PY'
import os
import json
import sys
from pathlib import Path

try:
    data = json.load(open(sys.argv[1]))
except Exception:
    print("unknown")
    raise SystemExit

hint = (os.environ.get("MEMORY_SOURCE_HINT") or "").strip().lower()
agent = str(data.get("agent") or data.get("platform") or "").strip().lower()
transcript = os.path.expanduser(str(data.get("transcript_path") or ""))

if hint in {"codex", "omp"}:
    print(hint)
elif agent in {"codex", "omp"}:
    print(agent)
elif transcript and "/.factory/" in transcript and Path(transcript).suffix == ".jsonl":
    print("factory-jsonl")
elif data.get("platform") == "amp" or data.get("amp_thread_id"):
    print("amp-json")
elif data.get("session_id") and not transcript:
    print("hermes-json")
else:
    print("claude-plugin")
PY
}

# Write a digest through the shared basic_memory implementation, then fire a
# bounded, non-overlapping reindex only when a new digest was actually appended.
dispatch_basic_digest() {
  digest_source="${2:-}"
  if digest_output="$(TACKROOM_MEMORY_SOURCE="$digest_source" python3 "$MEMORY_DIR/hooks/basic-session-end.py" <"$1")"; then
    printf '%s\n' "$digest_output"
    case "$digest_output" in
      *'"systemMessage":"basic memory appended'*|*'"message": "memsearch updated from'*|*'"systemMessage":"memsearch updated from'*) refresh_index_async ;;
    esac
  else
    printf '{"continue":true,"suppressOutput":true}\n'
  fi
}
