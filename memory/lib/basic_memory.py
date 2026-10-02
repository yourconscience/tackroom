#!/usr/bin/env python3
"""Basic markdown-backed memory hooks for tackroom.

This module intentionally has no memsearch or third-party dependency. It reads
hook payloads, stores compact digests under $KNOWLEDGE_DIR/sessions, and emits
Claude-compatible hook JSON on stdout.
"""

from __future__ import annotations

import json
import os
import re
import sys
import tempfile
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, TextIO

from safety import knowledge_dir_from_environment, locked_directory, redact_text, safe_identifier, truncate_redacted

MAX_DIGEST_CHARS = 3200
MAX_CONTEXT_CHARS = 6000
MAX_CONTEXT_FILES = 7
MAX_CONTEXT_FILE_CHARS = 2400

PATH_RE = re.compile(r"(?:~?/[^\s`'\"\\]+|/Users/[^\s`'\"\\]+|\./[^\s`'\"\\]+)")


class HookError(Exception):
    """Actionable hook failure."""


def _error(message: str) -> int:
    print(f"basic memory hook error: {message}", file=sys.stderr)
    return 1


def parse_payload(stdin: TextIO) -> dict[str, Any]:
    try:
        payload = json.load(stdin)
    except json.JSONDecodeError as exc:
        raise HookError(f"invalid hook JSON on stdin: {exc.msg} at line {exc.lineno} column {exc.colno}") from exc
    if not isinstance(payload, dict):
        raise HookError("hook payload must be a JSON object")
    return payload


def knowledge_dir_from_env() -> Path:
    return knowledge_dir_from_environment()


def ensure_sessions_dir(knowledge_dir: Path) -> Path:
    sessions_dir = knowledge_dir / "sessions"
    try:
        sessions_dir.mkdir(parents=True, exist_ok=True)
    except OSError as exc:
        raise HookError(f"cannot create sessions directory {sessions_dir}: {exc}") from exc
    if not sessions_dir.is_dir():
        raise HookError(f"sessions path is not a directory: {sessions_dir}")
    test_path: Path | None = None
    try:
        with tempfile.NamedTemporaryFile(
            mode="w",
            encoding="utf-8",
            dir=sessions_dir,
            prefix=".tackroom-basic-memory-write-test-",
            delete=False,
        ) as handle:
            test_path = Path(handle.name)
        test_path.unlink()
    except OSError as exc:
        if test_path is not None:
            try:
                test_path.unlink(missing_ok=True)
            except OSError:
                pass
        raise HookError(f"sessions directory is not writable: {sessions_dir}: {exc}") from exc
    return sessions_dir


def require_readable_sessions_dir(knowledge_dir: Path) -> Path | None:
    sessions_dir = knowledge_dir / "sessions"
    if not sessions_dir.exists():
        return None
    if not sessions_dir.is_dir():
        raise HookError(f"sessions path is not a directory: {sessions_dir}")
    try:
        next(sessions_dir.iterdir(), None)
    except OSError as exc:
        raise HookError(f"sessions directory is not readable: {sessions_dir}: {exc}") from exc
    return sessions_dir


def content_text(content: Any) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts: list[str] = []
        for item in content:
            if isinstance(item, str):
                parts.append(item)
            elif isinstance(item, dict):
                text = item.get("text") or item.get("content")
                if isinstance(text, str):
                    parts.append(text)
        return "\n".join(parts)
    if isinstance(content, dict):
        text = content.get("text") or content.get("content")
        if isinstance(text, str):
            return text
    return ""


def redacted(text: str) -> str:
    return redact_text(text)


def truncate(text: str, limit: int) -> str:
    return truncate_redacted(text, limit)


def parse_timestamp(value: Any) -> datetime | None:
    if isinstance(value, (int, float)):
        try:
            return datetime.fromtimestamp(value, tz=timezone.utc)
        except (OSError, OverflowError, ValueError):
            return None
    if not isinstance(value, str) or not value.strip():
        return None
    normalized = value.strip().replace("Z", "+00:00")
    try:
        parsed = datetime.fromisoformat(normalized)
    except ValueError:
        return None
    if parsed.tzinfo is None:
        return parsed.replace(tzinfo=timezone.utc)
    return parsed.astimezone(timezone.utc)


def first_timestamp(*items: Any) -> datetime:
    for item in items:
        parsed = parse_timestamp(item)
        if parsed is not None:
            return parsed
    return datetime.now(timezone.utc)


def message_from_record(record: dict[str, Any]) -> dict[str, Any] | None:
    role = record.get("role")
    content = record.get("content")
    timestamp = record.get("timestamp") or record.get("created_at")

    nested = record.get("message")
    if isinstance(nested, dict):
        if nested.get("visibility") == "llm_only":
            return None
        role = nested.get("role") or role
        content = nested.get("content") if "content" in nested else content
        timestamp = nested.get("timestamp") or timestamp

    if role is None and record.get("type") in {"user", "assistant", "system"}:
        role = record.get("type")
    if role is None or content is None:
        return None
    text = content_text(content).strip()
    if not text:
        return None
    return {"role": str(role), "content": text, "timestamp": timestamp}


def inline_messages(payload: dict[str, Any]) -> list[dict[str, Any]]:
    messages = payload.get("messages")
    if not isinstance(messages, list):
        return []
    parsed: list[dict[str, Any]] = []
    for item in messages:
        if isinstance(item, dict):
            message = message_from_record(item)
            if message is not None:
                parsed.append(message)
    return parsed


def read_transcript(path: Path, tolerant: bool = False) -> tuple[list[dict[str, Any]], datetime | None, str | None]:
    """Read a JSONL transcript.

    tolerant skips undecodable lines instead of failing. Factory/Droid writes its
    transcript while the hook may read it, so a partially written trailing record
    must not discard the whole session.
    """
    if not path.exists():
        return [], None, None
    if not path.is_file():
        raise HookError(f"transcript_path is not a file: {path}")

    messages: list[dict[str, Any]] = []
    started: datetime | None = None
    transcript_session_id: str | None = None
    try:
        with path.open(encoding="utf-8") as handle:
            for line_number, line in enumerate(handle, start=1):
                raw = line.strip()
                if not raw:
                    continue
                try:
                    record = json.loads(raw)
                except json.JSONDecodeError as exc:
                    if tolerant:
                        continue
                    raise HookError(f"invalid JSONL transcript at {path}:{line_number}: {exc.msg}") from exc
                if not isinstance(record, dict):
                    continue
                if started is None:
                    started = parse_timestamp(record.get("timestamp") or record.get("created_at"))
                if transcript_session_id is None:
                    candidate = record.get("session_id") or record.get("sessionId") or record.get("id")
                    if candidate:
                        transcript_session_id = str(candidate)
                message = message_from_record(record)
                if message is not None and message.get("role") != "system":
                    messages.append(message)
    except OSError as exc:
        raise HookError(f"cannot read transcript_path {path}: {exc}") from exc
    return messages, started, transcript_session_id


def provider_source(payload: dict[str, Any]) -> str:
    hint = os.environ.get("TACKROOM_MEMORY_SOURCE", "").strip().lower()
    if hint:
        return hint
    marker = str(payload.get("platform") or payload.get("agent") or "").strip().lower()
    if marker in {"amp", "codex", "omp", "droid", "factory", "hermes"}:
        return "droid" if marker == "factory" else marker
    if payload.get("amp_thread_id"):
        return "amp"
    transcript = str(payload.get("transcript_path") or "")
    if "/.factory/" in os.path.expanduser(transcript) and Path(transcript).suffix == ".jsonl":
        return "droid"
    session_id = payload.get("session_id") or payload.get("sessionId")
    if session_id and not transcript and not payload.get("messages"):
        hermes_home = Path(os.path.expanduser(os.environ.get("HERMES_HOME", "~/.hermes")))
        if (hermes_home / "sessions" / f"session_{session_id}.json").is_file():
            return "hermes"
    return "basic"


def normalize_provider_payload(payload: dict[str, Any]) -> tuple[dict[str, Any], str, bool]:
    """Load provider-owned session data while keeping digest rendering shared.

    The third value reports whether capture may proceed. Hermes content lives in
    a provider-owned session file that can lag the hook, and recording a stub
    digest for that session id would make the real transcript look like a replay
    later, so capture is skipped until the file is readable.
    """
    source = provider_source(payload)
    if source != "hermes":
        return payload, source, True

    session_id = payload.get("session_id") or payload.get("sessionId")
    if not session_id:
        return payload, source, True
    raw_id = str(session_id)
    if raw_id != Path(raw_id).name:
        raise HookError("Hermes session_id must not contain path separators")
    hermes_home = Path(os.path.expanduser(os.environ.get("HERMES_HOME", "~/.hermes")))
    session_path = hermes_home / "sessions" / f"session_{raw_id}.json"
    if not session_path.is_file():
        return payload, source, False
    try:
        session_data = json.loads(session_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise HookError(f"cannot read Hermes session file {session_path}: {exc}") from exc
    if not isinstance(session_data, dict):
        raise HookError(f"Hermes session file must contain a JSON object: {session_path}")
    normalized = dict(payload)
    normalized.update(session_data)
    normalized.setdefault("session_id", raw_id)
    normalized["source_transcript"] = str(session_path)
    return normalized, source, True


def collect_messages(payload: dict[str, Any]) -> tuple[list[dict[str, Any]], datetime | None, str | None]:
    messages = inline_messages(payload)
    transcript_started = None
    transcript_session_id = None
    transcript = payload.get("transcript_path")
    if isinstance(transcript, str) and transcript.strip():
        transcript_path = Path(os.path.expanduser(transcript)).resolve()
        tolerant = payload.get("tackroom_memory_source") == "droid"
        transcript_messages, transcript_started, transcript_session_id = read_transcript(transcript_path, tolerant=tolerant)
        if transcript_messages:
            messages = transcript_messages
    return messages, transcript_started, transcript_session_id


def stable_identifier(payload: dict[str, Any], transcript_session_id: str | None) -> str | None:
    for key in ("event_id", "hook_event_id", "session_id", "sessionId", "amp_thread_id", "conversation_id", "thread_id"):
        value = payload.get(key)
        if value:
            return sanitize_identifier(str(value))
    if transcript_session_id:
        return sanitize_identifier(transcript_session_id)
    transcript = payload.get("transcript_path")
    if isinstance(transcript, str) and transcript.strip():
        return sanitize_identifier(Path(transcript).stem)
    return None


def sanitize_identifier(value: str) -> str:
    return safe_identifier(value)


def collect_user_turns(messages: list[dict[str, Any]]) -> list[str]:
    return [msg["content"] for msg in messages if msg.get("role") == "user" and isinstance(msg.get("content"), str)]


def last_assistant_text(messages: list[dict[str, Any]]) -> str:
    for msg in reversed(messages):
        if msg.get("role") == "assistant" and isinstance(msg.get("content"), str) and msg["content"].strip():
            return msg["content"]
    return ""


def extract_paths(messages: list[dict[str, Any]], payload: dict[str, Any]) -> list[str]:
    found: list[str] = []
    seen: set[str] = set()
    for candidate in (payload.get("cwd"), payload.get("workspace")):
        if isinstance(candidate, str) and candidate.strip():
            cleaned_candidate = redact_text(candidate)
            if cleaned_candidate not in seen:
                seen.add(cleaned_candidate)
                found.append(cleaned_candidate)
    for msg in messages:
        content = msg.get("content")
        if not isinstance(content, str):
            continue
        for match in PATH_RE.findall(content):
            cleaned = redact_text(match.rstrip(".,;:)"))
            if cleaned.startswith(("/Users/", "~/", "./")) and cleaned not in seen:
                seen.add(cleaned)
                found.append(cleaned)
        if len(found) >= 8:
            break
    return found[:8]


def digest_started(payload: dict[str, Any], messages: list[dict[str, Any]], transcript_started: datetime | None) -> datetime:
    message_start = None
    if messages:
        message_start = parse_timestamp(messages[0].get("timestamp"))
    return first_timestamp(
        payload.get("session_start"),
        payload.get("started_at"),
        payload.get("timestamp"),
        payload.get("created_at"),
        transcript_started.isoformat() if transcript_started else None,
        message_start.isoformat() if message_start else None,
    )


def build_digest(payload: dict[str, Any], messages: list[dict[str, Any]], started: datetime, session_id: str | None) -> str:
    display_id = session_id or "unidentified"
    started_human = started.strftime("%Y-%m-%d %H:%M UTC")
    users = collect_user_turns(messages)
    assistant = last_assistant_text(messages)
    paths = extract_paths(messages, payload)
    # An explicit source hint wins over payload markers so a conflicting
    # platform/agent field in the payload cannot mislabel the digest.
    platform = (
        payload.get("tackroom_memory_source")
        or os.environ.get("TACKROOM_MEMORY_SOURCE")
        or payload.get("platform")
        or payload.get("agent")
        or payload.get("hook_event_name")
        or "basic"
    )
    model = payload.get("model")

    lines = []
    if session_id:
        lines.append(f"<!-- basic-memory-session:{session_id}:start -->")
    lines.extend([f"## Session {started_human} - {display_id}", ""])

    meta = [f"source: {truncate(str(platform), 80)}"]
    if model:
        meta.append(f"model: {truncate(str(model), 120)}")
    lines.append(f"- {'; '.join(meta)}")

    if users:
        lines.append(f"- first request: {truncate(users[0], 260)}")
    if len(users) > 1:
        lines.append("- user follow-ups:")
        for text in users[1:6]:
            lines.append(f"  - {truncate(text, 220)}")
    if assistant:
        lines.append(f"- final assistant output: {truncate(assistant, 420)}")
    if paths:
        lines.append("- paths mentioned:")
        for path in paths:
            lines.append(f"  - `{truncate(path, 180)}`")
    source_transcript = payload.get("source_transcript") or payload.get("transcript_path")
    if isinstance(source_transcript, str) and source_transcript.strip():
        lines.append(f"- source transcript: `{truncate(source_transcript, 220)}`")
    if not users and not assistant:
        lines.append("- summary: Session ended without supported transcript messages in the hook payload.")

    lines.append("")
    if session_id:
        lines.append(f"<!-- basic-memory-session:{session_id}:end -->")
        lines.append("")

    digest = "\n".join(lines)
    if len(digest) <= MAX_DIGEST_CHARS:
        return digest
    if session_id:
        footer = f"\n<!-- basic-memory-session:{session_id}:end -->\n"
    else:
        footer = "\n"
    return digest[: MAX_DIGEST_CHARS - len(footer) - 2].rstrip() + "…" + footer


def marker_exists(sessions_dir: Path, session_id: str) -> bool:
    markers = [
        f"<!-- basic-memory-session:{session_id}:start -->",
        f"<!-- amp-session:{session_id}:start -->",
        f"<!-- droid-session:{session_id}:start -->",
        f"<!-- hermes-session:{session_id}:start -->",
    ]
    try:
        paths = sorted({*sessions_dir.glob("*.md"), *sessions_dir.glob("*.markdown")})
    except OSError as exc:
        raise HookError(f"cannot list sessions directory {sessions_dir}: {exc}") from exc
    for path in paths:
        try:
            text = path.read_text(encoding="utf-8")
            if any(marker in text for marker in markers):
                return True
        except OSError as exc:
            raise HookError(f"cannot read existing session file {path}: {exc}") from exc
    return False


def append_digest(sessions_dir: Path, started: datetime, digest: str, session_id: str | None) -> tuple[Path, bool]:
    target = sessions_dir / f"{started.strftime('%Y-%m-%d')}.md"
    try:
        with locked_directory(sessions_dir):
            if session_id and marker_exists(sessions_dir, session_id):
                return target, False
            needs_separator = target.exists() and target.stat().st_size > 0
            with target.open("a", encoding="utf-8") as handle:
                if needs_separator:
                    handle.write("\n")
                handle.write(digest.rstrip() + "\n")
    except OSError as exc:
        raise HookError(f"cannot append session digest to {target}: {exc}") from exc
    return target, True


def continuation_json(**extra: Any) -> str:
    payload: dict[str, Any] = {"continue": True, "suppressOutput": True}
    payload.update(extra)
    return json.dumps(payload, sort_keys=True, separators=(",", ":"))


def context_json(context: str) -> str:
    return json.dumps(
        {
            "continue": True,
            "suppressOutput": True,
            "hookSpecificOutput": {
                "hookEventName": "SessionStart",
                "additionalContext": context,
            },
        },
        sort_keys=True,
        separators=(",", ":"),
    )


def session_end(stdin: TextIO = sys.stdin, stdout: TextIO = sys.stdout) -> int:
    try:
        payload, source, provider_ready = normalize_provider_payload(parse_payload(stdin))
        if not provider_ready:
            session_id = payload.get("session_id") or payload.get("sessionId")
            print(
                json.dumps({"action": "continue", "message": f"Hermes session log not found for {session_id}; capture skipped"}),
                file=stdout,
            )
            return 0
        if source != "basic":
            payload["tackroom_memory_source"] = source
        knowledge_dir = knowledge_dir_from_env()
        sessions_dir = ensure_sessions_dir(knowledge_dir)
        messages, transcript_started, transcript_session_id = collect_messages(payload)
        session_id = stable_identifier(payload, transcript_session_id)
        started = digest_started(payload, messages, transcript_started)
        digest = build_digest(payload, messages, started, session_id)
        target, appended = append_digest(sessions_dir, started, digest, session_id)
    except HookError as exc:
        return _error(str(exc))

    if appended:
        basic_message = f"basic memory appended session digest to {target}"
    else:
        basic_message = f"basic memory skipped replayed session {session_id}"

    display_source = {"amp": "Amp", "droid": "Droid", "hermes": "Hermes"}.get(source)
    if display_source:
        if appended:
            message = f"memsearch updated from {display_source} session {session_id}"
        else:
            message = f"memory skipped replayed {display_source} session {session_id}"
        if source in {"amp", "hermes"}:
            print(json.dumps({"action": "continue", "message": message, "output_file": str(target)}), file=stdout)
        else:
            print(continuation_json(output_file=str(target), systemMessage=message), file=stdout)
    else:
        print(continuation_json(output_file=str(target), systemMessage=basic_message), file=stdout)
    return 0


def session_markdown_files(sessions_dir: Path) -> list[Path]:
    try:
        paths = [path for path in sessions_dir.iterdir() if path.is_file() and path.suffix.lower() in {".md", ".markdown"}]
    except OSError as exc:
        raise HookError(f"cannot list sessions directory {sessions_dir}: {exc}") from exc
    return sorted(paths, key=lambda path: path.name, reverse=True)


def bounded_file_excerpt(path: Path) -> str:
    try:
        text = path.read_text(encoding="utf-8")
    except OSError as exc:
        raise HookError(f"cannot read session file {path}: {exc}") from exc
    text = text.strip()
    if len(text) <= MAX_CONTEXT_FILE_CHARS:
        return text
    return "…\n" + text[-(MAX_CONTEXT_FILE_CHARS - 2) :].lstrip()


def build_context(sessions_dir: Path | None) -> str:
    if sessions_dir is None:
        return ""
    sections: list[str] = []
    total = 0
    for path in session_markdown_files(sessions_dir)[:MAX_CONTEXT_FILES]:
        excerpt = bounded_file_excerpt(path)
        if not excerpt:
            continue
        section = f"### {path.name}\n\n{excerpt}"
        addition = ("\n\n" if sections else "") + section
        if total + len(addition) > MAX_CONTEXT_CHARS:
            remaining = MAX_CONTEXT_CHARS - total
            if remaining > 80:
                sections.append(addition[: remaining - 1].rstrip() + "…")
            break
        sections.append(addition if sections else section)
        total += len(addition)
    if not sections:
        return ""
    context = "Recent session memory digests from $KNOWLEDGE_DIR/sessions:\n\n" + "".join(sections)
    if len(context) > MAX_CONTEXT_CHARS:
        return context[: MAX_CONTEXT_CHARS - 1].rstrip() + "…"
    return context


def session_start(stdin: TextIO = sys.stdin, stdout: TextIO = sys.stdout) -> int:
    try:
        parse_payload(stdin)
        knowledge_dir = knowledge_dir_from_env()
        sessions_dir = require_readable_sessions_dir(knowledge_dir)
        context = build_context(sessions_dir)
    except HookError as exc:
        return _error(str(exc))
    print(context_json(context), file=stdout)
    return 0
