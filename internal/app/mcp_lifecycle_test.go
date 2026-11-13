package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveCodexMCPServerLeavesABrokenFileAlone(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	broken := "[mcp_servers.gone]\ncommand = \"x\"\n\n[mcp_servers.broken\ncommand = = \"y\"\n"
	writeSyncTestFile(t, configPath, []byte(broken))
	if err := removeMCPServer(agentCodex, "gone", home); err == nil {
		t.Fatal("removing from an unparseable config should fail")
	}
	if data, _ := os.ReadFile(configPath); string(data) != broken {
		t.Fatalf("broken config was modified:\n%s", data)
	}
}

func writeMCPLifecycleConfig(t *testing.T, repoRoot string, servers string) {
	t.Helper()
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
mcp_servers:
`+servers))
}

func TestSyncRemovesMCPServersDroppedFromTackroomYAML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	fakePath(t, "claude", "codex")
	repoRoot := filepath.Join(home, ".agents")
	configPath := filepath.Join(repoRoot, "tackroom.yaml")
	writeSyncTestFile(t, filepath.Join(repoRoot, "skills", "sample", "SKILL.md"), []byte("---\nname: sample\ndescription: s\n---\n"))
	// A server the user configured by hand must survive.
	writeSyncTestFile(t, filepath.Join(home, ".codex", "config.toml"), []byte("[mcp_servers.mine]\ncommand = \"mine-mcp\"\nargs = []\n"))
	writeMCPLifecycleConfig(t, repoRoot, `  - name: keep
    enabled: true
    command: keep-mcp
  - name: gone
    enabled: true
    command: gone-mcp
`)
	if err := runSync(runOptions{ConfigPath: configPath, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	for _, path := range []string{".claude.json", ".codex/config.toml"} {
		if data, _ := os.ReadFile(filepath.Join(home, path)); !strings.Contains(string(data), "gone-mcp") {
			t.Fatalf("%s should have the gone server after the first sync:\n%s", path, data)
		}
	}

	writeMCPLifecycleConfig(t, repoRoot, `  - name: keep
    enabled: true
    command: keep-mcp
`)
	var status bytes.Buffer
	if err := runStatus(runOptions{ConfigPath: configPath, Stdout: &status}); err == nil || !strings.Contains(status.String(), "gone") {
		t.Fatalf("status should report the stale server, err=%v\n%s", err, status.String())
	}

	var out bytes.Buffer
	if err := runSync(runOptions{ConfigPath: configPath, Stdout: &out}); err != nil {
		t.Fatalf("second sync: %v\n%s", err, out.String())
	}
	for _, agent := range []string{agentClaudeCode, agentCodex} {
		if state, err := inspectMCPServer(agent, mcpServerConfig{Name: "gone"}, home); err != nil || state != stateMissing {
			t.Fatalf("%s still has the removed server: %s, %v", agent, state, err)
		}
		if state, err := inspectMCPServer(agent, mcpServerConfig{Name: "keep", Command: "keep-mcp"}, home); err != nil || state != stateSynced {
			t.Fatalf("%s lost the kept server: %s, %v", agent, state, err)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml")); !strings.Contains(string(data), "mine-mcp") {
		t.Fatalf("a hand-configured server was removed:\n%s", data)
	}
	backups, _ := filepath.Glob(filepath.Join(home, ".local", "state", "tackroom", "backups", "*", ".codex", "config.toml"))
	if len(backups) != 1 {
		t.Fatalf("codex config should be backed up before removal, found %v", backups)
	}
	if data, _ := os.ReadFile(backups[0]); !strings.Contains(string(data), "gone-mcp") {
		t.Fatalf("backup should hold the pre-removal config:\n%s", data)
	}
	if err := runStatus(runOptions{ConfigPath: configPath, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("status after cleanup: %v", err)
	}
}

func TestSyncNeverClaimsAServerTheUserAlreadyHad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	fakePath(t, "claude", "codex")
	repoRoot := filepath.Join(home, ".agents")
	configPath := filepath.Join(repoRoot, "tackroom.yaml")
	writeSyncTestFile(t, filepath.Join(repoRoot, "skills", "sample", "SKILL.md"), []byte("---\nname: sample\ndescription: s\n---\n"))
	// Codex already has the exact entry tackroom.yaml asks for.
	writeSyncTestFile(t, filepath.Join(home, ".codex", "config.toml"), []byte("[mcp_servers.mine]\ncommand = \"mine-mcp\"\nargs = []\n"))
	writeMCPLifecycleConfig(t, repoRoot, `  - name: mine
    enabled: true
    command: mine-mcp
    agents: [codex]
`)
	if err := runSync(runOptions{ConfigPath: configPath, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	writeMCPLifecycleConfig(t, repoRoot, "  []\n")
	if err := runSync(runOptions{ConfigPath: configPath, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if state, err := inspectMCPServer(agentCodex, mcpServerConfig{Name: "mine", Command: "mine-mcp"}, home); err != nil || state != stateSynced {
		t.Fatalf("a server tackroom never wrote was removed: %s, %v", state, err)
	}
}

func TestMCPRemoveCleansUpUntrackedMatchingCopies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	fakePath(t, "claude", "codex")
	repoRoot := filepath.Join(home, ".agents")
	configPath := filepath.Join(repoRoot, "tackroom.yaml")
	writeSyncTestFile(t, filepath.Join(repoRoot, "skills", "sample", "SKILL.md"), []byte("---\nname: sample\ndescription: s\n---\n"))
	// A server listed after the removed one must not hide it from the claim.
	writeMCPLifecycleConfig(t, repoRoot, `  - name: old
    enabled: true
    command: old-mcp
  - name: other
    enabled: true
    command: other-mcp
`)
	// As written by a release that did not record ownership.
	for _, agent := range []string{agentClaudeCode, agentCodex} {
		if err := patchMCPServer(agent, mcpServerConfig{Name: "old", Command: "old-mcp"}, home); err != nil {
			t.Fatal(err)
		}
	}
	if err := runMCPRemove([]string{"old", "--config", configPath}); err != nil {
		t.Fatal(err)
	}
	if err := runSync(runOptions{ConfigPath: configPath, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	for _, agent := range []string{agentClaudeCode, agentCodex} {
		if state, err := inspectMCPServer(agent, mcpServerConfig{Name: "old"}, home); err != nil || state != stateMissing {
			t.Fatalf("%s still has the removed server: %s, %v", agent, state, err)
		}
	}
}

func TestUnreadableMCPOwnershipIsAnError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", "")
	writeSyncTestFile(t, mcpOwnershipPath(home), []byte("{not json"))
	if _, err := readMCPOwnership(home); err == nil || !strings.Contains(err.Error(), "delete it") {
		t.Fatalf("want a parse error with a fix, got %v", err)
	}
	if err := writeMCPOwnership(home, mcpOwnership{"codex": {"a"}}); err != nil {
		t.Fatal(err)
	}
	owned, err := readMCPOwnership(home)
	if err != nil || len(owned["codex"]) != 1 {
		t.Fatalf("round trip: %v %v", owned, err)
	}
}
