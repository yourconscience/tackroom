package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// settingsFormat selects how a harness stores its native settings file.
type settingsFormat int

const (
	settingsJSON settingsFormat = iota
	settingsYAML
)

// settingsTarget is a harness settings file tackroom patches key by key.
// Only the keys declared under an agent's `settings:` block are written;
// every other key in the file is preserved.
type settingsTarget struct {
	configPath func(home string) string
	format     settingsFormat
}

func (t settingsTarget) path(home string) string {
	return t.configPath(home)
}

// ompConfigPath is the OMP settings file (YAML).
func ompConfigPath(home string) string {
	return filepath.Join(home, ".omp", "agent", "config.yml")
}

// settingsManagedKeys lists the dotted keys each harness already manages
// through another surface; a `settings:` block must not touch them. A declared
// key matches when it equals a managed key or sits under one (hooks.X).
var settingsManagedKeys = map[string][]string{
	agentPi:         {"packages"},
	agentClaudeCode: {"hooks", "enabledPlugins", "extraKnownMarketplaces", "mcpServers"},
}

// secretKeySegment matches a key segment that must hold an environment
// reference rather than a literal secret.
var secretKeySegment = regexp.MustCompile(`(?i)^(token|secret|password|api.?key|authorization|credential)$`)

// settingsValues resolves the declared values for one agent, expanding every
// ${ENV} reference (a missing variable is an error, so a typo cannot write an
// empty secret). Values are returned keyed by their dotted key.
func settingsValues(agent agentConfig) (map[string]any, error) {
	if agent.Settings == nil {
		return nil, nil
	}
	resolved := make(map[string]any, len(agent.Settings))
	for key, value := range agent.Settings {
		expanded, err := expandSettingValue(value)
		if err != nil {
			return nil, fmt.Errorf("%s settings %q: %w", agent.Name, key, err)
		}
		resolved[key] = expanded
	}
	return resolved, nil
}

// expandSettingValue resolves ${ENV} references in declared values, recursing
// through mappings and lists so a secret nested anywhere still expands.
func expandSettingValue(value any) (any, error) {
	switch v := value.(type) {
	case string:
		if !strings.Contains(v, "${") {
			return v, nil
		}
		var missing string
		expanded := os.Expand(v, func(name string) string {
			if env, ok := os.LookupEnv(name); ok {
				return env
			}
			if missing == "" {
				missing = name
			}
			return ""
		})
		if missing != "" {
			return nil, fmt.Errorf("references unset environment variable %s", missing)
		}
		return expanded, nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			expanded, err := expandSettingValue(item)
			if err != nil {
				return nil, err
			}
			out[key] = expanded
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			expanded, err := expandSettingValue(item)
			if err != nil {
				return nil, err
			}
			out[i] = expanded
		}
		return out, nil
	default:
		return value, nil
	}
}

// validateSettingsSecrets rejects any declared value that holds a literal
// secret: a key whose segment matches the secret pattern must be exactly one
// ${ENV_VAR} reference. Nested mappings are checked by their dotted path.
func validateSettingsSecrets(settings map[string]any) error {
	for key, value := range settings {
		if err := validateSettingValue(key, value); err != nil {
			return err
		}
	}
	return nil
}

func validateSettingValue(key string, value any) error {
	if isSecretKey(key) {
		text, ok := value.(string)
		if !ok || !isEnvReference(text) {
			return fmt.Errorf("settings key %q holds a secret and must be a single ${ENV_VAR} reference", key)
		}
		return nil
	}
	if mapping, ok := value.(map[string]any); ok {
		for sub, item := range mapping {
			if err := validateSettingValue(key+"."+sub, item); err != nil {
				return err
			}
		}
	}
	return nil
}

func isSecretKey(key string) bool {
	for _, segment := range strings.Split(key, ".") {
		if secretKeySegment.MatchString(segment) {
			return true
		}
	}
	return false
}

// normalizeSettingValue makes YAML ints/bools and JSON floats comparable by
// round-tripping through JSON, so drift is decided structurally, not by text.
func normalizeSettingValue(value any) any {
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var normalized any
	if err := json.Unmarshal(data, &normalized); err != nil {
		return value
	}
	return normalized
}

// readSettingsRaw decodes a settings file into a map for drift comparison.
func readSettingsRaw(path string, format settingsFormat) (map[string]any, os.FileMode, bool, error) {
	mode := os.FileMode(0o600)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, mode, false, nil
	}
	if err != nil {
		return nil, mode, false, fmt.Errorf("read %s: %w", path, err)
	}
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	raw := map[string]any{}
	if len(strings.TrimSpace(string(data))) == 0 {
		return raw, mode, true, nil
	}
	switch format {
	case settingsJSON:
		if err := parseJSONConfig(path, data, &raw); err != nil {
			return nil, mode, true, fmt.Errorf("parse %s: %w", path, err)
		}
	case settingsYAML:
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, mode, true, fmt.Errorf("parse %s: %w", path, err)
		}
	default:
		return nil, mode, true, fmt.Errorf("unsupported settings format for %s", path)
	}
	if raw == nil {
		raw = map[string]any{}
	}
	return raw, mode, true, nil
}

// lookupSettingPath reads a dotted key such as "terminal.images".
func lookupSettingPath(raw map[string]any, key string) (any, bool) {
	current := any(raw)
	for _, part := range strings.Split(key, ".") {
		mapping, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := mapping[part]
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

// setSettingPath writes a dotted key, creating intermediate mappings.
func setSettingPath(raw map[string]any, key string, value any) error {
	parts := strings.Split(key, ".")
	current := raw
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part]
		if !ok {
			child := map[string]any{}
			current[part] = child
			current = child
			continue
		}
		child, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("settings key %q conflicts with a non-mapping value", key)
		}
		current = child
	}
	current[parts[len(parts)-1]] = value
	return nil
}

// applySettingsSync backs up the existing file (once) and writes the declared
// values, preserving the file mode and everything else in the file.
func applySettingsSync(path string, format settingsFormat, values map[string]any, home string) error {
	if _, err := os.Stat(path); err == nil {
		if err := backupNativeConfig(path, newConflictBackupDir(home), home); err != nil {
			return fmt.Errorf("back up %s: %w", path, err)
		}
	}
	switch format {
	case settingsJSON:
		raw := map[string]any{}
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if err == nil {
			if err := parseJSONConfig(path, data, &raw); err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			if raw == nil {
				return fmt.Errorf("parse %s: settings must be a JSON object", path)
			}
		}
		for key, value := range values {
			if err := setSettingPath(raw, key, value); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
		}
		return writeJSONConfig(path, raw)
	case settingsYAML:
		return applyYAMLSettingsSync(path, values)
	default:
		return fmt.Errorf("unsupported settings format for %s", path)
	}
}

// applyYAMLSettingsSync patches a yaml.Node tree so key order and comments
// survive. Only declared keys change; every other node is left untouched.
func applyYAMLSettingsSync(path string, values map[string]any) error {
	mode := os.FileMode(0o600)
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: yamlMapTag}}}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if info, statErr := os.Stat(path); statErr == nil {
			mode = info.Mode().Perm()
		}
		if len(strings.TrimSpace(string(data))) > 0 {
			var parsed yaml.Node
			if err := yaml.Unmarshal(data, &parsed); err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			if parsed.Kind != 0 && len(parsed.Content) > 0 {
				doc = &parsed
			}
		}
	case os.IsNotExist(err):
		// keep the empty mapping document
	default:
		return fmt.Errorf("read %s: %w", path, err)
	}

	root, err := yamlRootMapping(doc)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for _, key := range sortedSettingKeys(values) {
		if err := setYAMLSettingPath(root, key, values[key]); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	out, err := marshalSettingsYAML(doc)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	return atomicConfigWrite(path, out, mode)
}

func yamlRootMapping(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind != yaml.DocumentNode {
		return nil, fmt.Errorf("settings must be a YAML mapping at the document root")
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: yamlMapTag}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("settings must be a YAML mapping")
	}
	return root, nil
}

// setYAMLSettingPath writes a dotted key into a mapping node tree, creating
// intermediate mappings and replacing only the final value node.
func setYAMLSettingPath(root *yaml.Node, key string, value any) error {
	parts := strings.Split(key, ".")
	mapping := root
	for _, part := range parts[:len(parts)-1] {
		child := yamlChild(mapping, part)
		if child == nil {
			child = &yaml.Node{Kind: yaml.MappingNode, Tag: yamlMapTag}
			mapping.Content = append(mapping.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: part},
				child,
			)
			mapping = child
			continue
		}
		if child.Kind != yaml.MappingNode {
			return fmt.Errorf("settings key %q conflicts with a non-mapping value", key)
		}
		mapping = child
	}
	var valueNode yaml.Node
	if err := valueNode.Encode(value); err != nil {
		return fmt.Errorf("encode %q: %w", key, err)
	}
	setMappingNode(mapping, parts[len(parts)-1], &valueNode)
	return nil
}

func yamlChild(mapping *yaml.Node, key string) *yaml.Node {
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

func marshalSettingsYAML(doc *yaml.Node) ([]byte, error) {
	var b strings.Builder
	encoder := yaml.NewEncoder(&b)
	encoder.SetIndent(2)
	if err := encoder.Encode(doc); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func sortedSettingKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// augmentSettingsReport records which declared settings are already correct
// (managed) and which are missing or differ (drifted), plus a pending update.
func augmentSettingsReport(report *agentReport, agent agentConfig, home string) error {
	if agent.Settings == nil {
		return nil
	}
	h := harnessFor(agent.Name)
	if h == nil || h.Settings == nil {
		return nil
	}
	values, err := settingsValues(agent)
	if err != nil {
		return err
	}
	path := h.Settings.path(home)
	raw, _, _, err := readSettingsRaw(path, h.Settings.format)
	if err != nil {
		return err
	}
	for _, key := range sortedSettingKeys(values) {
		actual, ok := lookupSettingPath(raw, key)
		if ok && reflect.DeepEqual(normalizeSettingValue(actual), normalizeSettingValue(values[key])) {
			report.ManagedSetting = append(report.ManagedSetting, key)
			continue
		}
		report.DriftedSetting = append(report.DriftedSetting, key)
	}
	if len(report.DriftedSetting) > 0 {
		report.UpdatesSetting = append(report.UpdatesSetting, filepath.Base(path))
	}
	return nil
}

// applyAgentSettingsSync writes each detected harness's declared settings when
// the report shows drift. A clean report writes nothing, so a no-op sync never
// rewrites the file or creates another backup.
func applyAgentSettingsSync(reports []agentReport, selected []agentConfig, home string) error {
	byName := make(map[string]agentConfig, len(selected))
	for _, agent := range selected {
		byName[agent.Name] = agent
	}
	for _, report := range reports {
		agent, ok := byName[report.Name]
		if !ok || !report.Detected || len(report.UpdatesSetting) == 0 {
			continue
		}
		h := harnessFor(agent.Name)
		if h == nil || h.Settings == nil {
			continue
		}
		values, err := settingsValues(agent)
		if err != nil {
			return err
		}
		if err := applySettingsSync(h.Settings.path(home), h.Settings.format, values, home); err != nil {
			return fmt.Errorf("%s settings: %w", agent.Name, err)
		}
	}
	return nil
}
