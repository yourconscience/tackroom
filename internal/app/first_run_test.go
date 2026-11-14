package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestCodexDuplicateRepairKeepsSecretsAndOtherServersReadable(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	writeSyncTestFile(t, configPath, []byte(`[mcp_servers.dup]
command = "old"

[mcp_servers.dup]
command = "old"

[mcp_servers.dup.env]
API_KEY = "real-secret"

[mcp_servers.fine]
command = "fine-mcp"
`))
	// Another server's duplicates must not make this one unreadable.
	state, err := inspectMCPServer(agentCodex, mcpServerConfig{Name: "fine", Command: "fine-mcp"}, home)
	if err != nil || state != stateSynced {
		t.Fatalf("fine server = %s, %v", state, err)
	}
	server := mcpServerConfig{Name: "dup", Command: "new-mcp", Env: map[string]string{"API_KEY": "${API_KEY}"}}
	if err := patchMCPServer(agentCodex, server, home); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(configPath)
	if strings.Count(string(data), "[mcp_servers.dup]") != 1 || !strings.Contains(string(data), "real-secret") {
		t.Fatalf("repair should dedupe and keep the secret:\n%s", data)
	}
}

func TestConflictBackupsArePrivate(t *testing.T) {
	home := t.TempDir()
	native := filepath.Join(home, ".claude", "CLAUDE.md")
	writeSyncTestFile(t, native, []byte("# mine\n"))
	backupDir := filepath.Join(home, "backup")
	report := agentReport{Name: "claude", Conflicts: []string{"c"}, Replaceable: []replaceableConflict{{Path: native, Message: "c"}}}
	if _, err := replaceConflicts(&report, backupDir, home); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{backupDir, filepath.Join(backupDir, ".claude")} {
		info, err := os.Stat(dir)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode = %v, %v; want 0700", dir, info.Mode().Perm(), err)
		}
	}
}

func TestSetupImportsSymlinkedInstructions(t *testing.T) {
	home := t.TempDir()
	dotfile := filepath.Join(home, "dotfiles", "CLAUDE.md")
	writeSyncTestFile(t, dotfile, []byte("# from dotfiles\n"))
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dotfile, filepath.Join(home, ".claude", "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Join(home, ".agents")
	if err := importRootInstructions(repoRoot, []agentConfig{{Name: agentClaudeCode}}, home, setupIO{out: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(repoRoot, "AGENTS.md")); string(data) != "# from dotfiles\n" {
		t.Fatalf("symlinked instructions should be imported, got %q", data)
	}
}
