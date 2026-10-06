package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// DeepSeek Harness (dsh) keeps MCP servers as `@deepseek-ai/dsh-mcp-client`
// rows in Cordis patch files. tackroom owns the rows whose id starts with
// dshMCPRowPrefix in the home-level patch layer, which applies to every
// profile; every other row and patch op in that file is left untouched.
const (
	dshMCPClientPlugin = "@deepseek-ai/dsh-mcp-client"
	dshMCPRowPrefix    = "tackroom-mcp-"
)

// dshServerNamePattern is the serverName constraint from the dsh-mcp-client
// config catalog.
var dshServerNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// dshHome resolves the DeepSeek Harness home, honoring $DSH_HOME.
func dshHome(home string) string {
	if env := strings.TrimSpace(os.Getenv("DSH_HOME")); env != "" {
		return env
	}
	return filepath.Join(home, ".dsh")
}

func dshPatchPath(home string) string {
	return filepath.Join(dshHome(home), "cordis.patch.yml")
}

// dshReadsAgentsSkills reports whether dsh's filesystem skill provider already
// scans tackroom's skills. It reads <agentsHome>/skills, where agentsHome is
// $DSH_AGENTS_HOME or ~/.agents.
func dshReadsAgentsSkills(repoRoot string, home string) bool {
	if env := strings.TrimSpace(os.Getenv("DSH_AGENTS_HOME")); env != "" {
		return sameResolvedPath(repoRoot, env)
	}
	return readsAgentsSkillsRoot(repoRoot, home)
}

type dshMCPRowConfig struct {
	ServerName string            `yaml:"serverName"`
	Transport  string            `yaml:"transport"`
	Command    string            `yaml:"command"`
	Args       []string          `yaml:"args,omitempty"`
	Env        map[string]string `yaml:"env,omitempty"`
}

type dshMCPRow struct {
	ID     string          `yaml:"id"`
	Name   string          `yaml:"name"`
	Config dshMCPRowConfig `yaml:"config"`
}

// loadDSHPatch parses the patch file as a YAML node tree so `!!js` values in
// rows tackroom does not own survive a rewrite. A missing file is an empty
// patch list.
func loadDSHPatch(path string) (*yaml.Node, error) {
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.SequenceNode, Tag: "!!seq"}}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return doc, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var parsed yaml.Node
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if parsed.Kind == 0 || len(parsed.Content) == 0 {
		return doc, nil
	}
	if parsed.Content[0].Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%s: expected a list of patch operations", path)
	}
	return &parsed, nil
}

// dshInsertLists returns the row lists of every `insert` op in the patch list.
func dshInsertLists(ops *yaml.Node) []*yaml.Node {
	var lists []*yaml.Node
	for _, op := range ops.Content {
		if op.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(op.Content); i += 2 {
			if op.Content[i].Value == "insert" && op.Content[i+1].Kind == yaml.SequenceNode {
				lists = append(lists, op.Content[i+1])
			}
		}
	}
	return lists
}

func dshRowID(row *yaml.Node) string {
	if row.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(row.Content); i += 2 {
		if row.Content[i].Value == "id" {
			return row.Content[i+1].Value
		}
	}
	return ""
}

// dshMappingValue returns the value node for key in a mapping node, or nil.
func dshMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// dshRowServerName returns the serverName of a dsh-mcp-client row, or "".
func dshRowServerName(row *yaml.Node) string {
	if plugin := dshMappingValue(row, "name"); plugin == nil || plugin.Value != dshMCPClientPlugin {
		return ""
	}
	if name := dshMappingValue(dshMappingValue(row, "config"), "serverName"); name != nil {
		return name.Value
	}
	return ""
}

// findDSHRow locates the row for an MCP server: tackroom's own row first,
// then any dsh-mcp-client row with that serverName, since dsh refuses two
// rows with the same serverName.
func findDSHRow(ops *yaml.Node, name string) (*yaml.Node, int) {
	lists := dshInsertLists(ops)
	for _, list := range lists {
		for i, row := range list.Content {
			if dshRowID(row) == dshMCPRowPrefix+name {
				return list, i
			}
		}
	}
	for _, list := range lists {
		for i, row := range list.Content {
			if dshRowServerName(row) == name {
				return list, i
			}
		}
	}
	return nil, -1
}

// dshMCPServerNames lists the serverName of every dsh-mcp-client row in the
// patch file, for setup's import scan.
func dshMCPServerNames(path string) ([]string, error) {
	doc, err := loadDSHPatch(path)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var names []string
	for _, list := range dshInsertLists(doc.Content[0]) {
		for _, row := range list.Content {
			if name := dshRowServerName(row); name != "" && !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

func readDSHRow(home string, name string) (dshMCPRow, bool, error) {
	path := dshPatchPath(home)
	doc, err := loadDSHPatch(path)
	if err != nil {
		return dshMCPRow{}, false, err
	}
	list, index := findDSHRow(doc.Content[0], name)
	if list == nil {
		return dshMCPRow{}, false, nil
	}
	var row dshMCPRow
	if err := list.Content[index].Decode(&row); err != nil {
		return dshMCPRow{}, true, fmt.Errorf("%s: decode MCP row %s: %w", path, name, err)
	}
	return row, true, nil
}

func inspectDSHMCPServer(_ mcpTarget, server mcpServerConfig, home string) (string, error) {
	row, found, err := readDSHRow(home, server.Name)
	if err != nil {
		if found {
			return stateDrifted, nil
		}
		return stateMissing, err
	}
	if !found {
		return stateMissing, nil
	}
	cfg := row.Config
	if row.Name != dshMCPClientPlugin || cfg.ServerName != server.Name || cfg.Transport != "stdio" ||
		cfg.Command != server.Command || !stringSlicesEqual(cfg.Args, server.Args) {
		return stateDrifted, nil
	}
	for key, expected := range server.Env {
		if cfg.Env[key] != expected {
			return stateDrifted, nil
		}
	}
	return stateSynced, nil
}

func patchDSHMCPServer(_ mcpTarget, server mcpServerConfig, home string) error {
	if !dshServerNamePattern.MatchString(server.Name) {
		return fmt.Errorf("dsh MCP server name %q must match [A-Za-z0-9_-]{1,32}", server.Name)
	}
	path := dshPatchPath(home)
	doc, err := loadDSHPatch(path)
	if err != nil {
		return err
	}
	ops := doc.Content[0]
	if list, index := findDSHRow(ops, server.Name); list != nil {
		// Update the existing row in place so its id and any other config
		// (cwd, timeouts, !!js values) stay as they are.
		row := list.Content[index]
		setMappingString(row, "name", dshMCPClientPlugin)
		cfg := ensureMappingValue(row, "config")
		setMappingString(cfg, "serverName", server.Name)
		setMappingString(cfg, "transport", "stdio")
		setMappingString(cfg, "command", server.Command)
		setMappingStringSlice(cfg, "args", server.Args)
		if len(server.Env) > 0 {
			setMappingStringMap(ensureMappingValue(cfg, "env"), server.Env)
		}
		return writeDSHPatch(path, doc)
	}
	var rowNode yaml.Node
	if err := rowNode.Encode(dshMCPRow{
		ID:   dshMCPRowPrefix + server.Name,
		Name: dshMCPClientPlugin,
		Config: dshMCPRowConfig{
			ServerName: server.Name,
			Transport:  "stdio",
			Command:    server.Command,
			Args:       server.Args,
			Env:        server.Env,
		},
	}); err != nil {
		return fmt.Errorf("encode dsh MCP row: %w", err)
	}

	if managed := dshManagedInsertList(ops); managed != nil {
		managed.Content = append(managed.Content, &rowNode)
	} else {
		ops.Content = append(ops.Content, &yaml.Node{Kind: yaml.MappingNode, Tag: yamlMapTag, Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "insert"},
			{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{&rowNode}},
		}})
	}
	return writeDSHPatch(path, doc)
}

func writeDSHPatch(path string, doc *yaml.Node) error {
	var builder strings.Builder
	encoder := yaml.NewEncoder(&builder)
	encoder.SetIndent(2)
	if err := encoder.Encode(doc); err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// dshManagedInsertList returns the insert list already holding tackroom rows,
// so new servers group with the existing ones instead of adding an op each.
func dshManagedInsertList(ops *yaml.Node) *yaml.Node {
	for _, list := range dshInsertLists(ops) {
		for _, row := range list.Content {
			if strings.HasPrefix(dshRowID(row), dshMCPRowPrefix) {
				return list
			}
		}
	}
	return nil
}

func readDSHMCPServer(target mcpTarget, name string, home string) (mcpServerConfig, error) {
	row, found, err := readDSHRow(home, name)
	if err != nil {
		return mcpServerConfig{}, err
	}
	if !found {
		return mcpServerConfig{}, fmt.Errorf("MCP server %q not found in %s", name, target.agentName)
	}
	if strings.TrimSpace(row.Config.Command) == "" {
		return mcpServerConfig{}, fmt.Errorf("MCP server %q has no stdio command", name)
	}
	return mcpServerConfig{Name: name, Enabled: true, Command: row.Config.Command, Args: row.Config.Args, Env: row.Config.Env}, nil
}
