package app

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

func readConfigFile(t *testing.T, path string) config {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
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

func TestEnsureStarterAssetsCreatesMissingOnlyAndExecutableHooks(t *testing.T) {
	root := t.TempDir()
	customConfig := []byte("version: 1\nagents: []\n")
	writeSyncTestFile(t, filepath.Join(root, "tackroom.yaml"), customConfig)
	if err := ensureStarterAssets(root, filepath.Join(root, "tackroom.yaml")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "tackroom.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, customConfig) {
		t.Fatalf("tackroom.yaml overwritten:\n%s", got)
	}
	for _, path := range []string{
		filepath.Join(root, "AGENTS.md"),
		filepath.Join(root, "skills", "tackroom", "SKILL.md"),
		filepath.Join(root, "agents", "architect.md"),
		filepath.Join(root, "agents", "tester.md"),
		filepath.Join(root, "memory", "lib", "basic_memory.py"),
		filepath.Join(root, "memory", "tools", "rem", "main.go"),
		filepath.Join(root, "memory", "tools", "knowledge-sync", "main.go"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("starter asset %s missing: %v", path, err)
		}
	}
	info, err := os.Stat(filepath.Join(root, "memory", "hooks", "basic-session-start.py"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("basic-session-start.py mode = %v, want executable", info.Mode().Perm())
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

func TestMemoryTierDefinitionsAndMemsearchDependency(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".agents")
	cfg := config{Version: 1, Agents: []agentConfig{
		{Name: agentClaudeCode, Enabled: true, SkillRoot: filepath.Join(home, ".claude", "skills")},
		{Name: agentCodex, Enabled: true, SkillRoot: filepath.Join(home, ".codex", "skills")},
		{Name: agentDroid, Enabled: true, SkillRoot: filepath.Join(home, ".factory", "skills")},
		{Name: agentHermes, Enabled: true, SkillRoot: filepath.Join(home, ".hermes", "skills")},
	}, Hooks: []hookConfig{{Name: "memory-stop", Event: "Stop", Command: "old", Agents: []string{agentClaudeCode}}, {Name: "keep", Event: "SessionStart", Command: "custom", Agents: []string{agentCodex}}}}
	if err := applyMemoryTier(&cfg, memoryTierOff, root, home); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hooks) != 1 || cfg.Hooks[0].Name != "keep" {
		t.Fatalf("off hooks = %#v, want only custom hook", cfg.Hooks)
	}
	if err := applyMemoryTier(&cfg, memoryTierBasic, root, home); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hooks) != 3 || cfg.Hooks[1].Event != "SessionStart" || cfg.Hooks[1].Command != "~/.agents/memory/hooks/basic-session-start.py" || !stringSlicesEqual(cfg.Hooks[1].Agents, []string{agentClaudeCode, agentCodex, agentDroid, agentHermes}) || cfg.Hooks[2].Event != "SessionEnd" || !stringSlicesEqual(cfg.Hooks[2].Agents, []string{agentClaudeCode, agentCodex, agentDroid, agentHermes}) || strings.Contains(strings.Join([]string{cfg.Hooks[1].Command, cfg.Hooks[2].Command}, " "), "stop.sh") {
		t.Fatalf("basic hooks = %#v", cfg.Hooks)
	}
	fakePath(t)
	if err := applyMemoryTier(&cfg, memoryTierMemsearch, root, home); err == nil || !strings.Contains(err.Error(), "requires the memsearch binary") {
		t.Fatalf("memsearch without binary error = %v", err)
	}
	fakePath(t, "memsearch")
	if err := applyMemoryTier(&cfg, memoryTierMemsearch, root, home); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hooks) != 6 || cfg.Hooks[1].Command != "~/.agents/memory/hooks/session-start.sh" || !stringSlicesEqual(cfg.Hooks[1].Agents, []string{agentClaudeCode, agentCodex, agentDroid}) || cfg.Hooks[2].Command != "~/.agents/memory/hooks/stop.sh" || !stringSlicesEqual(cfg.Hooks[2].Agents, []string{agentClaudeCode, agentCodex, agentDroid}) || cfg.Hooks[3].Command != "~/.agents/memory/hooks/session-end.sh" || !stringSlicesEqual(cfg.Hooks[3].Agents, []string{agentClaudeCode, agentCodex, agentDroid, agentHermes}) || cfg.Hooks[4].Name != "memory-hermes-vault-to-memory" || cfg.Hooks[4].Command != "~/.agents/memory/hooks/sync-vault-to-memory.sh" || !stringSlicesEqual(cfg.Hooks[4].Agents, []string{agentHermes}) || cfg.Hooks[5].Name != "memory-hermes-memory-to-vault" || cfg.Hooks[5].Command != "~/.agents/memory/hooks/sync-memory-to-vault.sh" || !stringSlicesEqual(cfg.Hooks[5].Agents, []string{agentHermes}) {
		t.Fatalf("memsearch hooks = %#v", cfg.Hooks)
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

func TestSetupMemoryOffRemovesNativeBasicHooksPreservingUnrelated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fakePath(t, "claude")
	configPath := filepath.Join(home, ".agents", "tackroom.yaml")
	writeSyncTestFile(t, configPath, []byte(`version: 1
agents:
  - name: claude
    enabled: true
    skill_root: ~/.claude/skills
    agent_root: ~/.claude/agents
hooks:
  - name: memory-session-start
    enabled: true
    event: SessionStart
    command: ~/.agents/memory/hooks/basic-session-start.py
    timeout: 15
    agents: [claude]
  - name: memory-session-end
    enabled: true
    event: SessionEnd
    command: ~/.agents/memory/hooks/basic-session-end.py
    timeout: 30
    agents: [claude]
`))
	claudeSettings := filepath.Join(home, ".claude", "settings.json")
	writeSyncTestFile(t, claudeSettings, []byte(`{
  "hooks": {
    "SessionStart": [{"hooks": [{"command": "~/.agents/memory/hooks/basic-session-start.py", "timeout": 15}]}],
    "Stop": [{"hooks": [{"command": "echo keep", "timeout": 1}]}],
    "SessionEnd": [{"hooks": [{"command": "~/.agents/memory/hooks/basic-session-end.py", "timeout": 30}]}]
  }
}`))

	if err := runSetup(runOptions{MemoryTier: memoryTierOff, Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(claudeSettings)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "basic-session-start.py") || strings.Contains(text, "basic-session-end.py") {
		t.Fatalf("managed basic hooks survived setup --memory off:\n%s", text)
	}
	if !strings.Contains(text, "echo keep") {
		t.Fatalf("unrelated hook was not preserved:\n%s", text)
	}
	if cfg := readConfigFile(t, configPath); len(cfg.Hooks) != 0 {
		t.Fatalf("setup --memory off config hooks = %#v, want none", cfg.Hooks)
	}
}

func TestSetupMemoryBasicReplacesNativeMemsearchHooksPreservingUnrelated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fakePath(t, "claude")
	configPath := filepath.Join(home, ".agents", "tackroom.yaml")
	writeSyncTestFile(t, configPath, []byte(`version: 1
agents:
  - name: claude
    enabled: true
    skill_root: ~/.claude/skills
    agent_root: ~/.claude/agents
hooks:
  - name: memory-session-start
    enabled: true
    event: SessionStart
    command: ~/.agents/memory/hooks/session-start.sh
    timeout: 15
    agents: [claude]
  - name: memory-stop
    enabled: true
    event: Stop
    command: ~/.agents/memory/hooks/stop.sh
    timeout: 15
    agents: [claude]
  - name: memory-session-end
    enabled: true
    event: SessionEnd
    command: ~/.agents/memory/hooks/session-end.sh
    timeout: 30
    agents: [claude]
`))
	claudeSettings := filepath.Join(home, ".claude", "settings.json")
	writeSyncTestFile(t, claudeSettings, []byte(`{
  "hooks": {
    "SessionStart": [{"hooks": [{"command": "echo keep", "timeout": 1}, {"command": "~/.agents/memory/hooks/session-start.sh", "timeout": 15}]}],
    "Stop": [{"hooks": [{"command": "~/.agents/memory/hooks/stop.sh", "timeout": 15}]}],
    "SessionEnd": [{"hooks": [{"command": "~/.agents/memory/hooks/session-end.sh", "timeout": 30}]}]
  }
}`))

	if err := runSetup(runOptions{MemoryTier: memoryTierBasic, Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(claudeSettings)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, stale := range []string{"session-start.sh", "stop.sh", "session-end.sh"} {
		if strings.Contains(text, stale) {
			t.Fatalf("managed memsearch hook %s survived basic switch:\n%s", stale, text)
		}
	}
	for _, want := range []string{"basic-session-start.py", "basic-session-end.py", "echo keep"} {
		if !strings.Contains(text, want) {
			t.Fatalf("native settings missing %q after basic switch:\n%s", want, text)
		}
	}
	cfg := readConfigFile(t, configPath)
	if len(cfg.Hooks) != 2 || strings.Contains(cfg.Hooks[0].Command+cfg.Hooks[1].Command, "stop.sh") {
		t.Fatalf("basic config hooks = %#v", cfg.Hooks)
	}
}

func TestSetupMemoryBasicUsesCustomRootCommands(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "custom-agents")
	t.Setenv("HOME", home)
	fakePath(t, "claude")
	configPath := filepath.Join(root, "tackroom.yaml")
	writeSyncTestFile(t, configPath, []byte(`version: 1
agents:
  - name: claude
    enabled: true
    skill_root: ~/.claude/skills
    agent_root: ~/.claude/agents
`))
	if err := runSetup(runOptions{ConfigPath: configPath, MemoryTier: memoryTierBasic, Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	cfg := readConfigFile(t, configPath)
	wantStart := filepath.Join(root, "memory", "hooks", "basic-session-start.py")
	wantEnd := filepath.Join(root, "memory", "hooks", "basic-session-end.py")
	if len(cfg.Hooks) != 2 || cfg.Hooks[0].Command != wantStart || cfg.Hooks[1].Command != wantEnd {
		t.Fatalf("custom-root memory hooks = %#v, want %q and %q", cfg.Hooks, wantStart, wantEnd)
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, wantStart) || !strings.Contains(text, wantEnd) || strings.Contains(text, "~/.agents/memory/hooks") {
		t.Fatalf("native custom-root hooks not rendered from canonical root:\n%s", text)
	}
	linkPath := filepath.Join(home, ".claude", "skills", "tackroom")
	if !sameResolvedPath(linkPath, filepath.Join(root, "skills", "tackroom")) {
		rawTarget, _ := os.Readlink(linkPath)
		t.Fatalf("custom-root skill link %s -> %q, want %s", linkPath, rawTarget, filepath.Join(root, "skills", "tackroom"))
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

func TestImportedStarterNameWinsOnFreshSetupOrdering(t *testing.T) {
	root := t.TempDir()
	skillSrc := filepath.Join(t.TempDir(), "tackroom")
	writeSyncTestFile(t, filepath.Join(skillSrc, "SKILL.md"), []byte("imported tackroom skill\n"))
	roleData := []byte("---\nname: builder\ndescription: Imported builder\n---\n\nImported builder role.\n")
	role := nativeRoleCandidate{
		nativeImportCandidate: nativeImportCandidate{Name: "builder", Origin: agentClaudeCode, Path: filepath.Join(t.TempDir(), "builder.md")},
		TargetName:            "builder",
		Convert: func() ([]byte, error) {
			return roleData, nil
		},
	}
	skills := []nativeSkillCandidate{{nativeImportCandidate: nativeImportCandidate{Name: "tackroom", Origin: agentClaudeCode, Path: skillSrc}}}
	if err := importNativeContent(root, &config{Version: 1}, skills, []nativeRoleCandidate{role}, nil, setupIO{in: strings.NewReader("y\ny\n"), out: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	if err := ensureStarterAssets(root, filepath.Join(root, "tackroom.yaml")); err != nil {
		t.Fatal(err)
	}
	skillData, err := os.ReadFile(filepath.Join(root, "skills", "tackroom", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(skillData) != "imported tackroom skill\n" {
		t.Fatalf("starter overwrote imported same-name skill:\n%s", skillData)
	}
	gotRole, err := os.ReadFile(filepath.Join(root, "agents", "builder.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotRole, roleData) {
		t.Fatalf("starter overwrote imported same-name role:\n%s", gotRole)
	}
}

func TestSetupFirstRunSyncStatusEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fakePath(t, "claude")
	if err := runSetup(runOptions{MemoryTier: memoryTierBasic, Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, ".agents", "tackroom.yaml")
	cfg := readConfigFile(t, configPath)
	if len(cfg.Agents) != 1 || cfg.Agents[0].Name != agentClaudeCode {
		t.Fatalf("setup agents = %#v", cfg.Agents)
	}
	if len(cfg.Hooks) != 2 || !strings.Contains(cfg.Hooks[0].Command, "basic-session-start.py") || cfg.Hooks[1].Event != "SessionEnd" {
		t.Fatalf("setup memory hooks = %#v", cfg.Hooks)
	}
	for _, path := range []string{
		filepath.Join(home, ".agents", "agents", "tester.md"),
		filepath.Join(home, ".agents", "memory", "hooks", "basic-session-end.py"),
		filepath.Join(home, ".claude", "skills", "tackroom"),
		filepath.Join(home, ".claude", "agents", "builder.md"),
	} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("expected setup/sync output %s: %v", path, err)
		}
	}
	if err := runStatus(runOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestSetupOffersGitInitOnFreshRoot(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	home := t.TempDir()
	t.Cleanup(func() {
		// Git clone creates read-only objects; fix perms so TempDir can remove them.
		extDir := filepath.Join(home, ".agents", "external")
		_ = filepath.Walk(extDir, func(p string, info os.FileInfo, _ error) error {
			if info != nil {
				_ = os.Chmod(p, 0o777)
			}
			return nil
		})
		_ = os.RemoveAll(extDir)
	})
	t.Setenv("HOME", home)
	dir := fakePath(t, "claude")
	if err := os.Symlink(gitPath, filepath.Join(dir, "git")); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runSetup(runOptions{MemoryTier: memoryTierBasic, Stdin: strings.NewReader("y\ny\ny\ny\n"), Stdout: &out}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".agents")
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		t.Fatalf("expected git repository in %s: %v\noutput:\n%s", root, err, out.String())
	}
}

func TestSetupSkipsGitInitNonInteractive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fakePath(t, "claude")
	if err := runSetup(runOptions{MemoryTier: memoryTierBasic, Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", ".git")); err == nil {
		t.Fatal("expected no git repository without an interactive answer")
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

func TestSetupMemsearchDependencyFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fakePath(t, "codex")
	err := runSetup(runOptions{MemoryTier: memoryTierMemsearch, Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "requires the memsearch binary") {
		t.Fatalf("setup --memory memsearch error = %v", err)
	}
}
