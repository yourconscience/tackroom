package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testMCPServer() mcpServerConfig {
	return mcpServerConfig{
		Name:    "linkedin",
		Command: "uvx",
		Args:    []string{"linkedin-scraper-mcp==4.13.2"},
		Env:     map[string]string{"UV_HTTP_TIMEOUT": "300"},
	}
}

func TestDroidMCPInspectMissing(t *testing.T) {
	home := t.TempDir()
	state, err := inspectMCPServer(agentDroid, testMCPServer(), home)
	if err != nil {
		t.Fatal(err)
	}
	if state != stateMissing {
		t.Fatalf("inspect missing = %q, want %q", state, stateMissing)
	}
}

func TestDroidMCPInspectSynced(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".factory", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{
  "mcpServers": {
    "linkedin": {
      "type": "stdio",
      "command": "uvx",
      "args": ["linkedin-scraper-mcp==4.13.2"],
      "env": {"UV_HTTP_TIMEOUT": "300"},
      "disabled": false,
      "local": "kept"
    }
  }
}`)
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	state, err := inspectMCPServer(agentDroid, testMCPServer(), home)
	if err != nil {
		t.Fatal(err)
	}
	if state != stateSynced {
		t.Fatalf("inspect synced = %q, want %q", state, stateSynced)
	}
}

func TestDroidMCPReadAllowsMissingDisabled(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".factory", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{
  "mcpServers": {
    "linkedin": {
      "type": "stdio",
      "command": "uvx",
      "args": ["linkedin-scraper-mcp==4.13.2"],
      "env": {"UV_HTTP_TIMEOUT": "300"}
    }
  }
}`)
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	server, err := readNativeMCPServer(agentDroid, "linkedin", home)
	if err != nil {
		t.Fatal(err)
	}
	if server.Name != "linkedin" || server.Command != "uvx" {
		t.Fatalf("unexpected server: %#v", server)
	}
}

func TestDroidMCPReadRejectsDisabledTrue(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".factory", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{
  "mcpServers": {
    "linkedin": {
      "type": "stdio",
      "command": "uvx",
      "disabled": true
    }
  }
}`)
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := readNativeMCPServer(agentDroid, "linkedin", home); err == nil {
		t.Fatal("readNativeMCPServer succeeded with disabled true, want error")
	}
}

func TestDroidMCPInspectDrifted(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".factory", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{
  "mcpServers": {
    "linkedin": {
      "type": "stdio",
      "command": "uvx",
      "args": ["wrong"],
      "env": {"UV_HTTP_TIMEOUT": "300"},
      "disabled": false
    }
  }
}`)
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	state, err := inspectMCPServer(agentDroid, testMCPServer(), home)
	if err != nil {
		t.Fatal(err)
	}
	if state != stateDrifted {
		t.Fatalf("inspect drifted = %q, want %q", state, stateDrifted)
	}
}

func TestDroidMCPPatchPreservesUnrelatedServers(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".factory", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{
  "otherTopLevel": true,
  "mcpServers": {
    "other": {
      "type": "stdio",
      "command": "node",
      "args": ["server.js"],
      "disabled": false
    },
    "linkedin": {
      "type": "stdio",
      "command": "old",
      "args": ["old"],
      "env": {"LOCAL_ONLY": "keep"},
      "disabled": true,
      "local": "keep"
    }
  }
}`)
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := patchMCPServer(agentDroid, testMCPServer(), home); err != nil {
		t.Fatal(err)
	}

	out, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["otherTopLevel"] != true {
		t.Fatalf("top-level field was not preserved: %#v", raw)
	}
	servers, ok := asMap(raw["mcpServers"])
	if !ok {
		t.Fatalf("mcpServers missing: %#v", raw)
	}
	other, ok := asMap(servers["other"])
	if !ok || other["command"] != "node" {
		t.Fatalf("unrelated server was not preserved: %#v", servers["other"])
	}
	linkedin, ok := asMap(servers["linkedin"])
	if !ok {
		t.Fatalf("managed server missing: %#v", servers)
	}
	target, _ := mcpTargetForHarness(agentDroid)
	if !matchManagedMCPMap(linkedin, testMCPServer(), target.defaults) {
		t.Fatalf("managed server not patched: %#v", linkedin)
	}
	if linkedin["local"] != "keep" {
		t.Fatalf("unrelated managed-entry field was not preserved: %#v", linkedin)
	}
	env, ok := asMap(linkedin["env"])
	if !ok || env["LOCAL_ONLY"] != "keep" || env["UV_HTTP_TIMEOUT"] != "300" {
		t.Fatalf("env fields not merged: %#v", linkedin["env"])
	}
}

func TestClaudeMCPPatchCreatesMissingConfig(t *testing.T) {
	home := t.TempDir()
	if err := patchMCPServer(agentClaudeCode, testMCPServer(), home); err != nil {
		t.Fatal(err)
	}
	state, err := inspectMCPServer(agentClaudeCode, testMCPServer(), home)
	if err != nil {
		t.Fatal(err)
	}
	if state != stateSynced {
		t.Fatalf("inspect after create = %q, want %q", state, stateSynced)
	}
	out, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	servers, _ := asMap(raw["mcpServers"])
	linkedin, _ := asMap(servers["linkedin"])
	if linkedin["type"] != "stdio" {
		t.Fatalf("claude config should get stdio type default: %#v", linkedin)
	}
	if _, ok := linkedin["disabled"]; ok {
		t.Fatalf("claude config should not get droid disabled default: %#v", linkedin)
	}
}

func TestCodexMCPPatchDeduplicatesManagedSections(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`[mcp_servers.linkedin]
command = "old"
args = ["old"]

[mcp_servers.other]
command = "node"
args = ["server.js"]

[mcp_servers.linkedin]
command = "uvx"
args = ["wrong"]
env = { UV_HTTP_TIMEOUT = "30" }

[mcp_servers.linkedin.env]
UV_HTTP_TIMEOUT = "300"

[profiles.default]
model = "gpt-5"
`)
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := patchMCPServer(agentCodex, testMCPServer(), home); err != nil {
		t.Fatal(err)
	}

	out, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(out)
	if count := strings.Count(content, "[mcp_servers.linkedin]"); count != 1 {
		t.Fatalf("linkedin section count = %d, want 1:\n%s", count, content)
	}
	if strings.Contains(content, `command = "old"`) || strings.Contains(content, `args = ["wrong"]`) {
		t.Fatalf("stale linkedin section was not removed:\n%s", content)
	}
	if strings.Contains(content, "[mcp_servers.linkedin.env]") {
		t.Fatalf("stale linkedin env subsection was not removed:\n%s", content)
	}
	if !strings.Contains(content, "[mcp_servers.other]") || !strings.Contains(content, "[profiles.default]") {
		t.Fatalf("unrelated sections were not preserved:\n%s", content)
	}
	state, err := inspectMCPServer(agentCodex, testMCPServer(), home)
	if err != nil {
		t.Fatal(err)
	}
	if state != stateSynced {
		t.Fatalf("inspect after dedupe = %q, want %q", state, stateSynced)
	}
}

func TestClaudeMCPReadProjectScopedConfig(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	otherProjectDir := filepath.Join(t.TempDir(), "other")
	if err := os.MkdirAll(otherProjectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(projectDir)
	configPath := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(`{
  "mcpServers": {
    "imported": {
      "type": "stdio",
      "command": "wrong-user-command",
      "args": ["wrong-user.js"]
    }
  },
  "projects": {
    %q: {
      "mcpServers": {
        "imported": {
          "type": "stdio",
          "command": %q,
          "args": ["server.js"]
        }
      }
    },
    %q: {
      "mcpServers": {
        "imported": {
          "type": "stdio",
          "command": "wrong-project-command",
          "args": ["wrong-project.js"]
        }
      }
    }
  }
}`, projectDir, mcpTestNodeCommand, otherProjectDir)), 0o644); err != nil {
		t.Fatal(err)
	}
	server, err := readNativeMCPServer(agentClaudeCode, "imported", home)
	if err != nil {
		t.Fatal(err)
	}
	if server.Name != "imported" || server.Command != mcpTestNodeCommand || !stringSlicesEqual(server.Args, []string{"server.js"}) {
		t.Fatalf("unexpected project-scoped server: %#v", server)
	}
}

func TestPathInProjectResolvesSymlinks(t *testing.T) {
	root := t.TempDir()
	realProject := filepath.Join(root, "real", "repo")
	if err := os.MkdirAll(filepath.Join(realProject, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	linkProject := filepath.Join(root, "link")
	if err := os.Symlink(realProject, linkProject); err != nil {
		t.Fatal(err)
	}
	if !pathInProject(filepath.Join(linkProject, "subdir"), realProject) {
		t.Fatal("symlinked cwd should match real project path")
	}
}

func TestClaudeMCPReadLocalConfigBeforeMCPJSONAndUserConfig(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(t.TempDir(), "repo")
	subdir := filepath.Join(projectDir, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(subdir)
	if err := os.WriteFile(filepath.Join(projectDir, ".mcp.json"), []byte(`{
  "mcpServers": {
    "imported": {
      "type": "stdio",
      "command": "wrong-project-command",
      "args": ["wrong-project.js"]
    }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(`{
  "mcpServers": {
    "imported": {
      "type": "stdio",
      "command": "wrong-user-command",
      "args": ["wrong-user.js"]
    }
  },
  "projects": {
    %q: {
      "mcpServers": {
        "imported": {
          "type": "stdio",
          "command": %q,
          "args": ["local.js"]
        }
      }
    }
  }
}`, projectDir, mcpTestNodeCommand)), 0o644); err != nil {
		t.Fatal(err)
	}
	server, err := readNativeMCPServer(agentClaudeCode, "imported", home)
	if err != nil {
		t.Fatal(err)
	}
	if server.Name != "imported" || server.Command != mcpTestNodeCommand || !stringSlicesEqual(server.Args, []string{"local.js"}) {
		t.Fatalf("unexpected local-scoped server: %#v", server)
	}
}

func TestClaudeMCPReadMCPJSONWithoutClaudeJSON(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(projectDir)
	if err := os.WriteFile(filepath.Join(projectDir, ".mcp.json"), []byte(fmt.Sprintf(`{
  "mcpServers": {
    "imported": {
      "type": "stdio",
      "command": %q,
      "args": ["project.js"]
    }
  }
}`, mcpTestNodeCommand)), 0o644); err != nil {
		t.Fatal(err)
	}
	server, err := readNativeMCPServer(agentClaudeCode, "imported", home)
	if err != nil {
		t.Fatal(err)
	}
	if server.Name != "imported" || server.Command != mcpTestNodeCommand || !stringSlicesEqual(server.Args, []string{"project.js"}) {
		t.Fatalf("unexpected .mcp.json server: %#v", server)
	}
}

func TestAmpMCPPatchCreatesSettingsConfig(t *testing.T) {
	home := t.TempDir()
	if err := patchMCPServer(agentAmp, testMCPServer(), home); err != nil {
		t.Fatal(err)
	}
	state, err := inspectMCPServer(agentAmp, testMCPServer(), home)
	if err != nil {
		t.Fatal(err)
	}
	if state != stateSynced {
		t.Fatalf("inspect after create = %q, want %q", state, stateSynced)
	}
	out, err := os.ReadFile(filepath.Join(home, ".config", "amp", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	servers, _ := asMap(raw["amp.mcpServers"])
	linkedin, _ := asMap(servers["linkedin"])
	if linkedin["command"] != "uvx" {
		t.Fatalf("amp MCP command not patched: %#v", linkedin)
	}
	if _, ok := raw["mcpServers"]; ok {
		t.Fatalf("amp config should use amp.mcpServers, got top-level mcpServers: %#v", raw)
	}
}

func TestAmpMCPPatchUsesExistingSettingsJSONC(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".config", "amp", "settings.jsonc")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{
  // Amp accepts JSONC settings.
  "amp.skills.path": "~/.agents/skills",
  "amp.mcpServers": {
    "keep": {"command": "node", "args": [],},
  },
}`)
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if ok, err := ampHasSkillsPath(home, filepath.Join(home, ".agents", "skills")); err != nil || !ok {
		t.Fatalf("ampHasSkillsPath = %v, %v; want true, nil", ok, err)
	}
	if err := patchMCPServer(agentAmp, testMCPServer(), home); err != nil {
		t.Fatal(err)
	}
	state, err := inspectMCPServer(agentAmp, testMCPServer(), home)
	if err != nil {
		t.Fatal(err)
	}
	if state != stateSynced {
		t.Fatalf("inspect after jsonc patch = %q, want %q", state, stateSynced)
	}
	if hasFile(filepath.Join(home, ".config", "amp", "settings.json")) {
		t.Fatal("patch created settings.json even though settings.jsonc existed")
	}
	out, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	servers, _ := asMap(raw["amp.mcpServers"])
	if _, ok := servers["keep"]; !ok {
		t.Fatalf("existing jsonc MCP was not preserved: %#v", servers)
	}
	if _, ok := servers["linkedin"]; !ok {
		t.Fatalf("managed jsonc MCP was not added: %#v", servers)
	}
}

func TestAmpConfigPreservesExistingSkillPaths(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".config", "amp", "settings.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"amp.skills.path":"~/team-skills"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	patched, err := patchAmpConfig(home, filepath.Join(home, ".agents"))
	if err != nil {
		t.Fatal(err)
	}
	if !patched {
		t.Fatal("patchAmpConfig did not patch missing tackroom path")
	}
	out, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["amp.skills.path"] != "~/team-skills:~/.agents/skills" {
		t.Fatalf("amp.skills.path = %#v, want existing path preserved", raw["amp.skills.path"])
	}
	if ok, err := ampHasSkillsPath(home, filepath.Join(home, ".agents", "skills")); err != nil || !ok {
		t.Fatalf("ampHasSkillsPath = %v, %v; want true, nil", ok, err)
	}
}

func TestAmpConfigAcceptsExistingColonSeparatedSkillPath(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".config", "amp", "settings.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	const original = `{"amp.skills.path":"~/team-skills:~/.agents/skills"}`
	if err := os.WriteFile(configPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	patched, err := patchAmpConfig(home, filepath.Join(home, ".agents"))
	if err != nil {
		t.Fatal(err)
	}
	if patched {
		t.Fatal("patchAmpConfig patched an already-valid colon-separated path")
	}
	out, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != original {
		t.Fatalf("config changed unexpectedly: %s", string(out))
	}
}

func TestAmpSettingsPathPrefersExistingWorkspaceSettings(t *testing.T) {
	repoRoot := t.TempDir()
	home := t.TempDir()
	workspaceSettings := filepath.Join(repoRoot, ".amp", "settings.jsonc")
	if err := os.MkdirAll(filepath.Dir(workspaceSettings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspaceSettings, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	userSettings := filepath.Join(home, ".config", "amp", "settings.json")
	if err := os.MkdirAll(filepath.Dir(userSettings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userSettings, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := ampSettingsPathForRoots(repoRoot, home); got != workspaceSettings {
		t.Fatalf("ampSettingsPathForRoots = %q, want workspace settings %q", got, workspaceSettings)
	}
}

func TestUnsupportedMCPAgentErrors(t *testing.T) {
	home := t.TempDir()
	if _, err := inspectMCPServer("unsupported-agent", testMCPServer(), home); err == nil {
		t.Fatal("inspectMCPServer(unsupported-agent) succeeded, want error")
	}
	if err := patchMCPServer("unsupported-agent", testMCPServer(), home); err == nil {
		t.Fatal("patchMCPServer(unsupported-agent) succeeded, want error")
	}
}

func TestDesiredMCPServersEmptyAgentsOnlyTargetsSupportedAgents(t *testing.T) {
	cfg := config{
		MCPServers: []mcpServerConfig{
			{
				Name:    "default",
				Enabled: true,
				Command: "uvx",
			},
		},
	}

	if got := desiredMCPServersForAgent(cfg, agentClaudeCode); len(got) != 1 || got[0].Name != "default" {
		t.Fatalf("desiredMCPServersForAgent(%q) = %#v, want default server", agentClaudeCode, got)
	}
	if got := desiredMCPServersForAgent(cfg, "unsupported-agent"); len(got) != 0 {
		t.Fatalf("desiredMCPServersForAgent(unsupported-agent) = %#v, want no servers", got)
	}
}

func TestDroidMCPPatchCreatesMissingConfig(t *testing.T) {
	home := t.TempDir()
	if err := patchMCPServer(agentDroid, testMCPServer(), home); err != nil {
		t.Fatal(err)
	}
	state, err := inspectMCPServer(agentDroid, testMCPServer(), home)
	if err != nil {
		t.Fatal(err)
	}
	if state != stateSynced {
		t.Fatalf("inspect after create = %q, want %q", state, stateSynced)
	}
}

func TestTOMLSectionHelpersHandleCRLF(t *testing.T) {
	content := "codex_hooks = true\r\n[mcp_servers.linkedin]\r\ncommand = \"old\"\r\n\r\n[profiles.default]\r\nmodel = \"gpt-5\"\r\n"
	section := renderCodexMCPSection(testMCPServer())
	updated := upsertTOMLSection(content, "[mcp_servers.linkedin]", section)

	if strings.Contains(updated, "command = \"old\"") {
		t.Fatalf("old CRLF section was not replaced:\n%s", updated)
	}
	if count := strings.Count(updated, "[mcp_servers.linkedin]"); count != 1 {
		t.Fatalf("mcp section count = %d, want 1:\n%s", count, updated)
	}
	if !strings.Contains(updated, "[profiles.default]\r\nmodel = \"gpt-5\"") {
		t.Fatalf("following CRLF section was not preserved:\n%s", updated)
	}
}
