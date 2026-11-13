package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// settingsFormat selects how a harness stores its native settings file.
type settingsFormat int

const (
	settingsJSON settingsFormat = iota
	settingsYAML
	settingsTOML
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

// codexConfigPath is the Codex config file (TOML), shared by its MCP servers
// and its settings.
func codexConfigPath(home string) string {
	return filepath.Join(home, ".codex", "config.toml")
}

// settingsManagedKeys lists the dotted keys each harness already manages
// through another surface; a `settings:` block must not touch them. A declared
// key matches when it equals a managed key or sits under one (hooks.X).
var settingsManagedKeys = map[string][]string{
	agentPi:         {"packages"},
	agentClaudeCode: {"hooks", "enabledPlugins", "extraKnownMarketplaces", "mcpServers"},
	agentCodex:      {"mcp_servers", "plugins", "marketplaces", "features.hooks", "codex_hooks"},
}

// secretKeySegment matches a key segment that must hold an environment
// reference rather than a literal secret. The segment must end with the word,
// so ANTHROPIC_AUTH_TOKEN or clientSecret are caught but maxTokens is not.
var secretKeySegment = regexp.MustCompile(`(?i)(token|secret|password|passwd|api[_-]?key|authorization|credentials?)$`)

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
		return nil
	}
	if list, ok := value.([]any); ok {
		for _, item := range list {
			if err := validateSettingValue(key, item); err != nil {
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
	case settingsTOML:
		if _, err := toml.Decode(string(data), &raw); err != nil {
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
	case settingsTOML:
		return applyTOMLSettingsSync(path, values)
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
	out, err := marshalSettingsYAML(doc, yamlIndentOf(data))
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

// yamlIndentOf returns the indentation width of the first indented line, so a
// rewrite keeps the file's own indent; 2 when the file has none.
func yamlIndentOf(data []byte) int {
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if n := len(line) - len(trimmed); n >= 2 && n <= 8 {
			return n
		}
	}
	return 2
}

func marshalSettingsYAML(doc *yaml.Node, indent int) ([]byte, error) {
	var b strings.Builder
	encoder := yaml.NewEncoder(&b)
	encoder.SetIndent(indent)
	if err := encoder.Encode(doc); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// tomlBareKey matches a TOML bare key segment. Any other segment needs
// quoting, which a dotted settings key cannot express.
var tomlBareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// applyTOMLSettingsSync edits a TOML file as text so comments, key order and
// unrelated tables survive. Each declared key replaces its line or is added to
// its table. The result is decoded again and must equal the pre-edit file with
// only the declared keys changed; a file this editor cannot patch safely is
// refused with an error and left untouched.
func applyTOMLSettingsSync(path string, values map[string]any) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	mode := os.FileMode(0o600)
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if info, statErr := os.Stat(path); statErr == nil {
			mode = info.Mode().Perm()
		}
	case os.IsNotExist(err):
		// a missing file is created with only the declared keys
	default:
		return fmt.Errorf("read %s: %w", path, err)
	}
	content := string(data)
	before, expected := map[string]any{}, map[string]any{}
	if _, err := toml.Decode(content, &before); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if _, err := toml.Decode(content, &expected); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	keys := sortedSettingKeys(values)
	for _, key := range keys {
		for _, part := range strings.Split(key, ".") {
			if !tomlBareKey.MatchString(part) {
				return fmt.Errorf("%s: key segment %q needs TOML quoting, which settings keys cannot express", key, part)
			}
		}
		if err := setSettingPath(expected, key, values[key]); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	for _, key := range keys {
		rendered, err := renderTOMLValue(values[key])
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		if content, err = setTOMLSetting(content, before, strings.Split(key, "."), rendered); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	after := map[string]any{}
	if _, err := toml.Decode(content, &after); err != nil {
		return fmt.Errorf("refusing to edit %s: the result does not parse (%v); an inline table, dotted key or unusual table header needs a manual edit", path, err)
	}
	if !reflect.DeepEqual(normalizeSettingValue(after), normalizeSettingValue(expected)) {
		return fmt.Errorf("refusing to edit %s: the result differs from the declared settings; edit the file by hand", path)
	}
	if err := atomicConfigWrite(path, []byte(content), mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// setTOMLSetting sets one dotted key in TOML text. A top-level key lives in the
// block before the first table header; a.b.c lives in the [a.b] table. The
// key's line is replaced, or the key is added to its table (created when
// absent). before is the pre-edit decode, used to spot keys that exist in a
// form that has no line to patch.
func setTOMLSetting(content string, before map[string]any, parts []string, rendered string) (string, error) {
	key := strings.Join(parts, ".")
	leaf := parts[len(parts)-1]
	from, to, header := 0, len(content), ""
	if i := indexNextTOMLHeader(content); i != -1 {
		to = i
	}
	if len(parts) > 1 {
		prefix := strings.Join(parts[:len(parts)-1], ".")
		header = "[" + prefix + "]"
		start := indexTOMLSectionHeader(content, header)
		if start == -1 {
			_, defined := lookupSettingPath(before, prefix)
			if defined && indexTOMLHeaderCandidate(content, "["+prefix+".") == -1 && indexTOMLHeaderCandidate(content, "[["+prefix+".") == -1 {
				return "", fmt.Errorf("%s is already defined as an inline table or dotted key, which this editor cannot patch", prefix)
			}
			// New tables go at the end of the file, as Codex adds its own, so no
			// comment is separated from the table it introduces.
			section := header + "\n" + leaf + " = " + rendered + "\n"
			switch {
			case strings.TrimSpace(content) == "":
				return section, nil
			case strings.HasSuffix(content, "\n\n"):
				return content + section, nil
			case strings.HasSuffix(content, "\n"):
				return content + "\n" + section, nil
			default:
				return content + "\n\n" + section, nil
			}
		}
		from, to = start+len(header), endTOMLSection(content, start, header)
	}

	lineStart, lineEnd, valueAt, err := findTOMLKeyLine(content, from, to, leaf)
	if err != nil {
		return "", err
	}
	if lineStart != -1 {
		after := content[valueAt:lineEnd]
		stripped := stripTOMLInlineComments(after)
		value := strings.TrimSpace(stripped)
		if strings.HasPrefix(value, "{") || strings.HasPrefix(value, `"""`) || strings.HasPrefix(value, `'''`) || !tomlValueComplete(value) {
			return "", fmt.Errorf("the existing value is an inline table or spans several lines, which this editor cannot patch")
		}
		lead := after[:len(after)-len(strings.TrimLeft(after, " \t"))]
		tail := after[len(strings.TrimRight(stripped, " \t")):]
		return content[:valueAt] + lead + rendered + tail + content[lineEnd:], nil
	}
	if _, exists := lookupSettingPath(before, key); exists {
		return "", fmt.Errorf("the key already exists as a table, dotted key or array of tables, which this editor cannot patch")
	}

	line := leaf + " = " + rendered + "\n"
	at := lastTOMLAssignmentEnd(content, from, to)
	switch {
	case at != -1:
	case header != "":
		// an empty table: the key goes right under its header
		at = len(content)
		if nl := strings.IndexByte(content[from:], '\n'); nl != -1 {
			at = from + nl + 1
		}
	default:
		// no top-level keys yet: go above the first table and the comments that introduce it
		at = to
		if to < len(content) {
			for at > 0 {
				prev := strings.LastIndexByte(content[:at-1], '\n') + 1
				if !strings.HasPrefix(strings.TrimSpace(content[prev:at]), "#") {
					break
				}
				at = prev
			}
			line += "\n"
		}
	}
	if at > 0 && content[at-1] != '\n' {
		line = "\n" + line
	}
	return content[:at] + line + content[at:], nil
}

// findTOMLKeyLine finds the line in content[from:to] that assigns the bare key.
// It returns the line's bounds without its line break and the offset just past
// the "="; lineStart is -1 when no line assigns the key. A dotted-key form such
// as a.b = 1 is an error.
func findTOMLKeyLine(content string, from int, to int, key string) (int, int, int, error) {
	assign := regexp.MustCompile(`^[ \t]*` + regexp.QuoteMeta(key) + `[ \t]*([=.])`)
	for pos := from; pos < to; {
		next := to
		if nl := strings.IndexByte(content[pos:to], '\n'); nl != -1 {
			next = pos + nl + 1
		}
		line := strings.TrimRight(content[pos:next], "\r\n")
		if m := assign.FindStringSubmatchIndex(line); m != nil {
			if line[m[2]] == '.' {
				return -1, -1, -1, fmt.Errorf("the key is set with a dotted key, which this editor cannot patch")
			}
			return pos, pos + len(line), pos + m[1], nil
		}
		pos = next
	}
	return -1, -1, -1, nil
}

// lastTOMLAssignmentEnd returns the offset just past the last line in
// content[from:to] that is neither blank nor a comment, or -1.
func lastTOMLAssignmentEnd(content string, from int, to int) int {
	last := -1
	for pos := from; pos < to; {
		next := to
		if nl := strings.IndexByte(content[pos:to], '\n'); nl != -1 {
			next = pos + nl + 1
		}
		if text := strings.TrimSpace(content[pos:next]); text != "" && !strings.HasPrefix(text, "#") {
			last = next
		}
		pos = next
	}
	return last
}

// renderTOMLValue renders a declared settings value as a TOML value.
func renderTOMLValue(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return renderTOMLString(v), nil
	case bool:
		return strconv.FormatBool(v), nil
	case int:
		return strconv.Itoa(v), nil
	case uint64:
		return strconv.FormatUint(v, 10), nil
	case float64:
		text := strconv.FormatFloat(v, 'g', -1, 64)
		if !strings.ContainsAny(text, ".eE") {
			text += ".0"
		}
		return text, nil
	case []any:
		items := make([]string, 0, len(v))
		for _, item := range v {
			rendered, err := renderTOMLValue(item)
			if err != nil {
				return "", err
			}
			items = append(items, rendered)
		}
		return "[" + strings.Join(items, ", ") + "]", nil
	case map[string]any:
		if len(v) == 0 {
			return "{}", nil
		}
		fields := make([]string, 0, len(v))
		for _, name := range sortedSettingKeys(v) {
			rendered, err := renderTOMLValue(v[name])
			if err != nil {
				return "", err
			}
			if !tomlBareKey.MatchString(name) {
				name = renderTOMLString(name)
			}
			fields = append(fields, name+" = "+rendered)
		}
		return "{ " + strings.Join(fields, ", ") + " }", nil
	default:
		return "", fmt.Errorf("unsupported value type %T", value)
	}
}

// renderTOMLString renders text as a TOML basic string.
func renderTOMLString(text string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range text {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
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
		report.UnsupportedSetting = append(report.UnsupportedSetting, sortedSettingKeys(agent.Settings)...)
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
