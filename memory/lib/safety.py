#!/usr/bin/env python3
"""Shared safety helpers for public memory hooks."""

from __future__ import annotations

import contextlib
import fcntl
import os
import re
import tempfile
from pathlib import Path
from typing import Iterator

DEFAULT_KNOWLEDGE_DIR = "~/Workspace/knowledge"

_SECRET_PATTERNS: tuple[re.Pattern[str], ...] = (
    re.compile(r"(?i)\b(Bearer\s+)[A-Za-z0-9._~+/=-]{8,}"),
    re.compile(
        r"(?i)\b((?:api[_-]?key|secret|token|password|authorization|auth)\s*[:=]\s*)(?:\"[^\"]+\"|'[^']+'|[^\s`'\"\\]+)"
    ),
    re.compile(r"(?i)((?:[?&])(?:api[_-]?key|secret|token|password|auth)=)[^&\s`'\"\\]+"),
    re.compile(r"(?i)\b(sk-(?:ant-|proj-|svcacct-)?)[A-Za-z0-9_-]{16,}"),
    re.compile(r"(?i)\b(github_pat_)[A-Za-z0-9_]{20,}"),
    re.compile(r"(?i)\b(gh[pousr]_)[A-Za-z0-9]{20,}"),
    re.compile(r"(?i)\b(glpat-)[A-Za-z0-9_-]{20,}"),
    re.compile(r"(?i)\b(xox[baprs]-)[A-Za-z0-9-]{16,}"),
)


def redact_text(value: object) -> str:
    text = str(value)
    for pattern in _SECRET_PATTERNS:
        text = pattern.sub(r"\1[REDACTED]", text)
    return text


def truncate_redacted(value: object, limit: int = 220) -> str:
    text = re.sub(r"\s+", " ", redact_text(value)).strip()
    if len(text) <= limit:
        return text
    return text[: max(0, limit - 1)].rstrip() + "…"


def safe_identifier(value: object, *, fallback: str = "unknown", limit: int = 96) -> str:
    redacted = redact_text(value).strip()
    sanitized = re.sub(r"[^A-Za-z0-9_.-]+", "-", redacted).strip("-._")
    return sanitized[:limit] or fallback


def knowledge_dir_from_environment() -> Path:
    value = os.environ.get("KNOWLEDGE_DIR")
    if not value or not value.strip():
        value = DEFAULT_KNOWLEDGE_DIR
    return Path(os.path.expanduser(value)).resolve()


@contextlib.contextmanager
def locked_directory(directory: Path, lock_name: str = ".tackroom-memory.lock") -> Iterator[None]:
    directory.mkdir(parents=True, exist_ok=True)
    lock_path = directory / lock_name
    with lock_path.open("a", encoding="utf-8") as handle:
        fcntl.flock(handle.fileno(), fcntl.LOCK_EX)
        try:
            yield
        finally:
            fcntl.flock(handle.fileno(), fcntl.LOCK_UN)


def atomic_write_text(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp_name = tempfile.mkstemp(prefix=f".{path.name}.", suffix=".tmp", dir=str(path.parent))
    tmp_path = Path(tmp_name)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            handle.write(content)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(tmp_path, path)
    except Exception:
        try:
            tmp_path.unlink()
        except OSError:
            pass
        raise
