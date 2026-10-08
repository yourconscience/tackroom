package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Claude Code plugins are distributed by registering the config root as a
// directory marketplace: <config-root>/.claude-plugin/marketplace.json, read in
// place by Claude Code, so mods, workflows, monitors, output styles and every
// other plugin component load from the repo without a copy or an install step.
// tackroom only patches two user settings keys: extraKnownMarketplaces.<name>
// and enabledPlugins["<plugin>@<name>"]. A plugin the user turned off
// (enabledPlugins false) stays off.

func claudeMarketplacePath(repoRoot string) string {
	return filepath.Join(repoRoot, ".claude-plugin", "marketplace.json")
}

func claudeSettingsPath(home string) string {
	return filepath.Join(home, ".claude", "settings.json")
}

type claudeMarketplace struct {
	Name string
	// Plugins lists relative-path entries that should be enabled.
	Plugins []string
	// Disabled lists relative-path entries that set defaultEnabled: false.
	Disabled []string
	// Skipped lists entries tackroom does not manage (non-relative sources).
	Skipped []string
}

type claudeMarketplaceEntry struct {
	Name           string          `json:"name"`
	Source         json.RawMessage `json:"source"`
	DefaultEnabled *bool           `json:"defaultEnabled"`
}

// readClaudeMarketplace returns ok=false when the config root has no Claude
// Code marketplace, which turns the plugin surface off.
func readClaudeMarketplace(repoRoot string) (claudeMarketplace, bool, error) {
	path := claudeMarketplacePath(repoRoot)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return claudeMarketplace{}, false, nil
	}
	if err != nil {
		return claudeMarketplace{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	var raw struct {
		Name    string                   `json:"name"`
		Plugins []claudeMarketplaceEntry `json:"plugins"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return claudeMarketplace{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	if raw.Name == "" {
		return claudeMarketplace{}, false, fmt.Errorf("parse %s: marketplace name is required", path)
	}
	m := claudeMarketplace{Name: raw.Name}
	for _, entry := range raw.Plugins {
		if entry.Name == "" {
			continue
		}
		var source string
		if err := json.Unmarshal(entry.Source, &source); err != nil || (source != "." && !strings.HasPrefix(source, "./")) {
			m.Skipped = append(m.Skipped, entry.Name)
			continue
		}
		enabled := entry.DefaultEnabled
		if enabled == nil {
			enabled = pluginDefaultEnabled(filepath.Join(repoRoot, filepath.FromSlash(source)))
		}
		if enabled != nil && !*enabled {
			m.Disabled = append(m.Disabled, entry.Name)
			continue
		}
		m.Plugins = append(m.Plugins, entry.Name)
	}
	sort.Strings(m.Plugins)
	return m, true, nil
}

func pluginDefaultEnabled(pluginRoot string) *bool {
	data, err := os.ReadFile(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"))
	if err != nil {
		return nil
	}
	var manifest struct {
		DefaultEnabled *bool `json:"defaultEnabled"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return nil
	}
	return manifest.DefaultEnabled
}

func readClaudeSettings(home string) (map[string]interface{}, error) {
	path := claudeSettingsPath(home)
	raw := map[string]interface{}{}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return raw, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := parseJSONConfig(path, data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("parse %s: settings must be a JSON object", path)
	}
	return raw, nil
}

// directoryMarketplaceNames lists extraKnownMarketplaces entries whose
// directory source is the config root.
func directoryMarketplaceNames(settings map[string]interface{}, repoRoot string) []string {
	known, _ := asMap(settings["extraKnownMarketplaces"])
	var names []string
	for name, value := range known {
		entry, _ := asMap(value)
		source, _ := asMap(entry["source"])
		if source["source"] != "directory" {
			continue
		}
		if path, _ := source["path"].(string); path != "" && samePath(path, repoRoot) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

func augmentClaudePluginReport(report *agentReport, agent agentConfig, repoRoot string, home string) error {
	if normalizeAgentName(agent.Name) != agentClaudeCode {
		return nil
	}
	m, ok, err := readClaudeMarketplace(repoRoot)
	if err != nil || !ok {
		return err
	}
	settings, err := readClaudeSettings(home)
	if err != nil {
		return err
	}

	registered := false
	for _, name := range directoryMarketplaceNames(settings, repoRoot) {
		if name == m.Name {
			registered = true
			continue
		}
		report.StalePlugin = append(report.StalePlugin, "marketplace "+name)
	}
	if !registered {
		report.MissingPlugin = append(report.MissingPlugin, "marketplace "+m.Name)
	}

	enabled, _ := asMap(settings["enabledPlugins"])
	for _, plugin := range m.Plugins {
		switch enabled[plugin+"@"+m.Name] {
		case true:
			report.ManagedPlugin = append(report.ManagedPlugin, plugin)
		case false:
			report.DisabledPlugin = append(report.DisabledPlugin, plugin+" (disabled in settings)")
		default:
			report.MissingPlugin = append(report.MissingPlugin, plugin)
		}
	}
	for _, plugin := range m.Disabled {
		report.DisabledPlugin = append(report.DisabledPlugin, plugin+" (defaultEnabled: false)")
	}
	listed := map[string]bool{}
	for _, plugin := range append(append(append([]string{}, m.Plugins...), m.Disabled...), m.Skipped...) {
		listed[plugin] = true
	}
	for key := range enabled {
		plugin, ok := strings.CutSuffix(key, "@"+m.Name)
		if ok && !listed[plugin] {
			report.StalePlugin = append(report.StalePlugin, key)
		}
	}
	report.AddsPlugin = append([]string{}, report.MissingPlugin...)
	report.RemovesPlugin = append([]string{}, report.StalePlugin...)
	return nil
}

// syncClaudePlugins registers the config root marketplace, enables its
// plugins, and drops tackroom-owned entries the marketplace no longer lists.
func syncClaudePlugins(repoRoot string, home string) error {
	m, ok, err := readClaudeMarketplace(repoRoot)
	if err != nil || !ok {
		return err
	}
	settings, err := readClaudeSettings(home)
	if err != nil {
		return err
	}

	known, isMap := asMap(settings["extraKnownMarketplaces"])
	if !isMap {
		if settings["extraKnownMarketplaces"] != nil {
			return fmt.Errorf("%s: extraKnownMarketplaces is not an object", claudeSettingsPath(home))
		}
		known = map[string]interface{}{}
	}
	enabled, isMap := asMap(settings["enabledPlugins"])
	if !isMap {
		if settings["enabledPlugins"] != nil {
			return fmt.Errorf("%s: enabledPlugins is not an object", claudeSettingsPath(home))
		}
		enabled = map[string]interface{}{}
	}

	for _, name := range directoryMarketplaceNames(settings, repoRoot) {
		if name == m.Name {
			continue
		}
		delete(known, name)
		for key := range enabled {
			if strings.HasSuffix(key, "@"+name) {
				delete(enabled, key)
			}
		}
	}
	known[m.Name] = map[string]interface{}{
		"source": map[string]interface{}{"source": "directory", "path": repoRoot},
	}

	listed := map[string]bool{}
	for _, plugin := range append(append(append([]string{}, m.Plugins...), m.Disabled...), m.Skipped...) {
		listed[plugin] = true
	}
	for key := range enabled {
		if plugin, ok := strings.CutSuffix(key, "@"+m.Name); ok && !listed[plugin] {
			delete(enabled, key)
		}
	}
	for _, plugin := range m.Plugins {
		if _, set := enabled[plugin+"@"+m.Name]; !set {
			enabled[plugin+"@"+m.Name] = true
		}
	}

	settings["extraKnownMarketplaces"] = known
	settings["enabledPlugins"] = enabled
	return writeJSONConfig(claudeSettingsPath(home), settings)
}

func writeJSONConfig(path string, raw map[string]interface{}) error {
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func applyClaudePluginSync(reports []agentReport, repoRoot string, home string) error {
	for _, report := range reports {
		if !report.Detected || normalizeAgentName(report.Name) != agentClaudeCode {
			continue
		}
		if len(report.MissingPlugin)+len(report.StalePlugin) == 0 {
			continue
		}
		if err := syncClaudePlugins(repoRoot, home); err != nil {
			return err
		}
	}
	return nil
}

// claudePluginValidate runs Claude Code's own validator; a var so tests can
// stand in for the claude executable.
var claudePluginValidate = func(claude string, root string) ([]byte, error) {
	return exec.Command(claude, "plugin", "validate", root).CombinedOutput()
}

func checkClaudePlugins(repoRoot string) checkResult {
	const name = "claude plugins"
	m, ok, err := readClaudeMarketplace(repoRoot)
	if err != nil {
		return checkResult{name, checkStatusFail, err.Error()}
	}
	if !ok {
		return checkResult{name, checkStatusPass, "no .claude-plugin/marketplace.json, skipped"}
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		return checkResult{name, checkStatusWarn, "claude not on PATH; marketplace not validated"}
	}
	out, err := claudePluginValidate(claude, repoRoot)
	if err != nil {
		return checkResult{name, checkStatusFail, "claude plugin validate failed: " + lastLine(string(out))}
	}
	detail := fmt.Sprintf("marketplace %s: %d plugins, claude plugin validate passed", m.Name, len(m.Plugins))
	if strings.Contains(string(out), "with warnings") {
		detail += " with warnings"
	}
	return checkResult{name, checkStatusPass, detail}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
