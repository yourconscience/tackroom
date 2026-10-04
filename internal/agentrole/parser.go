package agentrole

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func (role *Role) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("agent role must be a mapping")
	}
	type plainRole Role
	var decoded plainRole
	for i := 0; i+1 < len(value.Content); i += 2 {
		key, node := value.Content[i].Value, value.Content[i+1]
		if key == "tools" {
			tools, err := decodeTools(node)
			if err != nil {
				return err
			}
			decoded.Tools = tools
			continue
		}
		var err error
		// Decode fields individually so tools can retain scalar compatibility.
		switch key {
		case "name":
			err = node.Decode(&decoded.Name)
		case "description":
			err = node.Decode(&decoded.Description)
		case "model":
			err = node.Decode(&decoded.Model)
		case "effort":
			err = node.Decode(&decoded.Effort)
		case "color":
			err = node.Decode(&decoded.Color)
		case "claude":
			err = node.Decode(&decoded.Claude)
		case "codex":
			err = node.Decode(&decoded.Codex)
		case "omp":
			err = node.Decode(&decoded.OMP)
		case "pi":
			err = node.Decode(&decoded.Pi)
		case "droid":
			err = node.Decode(&decoded.Droid)
		case "opencode":
			err = node.Decode(&decoded.Opencode)
		case "qwen":
			err = node.Decode(&decoded.Qwen)
		case "cursor":
			err = node.Decode(&decoded.Cursor)
		case "copilot":
			err = node.Decode(&decoded.Copilot)
		case "grok":
			err = node.Decode(&decoded.Grok)
		}
		if err != nil {
			return err
		}
	}
	*role = Role(decoded)
	return nil
}

func decodeTools(node *yaml.Node) ([]string, error) {
	switch node.Kind {
	case yaml.SequenceNode:
		var tools []string
		if err := node.Decode(&tools); err != nil {
			return nil, err
		}
		return tools, nil
	case yaml.ScalarNode:
		var tools []string
		for _, part := range strings.Split(node.Value, ",") {
			if tool := strings.TrimSpace(part); tool != "" {
				tools = append(tools, tool)
			}
		}
		return tools, nil
	default:
		return nil, fmt.Errorf("tools must be a YAML sequence or comma-separated string")
	}
}

// ParseMarkdown parses canonical YAML-frontmatter Markdown role bytes.
func ParseMarkdown(path string, data []byte) (Role, error) {
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return Role{}, fmt.Errorf("%s: missing YAML frontmatter", path)
	}
	rest := data[len("---\n"):]
	end := bytes.Index(rest, []byte("\n---"))
	if end < 0 {
		return Role{}, fmt.Errorf("%s: unterminated YAML frontmatter", path)
	}
	frontmatter := rest[:end]
	body := rest[end+len("\n---"):]
	if len(body) > 0 && body[0] == '\r' {
		body = body[1:]
	}
	if len(body) > 0 && body[0] == '\n' {
		body = body[1:]
	}
	var role Role
	if err := yaml.Unmarshal(frontmatter, &role); err != nil {
		return Role{}, fmt.Errorf("parse %s frontmatter: %w", path, err)
	}
	role.Instructions = strings.TrimSpace(string(body))
	role.Source = path
	return role, nil
}

// LoadFile loads and validates one canonical role.
func LoadFile(path string) (Role, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Role{}, fmt.Errorf("read %s: %w", path, err)
	}
	role, err := ParseMarkdown(path, data)
	if err != nil {
		return Role{}, err
	}
	if err := validate(&role, path); err != nil {
		return Role{}, err
	}
	return role, nil
}

func validate(role *Role, source string) error {
	role.Name = strings.ToLower(strings.TrimSpace(role.Name))
	role.Model = strings.TrimSpace(role.Model)
	role.Effort = strings.TrimSpace(role.Effort)
	role.Description = strings.TrimSpace(role.Description)
	role.Instructions = strings.TrimSpace(role.Instructions)
	if role.Name == "" {
		return fmt.Errorf("%s is missing name", source)
	}
	if role.Description == "" {
		return fmt.Errorf("%s: role %s is missing description", source, role.Name)
	}
	if role.Instructions == "" {
		return fmt.Errorf("%s: role %s is missing instructions", source, role.Name)
	}
	return nil
}

// RenderCanonical serializes a role in the editable canonical Markdown format.
func RenderCanonical(role Role) ([]byte, error) {
	if err := validate(&role, "imported role"); err != nil {
		return nil, err
	}
	front := struct {
		Name        string          `yaml:"name"`
		Description string          `yaml:"description"`
		Model       string          `yaml:"model,omitempty"`
		Effort      string          `yaml:"effort,omitempty"`
		Tools       []string        `yaml:"tools,omitempty"`
		Color       string          `yaml:"color,omitempty"`
		Claude      ClaudeOptions   `yaml:"claude,omitempty"`
		Codex       CodexOptions    `yaml:"codex,omitempty"`
		Droid       DroidOptions    `yaml:"droid,omitempty"`
		OpenCode    OpenCodeOptions `yaml:"opencode,omitempty"`
		OMP         OMPOptions      `yaml:"omp,omitempty"`
		Pi          PiOptions       `yaml:"pi,omitempty"`
	}{
		Name: role.Name, Description: role.Description, Model: role.Model, Effort: role.Effort,
		Tools: role.Tools, Color: role.Color, Claude: role.Claude, Codex: role.Codex,
		Droid: role.Droid, OpenCode: role.Opencode, OMP: role.OMP, Pi: role.Pi,
	}
	meta, err := yaml.Marshal(front)
	if err != nil {
		return nil, err
	}
	return []byte("---\n" + string(meta) + "---\n\n" + strings.TrimSpace(role.Instructions) + "\n"), nil
}

// Load reads and validates canonical roles from <root>/agents.
func Load(root string) ([]Role, error) {
	dir := filepath.Join(root, "agents")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || filepath.Ext(entry.Name()) != MarkdownExtension {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(paths)
	roles := make([]Role, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		role, err := LoadFile(path)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[role.Name]; ok {
			return nil, fmt.Errorf("agent role %s is duplicated", role.Name)
		}
		seen[role.Name] = struct{}{}
		roles = append(roles, role)
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i].Name < roles[j].Name })
	return roles, nil
}
