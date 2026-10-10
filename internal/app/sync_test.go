package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRunSyncRepairsHermesExternalDirsDrift(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TACKROOM_HOME", repoRoot)

	writeSyncTestFile(t, filepath.Join(repoRoot, "tackroom.yaml"), []byte(`version: 1
agents:
  - name: hermes
    enabled: true
    skill_root: ~/.hermes/skills
`))
	writeSyncTestFile(t, filepath.Join(repoRoot, "skills", "sample", "SKILL.md"), []byte("---\nname: sample\n---\n"))
	writeSyncTestFile(t, filepath.Join(home, ".hermes", "config.yaml"), []byte("{}\n"))

	if err := runSync(runOptions{Agents: agentHermes}); err != nil {
		t.Fatal(err)
	}

	out, err := os.ReadFile(filepath.Join(home, ".hermes", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	skills, ok := raw["skills"].(map[string]interface{})
	if !ok {
		t.Fatalf("skills = %#v, want map", raw["skills"])
	}
	dirs, ok := skills["external_dirs"].([]interface{})
	if !ok {
		t.Fatalf("external_dirs = %#v, want list", skills["external_dirs"])
	}
	if !containsInterfaceString(dirs, filepath.Join(repoRoot, "skills")) {
		t.Fatalf("external_dirs = %#v, want %q", dirs, filepath.Join(repoRoot, "skills"))
	}

	if _, err := os.Lstat(filepath.Join(home, ".hermes", "skills", "sample")); !os.IsNotExist(err) {
		t.Fatalf("runSync mirrored Hermes skill instead of repairing config, stat err = %v", err)
	}
}

func TestRunSyncProjectsPiSkillsRolesMCPAndInstructions(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TACKROOM_HOME", repoRoot)

	writeSyncTestFile(t, filepath.Join(repoRoot, "tackroom.yaml"), []byte(`version: 1
agents:
  - name: pi
    enabled: true
    skill_root: ~/.pi/agent/skills
    agent_root: ~/.pi/agent/agents
mcp_servers:
  - name: local
    enabled: true
    command: local-mcp
    agents: [pi]
`))
	writeSyncTestFile(t, filepath.Join(repoRoot, "AGENTS.md"), []byte("# Shared instructions\n"))
	writeSyncTestFile(t, filepath.Join(repoRoot, "skills", "sample", "SKILL.md"), []byte("---\nname: sample\ndescription: sample\n---\n"))
	writeSyncTestFile(t, filepath.Join(repoRoot, "agents", "reviewer.md"), []byte("---\nname: reviewer\ndescription: Review changes\neffort: high\ntools: [Read, Grep]\n---\n\nReview carefully.\n"))

	if err := runSync(runOptions{Agents: agentPi}); err != nil {
		t.Fatal(err)
	}

	piRoot := filepath.Join(home, ".pi", "agent")
	if target, err := os.Readlink(filepath.Join(piRoot, "skills", "sample")); err != nil || target != filepath.Join(repoRoot, "skills", "sample") {
		t.Fatalf("Pi skill link target=%q err=%v", target, err)
	}
	role, err := os.ReadFile(filepath.Join(piRoot, "agents", "reviewer.md"))
	if err != nil || !strings.Contains(string(role), `thinking: "high"`) || !strings.Contains(string(role), `- "grep"`) {
		t.Fatalf("Pi subagent role err=%v:\n%s", err, role)
	}
	var mcp map[string]interface{}
	data, err := os.ReadFile(filepath.Join(piRoot, "mcp.json"))
	if err != nil || json.Unmarshal(data, &mcp) != nil {
		t.Fatalf("Pi MCP adapter config err=%v: %s", err, data)
	}
	servers, _ := mcp["mcpServers"].(map[string]interface{})
	if _, ok := servers["local"]; !ok {
		t.Fatalf("Pi MCP server missing: %#v", mcp)
	}
	if target, err := os.Readlink(filepath.Join(piRoot, "AGENTS.md")); err != nil || target != filepath.Join(repoRoot, "AGENTS.md") {
		t.Fatalf("Pi instructions target=%q err=%v", target, err)
	}
}

func writeSyncTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInspectAgentAcceptsMatchingNativeCopyAndRejectsDifferentContent(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	writeSyncTestFile(t, filepath.Join(repoRoot, "AGENTS.md"), []byte("# Shared\n"))
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(repoRoot, "AGENTS.md"), filepath.Join(home, ".claude", "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(repoRoot, "skills", "existing")
	native := filepath.Join(home, ".claude", "skills", "existing")
	content := []byte("---\nname: existing\n---\n")
	writeSyncTestFile(t, filepath.Join(canonical, "SKILL.md"), content)
	writeSyncTestFile(t, filepath.Join(canonical, "references", "guide.txt"), []byte("same\n"))
	writeSyncTestFile(t, filepath.Join(native, "SKILL.md"), content)
	writeSyncTestFile(t, filepath.Join(native, "references", "guide.txt"), []byte("same\n"))
	agent := agentConfig{Name: agentClaudeCode, Enabled: true, SkillRoot: filepath.Dir(native)}

	report, err := inspectAgent(agent, map[string]string{"existing": canonical}, repoRoot, filepath.Join(repoRoot, "skills"), config{Version: 1}, home)
	if err != nil {
		t.Fatal(err)
	}
	// An identical copy is not a conflict; sync relinks it so later edits to
	// the shared skill reach this agent. Inspect itself changes nothing.
	if report.Synced || !stringSlicesEqual(report.Updates, []string{"existing"}) || len(report.Conflicts) != 0 {
		t.Fatalf("matching native copy report = %#v", report)
	}
	if info, err := os.Lstat(native); err != nil || !info.IsDir() {
		t.Fatalf("inspect modified the native copy: info=%v err=%v", info, err)
	}

	writeSyncTestFile(t, filepath.Join(native, "references", "guide.txt"), []byte("different\n"))
	report, err = inspectAgent(agent, map[string]string{"existing": canonical}, repoRoot, filepath.Join(repoRoot, "skills"), config{Version: 1}, home)
	if err != nil {
		t.Fatal(err)
	}
	if report.Synced || len(report.Conflicts) != 1 {
		t.Fatalf("different native copy report = %#v", report)
	}
}
