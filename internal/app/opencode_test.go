package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yourconscience/tackroom/internal/agentrole"
	"gopkg.in/yaml.v3"
)

func TestOpenCodeHarnessCapabilitiesAndDefaults(t *testing.T) {
	h := harnessFor(agentOpenCode)
	if h == nil {
		t.Fatal("OpenCode harness is not registered")
	}
	if h.Skills != skillsSymlink {
		t.Fatalf("OpenCode skills capability = %v, want symlink", h.Skills)
	}
	if h.SkillsNativeRoot == nil {
		t.Fatal("OpenCode must declare a native skill root predicate")
	}
	if h.MCP == nil {
		t.Fatal("OpenCode does not expose MCP support")
	}
	if h.roles == nil || h.roles.Extension() != ".md" {
		t.Fatalf("OpenCode roles capability = %#v, want Markdown roles", h.roles)
	}
	if h.Hooks != nil {
		t.Fatal("OpenCode must not declare hook support (JS plugin surface only)")
	}

	var found *agentConfig
	for _, agent := range defaultAgentConfigs() {
		if agent.Name == agentOpenCode {
			a := agent
			found = &a
		}
	}
	if found == nil {
		t.Fatal("opencode missing from defaultAgentConfigs")
	}
	if found.SkillRoot != "~/.config/opencode/skills" || found.AgentRoot != "~/.config/opencode/agents" || found.Detect != "opencode" {
		t.Fatalf("opencode default config = %#v", *found)
	}
}

func TestOpenCodeDetectionFromPATH(t *testing.T) {
	fakePath(t, "opencode")
	detected, err := detectDefaultAgents("")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, agent := range detected {
		names = append(names, agent.Name)
	}
	if strings.Join(names, ",") != agentOpenCode {
		t.Fatalf("detected agents = %v, want opencode", names)
	}
}

func TestOpenCodeRoleRenderDefaultMode(t *testing.T) {
	role := agentrole.Role{
		Name:         "researcher",
		Description:  "Find reliable evidence",
		Model:        "opus",
		Effort:       "high",
		Instructions: "Compare the sources.",
		Source:       "agents/researcher.md",
	}
	content := roleRenderer(agentOpenCode).Render(role)

	if strings.Contains(content, "name:") {
		t.Fatalf("opencode role should not emit a name field (filename is the name):\n%s", content)
	}
	if !strings.Contains(content, "description: \"Find reliable evidence\"") {
		t.Fatalf("opencode role missing description:\n%s", content)
	}
	if !strings.Contains(content, "mode: subagent") {
		t.Fatalf("opencode role missing default mode:\n%s", content)
	}
	if strings.Contains(content, "model:") || strings.Contains(content, "temperature:") {
		t.Fatalf("opencode role should omit model/temperature without an override:\n%s", content)
	}
	if !strings.Contains(content, agentrole.GeneratedMarker) {
		t.Fatalf("opencode role missing managed marker:\n%s", content)
	}
	if !strings.Contains(content, "Compare the sources.") {
		t.Fatalf("opencode role dropped instructions:\n%s", content)
	}
}

func TestOpenCodeRoleRenderHonorsOverride(t *testing.T) {
	role := agentrole.Role{
		Name:         "reviewer",
		Description:  "Reviews code",
		Instructions: "Review carefully.",
		Opencode:     agentrole.OpenCodeOptions{Model: "anthropic/claude-sonnet-4", Temperature: "0.1", Mode: "primary"},
	}
	content := roleRenderer(agentOpenCode).Render(role)
	if !strings.Contains(content, "mode: primary") {
		t.Fatalf("opencode override mode not applied:\n%s", content)
	}
	if !strings.Contains(content, "model: \"anthropic/claude-sonnet-4\"") {
		t.Fatalf("opencode override model not applied:\n%s", content)
	}
	if !strings.Contains(content, "temperature: 0.1") {
		t.Fatalf("opencode override temperature not applied:\n%s", content)
	}
}

func TestOpenCodeRoleRenderFromCanonicalMarkdown(t *testing.T) {
	repoRoot := t.TempDir()
	writeSyncTestFile(t, filepath.Join(repoRoot, "agents", "planner.md"), []byte(`---
name: planner
description: Plans work
opencode:
  mode: all
  temperature: 0.2
---

Plan the work.
`))
	agent := agentConfig{Name: agentOpenCode, AgentRoot: filepath.Join(t.TempDir(), "agents")}
	expected, err := expectedAgentRoles(repoRoot, agent)
	if err != nil {
		t.Fatal(err)
	}
	rendered, ok := expected["planner"]
	if !ok {
		t.Fatalf("planner role not rendered: %#v", expected)
	}
	if !strings.Contains(rendered.Content, "mode: all") || !strings.Contains(rendered.Content, "temperature: 0.2") {
		t.Fatalf("canonical opencode override not honored:\n%s", rendered.Content)
	}
}

func TestOpenCodeMCPFreshFilePatchAndInspect(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	server := testMCPServer()
	server.Agents = []string{agentOpenCode}

	if err := patchMCPServer(agentOpenCode, server, home); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, ".config", "opencode", "opencode.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("opencode.json not written: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	mcp, _ := raw["mcp"].(map[string]interface{})
	entry, _ := mcp["linkedin"].(map[string]interface{})
	if entry["type"] != "local" || entry["enabled"] != true {
		t.Fatalf("opencode MCP entry shape = %#v", entry)
	}
	command, _ := toStringSlice(entry["command"])
	if !stringSlicesEqual(command, []string{"uvx", "linkedin-scraper-mcp==4.13.2"}) {
		t.Fatalf("opencode command array = %#v, want folded command+args", entry["command"])
	}
	envMap, _ := asMap(entry["environment"])
	if envMap["UV_HTTP_TIMEOUT"] != "300" {
		t.Fatalf("opencode environment = %#v", entry["environment"])
	}
	if _, ok := entry["env"]; ok {
		t.Fatalf("opencode must use environment, not env: %#v", entry)
	}

	state, err := inspectMCPServer(agentOpenCode, server, home)
	if err != nil {
		t.Fatal(err)
	}
	if state != stateSynced {
		t.Fatalf("opencode MCP state after patch = %q, want synced", state)
	}
}

func TestOpenCodeMCPPreservesUnmanagedKeysAndServers(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	configPath := filepath.Join(home, ".config", "opencode", "opencode.json")
	writeSyncTestFile(t, configPath, []byte(`{
  "$schema": "https://opencode.ai/config.json",
  "theme": "opencode",
  "mcp": {
    "existing-remote": {"type": "remote", "url": "https://example.test/mcp", "enabled": true},
    "existing-local": {"type": "local", "command": ["node", "server.js"], "enabled": true}
  }
}`))

	server := testMCPServer()
	server.Agents = []string{agentOpenCode}
	if err := patchMCPServer(agentOpenCode, server, home); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["theme"] != "opencode" || raw["$schema"] != "https://opencode.ai/config.json" {
		t.Fatalf("unmanaged top-level keys not preserved: %#v", raw)
	}
	mcp, _ := raw["mcp"].(map[string]interface{})
	if _, ok := mcp["existing-remote"]; !ok {
		t.Fatalf("unmanaged remote server dropped: %#v", mcp)
	}
	if _, ok := mcp["existing-local"]; !ok {
		t.Fatalf("unmanaged local server dropped: %#v", mcp)
	}
	if _, ok := mcp["linkedin"]; !ok {
		t.Fatalf("managed server not added: %#v", mcp)
	}
}

func TestOpenCodeMCPImportReverseTranslation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	configPath := filepath.Join(home, ".config", "opencode", "opencode.json")
	writeSyncTestFile(t, configPath, []byte(`{
  "mcp": {
    "search": {"type": "local", "command": ["uvx", "search-mcp", "--fast"], "environment": {"TOKEN": "${TOKEN}"}, "enabled": true},
    "remote-only": {"type": "remote", "url": "https://example.test/mcp"}
  }
}`))

	agent := agentConfig{Name: agentOpenCode, Enabled: true}
	candidates, err := scanNativeMCP(agent, config{Version: 1}, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("import candidates = %#v, want only the local server", candidates)
	}
	got := candidates[0].Server
	if got.Name != "search" || got.Command != "uvx" || !stringSlicesEqual(got.Args, []string{"search-mcp", "--fast"}) {
		t.Fatalf("reverse translation = %#v", got)
	}
	if got.Env["TOKEN"] != "${TOKEN}" {
		t.Fatalf("environment not reverse-translated to env: %#v", got.Env)
	}
}

func TestOpenCodeConfigPathHonorsXDG(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	home := t.TempDir()
	if got, want := openCodeConfigPath(home), filepath.Join(xdg, "opencode", "opencode.json"); got != want {
		t.Fatalf("opencode config path = %q, want %q", got, want)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if got, want := openCodeConfigPath(home), filepath.Join(home, ".config", "opencode", "opencode.json"); got != want {
		t.Fatalf("opencode config path without XDG = %q, want %q", got, want)
	}
}

func TestOpenCodeSkillsNativeReadDoesNotMirror(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	repoRoot := filepath.Join(home, ".agents")
	writeSyncTestFile(t, filepath.Join(repoRoot, "skills", "grilling", "SKILL.md"), []byte("---\nname: grilling\n---\n"))

	agent := agentConfig{Name: agentOpenCode, Enabled: true, SkillRoot: filepath.Join(home, ".config", "opencode", "skills")}
	expected := map[string]string{"grilling": filepath.Join(repoRoot, "skills", "grilling")}
	cfg := config{Version: 1, Agents: []agentConfig{agent}}

	report, err := inspectAgent(agent, expected, repoRoot, filepath.Join(repoRoot, "skills"), cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Adds) != 0 || len(report.Missing) != 0 {
		t.Fatalf("native read must have no skill adds: %#v", report)
	}
	if !stringInSlice("grilling", report.Managed) {
		t.Fatalf("native read must report skill as managed: %#v", report.Managed)
	}

	if err := applyAgentSync([]agentReport{report}, cfg, repoRoot, home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(agent.SkillRoot); !os.IsNotExist(err) {
		t.Fatalf("native read must not create a skill mirror at %s (err=%v)", agent.SkillRoot, err)
	}
}

func TestOpenCodeSkillsMirrorWhenConfigRootDiffers(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	repoRoot := filepath.Join(home, "custom-agents")
	writeSyncTestFile(t, filepath.Join(repoRoot, "skills", "grilling", "SKILL.md"), []byte("---\nname: grilling\n---\n"))

	agent := agentConfig{Name: agentOpenCode, Enabled: true, SkillRoot: filepath.Join(home, ".config", "opencode", "skills")}
	expected := map[string]string{"grilling": filepath.Join(repoRoot, "skills", "grilling")}
	cfg := config{Version: 1, Agents: []agentConfig{agent}}

	report, err := inspectAgent(agent, expected, repoRoot, filepath.Join(repoRoot, "skills"), cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	if !stringInSlice("grilling", report.Adds) {
		t.Fatalf("custom root must mirror skills: %#v", report.Adds)
	}
	if err := applyAgentSync([]agentReport{report}, cfg, repoRoot, home); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(agent.SkillRoot, "grilling")
	if !sameResolvedPath(linkPath, filepath.Join(repoRoot, "skills", "grilling")) {
		rawTarget, _ := os.Readlink(linkPath)
		t.Fatalf("custom-root skill link %s -> %q, want %s", linkPath, rawTarget, filepath.Join(repoRoot, "skills", "grilling"))
	}
}

func TestOpenCodeDoctorWarnsOnDuplicateSkills(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	dir := fakePath(t, "opencode")
	_ = dir
	cfg := config{Version: 1, Agents: []agentConfig{{Name: agentOpenCode, Enabled: true, Detect: "opencode", SkillRoot: filepath.Join(home, ".config", "opencode", "skills")}}}

	// no duplicates yet
	res := checkOpenCodeDuplicateSkills("", home, cfg)
	if res.status != checkStatusPass {
		t.Fatalf("expected pass with no duplicates, got %#v", res)
	}

	writeSyncTestFile(t, filepath.Join(home, ".agents", "skills", "grilling", "SKILL.md"), []byte("---\nname: grilling\n---\n"))
	writeSyncTestFile(t, filepath.Join(home, ".config", "opencode", "skills", "grilling", "SKILL.md"), []byte("---\nname: grilling\n---\n"))

	res = checkOpenCodeDuplicateSkills("", home, cfg)
	if res.status != checkStatusWarn || !strings.Contains(res.detail, "grilling") {
		t.Fatalf("expected duplicate warning, got %#v", res)
	}
}

func TestOpenCodeSetupImportScansAgentsAndMCP(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	root := t.TempDir()
	writeSyncTestFile(t, filepath.Join(home, ".config", "opencode", "agents", "helper.md"), []byte(`---
description: Helps out
mode: subagent
---

Be helpful.
`))
	writeSyncTestFile(t, filepath.Join(home, ".config", "opencode", "opencode.json"), []byte(`{
  "mcp": {
    "search": {"type": "local", "command": ["uvx", "search-mcp"], "enabled": true}
  }
}`))

	detected := []agentConfig{{Name: agentOpenCode, Enabled: true, SkillRoot: filepath.Join(home, ".config", "opencode", "skills"), AgentRoot: filepath.Join(home, ".config", "opencode", "agents")}}
	skills, roles, mcps, err := scanNativeImports(config{Version: 1, Agents: detected}, detected, root, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Fatalf("unexpected skill candidates: %#v", skills)
	}
	if len(roles) != 1 || roles[0].TargetName != "helper" {
		t.Fatalf("opencode role import candidates = %#v", roles)
	}
	if len(mcps) != 1 || mcps[0].Name != "search" {
		t.Fatalf("opencode MCP import candidates = %#v", mcps)
	}
	data, err := roles[0].Convert()
	if err != nil {
		t.Fatal(err)
	}
	var fm struct {
		Description string `yaml:"description"`
	}
	parts := strings.SplitN(string(data), "---\n", 3)
	if len(parts) == 3 {
		_ = yaml.Unmarshal([]byte(parts[1]), &fm)
	}
	if !strings.Contains(string(data), "Be helpful.") {
		t.Fatalf("converted opencode role dropped body:\n%s", data)
	}
}

func TestOpenCodeRoleRenderUsesConfiguredModel(t *testing.T) {
	role := agentrole.Role{
		Name:         "builder",
		Description:  "Builds features",
		Model:        "openai/gpt-5.6",
		Instructions: "Implement.",
	}
	content := roleRenderer(agentOpenCode).Render(role)
	if !strings.Contains(content, "model: \"openai/gpt-5.6\"") {
		t.Fatalf("configured role_model not applied to OpenCode role:\n%s", content)
	}
}
