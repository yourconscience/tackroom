package app

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeExecutable(t *testing.T, dir string, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func fakePath(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		writeExecutable(t, dir, name)
	}
	t.Setenv("PATH", dir)
	return dir
}

func writeMinimalConfig(t *testing.T, path string, agentName string) {
	t.Helper()
	writeSyncTestFile(t, path, []byte(`version: 1
agents:
  - name: `+agentName+`
    enabled: true
    skill_root: ~/.`+agentName+`/skills
`))
}

func TestConfigResolutionPrecedenceAndIgnoresCWD(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	writeMinimalConfig(t, filepath.Join(cwd, "tackroom.yaml"), "cwd")
	writeMinimalConfig(t, filepath.Join(home, ".agents", "tackroom.yaml"), "home")

	repoRoot, _, cfg, _, err := loadContext(runOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if repoRoot != filepath.Join(home, ".agents") || cfg.Agents[0].Name != "home" {
		t.Fatalf("default resolution = root %q agent %q, want ~/.agents/home", repoRoot, cfg.Agents[0].Name)
	}

	tackroomHome := filepath.Join(home, "custom")
	writeMinimalConfig(t, filepath.Join(tackroomHome, "tackroom.yaml"), "tackroom-home")
	t.Setenv("TACKROOM_HOME", tackroomHome)
	repoRoot, _, cfg, _, err = loadContext(runOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if repoRoot != tackroomHome || cfg.Agents[0].Name != "tackroom-home" {
		t.Fatalf("TACKROOM_HOME resolution = root %q agent %q", repoRoot, cfg.Agents[0].Name)
	}

	explicit := filepath.Join(home, "explicit", "tackroom.yaml")
	writeMinimalConfig(t, explicit, "explicit")
	repoRoot, _, cfg, _, err = loadContext(runOptions{ConfigPath: explicit})
	if err != nil {
		t.Fatal(err)
	}
	if repoRoot != filepath.Dir(explicit) || cfg.Agents[0].Name != "explicit" {
		t.Fatalf("explicit resolution = root %q agent %q", repoRoot, cfg.Agents[0].Name)
	}
}

func TestDetectDefaultAgentsFromPATH(t *testing.T) {
	fakePath(t, "codex", "claude")
	detected, err := detectDefaultAgents("")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, agent := range detected {
		names = append(names, agent.Name)
	}
	if strings.Join(names, ",") != "claude,codex" {
		t.Fatalf("detected agents = %v, want claude,codex", names)
	}
}

func TestNativeImportSkillCopyRoleConversionMCPRedactionConflictAndPreservesNativeInputs(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	claudeSkill := filepath.Join(home, ".claude", "skills", "shared")
	codexSkill := filepath.Join(home, ".codex", "skills", "shared")
	writeSyncTestFile(t, filepath.Join(claudeSkill, "SKILL.md"), []byte("claude skill bytes\n"))
	writeSyncTestFile(t, filepath.Join(codexSkill, "SKILL.md"), []byte("codex skill bytes\n"))
	codexRolePath := filepath.Join(home, ".codex", "agents", "planner.toml")
	codexRoleBytes := []byte("name = \"planner\"\ndescription = \"Plans work\"\nmodel = \"gpt-5\"\nmodel_reasoning_effort = \"high\"\ndeveloper_instructions = \"Plan carefully\"\n")
	writeSyncTestFile(t, codexRolePath, codexRoleBytes)
	claudeMCPPath := filepath.Join(home, ".claude.json")
	claudeMCPBytes := []byte(`{"mcpServers":{"local":{"type":"stdio","command":"node","args":["server.js"],"env":{"TOKEN":"secret","REF":"${REF}"}}}}`)
	writeSyncTestFile(t, claudeMCPPath, claudeMCPBytes)

	detected := []agentConfig{
		{Name: agentClaudeCode, Enabled: true, SkillRoot: filepath.Join(home, ".claude", "skills"), AgentRoot: filepath.Join(home, ".claude", "agents")},
		{Name: agentCodex, Enabled: true, SkillRoot: filepath.Join(home, ".codex", "skills"), AgentRoot: filepath.Join(home, ".codex", "agents")},
	}
	cfg := config{Version: 1, Agents: detected}
	skills, roles, mcps, err := scanNativeImports(cfg, detected, root, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 || len(roles) != 1 || len(mcps) != 1 {
		t.Fatalf("scan counts skills=%d roles=%d mcps=%d", len(skills), len(roles), len(mcps))
	}
	streams := setupIO{in: strings.NewReader("y\nr\ny\ny\n"), out: &bytes.Buffer{}}
	if err := importNativeContent(root, &cfg, skills, roles, mcps, streams); err != nil {
		t.Fatal(err)
	}
	importedSkill, err := os.ReadFile(filepath.Join(root, "skills", "shared", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(importedSkill) != "codex skill bytes\n" {
		t.Fatalf("conflict choice imported %q, want codex bytes", importedSkill)
	}
	roleData, err := os.ReadFile(filepath.Join(root, "agents", "planner.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(roleData, []byte("description: Plans work")) || !bytes.Contains(roleData, []byte("Plan carefully")) {
		t.Fatalf("converted Codex role unexpected:\n%s", roleData)
	}
	if len(cfg.MCPServers) != 1 || cfg.MCPServers[0].Env["TOKEN"] != "${TOKEN}" || cfg.MCPServers[0].Env["REF"] != "${REF}" {
		t.Fatalf("imported MCP not redacted/preserved: %#v", cfg.MCPServers)
	}
	if !stringSlicesEqual(cfg.MCPServers[0].Agents, []string{agentClaudeCode}) {
		t.Fatalf("imported MCP agents = %#v, want source agent only", cfg.MCPServers[0].Agents)
	}
	for path, want := range map[string][]byte{codexRolePath: codexRoleBytes, claudeMCPPath: claudeMCPBytes} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("source %s changed", path)
		}
	}
}
func TestNativeMCPImportRejectsLiteralArgumentSecrets(t *testing.T) {
	home := t.TempDir()
	agent := agentConfig{Name: agentClaudeCode, Enabled: true}
	configPath := filepath.Join(home, ".claude.json")
	writeSyncTestFile(t, configPath, []byte(`{"mcpServers":{"private":{"type":"stdio","command":"node","args":["server.js","--token","sk-live-secret"]}}}`))

	_, err := scanNativeMCP(agent, config{Version: 1}, home)
	if err == nil || !strings.Contains(err.Error(), "move the secret to an environment variable") {
		t.Fatalf("literal secret import error = %v", err)
	}

	writeSyncTestFile(t, configPath, []byte(`{"mcpServers":{"private":{"type":"stdio","command":"node","args":["server.js","--token","${TOKEN}","--mode","safe"]}}}`))
	candidates, err := scanNativeMCP(agent, config{Version: 1}, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || !stringSlicesEqual(candidates[0].Server.Args, []string{"server.js", "--token", "${TOKEN}", "--mode", "safe"}) {
		t.Fatalf("safe imported MCP = %#v", candidates)
	}
	for _, args := range [][]string{
		{"--api-key=literal"},
		{"https://example.test/mcp?access_token=literal"},
		{"--header", "Authorization: Bearer literal"},
	} {
		if !importedMCPInvocationHasLiteralSecret("node", args) {
			t.Fatalf("literal secret not detected in %#v", args)
		}
	}
	for _, args := range [][]string{
		{"--api-key=${API_KEY}"},
		{"https://example.test/mcp?access_token=${ACCESS_TOKEN}"},
		{"--header", "Authorization: Bearer ${TOKEN}"},
		{"--mode", "safe"},
	} {
		if importedMCPInvocationHasLiteralSecret("node", args) {
			t.Fatalf("safe args flagged as secret in %#v", args)
		}
	}
}
func TestCopyDirMissingRejectsNestedSymlinkBeforeWriting(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "native")
	target := filepath.Join(root, "canonical")
	secret := filepath.Join(root, "secret.txt")
	writeSyncTestFile(t, filepath.Join(source, "SKILL.md"), []byte("---\nname: native\n---\n"))
	writeSyncTestFile(t, secret, []byte("must not be copied\n"))
	if err := os.Symlink(secret, filepath.Join(source, "secret-link")); err != nil {
		t.Fatal(err)
	}
	err := copyDirMissing(target, source)
	if err == nil || !strings.Contains(err.Error(), "refusing to import symlink") {
		t.Fatalf("nested symlink import error = %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("rejected import created canonical content: %v", err)
	}
}

func TestImportPromptsTreatEOFAsNo(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "skill")
	writeSyncTestFile(t, filepath.Join(src, "SKILL.md"), []byte("native skill\n"))
	cfg := config{Version: 1}
	skills := []nativeSkillCandidate{{nativeImportCandidate: nativeImportCandidate{Name: "native", Origin: agentClaudeCode, Path: src}}}
	if err := importNativeContent(root, &cfg, skills, nil, nil, setupIO{in: strings.NewReader(""), out: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "skills", "native")); !os.IsNotExist(err) {
		t.Fatalf("EOF prompt imported skill, stat error = %v", err)
	}
}

func TestThreeWaySkillConflictSkipPersists(t *testing.T) {
	items := []nativeSkillCandidate{
		{nativeImportCandidate: nativeImportCandidate{Name: "shared", Origin: "left", Path: "/left"}},
		{nativeImportCandidate: nativeImportCandidate{Name: "shared", Origin: "middle", Path: "/middle"}},
		{nativeImportCandidate: nativeImportCandidate{Name: "shared", Origin: "right", Path: "/right"}},
	}
	got := resolveSkillConflicts(items, setupIO{in: strings.NewReader("s\n"), out: &bytes.Buffer{}})
	if len(got) != 0 {
		t.Fatalf("three-way skip resolved to %#v, want no import", got)
	}
}

func TestScanNativeImportsExpandsPortableAgentPaths(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	writeSyncTestFile(t, filepath.Join(home, ".claude", "skills", "native", "SKILL.md"), []byte("native skill\n"))
	writeSyncTestFile(t, filepath.Join(home, ".claude", "agents", "native.md"), []byte("native role\n"))
	detected := []agentConfig{{Name: agentClaudeCode, Enabled: true, SkillRoot: "~/.claude/skills", AgentRoot: "~/.claude/agents"}}
	skills, roles, _, err := scanNativeImports(config{Version: 1, Agents: detected}, detected, root, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "native" || len(roles) != 1 || roles[0].TargetName != "native" {
		t.Fatalf("scan with portable paths skills=%#v roles=%#v", skills, roles)
	}
}

func TestConfirmDestructiveSyncActionsDeclineClearsPlan(t *testing.T) {
	reports := []agentReport{{
		Name: "codex", Detected: true, SkillRoot: "/tmp/skills", AgentRoot: "/tmp/agents",
		Removes: []string{"real-skill"}, UpdatesAgent: []string{"architect"}, Adds: []string{"starter"},
	}}
	var out bytes.Buffer
	confirmDestructiveSyncActions(reports, setupIO{in: strings.NewReader("n\n"), out: &out})
	if len(reports[0].Removes) != 0 || len(reports[0].UpdatesAgent) != 0 {
		t.Fatalf("declined harness kept destructive plan: %#v", reports[0])
	}
	if len(reports[0].Adds) != 1 {
		t.Fatalf("decline must not drop additive actions: %#v", reports[0])
	}
	if !strings.Contains(out.String(), "remove 1 skill(s) from /tmp/skills: real-skill") {
		t.Fatalf("missing removal preview:\n%s", out.String())
	}
}

func TestConfirmDestructiveSyncActionsDeclinePackageRemoval(t *testing.T) {
	reports := []agentReport{{
		Name: "pi", Detected: true,
		RemovesPackage: []string{"npm:old@1.0.0"}, UpdatesPackage: []string{"settings.json packages"},
	}}
	var out bytes.Buffer
	confirmDestructiveSyncActions(reports, setupIO{in: strings.NewReader("n\n"), out: &out})
	if len(reports[0].RemovesPackage) != 0 || len(reports[0].UpdatesPackage) != 0 {
		t.Fatalf("declined package removal kept replacement plan: %#v", reports[0])
	}
	if !strings.Contains(out.String(), "remove 1 Pi package declaration(s): npm:old@1.0.0") {
		t.Fatalf("missing package removal preview:\n%s", out.String())
	}
}

func TestConfirmDestructiveSyncActionsAcceptKeepsPlan(t *testing.T) {
	reports := []agentReport{{
		Name: "codex", Detected: true, SkillRoot: "/tmp/skills",
		Removes: []string{"real-skill"},
	}}
	var out bytes.Buffer
	confirmDestructiveSyncActions(reports, setupIO{in: strings.NewReader("y\n"), out: &out})
	if len(reports[0].Removes) != 1 {
		t.Fatalf("accepted harness lost its plan: %#v", reports[0])
	}
}

func TestConfirmDestructiveSyncActionsEOFDefaultsToNo(t *testing.T) {
	reports := []agentReport{{
		Name: "codex", Detected: true, SkillRoot: "/tmp/skills",
		Removes: []string{"real-skill"},
	}}
	var out bytes.Buffer
	confirmDestructiveSyncActions(reports, setupIO{in: strings.NewReader(""), out: &out})
	if len(reports[0].Removes) != 0 {
		t.Fatalf("EOF must decline destructive plan: %#v", reports[0])
	}
	if !strings.Contains(out.String(), "skipped (no input") {
		t.Fatalf("EOF skip must be announced:\n%s", out.String())
	}
}

func TestPromptYesNoEOFAnnouncesSkip(t *testing.T) {
	var out bytes.Buffer
	if promptYesNo(setupIO{in: strings.NewReader(""), out: &out}, "Do it?") {
		t.Fatal("EOF must answer no")
	}
	if !strings.Contains(out.String(), "skipped (no input") {
		t.Fatalf("EOF skip must be announced:\n%s", out.String())
	}
}

func TestValidateConfigPiMCPTargetIsPreserved(t *testing.T) {
	cfg := config{
		Agents: []agentConfig{
			{Name: "pi", Enabled: true, SkillRoot: "~/.pi/agent/skills"},
			{Name: "claude", Enabled: true, SkillRoot: "~/.claude/skills"},
		},
		MCPServers: []mcpServerConfig{
			{Name: "linkedin", Command: "uvx", Agents: []string{"pi", "claude"}},
		},
	}
	if err := validateConfig(&cfg, "/home/u", false); err != nil {
		t.Fatalf("Pi MCP target rejected: %v", err)
	}
	if !reflect.DeepEqual(cfg.MCPServers[0].Agents, []string{"pi", "claude"}) {
		t.Fatalf("Pi MCP target changed: %#v", cfg.MCPServers[0].Agents)
	}
}
