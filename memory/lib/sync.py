#!/usr/bin/env python3
"""Sync Hermes built-in memory with the tackroom knowledge vault.

Direction 1 (memory->vault): export Hermes MEMORY.md/USER.md facts to vault
  - Hermes MEMORY.md -> vault sessions/knowledge.md
  - Hermes USER.md -> vault profile/USER.md (appended as "Hermes Memory Sync" section)

Direction 2 (vault->memory): import vault profile facts into Hermes memory
  - vault profile/USER.md -> Hermes USER.md (compact, deduplicated)
  - vault recent sessions/ facts -> Hermes MEMORY.md (compact, deduplicated)

Usage:
  python sync.py memory-to-vault   # export Hermes -> vault
  python sync.py vault-to-memory   # import vault -> Hermes
  python sync.py both              # bidirectional
"""

import hashlib
import os
import re
import sys
from datetime import datetime, timezone
from pathlib import Path


# -- Paths -----------------------------------------------------------------
def resolve():
    home = Path(os.path.expanduser("~"))
    vault_dir = Path(os.path.expanduser(os.environ.get("KNOWLEDGE_DIR", "~/Workspace/knowledge")))
    sessions_dir = Path(os.path.expanduser(os.environ.get("SESSIONS_DIR", str(vault_dir / "sessions"))))
    notes_dir = Path(os.path.expanduser(os.environ.get("NOTES_DIR", str(vault_dir / "notes"))))
    profile_dir = Path(os.path.expanduser(os.environ.get("PROFILE_DIR", str(vault_dir / "profile"))))
    return {
        "hermes_memory": home / ".hermes" / "memories" / "MEMORY.md",
        "hermes_user": home / ".hermes" / "memories" / "USER.md",
        "vault_profile": profile_dir / "USER.md",
        "vault_knowledge": sessions_dir / "knowledge.md",
    }


# -- Parse Hermes section-delimited memory ---------------------------------
def parse_hermes_memory(path: Path) -> list[str]:
    """Split Hermes memory file by section delimiter, return non-empty entries."""
    if not path.exists():
        return []
    text = path.read_text(encoding="utf-8").strip()
    if not text:
        return []
    entries = [e.strip() for e in text.split("§")]
    return [e for e in entries if e]


def normalize(entry: str) -> str:
    """Normalize for dedup comparison: lowercase, drop punctuation, collapse whitespace."""
    text = entry.lower().strip()
    text = re.sub(r"[^\w+]+", " ", text)
    return re.sub(r"\s+", " ", text).strip()


def exported_entry_fingerprints(text: str, memory_entries: list[str]) -> set[bytes]:
    """Find exact serialized entries, preferring longer overlapping matches."""
    occupied: list[tuple[int, int]] = []
    fingerprints: set[bytes] = set()
    for entry in sorted(memory_entries, key=len, reverse=True):
        pattern = re.compile(
            rf"(?m)^- {re.escape(entry)}\n(?=- |\n## Sync |\Z)"
        )
        for match in pattern.finditer(text):
            start, end = match.span()
            if any(start < used_end and used_start < end for used_start, used_end in occupied):
                continue
            occupied.append((start, end))
            fingerprint = hashlib.sha256(normalize(entry).encode("utf-8")).digest()
            fingerprints.add(fingerprint)
            break
    return fingerprints


# -- Direction 1: Memory -> Vault ------------------------------------------
def memory_to_vault(paths: dict):
    """Export Hermes memory facts to the knowledge vault."""
    changed = False

    # --- MEMORY.md -> sessions/knowledge.md ---
    memory_entries = parse_hermes_memory(paths["hermes_memory"])
    knowledge_path = paths["vault_knowledge"]

    existing_knowledge: set[bytes] = set()
    if knowledge_path.exists():
        knowledge_text = knowledge_path.read_text(encoding="utf-8")
        existing_knowledge = exported_entry_fingerprints(knowledge_text, memory_entries)

    new_entries = []
    for entry in memory_entries:
        fingerprint = hashlib.sha256(normalize(entry).encode("utf-8")).digest()
        if fingerprint not in existing_knowledge:
            new_entries.append(entry)
            existing_knowledge.add(fingerprint)

    if new_entries:
        knowledge_path.parent.mkdir(parents=True, exist_ok=True)
        now = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
        is_new_file = not knowledge_path.exists() or knowledge_path.stat().st_size == 0
        with knowledge_path.open("a", encoding="utf-8") as f:
            if is_new_file:
                f.write("# Hermes Memory Export\n\n")
                f.write(f"Auto-synced from Hermes built-in memory. Last sync: {now}\n\n")
            else:
                f.write(f"\n## Sync {now}\n\n")
            for entry in new_entries:
                f.write(f"- {entry}\n")
        print(f"memory->vault: wrote {len(new_entries)} new entries to {knowledge_path}")
        changed = True
    else:
        print(f"memory->vault: {knowledge_path} up to date (no new entries)")

    # --- USER.md -> profile/USER.md ---
    user_entries = parse_hermes_memory(paths["hermes_user"])
    profile_path = paths["vault_profile"]

    if profile_path.exists():
        profile_text = profile_path.read_text(encoding="utf-8")
    else:
        profile_text = ""

    marker = "## Hermes Memory Sync"
    base_profile_text = profile_text[:profile_text.index(marker)].rstrip() if marker in profile_text else profile_text.rstrip()

    now = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
    sync_section = f"\n\n{marker}\n\nLast synced: {now}\n\n"
    for entry in user_entries:
        sync_section += f"- {entry}\n"
    sync_section += "\n"

    current_section_body = ""
    if marker in profile_text:
        current_section_body = profile_text[profile_text.index(marker):].strip()

    desired_section_body = sync_section.strip()
    # Strip timestamps before comparing to avoid no-op rewrites on every run
    current_without_timestamp = re.sub(r"Last synced: .*", "Last synced: <timestamp>", current_section_body)
    desired_without_timestamp = re.sub(r"Last synced: .*", "Last synced: <timestamp>", desired_section_body)

    if current_without_timestamp != desired_without_timestamp:
        profile_path.write_text(base_profile_text + sync_section, encoding="utf-8")
        print(f"memory->vault: wrote {len(user_entries)} user facts to {profile_path}")
        changed = True
    else:
        print(f"memory->vault: {profile_path} up to date (no user fact changes)")

    return changed


# -- Direction 2: Vault -> Memory ------------------------------------------
HERMES_MEMORY_LIMIT = 2200
HERMES_USER_LIMIT = 1375


def vault_to_memory(paths: dict):
    """Import vault facts into Hermes built-in memory, respecting char limits."""
    changed = False

    # --- profile/USER.md -> Hermes USER.md ---
    profile_path = paths["vault_profile"]
    hermes_user_path = paths["hermes_user"]

    existing_entries = parse_hermes_memory(hermes_user_path)
    existing_user = {normalize(e) for e in existing_entries}

    profile_facts = []
    if profile_path.exists():
        for line in profile_path.read_text(encoding="utf-8").splitlines():
            stripped = line.strip()
            if stripped.startswith("- "):
                fact = stripped[2:].strip()
                if fact and len(fact) > 15:
                    profile_facts.append(fact)

    # Hermes consolidates multiple vault bullets into single facts, so
    # substring containment and token-subset matching are needed for dedup,
    # not just exact normalized equality.
    def tokens(text: str) -> set[str]:
        stop = {"a", "an", "and", "the", "with", "in", "on", "at", "from", "via", "to", "of"}
        return {t for t in normalize(text).split() if t not in stop}

    existing_tokens = [tokens(existing) for existing in existing_user]
    new_facts = []
    for fact in profile_facts:
        fact_norm = normalize(fact)
        fact_tokens = tokens(fact)
        if fact_norm in existing_user:
            continue
        if any(fact_norm in existing or existing in fact_norm for existing in existing_user):
            continue
        if len(fact_tokens) >= 2 and any(fact_tokens <= toks for toks in existing_tokens):
            continue
        if len(fact_tokens) >= 2 and any(len(fact_tokens & toks) / len(fact_tokens) >= 0.70 for toks in existing_tokens):
            continue
        new_facts.append(fact)

    if new_facts:
        lines = list(existing_entries)

        def rendered_size(entries: list[str]) -> int:
            if not entries:
                return 0
            return len("\n§\n".join(entries) + "\n")

        current_size = rendered_size(lines)

        for fact in new_facts:
            candidate = lines + [fact]
            candidate_size = rendered_size(candidate)
            if candidate_size <= HERMES_USER_LIMIT:
                lines.append(fact)
                current_size = candidate_size
            else:
                break

        added = len(lines) - len(existing_entries)
        if added:
            hermes_user_path.write_text(
                "\n§\n".join(lines) + "\n",
                encoding="utf-8",
            )
        print(f"vault->memory: added {added} facts to {hermes_user_path} ({current_size}/{HERMES_USER_LIMIT} chars)")
        changed = added > 0
    else:
        print(f"vault->memory: {hermes_user_path} up to date")

    return changed


# -- Main ------------------------------------------------------------------
def main():
    if len(sys.argv) < 2:
        print("Usage: sync.py [memory-to-vault|vault-to-memory|both]", file=sys.stderr)
        sys.exit(1)

    paths = resolve()
    direction = sys.argv[1]
    if direction in ("memory-to-vault", "both"):
        memory_to_vault(paths)

    if direction in ("vault-to-memory", "both"):
        vault_to_memory(paths)


if __name__ == "__main__":
    main()
