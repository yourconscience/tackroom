package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
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

type claudeMarketplace struct {
	Name string
	// Plugins lists relative-path entries that should be enabled.
	Plugins []string
	// Disabled lists relative-path entries that set defaultEnabled: false.
	Disabled []string
	// Skipped lists entries tackroom does not manage (remote sources, or the
	// config root itself, which tackroom already syncs as skills and roles).
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
		Name     string `json:"name"`
		Metadata struct {
			PluginRoot string `json:"pluginRoot"`
		} `json:"metadata"`
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
		dir, ok := relativePluginDir(entry.Source, raw.Metadata.PluginRoot)
		if !ok {
			m.Skipped = append(m.Skipped, entry.Name)
			continue
		}
		enabled := entry.DefaultEnabled
		if enabled == nil {
			enabled = pluginDefaultEnabled(filepath.Join(repoRoot, filepath.FromSlash(dir)))
		}
		if enabled != nil && !*enabled {
			m.Disabled = append(m.Disabled, entry.Name)
			continue
		}
		m.Plugins = append(m.Plugins, entry.Name)
	}
	sort.Strings(m.Plugins)
	sort.Strings(m.Disabled)
	return m, true, nil
}

// relativePluginDir resolves a marketplace entry source that lives inside the
// marketplace: "./path", or a bare name under metadata.pluginRoot.
func relativePluginDir(raw json.RawMessage, pluginRoot string) (string, bool) {
	var source string
	if json.Unmarshal(raw, &source) != nil {
		return "", false
	}
	switch {
	case source == "." || source == "./":
		return "", false
	case strings.HasPrefix(source, "./"):
		return source, true
	case pluginRoot != "" && source != "" && !strings.Contains(source, "/"):
		return pluginRoot + "/" + source, true
	}
	return "", false
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
	path := claudeHooksConfigPath(home)
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

// claudePluginPlan is the one description of what sync changes for Claude
// Code plugins; status reports it and sync applies it.
type claudePluginPlan struct {
	Marketplace string
	Register    bool
	// Set holds enabledPlugins keys to write: true for a plugin to enable, or a
	// value carried over from a renamed marketplace (a user's false stays false).
	Set                map[string]bool
	Remove             []string
	RemoveMarketplaces []string
	Managed            []string
	Disabled           []string
	Conflicts          []string
}

func planClaudePlugins(repoRoot string, home string) (claudePluginPlan, error) {
	plan := claudePluginPlan{Set: map[string]bool{}}
	settings, err := readClaudeSettings(home)
	if err != nil {
		return plan, err
	}
	known, okKnown := asMap(settings["extraKnownMarketplaces"])
	enabled, okEnabled := asMap(settings["enabledPlugins"])
	for key, ok := range map[string]bool{"extraKnownMarketplaces": okKnown, "enabledPlugins": okEnabled} {
		if !ok && settings[key] != nil {
			plan.Conflicts = append(plan.Conflicts, fmt.Sprintf("%s: %s is not an object", claudeHooksConfigPath(home), key))
		}
	}
	m, ok, err := readClaudeMarketplace(repoRoot)
	if err != nil {
		plan.Conflicts = append(plan.Conflicts, err.Error())
	}
	if len(plan.Conflicts) > 0 {
		sort.Strings(plan.Conflicts)
		return plan, nil
	}

	var sameRoot []string
	for name, value := range known {
		entry, _ := asMap(value)
		source, _ := asMap(entry["source"])
		path, _ := source["path"].(string)
		if source["source"] == "directory" && path != "" && (path == repoRoot || sameResolvedPath(path, repoRoot)) {
			sameRoot = append(sameRoot, name)
		}
	}
	sort.Strings(sameRoot)

	removeMarketplace := func(name string) {
		plan.RemoveMarketplaces = append(plan.RemoveMarketplaces, name)
		for key := range enabled {
			if strings.HasSuffix(key, "@"+name) {
				plan.Remove = append(plan.Remove, key)
			}
		}
	}

	if !ok {
		// The marketplace file is gone: drop registrations tackroom made for it.
		for _, name := range sameRoot {
			removeMarketplace(name)
		}
		sort.Strings(plan.Remove)
		return plan, nil
	}

	plan.Marketplace = m.Name
	if _, taken := known[m.Name]; taken && !stringInSlice(m.Name, sameRoot) {
		plan.Conflicts = append(plan.Conflicts, fmt.Sprintf("marketplace %q is already registered in %s with another source", m.Name, claudeHooksConfigPath(home)))
		return plan, nil
	}
	plan.Register = !stringInSlice(m.Name, sameRoot)

	value := func(plugin string) (interface{}, bool) {
		v, set := enabled[plugin+"@"+m.Name]
		return v, set
	}
	for _, old := range sameRoot {
		if old == m.Name {
			continue
		}
		removeMarketplace(old)
		for key, v := range enabled {
			plugin, found := strings.CutSuffix(key, "@"+old)
			if b, isBool := v.(bool); found && isBool {
				if _, set := value(plugin); !set {
					plan.Set[plugin+"@"+m.Name] = b
				}
			}
		}
	}

	for _, plugin := range m.Plugins {
		key := plugin + "@" + m.Name
		v, _ := value(plugin)
		if carried, isCarried := plan.Set[key]; isCarried {
			v = carried
		}
		switch v {
		case true:
			plan.Managed = append(plan.Managed, plugin)
		case false:
			plan.Disabled = append(plan.Disabled, plugin+" (disabled in settings)")
		default:
			plan.Set[key] = true
		}
	}
	for _, plugin := range m.Disabled {
		key := plugin + "@" + m.Name
		v, _ := value(plugin)
		if carried, isCarried := plan.Set[key]; isCarried {
			v = carried
		}
		if v == true {
			plan.Managed = append(plan.Managed, plugin)
		} else {
			plan.Disabled = append(plan.Disabled, plugin+" (defaultEnabled: false)")
		}
	}

	listed := map[string]bool{}
	for _, plugin := range append(append(append([]string{}, m.Plugins...), m.Disabled...), m.Skipped...) {
		listed[plugin] = true
	}
	for key := range enabled {
		if plugin, found := strings.CutSuffix(key, "@"+m.Name); found && !listed[plugin] {
			plan.Remove = append(plan.Remove, key)
		}
	}
	for key := range plan.Set {
		plugin, _ := strings.CutSuffix(key, "@"+m.Name)
		if !listed[plugin] {
			delete(plan.Set, key)
		}
	}
	sort.Strings(plan.Remove)
	sort.Strings(plan.Managed)
	sort.Strings(plan.Disabled)
	return plan, nil
}

func augmentClaudePluginReport(report *agentReport, agent agentConfig, repoRoot string, home string) error {
	if normalizeAgentName(agent.Name) != agentClaudeCode {
		return nil
	}
	plan, err := planClaudePlugins(repoRoot, home)
	if err != nil {
		return err
	}
	for _, conflict := range plan.Conflicts {
		report.Conflicts = append(report.Conflicts, "plugins: "+conflict)
	}
	if plan.Register {
		report.MissingPlugin = append(report.MissingPlugin, "marketplace "+plan.Marketplace)
	}
	for key, v := range plan.Set {
		plugin, _ := strings.CutSuffix(key, "@"+plan.Marketplace)
		if v {
			report.MissingPlugin = append(report.MissingPlugin, plugin)
		} else {
			report.MissingPlugin = append(report.MissingPlugin, plugin+" (kept disabled)")
		}
	}
	for _, name := range plan.RemoveMarketplaces {
		report.StalePlugin = append(report.StalePlugin, "marketplace "+name)
	}
	report.StalePlugin = append(report.StalePlugin, plan.Remove...)
	report.ManagedPlugin = append(report.ManagedPlugin, plan.Managed...)
	report.DisabledPlugin = append(report.DisabledPlugin, plan.Disabled...)
	report.AddsPlugin = append([]string{}, report.MissingPlugin...)
	report.RemovesPlugin = append([]string{}, report.StalePlugin...)
	return nil
}

// syncClaudePlugins applies the current plan. Removals are skipped when the
// caller declined them (setup's confirmation, the view's destructive list).
func syncClaudePlugins(repoRoot string, home string, allowRemovals bool) error {
	plan, err := planClaudePlugins(repoRoot, home)
	if err != nil {
		return err
	}
	if len(plan.Conflicts) > 0 {
		return fmt.Errorf("claude plugins: %s", strings.Join(plan.Conflicts, "; "))
	}
	if !plan.Register && len(plan.Set) == 0 && (!allowRemovals || len(plan.Remove)+len(plan.RemoveMarketplaces) == 0) {
		return nil
	}
	settings, err := readClaudeSettings(home)
	if err != nil {
		return err
	}
	known, _ := asMap(settings["extraKnownMarketplaces"])
	if known == nil {
		known = map[string]interface{}{}
	}
	enabled, _ := asMap(settings["enabledPlugins"])
	if enabled == nil {
		enabled = map[string]interface{}{}
	}
	if allowRemovals {
		for _, name := range plan.RemoveMarketplaces {
			delete(known, name)
		}
		for _, key := range plan.Remove {
			delete(enabled, key)
		}
	}
	if plan.Register {
		known[plan.Marketplace] = map[string]interface{}{
			"source": map[string]interface{}{"source": "directory", "path": repoRoot},
		}
	}
	for key, v := range plan.Set {
		enabled[key] = v
	}
	settings["extraKnownMarketplaces"] = known
	settings["enabledPlugins"] = enabled
	return writeJSONConfig(claudeHooksConfigPath(home), settings)
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
		if len(report.AddsPlugin)+len(report.RemovesPlugin) == 0 {
			continue
		}
		if err := syncClaudePlugins(repoRoot, home, len(report.RemovesPlugin) > 0); err != nil {
			return err
		}
	}
	return nil
}

// claudePluginValidate runs Claude Code's own validator; a var so tests can
// stand in for the claude executable.
var claudePluginValidate = func(claude string, root string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return exec.CommandContext(ctx, claude, "plugin", "validate", root).CombinedOutput()
}

func checkClaudePlugins(repoRoot string, cfg config) checkResult {
	const name = "claude plugins"
	claude := ""
	for _, agent := range cfg.Agents {
		if normalizeAgentName(agent.Name) == agentClaudeCode && isDetected(agent) {
			detect := agent.Detect
			if detect == "" {
				detect = "claude"
			}
			claude, _ = exec.LookPath(detect)
		}
	}
	if claude == "" {
		return checkResult{name, checkStatusPass, agentClaudeCode + " not detected, skipped"}
	}
	m, ok, err := readClaudeMarketplace(repoRoot)
	if err != nil {
		return checkResult{name, checkStatusFail, err.Error()}
	}
	if !ok {
		return checkResult{name, checkStatusPass, "no .claude-plugin/marketplace.json, skipped"}
	}
	out, err := claudePluginValidate(claude, repoRoot)
	if err != nil {
		reason := strings.TrimSpace(string(out))
		if reason == "" {
			reason = err.Error()
		} else {
			lines := strings.Split(reason, "\n")
			reason = strings.TrimSpace(lines[len(lines)-1])
		}
		return checkResult{name, checkStatusFail, "claude plugin validate failed: " + reason}
	}
	detail := fmt.Sprintf("marketplace %s: %d plugins, claude plugin validate passed", m.Name, len(m.Plugins))
	if strings.Contains(string(out), "with warnings") {
		detail += " with warnings"
	}
	return checkResult{name, checkStatusPass, detail}
}
