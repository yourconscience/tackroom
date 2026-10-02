package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeAmpE2EConfig(t *testing.T, path string) {
	t.Helper()
	data := []byte(fmt.Sprintf(`version: 1
agents:
  - name: amp
    enabled: true
    skill_root: ~/.agents/skills
mcp_servers:
  - name: local
    enabled: true
    command: %s
    args:
      - local-mcp@latest
    env:
      TOKEN: ${TOKEN}
    agents:
      - amp
`, testMCPServer().Command))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func installFakeTackroomOnPath(t *testing.T, home string) {
	t.Helper()
	fakeBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "tackroom"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "amp"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)
}

func TestAmpSetupE2EConfiguresSkillsAndMCP(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TACKROOM_HOME", repoRoot)
	installFakeTackroomOnPath(t, home)
	configPath := filepath.Join(repoRoot, "tackroom.yaml")
	writeAmpE2EConfig(t, configPath)

	if err := Run([]string{"setup", "--agents=amp", "--config", configPath}); err != nil {
		t.Fatal(err)
	}

	settingsPath := filepath.Join(home, ".config", "amp", "settings.json")
	settingsData, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(settingsData, &settings); err != nil {
		t.Fatal(err)
	}
	if settings["amp.skills.path"] != filepath.Join(repoRoot, "skills") {
		t.Fatalf("amp.skills.path = %#v, want custom canonical root", settings["amp.skills.path"])
	}
	servers, ok := asMap(settings["amp.mcpServers"])
	if !ok {
		t.Fatalf("amp.mcpServers missing from %#v", settings)
	}
	local, ok := asMap(servers["local"])
	if !ok {
		t.Fatalf("local MCP missing from %#v", servers)
	}
	if local["command"] != testMCPServer().Command {
		t.Fatalf("local MCP = %#v", local)
	}

	if err := Run([]string{"status", "--agents=amp", "--config", configPath}); err != nil {
		t.Fatal(err)
	}
}

func TestAmpSetupE2EPreservesExistingSettings(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TACKROOM_HOME", repoRoot)
	installFakeTackroomOnPath(t, home)
	configPath := filepath.Join(repoRoot, "tackroom.yaml")
	writeAmpE2EConfig(t, configPath)
	settingsPath := filepath.Join(home, ".config", "amp", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"amp.url":"http://localhost:8317","amp.mcpServers":{"keep":{"command":"node","args":[]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Run([]string{"setup", "--agents=amp", "--config", configPath}); err != nil {
		t.Fatal(err)
	}

	settingsData, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(settingsData, &settings); err != nil {
		t.Fatal(err)
	}
	if settings["amp.url"] != "http://localhost:8317" {
		t.Fatalf("amp.url was not preserved: %#v", settings)
	}
	servers, _ := asMap(settings["amp.mcpServers"])
	if _, ok := servers["keep"]; !ok {
		t.Fatalf("existing MCP was not preserved: %#v", servers)
	}
	if _, ok := servers["local"]; !ok {
		t.Fatalf("managed MCP was not added: %#v", servers)
	}
}
