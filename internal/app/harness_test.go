package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yourconscience/tackroom/internal/agentrole"
	"gopkg.in/yaml.v3"
)

func TestPiAndOMPHarnessCapabilities(t *testing.T) {
	pi := harnessFor(agentPi)
	if pi == nil {
		t.Fatal("Pi harness is not registered")
	}
	if pi.Skills != skillsSymlink {
		t.Fatalf("Pi skills capability = %v, want symlink", pi.Skills)
	}
	if pi.MCP == nil {
		t.Fatal("Pi does not expose MCP adapter support")
	}
	if pi.roles == nil || pi.roles.Extension() != ".md" {
		t.Fatalf("Pi roles capability = %#v, want pi-subagents Markdown roles", pi.roles)
	}
	if pi.RootInstructions == nil {
		t.Fatal("Pi does not expose root-instruction support")
	}

	omp := harnessFor(agentOMP)
	if omp == nil {
		t.Fatal("OMP harness is not registered")
	}
	if omp.Skills != skillsSymlink {
		t.Fatalf("OMP skills capability = %v, want symlink", omp.Skills)
	}
	if omp.MCP == nil {
		t.Fatal("OMP does not expose MCP support")
	}
	if omp.roles == nil || omp.roles.Extension() != ".md" {
		t.Fatalf("OMP roles capability = %#v, want Markdown roles", omp.roles)
	}
}

func TestPublicTemplateStartsWithoutConfiguredAgents(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repoRoot, "tackroom.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents) != 0 {
		t.Fatalf("public template agents = %#v, want none before setup detection", cfg.Agents)
	}

	home := t.TempDir()
	target, err := mcpTargetForHarness(agentOMP)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := target.configPath(home), filepath.Join(home, ".omp", "agent", "mcp.json"); got != want {
		t.Fatalf("OMP MCP path = %q, want %q", got, want)
	}
}

func TestConfigAcceptsPiAndOMPMCP(t *testing.T) {
	home := t.TempDir()
	baseAgents := []agentConfig{
		{Name: agentPi, Enabled: true, SkillRoot: filepath.Join(home, ".pi", "agent", "skills")},
		{Name: agentOMP, Enabled: true, SkillRoot: filepath.Join(home, ".omp", "agent", "skills"), AgentRoot: filepath.Join(home, ".omp", "agent", "agents")},
	}

	piConfig := config{
		Version: 1,
		Agents:  append([]agentConfig(nil), baseAgents...),
		MCPServers: []mcpServerConfig{{
			Name: "search", Enabled: true, Command: "search-server", Agents: []string{agentPi},
		}},
	}
	if err := validateConfig(&piConfig, home, true); err != nil {
		t.Fatalf("Pi MCP config rejected: %v", err)
	}
	if !reflect.DeepEqual(piConfig.MCPServers[0].Agents, []string{agentPi}) {
		t.Fatalf("Pi MCP target changed: %#v", piConfig.MCPServers[0].Agents)
	}

	ompConfig := config{
		Version: 1,
		Agents:  append([]agentConfig(nil), baseAgents...),
		MCPServers: []mcpServerConfig{{
			Name: "search", Enabled: true, Command: "search-server", Agents: []string{agentOMP},
		}},
	}
	if err := validateConfig(&ompConfig, home, true); err != nil {
		t.Fatalf("OMP MCP config rejected: %v", err)
	}
}

func TestPiAndOMPMCPPatchesUseSeparateConfigs(t *testing.T) {
	home := t.TempDir()
	server := testMCPServer()
	server.Agents = []string{agentOMP}

	if err := patchMCPServer(agentOMP, server, home); err != nil {
		t.Fatal(err)
	}
	state, err := inspectMCPServer(agentOMP, server, home)
	if err != nil {
		t.Fatal(err)
	}
	if state != stateSynced {
		t.Fatalf("OMP MCP state after patch = %q, want %q", state, stateSynced)
	}
	if _, err := os.Stat(filepath.Join(home, ".omp", "agent", "mcp.json")); err != nil {
		t.Fatalf("OMP MCP config was not written to native path: %v", err)
	}
	server.Agents = []string{agentPi}
	if err := patchMCPServer(agentPi, server, home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", "mcp.json")); err != nil {
		t.Fatalf("Pi MCP adapter config was not written to native path: %v", err)
	}
}

func TestPiAndOMPRenderersProduceNativeRoles(t *testing.T) {
	role := agentrole.Role{
		Name:         "researcher",
		Description:  "Find reliable evidence",
		Model:        "opus",
		Effort:       "high",
		OMP:          agentrole.OMPOptions{Model: "gpt-5.6-luna-high"},
		Tools:        []string{"Read", "WebSearch", "Write", "NotebookEdit", "read"},
		Instructions: "Compare the sources.",
	}
	home := t.TempDir()

	piPath, piContent, ok := renderAgentRole(role, agentConfig{Name: agentPi, AgentRoot: filepath.Join(home, ".pi", "agent", "agents")})
	if !ok || piPath != filepath.Join(home, ".pi", "agent", "agents", "researcher.md") {
		t.Fatalf("Pi role path=%q ok=%v", piPath, ok)
	}
	if !strings.Contains(piContent, `thinking: "high"`) || strings.Contains(piContent, `model: "opus"`) {
		t.Fatalf("Pi role should map effort and omit legacy model tier:\n%s", piContent)
	}

	path, content, ok := renderAgentRole(role, agentConfig{Name: agentOMP, AgentRoot: filepath.Join(home, ".omp", "agent", "agents")})
	if !ok {
		t.Fatal("OMP role was not rendered")
	}
	if want := filepath.Join(home, ".omp", "agent", "agents", "researcher.md"); path != want {
		t.Fatalf("OMP role path = %q, want %q", path, want)
	}

	parts := strings.SplitN(content, "---\n", 3)
	if len(parts) != 3 {
		t.Fatalf("OMP role lacks YAML frontmatter:\n%s", content)
	}
	var frontmatter struct {
		Name        string   `yaml:"name"`
		Description string   `yaml:"description"`
		Model       []string `yaml:"model"`
		Tools       []string `yaml:"tools"`
	}
	if err := yaml.Unmarshal([]byte(parts[1]), &frontmatter); err != nil {
		t.Fatalf("parse OMP role frontmatter: %v", err)
	}
	if frontmatter.Name != role.Name || frontmatter.Description != role.Description {
		t.Fatalf("OMP identity frontmatter = %#v", frontmatter)
	}
	if !reflect.DeepEqual(frontmatter.Model, []string{"gpt-5.6-luna-high"}) {
		t.Fatalf("OMP model = %#v", frontmatter.Model)
	}
	if !reflect.DeepEqual(frontmatter.Tools, []string{"read", "web_search", "write"}) {
		t.Fatalf("OMP tools = %#v", frontmatter.Tools)
	}
	if !strings.Contains(parts[2], role.Instructions) {
		t.Fatalf("OMP role dropped instructions:\n%s", content)
	}
}

func TestPiDetectionRejectsOMPAlias(t *testing.T) {
	writeVersionCommand := func(name, version string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' '"+version+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}

	if !detectVanillaPi(writeVersionCommand("pi", "pi 0.60.0")) {
		t.Fatal("vanilla Pi executable was rejected")
	}
	if detectVanillaPi(writeVersionCommand("pi", "omp/0.4.5")) {
		t.Fatal("OMP's pi compatibility alias was accepted as vanilla Pi")
	}
}
