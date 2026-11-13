package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const pluginMCPSchemaID = "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json"

var pluginMCPServerNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

type pluginMCPFile struct {
	Schema     string                     `json:"$schema"`
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
}

type pluginMCPServerRaw struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	Cwd     string            `json:"cwd"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

func discoverPluginMCPServers(pluginRoot string, manifestSchema string, pluginDataDir string) ([]mcpServerConfig, []string, error) {
	mcpPath := filepath.Join(pluginRoot, "mcp.json")
	if !hasFile(mcpPath) {
		return nil, nil, nil
	}

	data, err := os.ReadFile(mcpPath)
	if err != nil {
		return nil, []string{fmt.Sprintf("cannot read mcp.json: %v", err)}, nil
	}

	var file pluginMCPFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, []string{fmt.Sprintf("mcp.json invalid JSON: %v", err)}, nil
	}

	if file.Schema == "" {
		return nil, []string{"mcp.json missing required $schema"}, nil
	}
	if file.Schema != pluginMCPSchemaID {
		return nil, []string{fmt.Sprintf("mcp.json unsupported $schema %q", file.Schema)}, nil
	}
	if pluginSchemaVersion(file.Schema) != pluginSchemaVersion(manifestSchema) {
		return nil, []string{"mcp.json $schema version differs from plugin.json"}, nil
	}
	if file.MCPServers == nil {
		return nil, []string{"mcp.json missing required mcpServers"}, nil
	}

	resolvedRoot, err := filepath.EvalSymlinks(pluginRoot)
	if err != nil {
		return nil, []string{fmt.Sprintf("cannot resolve plugin root: %v", err)}, nil
	}

	var servers []mcpServerConfig
	var problems []string

	for name, rawServer := range file.MCPServers {
		var server pluginMCPServerRaw
		if err := json.Unmarshal(rawServer, &server); err != nil {
			problems = append(problems, fmt.Sprintf("server %q: invalid entry: %v", name, err))
			continue
		}

		if !pluginMCPServerNamePattern.MatchString(name) {
			problems = append(problems, fmt.Sprintf("server %q: name must match [a-z0-9][a-z0-9_-]*", name))
			continue
		}

		switch server.Type {
		case "stdio":
			// proceed
		case "streamable-http", "sse":
			problems = append(problems, fmt.Sprintf("server %q: unsupported transport %q", name, server.Type))
			continue
		default:
			problems = append(problems, fmt.Sprintf("server %q: unknown type %q", name, server.Type))
			continue
		}

		if strings.TrimSpace(server.Command) == "" {
			problems = append(problems, fmt.Sprintf("server %q: empty command", name))
			continue
		}

		if server.Cwd != "" {
			problems = append(problems, fmt.Sprintf("server %q: cwd unsupported; server skipped", name))
			continue
		}

		command := server.Command
		if strings.HasPrefix(command, "./") {
			absCmd := filepath.Join(pluginRoot, strings.TrimPrefix(command, "./"))
			resolved, err := filepath.EvalSymlinks(absCmd)
			if err != nil {
				problems = append(problems, fmt.Sprintf("server %q: cannot resolve command %q: %v", name, command, err))
				continue
			}
			if !strings.HasPrefix(resolved, resolvedRoot+string(os.PathSeparator)) && resolved != resolvedRoot {
				problems = append(problems, fmt.Sprintf("server %q: command %q escapes plugin root", name, command))
				continue
			}
			command = absCmd
		} else if strings.Contains(command, "/") || strings.HasPrefix(command, "..") {
			problems = append(problems, fmt.Sprintf("server %q: command must be a bare name or start with ./", name))
			continue
		}

		reservedEnv := false
		for key := range server.Env {
			if key == "PLUGIN_ROOT" || key == "PLUGIN_DATA" {
				problems = append(problems, fmt.Sprintf("server %q: env must not contain reserved key %q", name, key))
				reservedEnv = true
				break
			}
		}
		if reservedEnv {
			continue
		}

		expandedArgs := make([]string, len(server.Args))
		for i, arg := range server.Args {
			expandedArgs[i] = expandPluginPlaceholders(arg, pluginRoot, pluginDataDir)
		}

		expandedEnv := make(map[string]string, len(server.Env)+2)
		for k, v := range server.Env {
			expandedEnv[k] = expandPluginPlaceholders(v, pluginRoot, pluginDataDir)
		}
		expandedEnv["PLUGIN_ROOT"] = pluginRoot
		expandedEnv["PLUGIN_DATA"] = pluginDataDir

		servers = append(servers, mcpServerConfig{
			Name:    name,
			Enabled: true,
			Command: command,
			Args:    expandedArgs,
			Env:     expandedEnv,
		})
	}

	return servers, problems, nil
}

func expandPluginPlaceholders(s string, pluginRoot string, pluginData string) string {
	const rootPlaceholder = "${PLUGIN_ROOT}"
	const dataPlaceholder = "${PLUGIN_DATA}"

	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		idx := strings.Index(s[i:], "${")
		if idx < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+idx])
		rest := s[i+idx:]
		if strings.HasPrefix(rest, rootPlaceholder) {
			b.WriteString(pluginRoot)
			i += idx + len(rootPlaceholder)
		} else if strings.HasPrefix(rest, dataPlaceholder) {
			b.WriteString(pluginData)
			i += idx + len(dataPlaceholder)
		} else {
			b.WriteString("${")
			i += idx + 2
		}
	}
	return b.String()
}

// pluginSchemaVersion extracts the version segment from a schema URL like
// "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json" -> "1.0.0".
func pluginSchemaVersion(schemaURL string) string {
	const prefix = "https://agent-plugins.org/schemas/"
	if !strings.HasPrefix(schemaURL, prefix) {
		return schemaURL
	}
	rest := strings.TrimPrefix(schemaURL, prefix)
	if idx := strings.Index(rest, "/"); idx > 0 {
		return rest[:idx]
	}
	return rest
}

func externalDataDir(home string) string {
	return filepath.Join(home, ".agents", "external-data")
}

func injectPluginMCPServers(cfg *config, home string) {
	injectPluginMCPServersMode(cfg, home, true)
}

// injectPluginMCPServersMode is injectPluginMCPServers with control over
// creating each plugin's data directory; a dry run passes false.
func injectPluginMCPServersMode(cfg *config, home string, createDataDir bool) {
	cacheRoot := externalCacheDir(home)
	dataRoot := externalDataDir(home)

	existingNames := make(map[string]bool, len(cfg.MCPServers))
	for _, s := range cfg.MCPServers {
		existingNames[s.Name] = true
	}

	nativeCodex := map[string]bool{}
	nativePlugins, duplicates := codexPluginSources(*cfg, home)
	for _, p := range nativePlugins {
		nativeCodex[p.Source] = true
	}
	for _, d := range duplicates {
		fmt.Fprintf(os.Stderr, "warning: plugin name declared by several sources: %s; none is installed into Codex\n", d)
	}

	type injected struct {
		server     mcpServerConfig
		sourceName string
	}
	pluginServers := make(map[string][]injected)

	for _, src := range cfg.ExternalSkills {
		if !src.MCP {
			continue
		}
		name := sourceName(src)
		cachePath := filepath.Join(cacheRoot, name)
		if !hasDir(filepath.Join(cachePath, ".git")) {
			continue
		}
		if !hasPluginManifest(cachePath) {
			fmt.Fprintf(os.Stderr, "warning: external %s has mcp: true but no plugin.json; MCP ignored\n", name)
			continue
		}
		manifest, _, ok, err := parsePluginManifest(cachePath)
		if err != nil || !ok {
			detail := "invalid"
			if err != nil {
				detail = err.Error()
			}
			fmt.Fprintf(os.Stderr, "warning: external %s plugin.json %s; MCP ignored\n", name, detail)
			continue
		}

		pluginDataPath := filepath.Join(dataRoot, name)
		if createDataDir {
			if err := os.MkdirAll(pluginDataPath, 0o755); err != nil {
				fmt.Fprintf(os.Stderr, "warning: external %s: cannot create plugin data dir: %v; MCP ignored\n", name, err)
				continue
			}
		}

		servers, problems, err := discoverPluginMCPServers(cachePath, manifest.Schema, pluginDataPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: external %s mcp.json error: %v\n", name, err)
			continue
		}
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "warning: external %s: %s\n", name, p)
		}

		for _, s := range servers {
			if len(src.MCPAgents) > 0 {
				s.Agents = src.MCPAgents
			}
			pluginServers[s.Name] = append(pluginServers[s.Name], injected{server: s, sourceName: name})
		}
	}

	for serverName, sources := range pluginServers {
		if existingNames[serverName] {
			fmt.Fprintf(os.Stderr, "warning: plugin MCP server %q skipped; name conflicts with user-configured server\n", serverName)
			continue
		}
		if len(sources) > 1 {
			var names []string
			for _, s := range sources {
				names = append(names, s.sourceName)
			}
			fmt.Fprintf(os.Stderr, "warning: plugin MCP server %q skipped; declared by multiple sources: %s\n", serverName, strings.Join(names, ", "))
			continue
		}
		cfg.MCPServers = append(cfg.MCPServers, sources[0].server)
		if nativeCodex[sources[0].sourceName] {
			if cfg.codexPluginMCP == nil {
				cfg.codexPluginMCP = map[string]bool{}
			}
			cfg.codexPluginMCP[serverName] = true
		}
	}
}
