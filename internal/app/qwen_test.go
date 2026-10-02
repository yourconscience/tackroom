package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yourconscience/tackroom/internal/agentrole"
	"gopkg.in/yaml.v3"
)

func TestQwenHarnessCapabilitiesAndDefaultDetection(t *testing.T) {
	h := harnessFor(agentQwenCode)
	if h == nil {
		t.Fatal("Qwen Code harness is not registered")
	}
	if h.Skills != skillsConfigDriven || h.Setup == nil || h.InspectSkills == nil {
		t.Fatalf("Qwen skills capability = %#v, want config-driven setup and inspection", h)
	}
	if h.MCP == nil || h.Hooks == nil || h.roles == nil || h.RootInstructions == nil {
		t.Fatalf("Qwen capabilities incomplete: %#v", h)
	}

	fakePath(t, "qwen")
	detected, err := detectDefaultAgents("")
	if err != nil {
		t.Fatal(err)
	}
	if len(detected) != 1 || detected[0].Name != agentQwenCode {
		t.Fatalf("detected agents = %#v, want only %s", detected, agentQwenCode)
	}
	if detected[0].SkillRoot != "~/.qwen/skills" || detected[0].AgentRoot != "~/.qwen/agents" {
		t.Fatalf("Qwen native roots = %#v", detected[0])
	}
}

func TestPatchQwenConfigPreservesSettingsAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	repoRoot := filepath.Join(home, ".agents")
	configPath := qwenSettingsPath(home)
	writeSyncTestFile(t, configPath, []byte(`{
  "model": {"name": "keep"},
  "skills": {"disabled": ["legacy"], "directories": ["/shared/skills"]}
}`))

	changed, err := patchQwenConfig(home, repoRoot, config{})
	if err != nil || !changed {
		t.Fatalf("first patch = %v, %v; want true, nil", changed, err)
	}
	changed, err = patchQwenConfig(home, repoRoot, config{})
	if err != nil || changed {
		t.Fatalf("second patch = %v, %v; want false, nil", changed, err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["model"].(map[string]interface{})["name"] != "keep" {
		t.Fatalf("unrelated setting changed: %#v", raw)
	}
	skills := raw["skills"].(map[string]interface{})
	if !reflect.DeepEqual(skills["disabled"], []interface{}{"legacy"}) {
		t.Fatalf("skills.disabled changed: %#v", skills)
	}
	wantDirectories := []interface{}{`/shared/skills`, `~/.agents/skills`}
	if !reflect.DeepEqual(skills["directories"], wantDirectories) {
		t.Fatalf("skills.directories = %#v, want %#v", skills["directories"], wantDirectories)
	}
	if ok, err := qwenHasSkillsDirectory(home, filepath.Join(repoRoot, "skills")); err != nil || !ok {
		t.Fatalf("qwenHasSkillsDirectory = %v, %v", ok, err)
	}
}

func TestPatchQwenConfigRejectsMalformedSkillsWithoutOverwrite(t *testing.T) {
	home := t.TempDir()
	path := qwenSettingsPath(home)
	original := []byte(`{"skills":"keep"}`)
	writeSyncTestFile(t, path, original)

	if _, err := patchQwenConfig(home, filepath.Join(home, ".agents"), config{}); err == nil || !strings.Contains(err.Error(), "not an object") {
		t.Fatalf("patch error = %v, want malformed skills error", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("malformed settings overwritten: %s", after)
	}
}

func TestQwenMCPPatchSharesSettingsFile(t *testing.T) {
	home := t.TempDir()
	writeSyncTestFile(t, qwenSettingsPath(home), []byte(`{"skills":{"directories":["~/.agents/skills"]},"ui":{"theme":"keep"}}`))
	server := testMCPServer()
	server.Agents = []string{agentQwenCode}

	if err := patchMCPServer(agentQwenCode, server, home); err != nil {
		t.Fatal(err)
	}
	state, err := inspectMCPServer(agentQwenCode, server, home)
	if err != nil || state != stateSynced {
		t.Fatalf("Qwen MCP state = %q, %v", state, err)
	}
	data, err := os.ReadFile(qwenSettingsPath(home))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["ui"].(map[string]interface{})["theme"] != "keep" || raw["skills"] == nil {
		t.Fatalf("Qwen MCP patch dropped unrelated settings: %#v", raw)
	}
}

func TestQwenHookPatchUsesMillisecondsAndPreservesSettings(t *testing.T) {
	home := t.TempDir()
	writeSyncTestFile(t, qwenSettingsPath(home), []byte(`{"ui":{"theme":"keep"},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo keep","timeout":10}]}]}}`))
	hook := testHook()
	hook.Agents = []string{agentQwenCode}

	if err := patchQwenHook(hook, home); err != nil {
		t.Fatal(err)
	}
	state, err := inspectQwenHook(hook, home)
	if err != nil || state != stateSynced {
		t.Fatalf("Qwen hook state = %q, %v", state, err)
	}
	data, err := os.ReadFile(qwenSettingsPath(home))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	groups := raw["hooks"].(map[string]interface{})["Stop"].([]interface{})
	var managed map[string]interface{}
	for _, groupRaw := range groups {
		group, ok := groupRaw.(map[string]interface{})
		if !ok {
			continue
		}
		for _, itemRaw := range group["hooks"].([]interface{}) {
			item := itemRaw.(map[string]interface{})
			if item["command"] == hook.Command {
				managed = item
			}
		}
	}
	if managed == nil || managed["timeout"] != float64(15000) || managed["type"] != "command" {
		t.Fatalf("managed Qwen hook = %#v", managed)
	}
	if raw["ui"].(map[string]interface{})["theme"] != "keep" {
		t.Fatalf("Qwen hook patch dropped settings: %#v", raw)
	}
}

func TestRenderQwenAgentRoleUsesNativeFrontmatter(t *testing.T) {
	role := agentrole.Role{
		Name:         "reviewer",
		Description:  "Review changes",
		Model:        "opus",
		Tools:        []string{"Read", "Glob", "Grep", "Bash", "Read"},
		Qwen:         agentrole.QwenOptions{ApprovalMode: "plan"},
		Instructions: "Review without editing.",
	}
	content := roleRenderer(agentQwenCode).Render(role)
	parts := strings.SplitN(content, "---\n", 3)
	if len(parts) != 3 {
		t.Fatalf("Qwen role lacks frontmatter:\n%s", content)
	}
	var frontmatter struct {
		Name         string   `yaml:"name"`
		Description  string   `yaml:"description"`
		Model        string   `yaml:"model"`
		ApprovalMode string   `yaml:"approvalMode"`
		Tools        []string `yaml:"tools"`
	}
	if err := yaml.Unmarshal([]byte(parts[1]), &frontmatter); err != nil {
		t.Fatal(err)
	}
	if frontmatter.Name != role.Name || frontmatter.Description != role.Description || frontmatter.Model != "inherit" || frontmatter.ApprovalMode != "plan" {
		t.Fatalf("Qwen frontmatter = %#v", frontmatter)
	}
	wantTools := []string{"read_file", "glob", "grep_search", "run_shell_command"}
	if !reflect.DeepEqual(frontmatter.Tools, wantTools) {
		t.Fatalf("Qwen tools = %#v, want %#v", frontmatter.Tools, wantTools)
	}
	if !strings.Contains(parts[2], agentrole.GeneratedMarker) || !strings.Contains(parts[2], role.Instructions) {
		t.Fatalf("Qwen role body = %q", parts[2])
	}
}

func TestQwenSyncEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fakePath(t, "qwen")
	repoRoot := filepath.Join(home, ".agents")
	writeSyncTestFile(t, filepath.Join(repoRoot, "tackroom.yaml"), []byte(`version: 1
agents:
  - name: qwen-code
    enabled: true
    skill_root: ~/.qwen/skills
    agent_root: ~/.qwen/agents
    detect: qwen
mcp_servers:
  - name: local
    enabled: true
    command: local-mcp
    agents: [qwen-code]
hooks:
  - name: memory-stop
    enabled: true
    event: Stop
    command: ~/.agents/memory/hooks/stop.sh
    timeout: 15
    agents: [qwen-code]
`))
	writeSyncTestFile(t, filepath.Join(repoRoot, "AGENTS.md"), []byte("# Shared instructions\n"))
	writeSyncTestFile(t, filepath.Join(repoRoot, "skills", "sample", "SKILL.md"), []byte("---\nname: sample\ndescription: sample skill\n---\n"))
	writeSyncTestFile(t, filepath.Join(repoRoot, "agents", "reviewer.md"), []byte("---\nname: reviewer\ndescription: Review code\n---\n\nReview carefully.\n"))
	writeSyncTestFile(t, qwenSettingsPath(home), []byte(`{"ui":{"theme":"keep"}}`))

	if err := runSync(runOptions{ConfigPath: filepath.Join(repoRoot, "tackroom.yaml"), Agents: agentQwenCode}); err != nil {
		t.Fatal(err)
	}
	if err := runStatus(runOptions{ConfigPath: filepath.Join(repoRoot, "tackroom.yaml"), Agents: agentQwenCode}); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(filepath.Join(home, ".qwen", "QWEN.md")); err != nil || target != filepath.Join(repoRoot, "AGENTS.md") {
		t.Fatalf("QWEN.md link = %q, %v", target, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".qwen", "agents", "reviewer.md")); err != nil {
		t.Fatalf("Qwen role missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".qwen", "skills", "sample")); !os.IsNotExist(err) {
		t.Fatalf("config-driven skills should not create a Qwen mirror: %v", err)
	}
}

func TestQwenRoleRenderUsesConfiguredModel(t *testing.T) {
	role := agentrole.Role{Name: "builder", Description: "Builds features", Model: "qwen3-coder-plus", Instructions: "Implement."}
	content := roleRenderer(agentQwenCode).Render(role)
	if !strings.Contains(content, "model: \"qwen3-coder-plus\"") {
		t.Fatalf("configured role_model not applied to Qwen role:\n%s", content)
	}

	alias := agentrole.Role{Name: "reviewer", Description: "Reviews code", Model: "opus", Instructions: "Review."}
	content = roleRenderer(agentQwenCode).Render(alias)
	if strings.Contains(content, "model: opus") {
		t.Fatalf("tier alias leaked into Qwen role:\n%s", content)
	}
}
