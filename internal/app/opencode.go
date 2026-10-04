package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// openCodeConfigDir resolves the OpenCode global config directory, honoring
// $XDG_CONFIG_HOME when set (per the OpenCode config docs) and falling back to
// ~/.config/opencode otherwise.
func openCodeConfigDir(home string) string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "opencode")
	}
	return filepath.Join(home, ".config", "opencode")
}

// openCodeConfigPath returns the shared OpenCode config file that also holds the
// managed `mcp` block.
func openCodeConfigPath(home string) string {
	return filepath.Join(openCodeConfigDir(home), "opencode.json")
}

// readsAgentsSkillsRoot reports whether a harness that natively loads
// ~/.agents/skills (OpenCode, Cursor, Copilot CLI, Grok Build) already reads
// tackroom skills from the config root. When the config root IS ~/.agents
// there is no need to mirror skills into the harness's own skill root (doing
// so would double-list every skill).
func readsAgentsSkillsRoot(repoRoot string, home string) bool {
	agentsRoot := filepath.Join(home, ".agents")
	if filepath.Clean(repoRoot) == filepath.Clean(agentsRoot) {
		return true
	}
	return sameResolvedPath(repoRoot, agentsRoot)
}

// openCodeCommandArray folds the canonical command + args into the single
// command array OpenCode expects for a local (stdio) MCP server.
func openCodeCommandArray(server mcpServerConfig) []string {
	out := make([]string, 0, len(server.Args)+1)
	out = append(out, server.Command)
	out = append(out, server.Args...)
	return out
}

func inspectOpenCodeMCPServer(target mcpTarget, server mcpServerConfig, home string) (string, error) {
	configPath := target.configPath(home)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return stateMissing, nil
		}
		return stateMissing, fmt.Errorf("read %s: %w", configPath, err)
	}
	var raw map[string]interface{}
	if err := parseJSONConfig(configPath, data, &raw); err != nil {
		return stateMissing, fmt.Errorf("parse %s: %w", configPath, err)
	}
	servers, ok := asMap(raw[target.rootKey])
	if !ok {
		return stateMissing, nil
	}
	entryRaw, ok := servers[server.Name]
	if !ok {
		return stateMissing, nil
	}
	entry, ok := asMap(entryRaw)
	if !ok {
		return stateDrifted, nil
	}
	if openCodeMCPEntryMatches(entry, server) {
		return stateSynced, nil
	}
	return stateDrifted, nil
}

func openCodeMCPEntryMatches(entry map[string]interface{}, server mcpServerConfig) bool {
	if kind, _ := entry["type"].(string); kind != "local" {
		return false
	}
	command, ok := toStringSlice(entry["command"])
	if !ok || !stringSlicesEqual(command, openCodeCommandArray(server)) {
		return false
	}
	if enabled, ok := entry["enabled"].(bool); !ok || !enabled {
		return false
	}
	if len(server.Env) > 0 {
		envMap, ok := asMap(entry["environment"])
		if !ok {
			return false
		}
		for key, expected := range server.Env {
			actual, ok := envMap[key].(string)
			if !ok || actual != expected {
				return false
			}
		}
	}
	return true
}

func patchOpenCodeMCPServer(target mcpTarget, server mcpServerConfig, home string) error {
	configPath := target.configPath(home)
	data, err := os.ReadFile(configPath)
	raw := map[string]interface{}{}
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read %s: %w", configPath, err)
		}
	} else if err := parseJSONConfig(configPath, data, &raw); err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}

	servers, _ := asMap(raw[target.rootKey])
	if servers == nil {
		servers = map[string]interface{}{}
	}
	entry, _ := asMap(servers[server.Name])
	if entry == nil {
		entry = map[string]interface{}{}
	}
	entry["type"] = "local"
	command := make([]interface{}, 0, len(server.Args)+1)
	for _, part := range openCodeCommandArray(server) {
		command = append(command, part)
	}
	entry["command"] = command
	if len(server.Env) > 0 {
		envMap, _ := asMap(entry["environment"])
		if envMap == nil {
			envMap = map[string]interface{}{}
		}
		for key, value := range server.Env {
			envMap[key] = value
		}
		entry["environment"] = envMap
	}
	entry["enabled"] = true
	servers[server.Name] = entry
	raw[target.rootKey] = servers

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", configPath, err)
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(configPath), err)
	}
	if err := os.WriteFile(configPath, out, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", configPath, err)
	}
	return nil
}

func readOpenCodeMCPServer(target mcpTarget, name string, home string) (mcpServerConfig, error) {
	configPath := target.configPath(home)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return mcpServerConfig{}, fmt.Errorf("read %s: %w", configPath, err)
	}
	var raw map[string]interface{}
	if err := parseJSONConfig(configPath, data, &raw); err != nil {
		return mcpServerConfig{}, fmt.Errorf("parse %s: %w", configPath, err)
	}
	servers, ok := asMap(raw[target.rootKey])
	if !ok {
		return mcpServerConfig{}, fmt.Errorf("MCP server %q not found in %s", name, target.agentName)
	}
	entry, ok := asMap(servers[name])
	if !ok {
		return mcpServerConfig{}, fmt.Errorf("MCP server %q not found in %s", name, target.agentName)
	}
	if kind, _ := entry["type"].(string); kind != "" && kind != "local" {
		return mcpServerConfig{}, fmt.Errorf("MCP server %q in %s is not a local stdio server", name, target.agentName)
	}
	command, ok := toStringSlice(entry["command"])
	if !ok || len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return mcpServerConfig{}, fmt.Errorf("MCP server %q in %s has no stdio command", name, target.agentName)
	}
	var args []string
	if len(command) > 1 {
		args = append([]string{}, command[1:]...)
	}
	var env map[string]string
	if envRaw, ok := entry["environment"]; ok {
		envMap, ok := asMap(envRaw)
		if !ok {
			return mcpServerConfig{}, fmt.Errorf("MCP server %q environment is not a map", name)
		}
		env = make(map[string]string, len(envMap))
		for key, value := range envMap {
			str, ok := value.(string)
			if !ok {
				return mcpServerConfig{}, fmt.Errorf("MCP server %q environment %s is not a string", name, key)
			}
			env[key] = str
		}
	}
	return mcpServerConfig{Name: name, Enabled: true, Command: command[0], Args: args, Env: env}, nil
}

// checkOpenCodeDuplicateSkills warns when a skill of the same name exists in
// both ~/.agents/skills and ~/.config/opencode/skills, which OpenCode would list
// twice because it reads both roots.
func checkOpenCodeDuplicateSkills(_ string, home string, cfg config) checkResult {
	const name = "opencode duplicate skills"
	if !isAgentDetected(cfg, agentOpenCode) {
		return checkResult{name, checkStatusPass, agentOpenCode + " not detected, skipped"}
	}
	agentsSkills := openCodeSkillDirNames(filepath.Join(home, ".agents", "skills"))
	mirrorSkills := openCodeSkillDirNames(filepath.Join(openCodeConfigDir(home), "skills"))
	var dups []string
	for skill := range mirrorSkills {
		if agentsSkills[skill] {
			dups = append(dups, skill)
		}
	}
	if len(dups) > 0 {
		sort.Strings(dups)
		return checkResult{name, checkStatusWarn, fmt.Sprintf("%s listed in both ~/.agents/skills and %s", strings.Join(dups, ", "), filepath.Join(openCodeConfigDir(home), "skills"))}
	}
	return checkResult{name, checkStatusPass, "no duplicate skill listings"}
}

func openCodeSkillDirNames(root string) map[string]bool {
	names := map[string]bool{}
	entries, err := os.ReadDir(root)
	if err != nil {
		return names
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if hasFile(filepath.Join(root, entry.Name(), "SKILL.md")) {
			names[entry.Name()] = true
		}
	}
	return names
}
