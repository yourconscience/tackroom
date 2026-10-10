package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedMessyHome writes the setup the 1.2.0 end-to-end test used: different
// instructions per agent, one skill that differs between agents, and a Codex
// MCP server whose secret lives in an env table.
func seedMessyHome(t *testing.T, home string) {
	t.Helper()
	writeSyncTestFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), []byte("# claude rules\n"))
	writeSyncTestFile(t, filepath.Join(home, ".codex", "AGENTS.md"), []byte("# codex rules\n"))
	writeSyncTestFile(t, filepath.Join(home, ".claude", "skills", "pr-review", "SKILL.md"), []byte("---\nname: pr-review\ndescription: review\n---\nA\n"))
	writeSyncTestFile(t, filepath.Join(home, ".codex", "skills", "pr-review", "SKILL.md"), []byte("---\nname: pr-review\ndescription: review\n---\nB\n"))
	writeSyncTestFile(t, filepath.Join(home, ".codex", "config.toml"), []byte("[mcp_servers.linear]\ncommand = \"npx\"\nargs = [\"-y\", \"linear-mcp\"]\n\n[mcp_servers.linear.env]\nLINEAR_API_KEY = \"lin_api_FAKE\"\n"))
}

func TestSetupYesAdoptsAnExistingMultiAgentHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	fakePath(t, "claude", "codex")
	seedMessyHome(t, home)

	// An endless stdin proves --yes never reads it.
	var out bytes.Buffer
	err := runSetup(runOptions{Agents: "claude,codex", MemoryTier: memoryTierOff, AssumeYes: true, Stdin: strings.NewReader(strings.Repeat("x", 1<<16)), Stdout: &out})
	if err != nil {
		t.Fatalf("setup: %v\n%s", err, out.String())
	}

	shared, err := os.ReadFile(filepath.Join(home, ".agents", "AGENTS.md"))
	if err != nil || !strings.Contains(string(shared), "# claude rules") || !strings.Contains(string(shared), "# codex rules") {
		t.Fatalf("AGENTS.md should hold both agents' instructions: %v\n%s", err, shared)
	}
	for _, link := range []string{".claude/CLAUDE.md", ".codex/AGENTS.md", ".claude/skills/pr-review", ".codex/skills/pr-review"} {
		if info, err := os.Lstat(filepath.Join(home, link)); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s should be a link after setup (err=%v)", link, err)
		}
	}
	backups, _ := filepath.Glob(filepath.Join(home, ".local", "state", "tackroom", "backups", "*", ".codex", "skills", "pr-review", "SKILL.md"))
	if len(backups) != 1 {
		t.Fatalf("the differing Codex skill should be backed up, found %v\n%s", backups, out.String())
	}
	if data, _ := os.ReadFile(backups[0]); !strings.Contains(string(data), "B") {
		t.Fatalf("backup lost the Codex version:\n%s", data)
	}
	codex, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if !strings.Contains(string(codex), "lin_api_FAKE") {
		t.Fatalf("Codex lost its secret:\n%s", codex)
	}
	cfg, _ := os.ReadFile(filepath.Join(home, ".agents", "tackroom.yaml"))
	if strings.Contains(string(cfg), "lin_api_FAKE") || !strings.Contains(string(cfg), "${LINEAR_API_KEY}") {
		t.Fatalf("tackroom.yaml should hold a reference, not the secret:\n%s", cfg)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "memory", "tools")); !os.IsNotExist(err) {
		t.Fatalf("--memory off must not ship memory tool sources (err=%v)", err)
	}
}

func TestSyncSkipsOnlyTheConflictingAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	fakePath(t, "claude", "codex")
	repoRoot := filepath.Join(home, ".agents")
	writeSyncTestFile(t, filepath.Join(repoRoot, "tackroom.yaml"), []byte(`version: 1
agents:
  - name: claude
    enabled: true
    skill_root: ~/.claude/skills
    detect: claude
  - name: codex
    enabled: true
    skill_root: ~/.codex/skills
    detect: codex
`))
	writeSyncTestFile(t, filepath.Join(repoRoot, "AGENTS.md"), []byte("# Shared\n"))
	writeSyncTestFile(t, filepath.Join(repoRoot, "skills", "sample", "SKILL.md"), []byte("---\nname: sample\ndescription: s\n---\n"))
	writeSyncTestFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), []byte("# hand-written\n"))
	configPath := filepath.Join(repoRoot, "tackroom.yaml")

	err := runSync(runOptions{ConfigPath: configPath, Stdout: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "--replace-conflicts") {
		t.Fatalf("sync should fail with the fix for the Claude conflict, got %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".codex", "skills", "sample")); err != nil {
		t.Fatalf("Codex should still sync while Claude has a conflict: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md")); string(data) != "# hand-written\n" {
		t.Fatalf("plain sync must not touch the conflicting file:\n%s", data)
	}

	if err := runSync(runOptions{ConfigPath: configPath, ReplaceConflicts: true, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("sync --replace-conflicts: %v", err)
	}
	if target, err := os.Readlink(filepath.Join(home, ".claude", "CLAUDE.md")); err != nil || target != filepath.Join(repoRoot, "AGENTS.md") {
		t.Fatalf("CLAUDE.md should now link the shared file: %q %v", target, err)
	}
	backups, _ := filepath.Glob(filepath.Join(home, ".local", "state", "tackroom", "backups", "*", ".claude", "CLAUDE.md"))
	if len(backups) != 1 {
		t.Fatalf("the hand-written CLAUDE.md should be backed up, found %v", backups)
	}
}

func TestMergeManagedEnvNeverReplacesASecretWithAReference(t *testing.T) {
	native := map[string]string{"API_KEY": "real", "EXTRA": "x"}
	got := mergeManagedEnv(native, map[string]string{"API_KEY": "${API_KEY}", "MODE": "fast"})
	if got["API_KEY"] != "real" || got["EXTRA"] != "x" || got["MODE"] != "fast" {
		t.Fatalf("merged env = %v", got)
	}
	if got := mergeManagedEnv(native, map[string]string{"API_KEY": "rotated"}); got["API_KEY"] != "rotated" {
		t.Fatalf("a literal canonical value should win: %v", got)
	}
	if !envSatisfied(map[string]string{"API_KEY": "${API_KEY}"}, native) || envSatisfied(map[string]string{"API_KEY": "${API_KEY}"}, map[string]string{}) {
		t.Fatal("a reference is satisfied by any native value and only by one")
	}
}
