package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	starter "github.com/yourconscience/tackroom"
	"github.com/yourconscience/tackroom/internal/agentrole"
	"gopkg.in/yaml.v3"
)

const (
	memoryTierOff       = "off"
	memoryTierBasic     = "basic"
	memoryTierMemsearch = "memsearch"
)

var managedMemoryHookNames = map[string]struct{}{
	"memory-session-start":          {},
	"memory-stop":                   {},
	"memory-session-end":            {},
	"memory-hermes-vault-to-memory": {},
	"memory-hermes-memory-to-vault": {},
}

type setupIO struct {
	in  io.Reader
	out io.Writer
}

type nativeImportCandidate struct {
	Name   string
	Origin string
	Path   string
}

type nativeSkillCandidate struct {
	nativeImportCandidate
}

type nativeRoleCandidate struct {
	nativeImportCandidate
	TargetName string
	Data       []byte
	Convert    func() ([]byte, error)
}

type nativeMCPCandidate struct {
	nativeImportCandidate
	Server mcpServerConfig
}

func setupStreams(opts runOptions) setupIO {
	streams := setupIO{in: opts.Stdin, out: opts.Stdout}
	if streams.in == nil {
		streams.in = os.Stdin
	}
	if streams.out == nil {
		streams.out = os.Stdout
	}
	return streams
}

func ensureStarterAssets(root string, configPath string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", root, err)
	}
	return fs.WalkDir(starter.StarterAssets, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		targetPath := path
		if strings.HasSuffix(targetPath, "/go.mod.template") {
			targetPath = strings.TrimSuffix(targetPath, ".template")
		}
		target := filepath.Join(root, filepath.FromSlash(targetPath))
		if path == "tackroom.yaml" {
			target = configPath
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if _, err := os.Lstat(target); err == nil {
			return nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", target, err)
		}
		data, err := starter.StarterAssets.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read starter %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(target), err)
		}
		mode := fs.FileMode(0o644)
		if strings.HasPrefix(path, "memory/hooks/") {
			mode = 0o755
		}
		if err := os.WriteFile(target, data, mode); err != nil {
			return fmt.Errorf("write starter %s: %w", target, err)
		}
		return nil
	})
}

func loadSetupConfig(configPath string, home string) (config, error) {
	data, err := os.ReadFile(configPath)
	if errors.Is(err, fs.ErrNotExist) {
		data, err = starter.StarterAssets.ReadFile("tackroom.yaml")
		if err != nil {
			return config{}, fmt.Errorf("read embedded starter config: %w", err)
		}
	} else if err != nil {
		return config{}, fmt.Errorf("read config %s: %w", configPath, err)
	}
	var cfg config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return config{}, fmt.Errorf("yaml decode: %w", err)
	}
	if err := validateConfig(&cfg, home, false); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func writeSetupConfig(configPath string, cfg config, home string) error {
	return saveConfigDocument(configPath, home, cfg)
}

func defaultAgentConfigs() []agentConfig {
	return []agentConfig{
		{Name: agentClaudeCode, Enabled: true, SkillRoot: "~/.claude/skills", AgentRoot: "~/.claude/agents", Detect: "claude"},
		{Name: agentCodex, Enabled: true, SkillRoot: "~/.codex/skills", AgentRoot: "~/.codex/agents", Detect: "codex"},
		{Name: agentDroid, Enabled: true, SkillRoot: "~/.factory/skills", AgentRoot: "~/.factory/droids", Detect: "droid"},
		{Name: agentHermes, Enabled: true, SkillRoot: "~/.hermes/skills", Detect: "hermes"},
		{Name: agentOMP, Enabled: true, SkillRoot: "~/.omp/agent/skills", AgentRoot: "~/.omp/agent/agents", Detect: "omp"},
		{Name: agentOpenCode, Enabled: true, SkillRoot: "~/.config/opencode/skills", AgentRoot: "~/.config/opencode/agents", Detect: "opencode"},
		{Name: agentPi, Enabled: true, SkillRoot: "~/.pi/agent/skills", AgentRoot: "~/.pi/agent/agents", Detect: "pi"},
		{Name: agentQwenCode, Enabled: true, SkillRoot: "~/.qwen/skills", AgentRoot: "~/.qwen/agents", Detect: "qwen"},
		{Name: agentCursor, Enabled: true, SkillRoot: "~/.cursor/skills", AgentRoot: "~/.cursor/agents", Detect: "cursor-agent"},
		{Name: agentCopilot, Enabled: true, SkillRoot: "~/.copilot/skills", AgentRoot: "~/.copilot/agents", Detect: "copilot"},
		{Name: agentGrok, Enabled: true, SkillRoot: "~/.grok/skills", AgentRoot: "~/.grok/agents", Detect: "grok"},
		{Name: agentOpenClaw, Enabled: true, SkillRoot: "~/.openclaw/skills", Detect: "openclaw"},
		{Name: agentDSH, Enabled: true, SkillRoot: "~/.dsh/skills", Detect: "dsh"},
	}
}

func setupSelectableAgentConfigs() []agentConfig {
	agents := append([]agentConfig{}, defaultAgentConfigs()...)
	agents = append(agents, agentConfig{Name: agentAmp, Enabled: true, SkillRoot: tackroomSkillsPathValue, Detect: "amp"})
	return agents
}

func detectDefaultAgents(override string) ([]agentConfig, error) {
	defaults := defaultAgentConfigs()
	if strings.TrimSpace(override) != "" {
		selectable := setupSelectableAgentConfigs()
		index := make(map[string]agentConfig, len(selectable))
		for _, agent := range selectable {
			index[agent.Name] = agent
		}
		var selected []agentConfig
		seen := make(map[string]struct{})
		for _, part := range strings.Split(override, ",") {
			name := normalizeAgentName(part)
			if name == "" {
				continue
			}
			agent, ok := index[name]
			if !ok {
				return nil, fmt.Errorf("unknown agent %q in --agents", name)
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			if isDetected(agent) {
				selected = append(selected, agent)
			}
		}
		return selected, nil
	}
	var detected []agentConfig
	for _, agent := range defaults {
		if isDetected(agent) {
			detected = append(detected, agent)
		}
	}
	return detected, nil
}

func upsertSetupAgents(cfg *config, detected []agentConfig) {
	index := make(map[string]int, len(cfg.Agents))
	for i := range cfg.Agents {
		cfg.Agents[i].Name = normalizeAgentName(cfg.Agents[i].Name)
		index[cfg.Agents[i].Name] = i
	}
	for _, agent := range detected {
		if i, ok := index[agent.Name]; ok {
			cfg.Agents[i].Enabled = true
			if strings.TrimSpace(cfg.Agents[i].SkillRoot) == "" {
				cfg.Agents[i].SkillRoot = agent.SkillRoot
			}
			if strings.TrimSpace(cfg.Agents[i].AgentRoot) == "" {
				cfg.Agents[i].AgentRoot = agent.AgentRoot
			}
			if strings.TrimSpace(cfg.Agents[i].Detect) == "" {
				cfg.Agents[i].Detect = agent.Detect
			}
			continue
		}
		cfg.Agents = append(cfg.Agents, agent)
	}
}

func applyMemoryTier(cfg *config, tier string, root string, home string) error {
	tier = normalizeMemoryTier(tier)
	startTargets := memoryHookAgents(*cfg, "SessionStart")
	stopTargets := memoryHookAgents(*cfg, "Stop")
	endTargets := memoryHookAgents(*cfg, "SessionEnd")
	hermesTargets := enabledAgentNames(*cfg, agentHermes)
	switch tier {
	case memoryTierOff:
		removeManagedMemoryHooks(cfg)
		return nil
	case memoryTierBasic:
		removeManagedMemoryHooks(cfg)
		if len(startTargets) > 0 {
			cfg.Hooks = append(cfg.Hooks, hookConfig{Name: "memory-session-start", Enabled: true, Event: "SessionStart", Command: managedMemoryHookCommand(root, home, "basic-session-start.py"), Timeout: 15, Agents: startTargets})
		}
		if len(endTargets) > 0 {
			cfg.Hooks = append(cfg.Hooks, hookConfig{Name: "memory-session-end", Enabled: true, Event: "SessionEnd", Command: managedMemoryHookCommand(root, home, "basic-session-end.py"), Timeout: 30, Agents: endTargets})
		}
		return nil
	case memoryTierMemsearch:
		if _, err := exec.LookPath("memsearch"); err != nil {
			return fmt.Errorf("setup --memory memsearch requires the memsearch binary on PATH; install memsearch or rerun with --memory basic/off")
		}
		if err := ensureMemsearchConfig(root, home); err != nil {
			return fmt.Errorf("configure memsearch: %w", err)
		}
		removeManagedMemoryHooks(cfg)
		// session-start.sh delegates to the Claude memsearch plugin, whose
		// hookSpecificOutput response is not consumed by Hermes. Hermes imports
		// the shared vault into its built-in memory instead.
		if targets := withoutAgent(startTargets, agentHermes); len(targets) > 0 {
			cfg.Hooks = append(cfg.Hooks, hookConfig{Name: "memory-session-start", Enabled: true, Event: "SessionStart", Command: managedMemoryHookCommand(root, home, "session-start.sh"), Timeout: 15, Agents: targets})
		}
		if len(stopTargets) > 0 {
			cfg.Hooks = append(cfg.Hooks, hookConfig{Name: "memory-stop", Enabled: true, Event: "Stop", Command: managedMemoryHookCommand(root, home, "stop.sh"), Timeout: 15, Agents: stopTargets})
		}
		if len(endTargets) > 0 {
			cfg.Hooks = append(cfg.Hooks, hookConfig{Name: "memory-session-end", Enabled: true, Event: "SessionEnd", Command: managedMemoryHookCommand(root, home, "session-end.sh"), Timeout: 30, Agents: endTargets})
		}
		if len(hermesTargets) > 0 {
			cfg.Hooks = append(cfg.Hooks,
				hookConfig{Name: "memory-hermes-vault-to-memory", Enabled: true, Event: "SessionStart", Command: managedMemoryHookCommand(root, home, "sync-vault-to-memory.sh"), Timeout: 30, Agents: hermesTargets},
				hookConfig{Name: "memory-hermes-memory-to-vault", Enabled: true, Event: "SessionEnd", Command: managedMemoryHookCommand(root, home, "sync-memory-to-vault.sh"), Timeout: 30, Agents: hermesTargets},
			)
		}
		return nil
	default:
		return fmt.Errorf("unknown memory tier %q (want off, basic, or memsearch)", tier)
	}
}

func managedMemoryHookCommand(root string, home string, script string) string {
	rel := filepath.Join("memory", "hooks", script)
	if filepath.Clean(root) == filepath.Clean(filepath.Join(home, ".agents")) {
		return filepath.Join("~", ".agents", rel)
	}
	return filepath.Join(root, rel)
}

func memoryHookAgents(cfg config, event string) []string {
	var agents []string
	for _, agent := range cfg.Agents {
		if !agent.Enabled {
			continue
		}
		if _, ok := hookTargetForHarness(agent.Name); !ok {
			continue
		}
		if normalizeAgentName(agent.Name) == agentHermes {
			if _, ok := hermesHookEvent(event); !ok {
				continue
			}
		}
		agents = append(agents, agent.Name)
	}
	sort.Strings(agents)
	return agents
}

func enabledAgentNames(cfg config, name string) []string {
	var names []string
	for _, agent := range cfg.Agents {
		if agent.Enabled && normalizeAgentName(agent.Name) == name {
			names = append(names, agent.Name)
		}
	}
	sort.Strings(names)
	return names
}

func withoutAgent(agents []string, excluded string) []string {
	filtered := make([]string, 0, len(agents))
	for _, agent := range agents {
		if normalizeAgentName(agent) != excluded {
			filtered = append(filtered, agent)
		}
	}
	return filtered
}

func normalizeMemoryTier(tier string) string {
	trimmed := strings.ToLower(strings.TrimSpace(tier))
	if trimmed == "" {
		return memoryTierBasic
	}
	return trimmed
}

func removeManagedMemoryHooks(cfg *config) {
	kept := cfg.Hooks[:0]
	for _, hook := range cfg.Hooks {
		if _, managed := managedMemoryHookNames[hook.Name]; managed {
			continue
		}
		kept = append(kept, hook)
	}
	cfg.Hooks = kept
}

func ensureMemoryHookExecutables(root string) error {
	return filepath.WalkDir(filepath.Join(root, "memory", "hooks"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.Chmod(path, info.Mode().Perm()|0o755)
	})
}

func scanNativeImports(cfg config, detected []agentConfig, root string, home string) ([]nativeSkillCandidate, []nativeRoleCandidate, []nativeMCPCandidate, error) {
	var skills []nativeSkillCandidate
	var roles []nativeRoleCandidate
	var servers []nativeMCPCandidate
	canonicalSkills := filepath.Join(root, "skills")
	canonicalAgents := filepath.Join(root, "agents")
	for _, agent := range detected {
		agent.SkillRoot = expandPath(agent.SkillRoot, home)
		agent.AgentRoot = expandPath(agent.AgentRoot, home)
		if agent.SkillRoot != "" {
			found, err := scanNativeSkills(agent, canonicalSkills)
			if err != nil {
				return nil, nil, nil, err
			}
			skills = append(skills, found...)
		}
		if agent.AgentRoot != "" {
			found, err := scanNativeRoles(agent, canonicalAgents)
			if err != nil {
				return nil, nil, nil, err
			}
			roles = append(roles, found...)
		}
		foundMCP, err := scanNativeMCP(agent, cfg, home)
		if err != nil {
			return nil, nil, nil, err
		}
		servers = append(servers, foundMCP...)
	}
	return skills, roles, servers, nil
}

func scanNativeSkills(agent agentConfig, canonicalSkills string) ([]nativeSkillCandidate, error) {
	entries, err := os.ReadDir(agent.SkillRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", agent.SkillRoot, err)
	}
	var out []nativeSkillCandidate
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(agent.SkillRoot, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || sameResolvedPath(path, filepath.Join(canonicalSkills, entry.Name())) {
			continue
		}
		if !hasFile(filepath.Join(path, "SKILL.md")) {
			continue
		}
		out = append(out, nativeSkillCandidate{nativeImportCandidate: nativeImportCandidate{Name: entry.Name(), Origin: agent.Name, Path: path}})
	}
	return out, nil
}

func scanNativeRoles(agent agentConfig, canonicalAgents string) ([]nativeRoleCandidate, error) {
	h := harnessFor(agent.Name)
	if h == nil || h.roles == nil {
		return nil, nil
	}
	entries, err := os.ReadDir(agent.AgentRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", agent.AgentRoot, err)
	}
	var out []nativeRoleCandidate
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || filepath.Ext(entry.Name()) != h.roles.Extension() {
			continue
		}
		path := filepath.Join(agent.AgentRoot, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || sameResolvedPath(path, filepath.Join(canonicalAgents, strings.TrimSuffix(entry.Name(), h.roles.Extension())+agentrole.MarkdownExtension)) {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if isManagedAgentFile(path, data, filepath.Dir(filepath.Dir(canonicalAgents))) {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(entry.Name(), h.roles.Extension())))
		candidate := nativeRoleCandidate{nativeImportCandidate: nativeImportCandidate{Name: name, Origin: agent.Name, Path: path}, TargetName: name, Data: append([]byte(nil), data...)}
		switch agent.Name {
		case agentCodex:
			candidate.Convert = func() ([]byte, error) { return convertCodexRoleTOMLToMarkdown(path, data) }
		default:
			candidate.Convert = func() ([]byte, error) { return convertNativeRoleMarkdown(path, data, name) }
		}
		out = append(out, candidate)
	}
	return out, nil
}

func scanNativeMCP(agent agentConfig, cfg config, home string) ([]nativeMCPCandidate, error) {
	if !hasMCPSupport(agent.Name) {
		return nil, nil
	}
	names, err := nativeMCPServerNames(agent.Name, home)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]struct{}, len(cfg.MCPServers))
	for _, server := range cfg.MCPServers {
		existing[server.Name] = struct{}{}
	}
	var out []nativeMCPCandidate
	for _, name := range names {
		if _, ok := existing[name]; ok {
			continue
		}
		server, err := readNativeMCPServer(agent.Name, name, home)
		if err != nil {
			continue
		}
		server.Enabled = true
		if importedMCPInvocationHasLiteralSecret(server.Command, server.Args) {
			return nil, fmt.Errorf("native MCP server %q for %s has a literal secret in command arguments; move the secret to an environment variable before import", name, agent.Name)
		}
		server.Env = redactImportedMCPEnv(server.Env)
		out = append(out, nativeMCPCandidate{nativeImportCandidate: nativeImportCandidate{Name: name, Origin: agent.Name, Path: name}, Server: server})
	}
	return out, nil
}
func importedMCPInvocationHasLiteralSecret(command string, args []string) bool {
	invocation := append([]string{command}, args...)
	for i, arg := range invocation {
		fields := strings.Fields(arg)
		for j, field := range fields {
			if key, value, ok := strings.Cut(field, "="); ok && isSensitiveMCPArgumentName(key) && hasLiteralMCPSecretValue(value) {
				return true
			}
			for _, part := range strings.FieldsFunc(field, func(r rune) bool { return r == '?' || r == '&' }) {
				key, value, ok := strings.Cut(part, "=")
				if ok && isSensitiveMCPArgumentName(key) && hasLiteralMCPSecretValue(value) {
					return true
				}
			}
			normalizedField := strings.ToLower(strings.Trim(field, `"' :`))
			if normalizedField == "bearer" && j+1 < len(fields) && hasLiteralMCPSecretValue(fields[j+1]) {
				return true
			}
			if !isSensitiveMCPArgumentName(field) {
				continue
			}
			if normalizedField == "authorization" && j+2 < len(fields) && strings.EqualFold(strings.Trim(fields[j+1], `"' :`), "bearer") {
				if hasLiteralMCPSecretValue(fields[j+2]) {
					return true
				}
				continue
			}
			if j+1 < len(fields) {
				if hasLiteralMCPSecretValue(fields[j+1]) {
					return true
				}
				continue
			}
			if i+1 < len(invocation) && hasLiteralMCPSecretValue(invocation[i+1]) {
				return true
			}
		}
	}
	return false
}

func hasLiteralMCPSecretValue(value string) bool {
	trimmed := strings.Trim(strings.TrimSpace(value), `"',`)
	return trimmed != "" && !isEnvReference(trimmed)
}

func isSensitiveMCPArgumentName(name string) bool {
	normalized := strings.ToLower(strings.TrimLeft(strings.Trim(strings.TrimSpace(name), `"' :`), "-"))
	normalized = strings.NewReplacer("-", "_", ".", "_").Replace(normalized)
	switch normalized {
	case "api_key", "apikey", "token", "access_token", "secret", "password", "authorization", "credential", "credentials":
		return true
	default:
		return false
	}
}

func nativeMCPServerNames(agentName string, home string) ([]string, error) {
	target, err := mcpTargetForHarness(agentName)
	if err != nil {
		return nil, nil
	}
	path := target.configPath(home)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if agentName == agentCodex {
		return codexMCPServerNames(string(data)), nil
	}
	if agentName == agentHermes {
		return yamlMCPServerNames(path, data, target.rootKey)
	}
	var raw map[string]interface{}
	if err := parseJSONConfig(path, data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var entry map[string]interface{}
	if agentName == agentClaudeCode {
		entry, _ = asMap(raw[target.rootKey])
		if len(entry) == 0 {
			entry, _ = claudeProjectMCPMap(raw)
		}
	} else {
		entry, _ = asMap(raw[target.rootKey])
	}
	var names []string
	for name := range entry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
func yamlMCPServerNames(path string, data []byte, rootKey string) ([]string, error) {
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	entry, _ := asMap(raw[rootKey])
	var names []string
	for name := range entry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func claudeProjectMCPMap(raw map[string]interface{}) (map[string]interface{}, bool) {
	projects, ok := asMap(raw["projects"])
	if !ok || len(projects) != 1 {
		return nil, false
	}
	for _, value := range projects {
		project, ok := asMap(value)
		if !ok {
			return nil, false
		}
		return asMap(project["mcpServers"])
	}
	return nil, false
}

func codexMCPServerNames(content string) []string {
	var names []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "[mcp_servers.") || !strings.HasSuffix(trimmed, "]") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(trimmed, "[mcp_servers."), "]")
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func convertNativeRoleMarkdown(path string, data []byte, fallbackName string) ([]byte, error) {
	if bytes.HasPrefix(data, []byte("---\n")) {
		if _, err := agentrole.ParseMarkdown(path, data); err == nil {
			return data, nil
		}
	}
	role := agentrole.Role{Name: fallbackName, Description: "Imported from " + path, Model: "opus", Effort: "high", Instructions: strings.TrimSpace(string(data))}
	return agentrole.RenderCanonical(role)
}

func convertCodexRoleTOMLToMarkdown(path string, data []byte) ([]byte, error) {
	values := parseTOMLTopLevelValues(string(data))
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if raw := values["name"]; raw != "" {
		if parsed, err := parseTOMLString(raw); err == nil && strings.TrimSpace(parsed) != "" {
			name = parsed
		}
	}
	description := "Imported from " + path
	if raw := values["description"]; raw != "" {
		if parsed, err := parseTOMLString(raw); err == nil && strings.TrimSpace(parsed) != "" {
			description = parsed
		}
	}
	model := ""
	if raw := values["model"]; raw != "" {
		model, _ = parseTOMLString(raw)
	}
	effort := ""
	if raw := values["model_reasoning_effort"]; raw != "" {
		effort, _ = parseTOMLString(raw)
	}
	instructions := ""
	if raw := values["developer_instructions"]; raw != "" {
		instructions, _ = parseTOMLString(raw)
	}
	if strings.TrimSpace(instructions) == "" {
		instructions = strings.TrimSpace(string(data))
	}
	role := agentrole.Role{Name: name, Description: description, Effort: effort, Instructions: instructions, Codex: agentrole.CodexOptions{Model: model, ModelReasoningEffort: effort}}
	return agentrole.RenderCanonical(role)
}

// confirmDestructiveSyncActions previews per-harness removals and role
// overwrites before a setup-driven sync applies them. A declined (or
// unanswered) harness keeps its existing content: removals and role overwrites
// are dropped from that harness's plan while adds and skill updates still
// apply.
func confirmDestructiveSyncActions(reports []agentReport, streams setupIO) {
	for i := range reports {
		r := &reports[i]
		if !r.Detected {
			continue
		}
		if len(r.Removes)+len(r.RemovesAgent)+len(r.UpdatesAgent)+len(r.RemovesPackage) == 0 {
			continue
		}
		fmt.Fprintf(streams.out, "\n%s has existing content this sync would change:\n", r.Name)
		if len(r.Removes) > 0 {
			fmt.Fprintf(streams.out, "  remove %d skill(s) from %s: %s\n", len(r.Removes), r.SkillRoot, strings.Join(r.Removes, ", "))
		}
		if len(r.RemovesAgent) > 0 {
			fmt.Fprintf(streams.out, "  remove %d agent role(s) from %s: %s\n", len(r.RemovesAgent), r.AgentRoot, strings.Join(r.RemovesAgent, ", "))
		}
		if len(r.UpdatesAgent) > 0 {
			fmt.Fprintf(streams.out, "  overwrite %d agent role(s) in %s: %s\n", len(r.UpdatesAgent), r.AgentRoot, strings.Join(r.UpdatesAgent, ", "))
		}
		if len(r.RemovesPackage) > 0 {
			fmt.Fprintf(streams.out, "  remove %d Pi package declaration(s): %s\n", len(r.RemovesPackage), strings.Join(r.RemovesPackage, ", "))
		}
		if !promptYesNoDefaultNo(streams, fmt.Sprintf("Apply these changes to %s?", r.Name)) {
			fmt.Fprintf(streams.out, "%s: keeping existing content; removals and overwrites skipped this run\n", r.Name)
			r.Removes = nil
			r.RemovesAgent = nil
			r.UpdatesAgent = nil
			r.RemovesPackage = nil
			r.UpdatesPackage = nil
		}
	}
}

func promptYesNoDefaultNo(streams setupIO, prompt string) bool {
	fmt.Fprintf(streams.out, "%s [y/N] ", prompt)
	line, answered := readSetupLine(streams.in)
	if !answered {
		fmt.Fprintln(streams.out, "skipped (no input; answering no)")
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func promptYesNo(streams setupIO, prompt string) bool {
	fmt.Fprintf(streams.out, "%s [Y/n] ", prompt)
	line, answered := readSetupLine(streams.in)
	if !answered {
		fmt.Fprintln(streams.out, "skipped (no input; answering no)")
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "" || answer == "y" || answer == "yes"
}

func promptConflict(streams setupIO, category string, name string, left string, right string) string {
	fmt.Fprintf(streams.out, "%s %q conflict: keep left (%s), keep right (%s), or skip? [l/r/s] ", category, name, left, right)
	line, answered := readSetupLine(streams.in)
	if !answered {
		fmt.Fprintln(streams.out, "skipped (no input)")
		return "skip"
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "r", "right", "keep right":
		return "right"
	case "s", "skip":
		return "skip"
	default:
		return "left"
	}
}

func readSetupLine(r io.Reader) (string, bool) {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				return b.String(), true
			}
			b.WriteByte(buf[0])
		}
		if err != nil {
			return b.String(), b.Len() > 0
		}
	}
}

func importNativeContent(root string, cfg *config, skills []nativeSkillCandidate, roles []nativeRoleCandidate, mcps []nativeMCPCandidate, streams setupIO) error {
	if len(skills) > 0 && promptYesNo(streams, fmt.Sprintf("Import %d skills into %s?", len(skills), filepath.Join(root, "skills"))) {
		chosen := resolveSkillConflicts(skills, streams)
		for _, skill := range chosen {
			if err := copyDirMissing(filepath.Join(root, "skills", skill.Name), skill.Path); err != nil {
				return err
			}
		}
	}
	if len(roles) > 0 && promptYesNo(streams, fmt.Sprintf("Import %d agent roles into %s?", len(roles), filepath.Join(root, "agents"))) {
		chosen := resolveRoleConflicts(roles, streams)
		for _, role := range chosen {
			data, err := role.Convert()
			if err != nil {
				return err
			}
			path := filepath.Join(root, "agents", role.TargetName+agentrole.MarkdownExtension)
			if _, err := os.Lstat(path); err == nil {
				continue
			} else if !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("stat %s: %w", path, err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
		}
	}
	if len(mcps) > 0 && promptYesNo(streams, fmt.Sprintf("Import %d MCP servers into tackroom.yaml?", len(mcps))) {
		chosen := resolveMCPConflicts(mcps, streams)
		for _, candidate := range chosen {
			server := candidate.Server
			server.Agents = []string{candidate.Origin}
			if err := upsertCanonicalMCPServer(cfg, server); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveSkillConflicts(items []nativeSkillCandidate, streams setupIO) []nativeSkillCandidate {
	byName := make(map[string]nativeSkillCandidate)
	skipped := make(map[string]struct{})
	for _, item := range items {
		if _, ok := skipped[item.Name]; ok {
			continue
		}
		if existing, ok := byName[item.Name]; ok {
			switch promptConflict(streams, "skill", item.Name, existing.Origin, item.Origin) {
			case "right":
				byName[item.Name] = item
			case "skip":
				delete(byName, item.Name)
				skipped[item.Name] = struct{}{}
			}
			continue
		}
		byName[item.Name] = item
	}
	keys := sortedKeysNative(byName)
	out := make([]nativeSkillCandidate, 0, len(keys))
	for _, key := range keys {
		out = append(out, byName[key])
	}
	return out
}

func resolveRoleConflicts(items []nativeRoleCandidate, streams setupIO) []nativeRoleCandidate {
	byName := make(map[string]nativeRoleCandidate)
	skipped := make(map[string]struct{})
	for _, item := range items {
		if _, ok := skipped[item.TargetName]; ok {
			continue
		}
		if existing, ok := byName[item.TargetName]; ok {
			switch promptConflict(streams, "role", item.TargetName, existing.Origin, item.Origin) {
			case "right":
				byName[item.TargetName] = item
			case "skip":
				delete(byName, item.TargetName)
				skipped[item.TargetName] = struct{}{}
			}
			continue
		}
		byName[item.TargetName] = item
	}
	keys := sortedKeysNative(byName)
	out := make([]nativeRoleCandidate, 0, len(keys))
	for _, key := range keys {
		out = append(out, byName[key])
	}
	return out
}

func resolveMCPConflicts(items []nativeMCPCandidate, streams setupIO) []nativeMCPCandidate {
	byName := make(map[string]nativeMCPCandidate)
	skipped := make(map[string]struct{})
	for _, item := range items {
		if _, ok := skipped[item.Name]; ok {
			continue
		}
		if existing, ok := byName[item.Name]; ok {
			switch promptConflict(streams, "MCP server", item.Name, existing.Origin, item.Origin) {
			case "right":
				byName[item.Name] = item
			case "skip":
				delete(byName, item.Name)
				skipped[item.Name] = struct{}{}
			}
			continue
		}
		byName[item.Name] = item
	}
	keys := sortedKeysNative(byName)
	out := make([]nativeMCPCandidate, 0, len(keys))
	for _, key := range keys {
		out = append(out, byName[key])
	}
	return out
}

func sortedKeysNative[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func copyDirMissing(dst string, src string) error {
	if _, err := os.Lstat(dst); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", dst, err)
	}
	if err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to import symlink %s; replace it with an in-tree file or directory before setup", path)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !d.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to import special file %s", path)
		}
		return nil
	}); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to import symlink %s; replace it with an in-tree file or directory before setup", path)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to import special file %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, info.Mode().Perm()); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		return nil
	})
}
