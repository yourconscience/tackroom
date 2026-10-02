import json
import os
import stat
import subprocess
import sys
import tempfile
import time
import unittest
import importlib.util
from pathlib import Path


MEMORY_DIR = Path(__file__).parents[1]
SYNC_PATH = MEMORY_DIR / "lib" / "sync.py"
SPEC = importlib.util.spec_from_file_location("tackroom_memory_sync", SYNC_PATH)
assert SPEC is not None
SYNC = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(SYNC)


class VaultToMemoryTests(unittest.TestCase):
    def test_full_user_memory_is_not_rewritten_or_reported_changed(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            hermes_user = root / "hermes" / "USER.md"
            vault_profile = root / "vault" / "profile" / "USER.md"
            hermes_user.parent.mkdir(parents=True)
            vault_profile.parent.mkdir(parents=True)

            existing = "x" * (SYNC.HERMES_USER_LIMIT - 1) + "\n"
            hermes_user.write_text(existing, encoding="utf-8")
            vault_profile.write_text("# Profile\n\n- a distinct durable fact that cannot fit\n", encoding="utf-8")
            before_mtime = hermes_user.stat().st_mtime_ns

            changed = SYNC.vault_to_memory(
                {
                    "hermes_user": hermes_user,
                    "vault_profile": vault_profile,
                }
            )

            self.assertFalse(changed)
            self.assertEqual(hermes_user.read_text(encoding="utf-8"), existing)
            self.assertEqual(hermes_user.stat().st_mtime_ns, before_mtime)


class HermesSyncHookTests(unittest.TestCase):
    def wait_for(self, predicate, timeout: float = 5.0) -> bool:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if predicate():
                return True
            time.sleep(0.05)
        return predicate()

    def test_both_successful_wrappers_use_shared_refresh_helper(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            knowledge = root / "knowledge"
            (home / ".hermes" / "memories").mkdir(parents=True)
            (knowledge / "profile").mkdir(parents=True)
            (knowledge / "sessions").mkdir(parents=True)
            (knowledge / "notes").mkdir(parents=True)
            (knowledge / "profile" / "USER.md").write_text("# Profile\n", encoding="utf-8")

            fake_bin = root / "bin"
            fake_bin.mkdir()
            log = root / "memsearch.log"
            fake = fake_bin / "memsearch"
            fake.write_text(f'#!/bin/sh\nprintf "%s\\n" "$*" >> "{log}"\n', encoding="utf-8")
            fake.chmod(fake.stat().st_mode | stat.S_IXUSR)

            env = os.environ.copy()
            env.update(
                {
                    "HOME": str(home),
                    "KNOWLEDGE_DIR": str(knowledge),
                    "MEMSEARCH_STATE_DIR": str(root / "state"),
                    "PATH": str(fake_bin) + os.pathsep + env.get("PATH", ""),
                }
            )
            for index, name in enumerate(("sync-memory-to-vault.sh", "sync-vault-to-memory.sh"), start=1):
                result = subprocess.run(
                    ["/bin/bash", str(MEMORY_DIR / "hooks" / name)],
                    text=True,
                    capture_output=True,
                    env=env,
                    check=False,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(json.loads(result.stdout)["action"], "continue")
                self.assertTrue(
                    self.wait_for(lambda: log.exists() and len(log.read_text(encoding="utf-8").splitlines()) >= index),
                    f"{name} did not trigger the shared refresh helper",
                )
                self.assertTrue(
                    self.wait_for(lambda: not (root / "state" / "reindex.lock").exists()),
                    f"{name} refresh did not release its lock",
                )

            for line in log.read_text(encoding="utf-8").splitlines():
                self.assertIn("index", line)
                self.assertIn("--collection ai", line)


if __name__ == "__main__":
    unittest.main()
