package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

type mcpServerConfig struct {
	Name    string            `yaml:"name"`
	Enabled bool              `yaml:"enabled"`
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
	Agents  []string          `yaml:"agents"`
}

type mcpTarget struct {
	agentName  string
	configPath func(home string) string
	inspect    func(mcpTarget, mcpServerConfig, string) (string, error)
	patch      func(mcpTarget, mcpServerConfig, string) error
	read       func(mcpTarget, string, string) (mcpServerConfig, error)
	rootKey    string
	// parentKey, when set, names the top-level object that holds rootKey
	// (OpenClaw keeps servers under "mcp" -> "servers").
	parentKey string
	defaults  map[string]interface{}
}

const yamlMapTag = "!!map"

func desiredMCPServersForAgent(cfg config, agentName string) []mcpServerConfig {
	var servers []mcpServerConfig
	agentName = normalizeAgentName(agentName)
	for _, server := range cfg.MCPServers {
		if !server.Enabled {
			continue
		}
		if len(server.Agents) == 0 {
			if hasMCPSupport(agentName) {
				servers = append(servers, server)
			}
			continue
		}
		if stringInSlice(agentName, server.Agents) {
			servers = append(servers, server)
		}
	}
	return servers
}

func augmentMCPReport(report *agentReport, agent agentConfig, cfg config, home string) error {
	servers := desiredMCPServersForAgent(cfg, agent.Name)
	for _, server := range servers {
		state, err := inspectMCPServer(agent.Name, server, home)
		if err != nil {
			return err
		}
		switch state {
		case stateSynced:
			report.ManagedMCP = append(report.ManagedMCP, server.Name)
		case stateMissing:
			report.MissingMCP = append(report.MissingMCP, server.Name)
			report.AddsMCP = append(report.AddsMCP, server.Name)
		case stateDrifted:
			report.DriftedMCP = append(report.DriftedMCP, server.Name)
			report.UpdatesMCP = append(report.UpdatesMCP, server.Name)
		default:
			return fmt.Errorf("unsupported MCP inspect state %q for %s/%s", state, agent.Name, server.Name)
		}
	}
	return nil
}

func applyAgentMCPSync(reports []agentReport, cfg config, home string) error {
	for _, report := range reports {
		if !report.Detected {
			continue
		}
		servers := desiredMCPServersForAgent(cfg, report.Name)
		if len(servers) == 0 {
			continue
		}
		byName := make(map[string]mcpServerConfig, len(servers))
		for _, server := range servers {
			byName[server.Name] = server
		}
		for _, name := range append(report.AddsMCP, report.UpdatesMCP...) {
			server, ok := byName[name]
			if !ok {
				return fmt.Errorf("missing MCP config for %s/%s", report.Name, name)
			}
			if err := patchMCPServer(report.Name, server, home); err != nil {
				return err
			}
		}
	}
	return nil
}

func inspectMCPServer(agentName string, server mcpServerConfig, home string) (string, error) {
	target, err := mcpTargetForHarness(agentName)
	if err != nil {
		return stateMissing, err
	}
	return target.inspect(target, server, home)
}

func patchMCPServer(agentName string, server mcpServerConfig, home string) error {
	target, err := mcpTargetForHarness(agentName)
	if err != nil {
		return err
	}
	return target.patch(target, server, home)
}

func readNativeMCPServer(agentName string, name string, home string) (mcpServerConfig, error) {
	target, err := mcpTargetForHarness(agentName)
	if err != nil {
		return mcpServerConfig{}, err
	}
	return target.read(target, name, home)
}

func inspectJSONMCPServer(target mcpTarget, server mcpServerConfig, home string) (string, error) {
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
	return inspectMapMCPServer(jsonMCPParent(raw, target), target.rootKey, server, target.defaults), nil
}

// jsonMCPParent returns the object that holds target.rootKey: the document
// itself, or the object under target.parentKey (empty when absent).
func jsonMCPParent(raw map[string]interface{}, target mcpTarget) map[string]interface{} {
	if target.parentKey == "" {
		return raw
	}
	parent, _ := asMap(raw[target.parentKey])
	if parent == nil {
		return map[string]interface{}{}
	}
	return parent
}

func patchJSONMCPServer(target mcpTarget, server mcpServerConfig, home string) error {
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
	parent := raw
	if target.parentKey != "" {
		if existing, ok := raw[target.parentKey]; ok {
			if _, isMap := asMap(existing); !isMap {
				return fmt.Errorf("%s: %q is not an object", configPath, target.parentKey)
			}
		}
		parent = jsonMCPParent(raw, target)
		raw[target.parentKey] = parent
	}
	upsertMapMCPServer(parent, target.rootKey, server, target.defaults)

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

func readJSONMCPServer(target mcpTarget, name string, home string) (mcpServerConfig, error) {
	configPath := target.configPath(home)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return mcpServerConfig{}, fmt.Errorf("read %s: %w", configPath, err)
	}
	var raw map[string]interface{}
	if err := parseJSONConfig(configPath, data, &raw); err != nil {
		return mcpServerConfig{}, fmt.Errorf("parse %s: %w", configPath, err)
	}
	entry, ok := mapMCPEntry(jsonMCPParent(raw, target), target.rootKey, name)
	if !ok {
		return mcpServerConfig{}, fmt.Errorf("MCP server %q not found in %s", name, target.agentName)
	}
	if err := validateNativeDefaults(target, entry); err != nil {
		return mcpServerConfig{}, err
	}
	return mcpServerFromMap(name, entry)
}

func readClaudeMCPServer(target mcpTarget, name string, home string) (mcpServerConfig, error) {
	configPath := target.configPath(home)
	data, err := os.ReadFile(configPath)
	raw := map[string]interface{}{}
	if err != nil && !os.IsNotExist(err) {
		return mcpServerConfig{}, fmt.Errorf("read %s: %w", configPath, err)
	} else if err == nil {
		if err := parseJSONConfig(configPath, data, &raw); err != nil {
			return mcpServerConfig{}, fmt.Errorf("parse %s: %w", configPath, err)
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return mcpServerConfig{}, fmt.Errorf("resolve cwd: %w", err)
	}
	if entry, ok := claudeProjectMCPEntry(raw, name, cwd); ok {
		if err := validateNativeDefaults(target, entry); err != nil {
			return mcpServerConfig{}, err
		}
		return mcpServerFromMap(name, entry)
	}
	if entry, ok, err := claudeMCPJSONEntry(name, cwd); err != nil {
		return mcpServerConfig{}, err
	} else if ok {
		if err := validateNativeDefaults(target, entry); err != nil {
			return mcpServerConfig{}, err
		}
		return mcpServerFromMap(name, entry)
	}
	if entry, ok := mapMCPEntry(raw, target.rootKey, name); ok {
		if err := validateNativeDefaults(target, entry); err != nil {
			return mcpServerConfig{}, err
		}
		return mcpServerFromMap(name, entry)
	}
	return mcpServerConfig{}, fmt.Errorf("MCP server %q not found in %s", name, target.agentName)
}

func claudeMCPJSONEntry(name string, cwd string) (map[string]interface{}, bool, error) {
	dir := filepath.Clean(cwd)
	for {
		configPath := filepath.Join(dir, ".mcp.json")
		data, err := os.ReadFile(configPath)
		if err == nil {
			var raw map[string]interface{}
			if err := parseJSONConfig(configPath, data, &raw); err != nil {
				return nil, false, fmt.Errorf("parse %s: %w", configPath, err)
			}
			if entry, ok := mapMCPEntry(raw, "mcpServers", name); ok {
				return entry, true, nil
			}
		} else if !os.IsNotExist(err) {
			return nil, false, fmt.Errorf("read %s: %w", configPath, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, false, nil
		}
		dir = parent
	}
}

func inspectYAMLMCPServer(target mcpTarget, server mcpServerConfig, home string) (string, error) {
	configPath := target.configPath(home)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return stateMissing, nil
		}
		return stateMissing, fmt.Errorf("read %s: %w", configPath, err)
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return stateMissing, fmt.Errorf("parse %s: %w", configPath, err)
	}
	return inspectMapMCPServer(raw, target.rootKey, server, target.defaults), nil
}

func patchYAMLMCPServer(target mcpTarget, server mcpServerConfig, home string) error {
	configPath := target.configPath(home)
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", configPath, err)
	}
	var doc yaml.Node
	if err == nil {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("parse %s: %w", configPath, err)
		}
	}
	if err := upsertYAMLMCPNode(&doc, target.rootKey, server, target.defaults); err != nil {
		return fmt.Errorf("update %s: %w", configPath, err)
	}
	out, err := marshalYAMLNode(&doc)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", configPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(configPath), err)
	}
	if err := os.WriteFile(configPath, out, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", configPath, err)
	}
	return nil
}

func readYAMLMCPServer(target mcpTarget, name string, home string) (mcpServerConfig, error) {
	configPath := target.configPath(home)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return mcpServerConfig{}, fmt.Errorf("read %s: %w", configPath, err)
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return mcpServerConfig{}, fmt.Errorf("parse %s: %w", configPath, err)
	}
	entry, ok := mapMCPEntry(raw, target.rootKey, name)
	if !ok {
		return mcpServerConfig{}, fmt.Errorf("MCP server %q not found in %s", name, target.agentName)
	}
	return mcpServerFromMap(name, entry)
}

func inspectMapMCPServer(raw map[string]interface{}, rootKey string, server mcpServerConfig, defaults map[string]interface{}) string {
	serversRaw, ok := raw[rootKey]
	if !ok {
		return stateMissing
	}
	serversMap, ok := asMap(serversRaw)
	if !ok {
		return stateDrifted
	}
	entryRaw, ok := serversMap[server.Name]
	if !ok {
		return stateMissing
	}
	entry, ok := asMap(entryRaw)
	if !ok {
		return stateDrifted
	}
	if !matchManagedMCPMap(entry, server, defaults) {
		return stateDrifted
	}
	return stateSynced
}

func mapMCPEntry(raw map[string]interface{}, rootKey string, name string) (map[string]interface{}, bool) {
	serversRaw, ok := raw[rootKey]
	if !ok {
		return nil, false
	}
	serversMap, ok := asMap(serversRaw)
	if !ok {
		return nil, false
	}
	entryRaw, ok := serversMap[name]
	if !ok {
		return nil, false
	}
	entry, ok := asMap(entryRaw)
	return entry, ok
}

func claudeProjectMCPEntry(raw map[string]interface{}, name string, cwd string) (map[string]interface{}, bool) {
	projectsRaw, ok := raw["projects"]
	if !ok {
		return nil, false
	}
	projects, ok := asMap(projectsRaw)
	if !ok {
		return nil, false
	}
	cwd = filepath.Clean(cwd)
	var projectKeys []string
	for projectPath := range projects {
		if pathInProject(cwd, projectPath) {
			projectKeys = append(projectKeys, projectPath)
		}
	}
	sort.Slice(projectKeys, func(i, j int) bool {
		if len(projectKeys[i]) == len(projectKeys[j]) {
			return projectKeys[i] < projectKeys[j]
		}
		return len(projectKeys[i]) > len(projectKeys[j])
	})
	for _, projectPath := range projectKeys {
		projectRaw := projects[projectPath]
		project, ok := asMap(projectRaw)
		if !ok {
			continue
		}
		if entry, ok := mapMCPEntry(project, "mcpServers", name); ok {
			return entry, true
		}
	}
	return nil, false
}

func pathInProject(cwd string, projectPath string) bool {
	cwd = canonicalPath(cwd)
	projectPath = canonicalPath(projectPath)
	return cwd == projectPath || strings.HasPrefix(cwd, projectPath+string(os.PathSeparator))
}

func canonicalPath(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func upsertMapMCPServer(raw map[string]interface{}, rootKey string, server mcpServerConfig, defaults map[string]interface{}) {
	serversMap, _ := asMap(raw[rootKey])
	if serversMap == nil {
		serversMap = map[string]interface{}{}
		raw[rootKey] = serversMap
	}
	entry, _ := asMap(serversMap[server.Name])
	if entry == nil {
		entry = map[string]interface{}{}
	}
	applyManagedMCPMap(entry, server, defaults)
	serversMap[server.Name] = entry
}

func parseJSONConfig(path string, data []byte, v interface{}) error {
	if strings.EqualFold(filepath.Ext(path), ".jsonc") {
		data = removeTrailingJSONCommas(stripJSONComments(data))
	}
	return json.Unmarshal(data, v)
}

func stripJSONComments(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	escaped := false
	lineComment := false
	blockComment := false

	for i := 0; i < len(data); i++ {
		ch := data[i]
		var next byte
		if i+1 < len(data) {
			next = data[i+1]
		}

		if lineComment {
			if ch == '\n' || ch == '\r' {
				lineComment = false
				out = append(out, ch)
			}
			continue
		}
		if blockComment {
			if ch == '*' && next == '/' {
				blockComment = false
				i++
			}
			continue
		}
		if inString {
			out = append(out, ch)
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}

		if ch == '"' {
			inString = true
			out = append(out, ch)
			continue
		}
		if ch == '/' && next == '/' {
			lineComment = true
			i++
			continue
		}
		if ch == '/' && next == '*' {
			blockComment = true
			i++
			continue
		}
		out = append(out, ch)
	}
	return out
}

func removeTrailingJSONCommas(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		ch := data[i]
		if inString {
			out = append(out, ch)
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			out = append(out, ch)
			continue
		}
		if ch == ',' {
			j := i + 1
			for j < len(data) && (data[j] == ' ' || data[j] == '\t' || data[j] == '\n' || data[j] == '\r') {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue
			}
		}
		out = append(out, ch)
	}
	return out
}

// errCodexEntryRepairable means the file only fails to parse because some
// [mcp_servers.NAME] table appears more than once (an older release could
// write duplicates) and NAME is one of them; rewriting it repairs the file.
var errCodexEntryRepairable = errors.New("managed MCP sections need repair")

var tomlMCPServerHeader = regexp.MustCompile(`^\[mcp_servers\.([A-Za-z0-9_-]+)\]\s*(#.*)?$`)

// duplicatedMCPServerNames lists server names whose table header repeats.
func duplicatedMCPServerNames(content string) []string {
	counts := map[string]int{}
	var order []string
	for _, line := range strings.Split(content, "\n") {
		m := tomlMCPServerHeader.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		if counts[m[1]] == 0 {
			order = append(order, m[1])
		}
		counts[m[1]]++
	}
	var dups []string
	for _, name := range order {
		if counts[name] > 1 {
			dups = append(dups, name)
		}
	}
	return dups
}

// mergedDuplicateEntry decodes every copy of one server's sections and
// merges them (later copies win, env tables are combined), so a repair keeps
// whatever env values the duplicates held.
func mergedDuplicateEntry(content string, name string) map[string]interface{} {
	header := fmt.Sprintf("[mcp_servers.%s]", name)
	merged := map[string]interface{}{}
	env := map[string]interface{}{}
	for {
		start := indexTOMLSectionHeader(content, header)
		if start == -1 {
			break
		}
		end := endTOMLSectionIncludingDescendants(content, start, header)
		var doc map[string]interface{}
		if _, err := toml.Decode(content[start:end], &doc); err == nil {
			servers, _ := doc["mcp_servers"].(map[string]interface{})
			if entry, ok := servers[name].(map[string]interface{}); ok {
				for key, value := range entry {
					if key == "env" {
						if table, ok := value.(map[string]interface{}); ok {
							for k, v := range table {
								env[k] = v
							}
						}
						continue
					}
					merged[key] = value
				}
			}
		}
		content = content[:start] + content[end:]
	}
	if len(env) > 0 {
		merged["env"] = env
	}
	return merged
}

// decodeCodexMCPEntry parses the whole TOML file (so a broken file is
// reported instead of appended to) and returns one mcp_servers entry.
// Duplicated server tables are set aside first: a file that parses without
// them is still readable, and the duplicated servers are repairable.
func decodeCodexMCPEntry(path string, data []byte, name string) (map[string]interface{}, bool, error) {
	content := string(data)
	var doc map[string]interface{}
	_, parseErr := toml.Decode(content, &doc)
	if parseErr != nil {
		dups := duplicatedMCPServerNames(content)
		if len(dups) == 0 {
			return nil, false, fmt.Errorf("parse %s: %w", path, parseErr)
		}
		rest := content
		for _, dup := range dups {
			rest = removeTOMLSectionsIncludingDescendants(rest, fmt.Sprintf("[mcp_servers.%s]", dup))
		}
		doc = nil
		if _, err := toml.Decode(rest, &doc); err != nil {
			return nil, false, fmt.Errorf("parse %s: %w", path, parseErr)
		}
		if stringInSlice(name, dups) {
			return mergedDuplicateEntry(content, name), true, errCodexEntryRepairable
		}
	}
	servers, _ := doc["mcp_servers"].(map[string]interface{})
	entry, ok := servers[name].(map[string]interface{})
	return entry, ok, nil
}

func tomlEntryEnv(entry map[string]interface{}) map[string]string {
	raw, _ := entry["env"].(map[string]interface{})
	if len(raw) == 0 {
		return nil
	}
	env := make(map[string]string, len(raw))
	for key, value := range raw {
		if s, ok := value.(string); ok {
			env[key] = s
		}
	}
	return env
}

func inspectCodexMCPServer(target mcpTarget, server mcpServerConfig, home string) (string, error) {
	configPath := target.configPath(home)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return stateMissing, nil
		}
		return stateMissing, fmt.Errorf("read %s: %w", configPath, err)
	}
	entry, ok, err := decodeCodexMCPEntry(configPath, data, server.Name)
	if errors.Is(err, errCodexEntryRepairable) {
		return stateDrifted, nil
	}
	if err != nil {
		return stateMissing, err
	}
	if !ok {
		return stateMissing, nil
	}
	command, _ := entry["command"].(string)
	args, _ := toStringSlice(entry["args"])
	if command != server.Command || !stringSlicesEqual(args, server.Args) || !envSatisfied(server.Env, tomlEntryEnv(entry)) {
		return stateDrifted, nil
	}
	return stateSynced, nil
}

func patchCodexMCPServer(target mcpTarget, server mcpServerConfig, home string) error {
	configPath := target.configPath(home)
	data, err := os.ReadFile(configPath)
	content := ""
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read %s: %w", configPath, err)
		}
	} else {
		content = string(data)
		entry, _, err := decodeCodexMCPEntry(configPath, data, server.Name)
		if err != nil && !errors.Is(err, errCodexEntryRepairable) {
			return err
		}
		server.Env = mergeManagedEnv(tomlEntryEnv(entry), server.Env)
	}
	header := fmt.Sprintf("[mcp_servers.%s]", server.Name)
	section := renderCodexMCPSection(server)
	updated := upsertTOMLSectionIncludingDescendants(content, header, section)
	if updated == content {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(configPath), err)
	}
	if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", configPath, err)
	}
	return nil
}

func readCodexMCPServer(target mcpTarget, name string, home string) (mcpServerConfig, error) {
	configPath := target.configPath(home)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return mcpServerConfig{}, fmt.Errorf("read %s: %w", configPath, err)
	}
	entry, ok, err := decodeCodexMCPEntry(configPath, data, name)
	if err != nil && !errors.Is(err, errCodexEntryRepairable) {
		return mcpServerConfig{}, err
	}
	if !ok {
		return mcpServerConfig{}, fmt.Errorf("MCP server %q not found in %s", name, target.agentName)
	}
	command, _ := entry["command"].(string)
	if strings.TrimSpace(command) == "" {
		return mcpServerConfig{}, fmt.Errorf("MCP server %q in %s has no stdio command", name, target.agentName)
	}
	args, _ := toStringSlice(entry["args"])
	// Inline `env = {...}` and an [mcp_servers.NAME.env] table both land here.
	return mcpServerConfig{Name: name, Enabled: true, Command: command, Args: args, Env: tomlEntryEnv(entry)}, nil
}

func renderCodexMCPSection(server mcpServerConfig) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("[mcp_servers.%s]", server.Name))
	lines = append(lines, fmt.Sprintf("command = %q", server.Command))
	lines = append(lines, fmt.Sprintf("args = %s", renderTOMLStringArray(server.Args)))
	if len(server.Env) > 0 {
		lines = append(lines, fmt.Sprintf("env = %s", renderTOMLEnvInline(server.Env)))
	}
	return strings.Join(lines, "\n") + "\n\n"
}

func parseTOMLBlockValues(block string) map[string]string {
	values := make(map[string]string)
	var currentKey string
	var currentValue strings.Builder
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "[") {
			continue
		}
		if currentKey != "" {
			if currentValue.Len() > 0 {
				currentValue.WriteString("\n")
			}
			currentValue.WriteString(trimmed)
			if tomlValueComplete(currentValue.String()) {
				values[currentKey] = strings.TrimSpace(currentValue.String())
				currentKey = ""
				currentValue.Reset()
			}
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if tomlValueComplete(value) {
			values[key] = value
			continue
		}
		currentKey = key
		currentValue.WriteString(value)
	}
	if currentKey != "" {
		values[currentKey] = strings.TrimSpace(currentValue.String())
	}
	return values
}

func tomlValueComplete(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return true
	}
	if strings.HasPrefix(trimmed, "[") && !balancedTOMLDelimiters(trimmed, '[', ']') {
		return false
	}
	if strings.HasPrefix(trimmed, "{") && !balancedTOMLDelimiters(trimmed, '{', '}') {
		return false
	}
	return true
}

func balancedTOMLDelimiters(raw string, open rune, close rune) bool {
	depth := 0
	var quote rune
	escaped := false
	for _, r := range raw {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && quote == '"' {
			escaped = true
			continue
		}
		if (r == '"' || r == '\'') && quote == 0 {
			quote = r
			continue
		}
		if r == quote {
			quote = 0
			continue
		}
		if quote != 0 {
			continue
		}
		if r == open {
			depth++
			continue
		}
		if r == close {
			depth--
		}
	}
	return depth <= 0
}

func extractTOMLSection(content string, header string) (string, bool) {
	start := indexTOMLSectionHeader(content, header)
	if start == -1 {
		return "", false
	}
	return content[start:endTOMLSection(content, start, header)], true
}

func upsertTOMLSection(content string, header string, section string) string {
	start := indexTOMLSectionHeader(content, header)
	if start == -1 {
		insertAt := findTOMLInsertPoint(content)
		if insertAt == -1 {
			if strings.TrimSpace(content) == "" {
				return section
			}
			if strings.HasSuffix(content, "\n\n") {
				return content + section
			}
			if strings.HasSuffix(content, "\n") {
				return content + "\n" + section
			}
			return content + "\n\n" + section
		}
		prefix := content[:insertAt]
		suffix := content[insertAt:]
		if prefix != "" && !strings.HasSuffix(prefix, "\n\n") {
			if strings.HasSuffix(prefix, "\n") {
				prefix += "\n"
			} else {
				prefix += "\n\n"
			}
		}
		return prefix + section + suffix
	}
	cleaned := removeTOMLSections(content, header)
	return cleaned[:start] + ensureTrailingBlankLine(section) + cleaned[start:]
}

func upsertTOMLSectionIncludingDescendants(content string, header string, section string) string {
	start := indexTOMLSectionHeader(content, header)
	if start == -1 {
		return upsertTOMLSection(content, header, section)
	}
	cleaned := removeTOMLSectionsIncludingDescendants(content, header)
	return cleaned[:start] + ensureTrailingBlankLine(section) + cleaned[start:]
}

func removeTOMLSections(content string, header string) string {
	var out strings.Builder
	cursor := 0
	for {
		startRel := indexTOMLSectionHeader(content[cursor:], header)
		if startRel == -1 {
			out.WriteString(content[cursor:])
			return out.String()
		}
		start := cursor + startRel
		out.WriteString(content[cursor:start])
		cursor = endTOMLSection(content, start, header)
	}
}

func removeTOMLSectionsIncludingDescendants(content string, header string) string {
	var out strings.Builder
	cursor := 0
	for {
		startRel := indexTOMLSectionHeader(content[cursor:], header)
		if startRel == -1 {
			out.WriteString(content[cursor:])
			return out.String()
		}
		start := cursor + startRel
		out.WriteString(content[cursor:start])
		cursor = endTOMLSectionIncludingDescendants(content, start, header)
	}
}

func indexTOMLSectionHeader(content string, header string) int {
	for i := 0; i < len(content); i++ {
		if i != 0 && content[i-1] != '\n' {
			continue
		}
		if !strings.HasPrefix(content[i:], header) {
			continue
		}
		end := i + len(header)
		if tomlSectionHeaderSuffixMatches(content, end) {
			return i
		}
	}
	return -1
}

func tomlSectionHeaderSuffixMatches(content string, start int) bool {
	if start == len(content) {
		return true
	}
	lineEnd := start
	for lineEnd < len(content) && content[lineEnd] != '\n' && content[lineEnd] != '\r' {
		lineEnd++
	}
	suffix := strings.TrimSpace(content[start:lineEnd])
	if suffix != "" && !strings.HasPrefix(suffix, "#") {
		return false
	}
	if lineEnd == len(content) {
		return true
	}
	if content[lineEnd] == '\n' {
		return true
	}
	return lineEnd+1 == len(content) || content[lineEnd+1] == '\n'
}

func endTOMLSection(content string, start int, header string) int {
	searchStart := start + len(header)
	if searchStart < len(content) && content[searchStart] == '\r' {
		searchStart++
	}
	if searchStart < len(content) && content[searchStart] == '\n' {
		searchStart++
	}
	endRel := indexNextTOMLHeader(content[searchStart:])
	if endRel == -1 {
		return len(content)
	}
	return searchStart + endRel
}

func endTOMLSectionIncludingDescendants(content string, start int, header string) int {
	searchStart := start + len(header)
	if searchStart < len(content) && content[searchStart] == '\r' {
		searchStart++
	}
	if searchStart < len(content) && content[searchStart] == '\n' {
		searchStart++
	}
	descendantPrefix := strings.TrimSuffix(header, "]") + "."
	for {
		endRel := indexNextTOMLHeader(content[searchStart:])
		if endRel == -1 {
			return len(content)
		}
		end := searchStart + endRel
		if !strings.HasPrefix(content[end:], descendantPrefix) {
			return end
		}
		searchStart = end + 1
	}
}

func findTOMLInsertPoint(content string) int {
	candidates := []string{"[profiles.", "[projects.", "[tui]", "[analytics]", "[notice]", "[[skills.config]]", "[env]", "[agents]", "[marketplaces."}
	best := -1
	for _, candidate := range candidates {
		idx := indexTOMLHeaderCandidate(content, candidate)
		if idx != -1 && (best == -1 || idx < best) {
			best = idx
		}
	}
	return best
}

func indexTOMLHeaderCandidate(content string, candidate string) int {
	for i := 0; i < len(content); i++ {
		if !strings.HasPrefix(content[i:], candidate) {
			continue
		}
		if i == 0 || content[i-1] == '\n' {
			return i
		}
	}
	return -1
}

func indexNextTOMLHeader(content string) int {
	for i := 0; i < len(content); i++ {
		if content[i] != '[' {
			continue
		}
		if i == 0 || content[i-1] == '\n' {
			return i
		}
	}
	return -1
}

func ensureTrailingBlankLine(section string) string {
	section = strings.TrimRight(section, "\n") + "\n"
	if !strings.HasSuffix(section, "\n\n") {
		section += "\n"
	}
	return section
}

func renderTOMLStringArray(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, item := range items {
		quoted = append(quoted, fmt.Sprintf("%q", item))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func renderTOMLEnvInline(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s = %q", key, env[key]))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func parseTOMLString(raw string) (string, error) {
	trimmed := strings.TrimSpace(stripTOMLInlineComments(raw))
	if trimmed == "" {
		return "", fmt.Errorf("empty string")
	}
	if strings.HasPrefix(trimmed, "'") && strings.HasSuffix(trimmed, "'") {
		return strings.TrimSuffix(strings.TrimPrefix(trimmed, "'"), "'"), nil
	}
	return strconv.Unquote(trimmed)
}

func stripTOMLInlineComments(raw string) string {
	var out strings.Builder
	var quote rune
	escaped := false
	inComment := false
	for _, r := range raw {
		if inComment {
			if r == '\n' {
				inComment = false
				out.WriteRune(r)
			}
			continue
		}
		if escaped {
			out.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote == '"' {
			out.WriteRune(r)
			escaped = true
			continue
		}
		if (r == '"' || r == '\'') && quote == 0 {
			quote = r
			out.WriteRune(r)
			continue
		}
		if r == quote {
			quote = 0
			out.WriteRune(r)
			continue
		}
		if r == '#' && quote == 0 {
			inComment = true
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func matchManagedMCPMap(entry map[string]interface{}, server mcpServerConfig, defaults map[string]interface{}) bool {
	if err := validateNativeDefaults(mcpTarget{defaults: defaults}, entry); err != nil {
		return false
	}
	command, _ := entry["command"].(string)
	if command != server.Command {
		return false
	}
	args, ok := toStringSlice(entry["args"])
	if !ok || !stringSlicesEqual(args, server.Args) {
		return false
	}
	if len(server.Env) == 0 {
		return true
	}
	envMap, ok := asMap(entry["env"])
	if !ok {
		return false
	}
	native := make(map[string]string, len(envMap))
	for key, value := range envMap {
		if s, ok := value.(string); ok {
			native[key] = s
		}
	}
	return envSatisfied(server.Env, native)
}

// envSatisfied reports whether an agent's native env covers the canonical
// one. A ${KEY} reference only records that the server needs KEY, so any
// value the agent already has satisfies it.
func envSatisfied(canonical map[string]string, native map[string]string) bool {
	for key, expected := range canonical {
		actual, ok := native[key]
		if !ok {
			return false
		}
		if isEnvReference(expected) {
			if strings.TrimSpace(actual) == "" {
				return false
			}
			continue
		}
		if actual != expected {
			return false
		}
	}
	return true
}

// mergeManagedEnv returns the env to write for a managed server: keys the
// agent already has stay, a literal canonical value wins, and a ${KEY}
// reference never replaces a value the agent already has, so sync cannot
// swap a working secret for a placeholder the agent may not expand.
func mergeManagedEnv(native map[string]string, canonical map[string]string) map[string]string {
	if len(native) == 0 && len(canonical) == 0 {
		return nil
	}
	merged := make(map[string]string, len(native)+len(canonical))
	for key, value := range native {
		merged[key] = value
	}
	for key, value := range canonical {
		if existing, ok := native[key]; ok && strings.TrimSpace(existing) != "" && isEnvReference(value) {
			continue
		}
		merged[key] = value
	}
	return merged
}

func applyManagedMCPMap(entry map[string]interface{}, server mcpServerConfig, defaults map[string]interface{}) {
	entry["command"] = server.Command
	args := make([]interface{}, 0, len(server.Args))
	for _, arg := range server.Args {
		args = append(args, arg)
	}
	entry["args"] = args
	if len(server.Env) > 0 {
		envMap, _ := asMap(entry["env"])
		if envMap == nil {
			envMap = map[string]interface{}{}
		}
		for key, value := range server.Env {
			if existing, ok := envMap[key].(string); ok && strings.TrimSpace(existing) != "" && isEnvReference(value) {
				continue // keep the agent's real value; see mergeManagedEnv
			}
			envMap[key] = value
		}
		entry["env"] = envMap
	}
	for key, value := range defaults {
		entry[key] = value
	}
}

func mcpServerFromMap(name string, entry map[string]interface{}) (mcpServerConfig, error) {
	command, _ := entry["command"].(string)
	if strings.TrimSpace(command) == "" {
		return mcpServerConfig{}, fmt.Errorf("MCP server %q has no stdio command", name)
	}
	args, ok := toStringSlice(entry["args"])
	if !ok {
		args = nil
	}
	env := map[string]string(nil)
	if envRaw, ok := entry["env"]; ok {
		envMap, ok := asMap(envRaw)
		if !ok {
			return mcpServerConfig{}, fmt.Errorf("MCP server %q env is not a map", name)
		}
		env = make(map[string]string, len(envMap))
		for key, value := range envMap {
			str, ok := value.(string)
			if !ok {
				return mcpServerConfig{}, fmt.Errorf("MCP server %q env %s is not a string", name, key)
			}
			env[key] = str
		}
	}
	return mcpServerConfig{Name: name, Enabled: true, Command: command, Args: args, Env: env}, nil
}

func validateNativeDefaults(target mcpTarget, entry map[string]interface{}) error {
	for key, expected := range target.defaults {
		actual, ok := entry[key]
		if !ok && key == "disabled" && expected == false {
			continue
		}
		if !ok || actual != expected {
			return fmt.Errorf("MCP server is not supported stdio shape: %s must be %v", key, expected)
		}
	}
	return nil
}

func asMap(v interface{}) (map[string]interface{}, bool) {
	switch t := v.(type) {
	case map[string]interface{}:
		return t, true
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(t))
		for key, value := range t {
			keyStr, ok := key.(string)
			if !ok {
				return nil, false
			}
			out[keyStr] = value
		}
		return out, true
	default:
		return nil, false
	}
}

func toStringSlice(v interface{}) ([]string, bool) {
	switch t := v.(type) {
	case nil:
		return nil, true
	case []string:
		return append([]string{}, t...), true
	case []interface{}:
		out := make([]string, 0, len(t))
		for _, item := range t {
			str, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, str)
		}
		return out, true
	default:
		return nil, false
	}
}

func stringSlicesEqual(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func upsertYAMLMCPNode(doc *yaml.Node, rootKey string, server mcpServerConfig, defaults map[string]interface{}) error {
	root := doc
	if root.Kind == 0 {
		root.Kind = yaml.DocumentNode
	}
	if root.Kind != yaml.DocumentNode {
		return fmt.Errorf("unexpected YAML root kind %d", root.Kind)
	}
	if len(root.Content) == 0 {
		root.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: yamlMapTag}}
	}
	mapping := root.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return fmt.Errorf("top-level YAML node is not a mapping")
	}

	serversNode := ensureMappingValue(mapping, rootKey)
	entryNode := ensureMappingValue(serversNode, server.Name)
	setMappingString(entryNode, "command", server.Command)
	setMappingStringSlice(entryNode, "args", server.Args)
	if len(server.Env) > 0 {
		envNode := ensureMappingValue(entryNode, "env")
		setMappingStringMap(envNode, server.Env)
	}
	for key, value := range defaults {
		setMappingScalar(entryNode, key, value)
	}
	return nil
}

func marshalYAMLNode(doc *yaml.Node) ([]byte, error) {
	var builder strings.Builder
	encoder := yaml.NewEncoder(&builder)
	encoder.SetIndent(4)
	if err := encoder.Encode(doc); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return []byte(builder.String()), nil
}

func ensureMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping.Kind != yaml.MappingNode {
		mapping.Kind = yaml.MappingNode
		mapping.Tag = yamlMapTag
		mapping.Content = nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		keyNode := mapping.Content[i]
		if keyNode.Value == key {
			valueNode := mapping.Content[i+1]
			if valueNode.Kind == 0 {
				valueNode.Kind = yaml.MappingNode
				valueNode.Tag = yamlMapTag
			}
			return valueNode
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valueNode := &yaml.Node{Kind: yaml.MappingNode, Tag: yamlMapTag}
	mapping.Content = append(mapping.Content, keyNode, valueNode)
	return valueNode
}

func setMappingString(mapping *yaml.Node, key string, value string) {
	setMappingNode(mapping, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

func setMappingScalar(mapping *yaml.Node, key string, value interface{}) {
	switch t := value.(type) {
	case bool:
		setMappingNode(mapping, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(t)})
	case string:
		setMappingString(mapping, key, t)
	default:
		setMappingString(mapping, key, fmt.Sprint(t))
	}
}

func setMappingStringSlice(mapping *yaml.Node, key string, values []string) {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, value := range values {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
	}
	setMappingNode(mapping, key, seq)
}

func setMappingStringMap(mapping *yaml.Node, values map[string]string) {
	if mapping.Kind != yaml.MappingNode {
		mapping.Kind = yaml.MappingNode
		mapping.Tag = yamlMapTag
		mapping.Content = nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		setMappingString(mapping, key, values[key])
	}
}

func setMappingNode(mapping *yaml.Node, key string, value *yaml.Node) {
	if mapping.Kind != yaml.MappingNode {
		mapping.Kind = yaml.MappingNode
		mapping.Tag = yamlMapTag
		mapping.Content = nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
}

func stringInSlice(needle string, haystack []string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}
