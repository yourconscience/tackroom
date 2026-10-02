package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yourconscience/tackroom/internal/agentrole"
)

func writeAgentsFixture(t *testing.T, repoRoot, name, data string) {
	t.Helper()
	agentsDir := filepath.Join(repoRoot, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, name), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRenderAgentRolePrefillsConfiguredModel(t *testing.T) {
	role := agentrole.Role{Name: "builder", Description: "Builds features", Instructions: "Implement."}
	path, content, ok := renderAgentRole(role, agentConfig{Name: agentClaudeCode, AgentRoot: t.TempDir(), RoleModel: "configured-model"})
	if !ok {
		t.Fatal("claude role was not rendered")
	}
	if !strings.Contains(content, `model: "configured-model"`) {
		t.Fatalf("role missing configured role_model:\n%s", content)
	}
	if path == "" {
		t.Fatal("empty target path")
	}
}

func TestExpectedAgentRolesLoadsCanonicalMarkdown(t *testing.T) {
	repoRoot := t.TempDir()
	writeAgentsFixture(t, repoRoot, "builder.md", `---
name: builder
description: Builds features
model: sonnet
tools: Read, Grep
---

Implement the change.
`)
	roles, err := expectedAgentRoles(repoRoot, agentConfig{Name: agentClaudeCode, AgentRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	role, ok := roles["builder"]
	if !ok {
		t.Fatalf("rendered roles = %#v", roles)
	}
	if !strings.Contains(role.Content, "tools: Read, Grep\n") {
		t.Fatalf("canonical scalar tools not preserved in projection:\n%s", role.Content)
	}
}

func TestIsManagedAgentFile(t *testing.T) {
	repoRoot := t.TempDir()
	managed := filepath.Join(repoRoot, "managed.toml")
	if err := os.WriteFile(managed, []byte("# "+agentrole.GeneratedMarker+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isManagedAgentFile(managed, []byte("# "+agentrole.GeneratedMarker+"\n"), repoRoot) {
		t.Fatal("generated marker should be managed")
	}

	legacy := filepath.Join(repoRoot, "legacy.toml")
	if err := os.WriteFile(legacy, []byte("# "+agentrole.LegacyGeneratedMarker+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isManagedAgentFile(legacy, []byte("# "+agentrole.LegacyGeneratedMarker+"\n"), repoRoot) {
		t.Fatal("roles rendered before the rename should stay managed")
	}

	unmanaged := filepath.Join(repoRoot, "unmanaged.toml")
	if err := os.WriteFile(unmanaged, []byte("name = \"local\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isManagedAgentFile(unmanaged, []byte("name = \"local\"\n"), repoRoot) {
		t.Fatal("unmarked real file should not be managed")
	}
}
