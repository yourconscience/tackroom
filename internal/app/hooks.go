package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

type hookConfig struct {
	Name    string   `yaml:"name"`
	Enabled bool     `yaml:"enabled"`
	Event   string   `yaml:"event"`
	Command string   `yaml:"command"`
	Timeout int      `yaml:"timeout,omitempty"`
	Agents  []string `yaml:"agents"`
}

type hookTarget struct {
	agentName string
	inspect   func(hookConfig, string) (string, error)
	patch     func(hookConfig, string) error
}

const (
	codexSessionEndMaxTimeout = 3
	hermesMaxHookTimeout      = 300
)

func desiredHooksForAgent(cfg config, agentName string) ([]hookConfig, bool) {
	agentName = normalizeAgentName(agentName)
	var hooks []hookConfig
	for _, hook := range cfg.Hooks {
		if !hook.Enabled {
			continue
		}
		if len(hook.Agents) == 0 || stringInSlice(agentName, hook.Agents) {
			hooks = append(hooks, hook)
		}
	}
	_, supported := hookTargetForHarness(agentName)
	return hooks, supported
}

func augmentHookReport(report *agentReport, agent agentConfig, cfg config, home string) error {
	hooks, supported := desiredHooksForAgent(cfg, agent.Name)
	if len(hooks) == 0 {
		return nil
	}
	if !supported {
		for _, hook := range hooks {
			report.UnsupportedHook = append(report.UnsupportedHook, hook.Name)
		}
		return nil
	}
	target, _ := hookTargetForHarness(agent.Name)
	for _, hook := range hooks {
		state, err := target.inspect(hook, home)
		if err != nil {
			return err
		}
		switch state {
		case stateSynced:
			report.ManagedHook = append(report.ManagedHook, hook.Name)
		case stateMissing:
			report.MissingHook = append(report.MissingHook, hook.Name)
			report.AddsHook = append(report.AddsHook, hook.Name)
		case stateDrifted:
			report.DriftedHook = append(report.DriftedHook, hook.Name)
			report.UpdatesHook = append(report.UpdatesHook, hook.Name)
		case stateUnsupported:
			report.UnsupportedHook = append(report.UnsupportedHook, hook.Name)
		default:
			return fmt.Errorf("unsupported hook inspect state %q for %s/%s", state, agent.Name, hook.Name)
		}
	}
	return nil
}

func applyAgentHookSync(reports []agentReport, cfg config, home string) error {
	for _, report := range reports {
		if !report.Detected {
			continue
		}
		target, supported := hookTargetForHarness(report.Name)
		if !supported {
			continue
		}
		hooks, _ := desiredHooksForAgent(cfg, report.Name)
		byName := make(map[string]hookConfig, len(hooks))
		for _, hook := range hooks {
			byName[hook.Name] = hook
		}
		for _, name := range append(report.AddsHook, report.UpdatesHook...) {
			hook, ok := byName[name]
			if !ok {
				return fmt.Errorf("missing hook config for %s/%s", report.Name, name)
			}
			if err := target.patch(hook, home); err != nil {
				return err
			}
		}
	}
	return nil
}

func inspectClaudeHook(hook hookConfig, home string) (string, error) {
	configPath := claudeHooksConfigPath(home)
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
	return inspectClaudeHookMap(raw, hook), nil
}

func patchClaudeHook(hook hookConfig, home string) error {
	configPath := claudeHooksConfigPath(home)
	data, err := os.ReadFile(configPath)
	raw := map[string]interface{}{}
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read %s: %w", configPath, err)
		}
	} else if err := parseJSONConfig(configPath, data, &raw); err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}
	if err := upsertClaudeHookMap(raw, hook); err != nil {
		return fmt.Errorf("patch %s: %w", configPath, err)
	}
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

func inspectHermesHook(hook hookConfig, home string) (string, error) {
	nativeEvent, ok := hermesHookEvent(hook.Event)
	if !ok {
		return stateUnsupported, nil
	}
	hook = nativeHermesHook(hook)
	configPath := filepath.Join(home, ".hermes", "config.yaml")
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
	return inspectSimpleHookMap(raw, "hooks", nativeEvent, hook), nil
}

func patchHermesHook(hook hookConfig, home string) error {
	nativeEvent, ok := hermesHookEvent(hook.Event)
	if !ok {
		return fmt.Errorf("unsupported hermes hook event %q for %s", hook.Event, hook.Name)
	}
	hook = nativeHermesHook(hook)
	configPath := filepath.Join(home, ".hermes", "config.yaml")
	data, err := os.ReadFile(configPath)
	raw := map[string]interface{}{}
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read %s: %w", configPath, err)
		}
	} else if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}
	if err := upsertSimpleHookMap(raw, "hooks", nativeEvent, hook); err != nil {
		return fmt.Errorf("patch %s: %w", configPath, err)
	}
	out, err := yaml.Marshal(raw)
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

func nativeHermesHook(hook hookConfig) hookConfig {
	if hook.Timeout > hermesMaxHookTimeout {
		hook.Timeout = hermesMaxHookTimeout
	}
	return hook
}

func inspectCodexHook(hook hookConfig, home string) (string, error) {
	state, err := inspectNestedJSONHook(codexHooksConfigPath(home), nativeCodexHook(hook))
	if err != nil || state != stateSynced {
		return state, err
	}
	if !codexHooksFeatureEnabled(home) {
		return stateDrifted, nil
	}
	return stateSynced, nil
}

func patchCodexHook(hook hookConfig, home string) error {
	if err := patchNestedJSONHook(codexHooksConfigPath(home), nativeCodexHook(hook)); err != nil {
		return err
	}
	return patchCodexHooksFeature(home)
}

func nativeCodexHook(hook hookConfig) hookConfig {
	// Codex gives SessionEnd hooks at most three seconds, even when a larger
	// timeout is configured. Render the effective value so status does not
	// report permanent drift from an impossible desired timeout.
	if hook.Event == "SessionEnd" && hook.Timeout > codexSessionEndMaxTimeout {
		hook.Timeout = codexSessionEndMaxTimeout
	}
	return hook
}

func inspectDroidHook(hook hookConfig, home string) (string, error) {
	return inspectNestedJSONHook(activeDroidHooksConfigPath(home), hook)
}

func patchDroidHook(hook hookConfig, home string) error {
	return patchNestedJSONHook(activeDroidHooksConfigPath(home), hook)
}

func inspectQwenHook(hook hookConfig, home string) (string, error) {
	return inspectNestedJSONHook(qwenSettingsPath(home), nativeQwenHook(hook))
}

func patchQwenHook(hook hookConfig, home string) error {
	return patchNestedJSONHook(qwenSettingsPath(home), nativeQwenHook(hook))
}

func nativeQwenHook(hook hookConfig) hookConfig {
	// Qwen Code expresses command hook timeouts in milliseconds. Canonical
	// tackroom hook timeouts are seconds, matching the other harnesses.
	if hook.Timeout > 0 {
		hook.Timeout *= 1000
	}
	return hook
}

func removeNativeManagedMemoryHooks(home string, root string, prior config, current config) (int, error) {
	commands := managedMemoryNativeCommands(home, root, prior, current)
	if len(commands) == 0 {
		return 0, nil
	}
	changed := 0
	removers := []func(string, []string) (bool, error){
		func(home string, commands []string) (bool, error) {
			return removeGroupedJSONHookCommands(claudeHooksConfigPath(home), commands)
		},
		func(home string, commands []string) (bool, error) {
			return removeGroupedJSONHookCommands(codexHooksConfigPath(home), commands)
		},
		func(home string, commands []string) (bool, error) {
			return removeGroupedJSONHookCommands(droidHooksConfigPath(home), commands)
		},
		func(home string, commands []string) (bool, error) {
			return removeGroupedJSONHookCommands(droidLegacyHooksConfigPath(home), commands)
		},
		func(home string, commands []string) (bool, error) {
			return removeGroupedJSONHookCommands(qwenSettingsPath(home), commands)
		},
		func(home string, commands []string) (bool, error) {
			return removeSimpleYAMLHookCommands(filepath.Join(home, ".hermes", "config.yaml"), commands)
		},
	}
	for _, remove := range removers {
		didChange, err := remove(home, commands)
		if err != nil {
			return changed, err
		}
		if didChange {
			changed++
		}
	}
	return changed, nil
}

func managedMemoryNativeCommands(home string, root string, cfgs ...config) []string {
	seen := make(map[string]struct{})
	var commands []string
	add := func(command string) {
		command = strings.TrimSpace(command)
		if command == "" {
			return
		}
		if _, ok := seen[command]; ok {
			return
		}
		seen[command] = struct{}{}
		commands = append(commands, command)
	}
	for _, cfg := range cfgs {
		for _, hook := range cfg.Hooks {
			if _, managed := managedMemoryHookNames[hook.Name]; managed {
				add(hook.Command)
			}
		}
	}
	for _, commandRoot := range []string{root, filepath.Join(home, ".agents")} {
		for _, script := range []string{"basic-session-start.py", "basic-session-end.py", "session-start.sh", "stop.sh", "session-end.sh", "sync-vault-to-memory.sh", "sync-memory-to-vault.sh"} {
			add(managedMemoryHookCommand(commandRoot, home, script))
		}
	}
	return commands
}

func removeGroupedJSONHookCommands(configPath string, commands []string) (bool, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", configPath, err)
	}
	var raw map[string]interface{}
	if err := parseJSONConfig(configPath, data, &raw); err != nil {
		return false, fmt.Errorf("parse %s: %w", configPath, err)
	}
	if !removeGroupedHookCommands(raw, commands) {
		return false, nil
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal %s: %w", configPath, err)
	}
	out = append(out, '\n')
	if err := os.WriteFile(configPath, out, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", configPath, err)
	}
	return true, nil
}

func removeSimpleYAMLHookCommands(configPath string, commands []string) (bool, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", configPath, err)
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return false, fmt.Errorf("parse %s: %w", configPath, err)
	}
	if !removeSimpleHookCommands(raw, "hooks", commands) {
		return false, nil
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		return false, fmt.Errorf("marshal %s: %w", configPath, err)
	}
	if err := os.WriteFile(configPath, out, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", configPath, err)
	}
	return true, nil
}

func codexHooksConfigPath(home string) string {
	return filepath.Join(home, ".codex", "hooks.json")
}

func droidHooksConfigPath(home string) string {
	return filepath.Join(home, ".factory", "hooks.json")
}

func droidLegacyHooksConfigPath(home string) string {
	return filepath.Join(home, ".factory", "settings.json")
}

func activeDroidHooksConfigPath(home string) string {
	primary := droidHooksConfigPath(home)
	if hasFile(primary) {
		return primary
	}
	legacy := droidLegacyHooksConfigPath(home)
	if hasDroidLegacyHooks(legacy) {
		return legacy
	}
	return primary
}

func hasDroidLegacyHooks(configPath string) bool {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return false
	}
	var raw map[string]interface{}
	if err := parseJSONConfig(configPath, data, &raw); err != nil {
		return false
	}
	_, ok := raw["hooks"].(map[string]interface{})
	return ok
}

func inspectNestedJSONHook(configPath string, hook hookConfig) (string, error) {
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
	return inspectNestedJSONHookMap(raw, hook), nil
}

func patchNestedJSONHook(configPath string, hook hookConfig) error {
	data, err := os.ReadFile(configPath)
	raw := map[string]interface{}{}
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read %s: %w", configPath, err)
		}
	} else if err := parseJSONConfig(configPath, data, &raw); err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}
	if err := upsertNestedJSONHookMap(raw, hook); err != nil {
		return fmt.Errorf("patch %s: %w", configPath, err)
	}
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

func claudeHooksConfigPath(home string) string {
	return filepath.Join(home, ".claude", "settings.json")
}

func inspectClaudeHookMap(raw map[string]interface{}, hook hookConfig) string {
	// Claude Code ignores grouped hook entries that lack "type": "command",
	// so require it: a type-less entry is drift that must be rewritten.
	return inspectGroupedHookMap(raw, hook, true)
}

func inspectNestedJSONHookMap(raw map[string]interface{}, hook hookConfig) string {
	return inspectGroupedHookMap(raw, hook, true)
}

func inspectGroupedHookMap(raw map[string]interface{}, hook hookConfig, requireType bool) string {
	hooksRoot, ok := raw["hooks"].(map[string]interface{})
	if !ok {
		return stateMissing
	}
	groups, ok := hooksRoot[hook.Event].([]interface{})
	if !ok {
		return stateMissing
	}
	found := false
	for _, groupRaw := range groups {
		group, ok := groupRaw.(map[string]interface{})
		if !ok {
			continue
		}
		items, ok := group["hooks"].([]interface{})
		if !ok {
			continue
		}
		for _, itemRaw := range items {
			item, ok := itemRaw.(map[string]interface{})
			if !ok || !hookCommandMatches(item["command"], hook.Command) {
				continue
			}
			found = true
			if (!requireType || item["type"] == "command") && hookTimeoutMatches(item, hook.Timeout) {
				return stateSynced
			}
		}
	}
	if found {
		return stateDrifted
	}
	return stateMissing
}

func upsertClaudeHookMap(raw map[string]interface{}, hook hookConfig) error {
	// Claude Code requires "type": "command" on grouped hook entries; render
	// the nested form so patched hooks are honored rather than silently ignored.
	return upsertGroupedHookMap(raw, hook, renderNestedHookEntry)
}

func upsertNestedJSONHookMap(raw map[string]interface{}, hook hookConfig) error {
	return upsertGroupedHookMap(raw, hook, renderNestedHookEntry)
}

func upsertGroupedHookMap(raw map[string]interface{}, hook hookConfig, render func(hookConfig) map[string]interface{}) error {
	hooksValue, hooksExist := raw["hooks"]
	hooksRoot, hooksValid := hooksValue.(map[string]interface{})
	if hooksExist && hooksValue != nil && !hooksValid {
		return fmt.Errorf("hooks must be an object")
	}
	if !hooksExist || hooksValue == nil {
		hooksRoot = map[string]interface{}{}
		raw["hooks"] = hooksRoot
	}
	groupsValue, groupsExist := hooksRoot[hook.Event]
	groups, groupsValid := groupsValue.([]interface{})
	if groupsExist && groupsValue != nil && !groupsValid {
		return fmt.Errorf("hooks.%s must be an array", hook.Event)
	}

	targetIndex := -1
	for i, groupRaw := range groups {
		group, ok := groupRaw.(map[string]interface{})
		if !ok {
			continue
		}
		items, ok := group["hooks"].([]interface{})
		if !ok {
			continue
		}
		if containsHookCommand(items, hook.Command) {
			targetIndex = i
			break
		}
	}
	if targetIndex == -1 {
		// A new unfiltered hook must get its own matcher-free group. Appending
		// it to an existing matcher group would silently narrow when it runs.
		groups = append(groups, map[string]interface{}{
			"hooks": []interface{}{render(hook)},
		})
		hooksRoot[hook.Event] = groups
		return nil
	}

	for i, groupRaw := range groups {
		group, ok := groupRaw.(map[string]interface{})
		if !ok {
			continue
		}
		items, ok := group["hooks"].([]interface{})
		if !ok {
			continue
		}
		items = removeHookCommand(items, hook.Command)
		if i == targetIndex {
			items = append(items, render(hook))
		}
		group["hooks"] = items
	}
	hooksRoot[hook.Event] = groups
	return nil
}

func inspectSimpleHookMap(raw map[string]interface{}, rootKey string, event string, hook hookConfig) string {
	root, ok := raw[rootKey].(map[string]interface{})
	if !ok {
		return stateMissing
	}
	items, ok := root[event].([]interface{})
	if !ok {
		return stateMissing
	}
	found := false
	for _, itemRaw := range items {
		item, ok := itemRaw.(map[string]interface{})
		if !ok || !hookCommandMatches(item["command"], hook.Command) {
			continue
		}
		found = true
		if hookTimeoutMatches(item, hook.Timeout) {
			return stateSynced
		}
	}
	if found {
		return stateDrifted
	}
	return stateMissing
}

func upsertSimpleHookMap(raw map[string]interface{}, rootKey string, event string, hook hookConfig) error {
	rootValue, rootExists := raw[rootKey]
	root, rootValid := rootValue.(map[string]interface{})
	if rootExists && rootValue != nil && !rootValid {
		return fmt.Errorf("%s must be an object", rootKey)
	}
	if !rootExists || rootValue == nil {
		root = map[string]interface{}{}
		raw[rootKey] = root
	}
	itemsValue, itemsExist := root[event]
	items, itemsValid := itemsValue.([]interface{})
	if itemsExist && itemsValue != nil && !itemsValid {
		return fmt.Errorf("%s.%s must be an array", rootKey, event)
	}
	items = removeHookCommand(items, hook.Command)
	items = append(items, renderHookEntry(hook))
	root[event] = items
	return nil
}

func renderHookEntry(hook hookConfig) map[string]interface{} {
	entry := map[string]interface{}{"command": hook.Command}
	if hook.Timeout > 0 {
		entry["timeout"] = hook.Timeout
	}
	return entry
}
func renderNestedHookEntry(hook hookConfig) map[string]interface{} {
	entry := renderHookEntry(hook)
	entry["type"] = "command"
	return entry
}

func removeHookCommand(items []interface{}, command string) []interface{} {
	out := items[:0]
	for _, itemRaw := range items {
		item, ok := itemRaw.(map[string]interface{})
		if ok && hookCommandMatches(item["command"], command) {
			continue
		}
		out = append(out, itemRaw)
	}
	return out
}

func removeHookCommands(items []interface{}, commands []string) ([]interface{}, bool) {
	changed := false
	out := items[:0]
	for _, itemRaw := range items {
		item, ok := itemRaw.(map[string]interface{})
		if ok && hookCommandMatchesAny(item["command"], commands) {
			changed = true
			continue
		}
		out = append(out, itemRaw)
	}
	return out, changed
}

func removeGroupedHookCommands(raw map[string]interface{}, commands []string) bool {
	hooksRoot, ok := raw["hooks"].(map[string]interface{})
	if !ok {
		return false
	}
	changed := false
	for event, groupsRaw := range hooksRoot {
		groups, ok := groupsRaw.([]interface{})
		if !ok {
			continue
		}
		keptGroups := groups[:0]
		for _, groupRaw := range groups {
			group, ok := groupRaw.(map[string]interface{})
			if !ok {
				keptGroups = append(keptGroups, groupRaw)
				continue
			}
			items, ok := group["hooks"].([]interface{})
			if !ok {
				keptGroups = append(keptGroups, groupRaw)
				continue
			}
			filtered, removed := removeHookCommands(items, commands)
			if removed {
				changed = true
				group["hooks"] = filtered
			}
			if len(filtered) > 0 || !removed {
				keptGroups = append(keptGroups, groupRaw)
			}
		}
		if len(keptGroups) == 0 {
			delete(hooksRoot, event)
			if len(groups) > 0 {
				changed = true
			}
			continue
		}
		hooksRoot[event] = keptGroups
	}
	if len(hooksRoot) == 0 {
		delete(raw, "hooks")
	}
	return changed
}

func removeSimpleHookCommands(raw map[string]interface{}, rootKey string, commands []string) bool {
	root, ok := raw[rootKey].(map[string]interface{})
	if !ok {
		return false
	}
	changed := false
	for event, itemsRaw := range root {
		items, ok := itemsRaw.([]interface{})
		if !ok {
			continue
		}
		filtered, removed := removeHookCommands(items, commands)
		if !removed {
			continue
		}
		changed = true
		if len(filtered) == 0 {
			delete(root, event)
			continue
		}
		root[event] = filtered
	}
	if len(root) == 0 {
		delete(raw, rootKey)
	}
	return changed
}

func hookCommandMatchesAny(actual interface{}, commands []string) bool {
	for _, command := range commands {
		if hookCommandMatches(actual, command) {
			return true
		}
	}
	return false
}

func containsHookCommand(items []interface{}, command string) bool {
	for _, itemRaw := range items {
		item, ok := itemRaw.(map[string]interface{})
		if ok && hookCommandMatches(item["command"], command) {
			return true
		}
	}
	return false
}

func hookCommandMatches(actual interface{}, expected string) bool {
	actualString, ok := actual.(string)
	if !ok {
		return false
	}
	if actualString == expected {
		return true
	}
	return normalizeHookCommand(actualString) == normalizeHookCommand(expected)
}

func normalizeHookCommand(command string) string {
	command = strings.TrimSpace(command)
	if !strings.HasPrefix(command, "~/") {
		return command
	}
	pathEnd := strings.IndexAny(command, " \t\r\n")
	if pathEnd == -1 {
		pathEnd = len(command)
	}
	pathToken := command[:pathEnd]
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		pathToken = filepath.Join(home, strings.TrimPrefix(pathToken, "~/"))
	}
	return pathToken + command[pathEnd:]
}

func hookTimeoutMatches(item map[string]interface{}, timeout int) bool {
	if timeout == 0 {
		_, exists := item["timeout"]
		return !exists
	}
	switch value := item["timeout"].(type) {
	case int:
		return value == timeout
	case int64:
		return value == int64(timeout)
	case float64:
		return value == float64(timeout)
	default:
		return false
	}
}

func hermesHookEvent(event string) (string, bool) {
	switch event {
	case "SessionStart":
		return "on_session_start", true
	case "SessionEnd":
		return "on_session_finalize", true
	case "on_session_start",
		"pre_llm_call",
		"post_llm_call",
		"pre_verify",
		"pre_approval_request",
		"post_approval_response",
		"on_session_end",
		"on_session_finalize",
		"on_session_reset",
		"subagent_start",
		"subagent_stop",
		"pre_gateway_dispatch",
		"pre_tool_call",
		"post_tool_call",
		"transform_tool_result",
		"transform_terminal_output",
		"transform_llm_output":
		return event, true
	default:
		return "", false
	}
}
func codexHooksFeatureEnabled(home string) bool {
	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		return false
	}
	content := string(data)
	values := parseTOMLTopLevelValues(content)
	if values["features.hooks"] == "true" || values["codex_hooks"] == "true" {
		return true
	}
	block, ok := extractTOMLSection(content, "[features]")
	if !ok {
		return false
	}
	return parseTOMLBlockValues(block)["hooks"] == "true"
}

func patchCodexHooksFeature(home string) error {
	configPath := filepath.Join(home, ".codex", "config.toml")
	data, err := os.ReadFile(configPath)
	content := ""
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read %s: %w", configPath, err)
		}
	} else {
		content = string(data)
		var doc map[string]interface{}
		if _, err := toml.Decode(content, &doc); err != nil {
			return fmt.Errorf("parse %s: %w", configPath, err)
		}
	}
	updated := upsertCodexHooksFeature(content)
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

func upsertCodexHooksFeature(content string) string {
	content = removeTOMLTopLevelKeys(content, "codex_hooks", "features.hooks")
	start := indexTOMLSectionHeader(content, "[features]")
	if start == -1 {
		return upsertTOMLSection(content, "[features]", "[features]\nhooks = true\n\n")
	}
	end := endTOMLSection(content, start, "[features]")
	section := upsertTOMLBlockBool(content[start:end], "hooks", true)
	return content[:start] + ensureTrailingBlankLine(section) + content[end:]
}

func parseTOMLTopLevelValues(content string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			break
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		values[strings.TrimSpace(parts[0])] = strings.TrimSpace(stripTOMLInlineComments(parts[1]))
	}
	return values
}

func removeTOMLTopLevelKeys(content string, keys ...string) string {
	if content == "" {
		return content
	}
	remove := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		remove[key] = struct{}{}
	}
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "[") {
			break
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			i++
			continue
		}
		key := strings.TrimSpace(parts[0])
		if _, ok := remove[key]; ok {
			lines = append(lines[:i], lines[i+1:]...)
			continue
		}
		i++
	}
	return strings.Join(lines, "\n")
}

func upsertTOMLBlockBool(block string, key string, value bool) string {
	valueString := "false"
	if value {
		valueString = "true"
	}
	line := fmt.Sprintf("%s = %s", key, valueString)
	newline := "\n"
	if strings.Contains(block, "\r\n") {
		newline = "\r\n"
		block = strings.ReplaceAll(block, "\r\n", "\n")
	}
	lines := strings.Split(block, "\n")
	if len(lines) == 0 {
		return line + "\n"
	}
	for i := 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == key {
			lines[i] = line
			return strings.Join(lines, newline)
		}
	}
	insertAt := 1
	lines = append(lines, "")
	copy(lines[insertAt+1:], lines[insertAt:])
	lines[insertAt] = line
	return strings.Join(lines, newline)
}

// inspectVersionedHookFile reads a `{"version": 1, "hooks": {event: [...]}}`
// file (Cursor and Copilot CLI) with flat command entries.
func inspectVersionedHookFile(configPath string, event string, hook hookConfig) (string, error) {
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
	state := inspectSimpleHookMap(raw, "hooks", event, hook)
	if state == stateSynced && raw["version"] == nil {
		return stateDrifted, nil
	}
	return state, nil
}

func patchVersionedHookFile(configPath string, event string, hook hookConfig) error {
	data, err := os.ReadFile(configPath)
	raw := map[string]interface{}{}
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read %s: %w", configPath, err)
		}
	} else if err := parseJSONConfig(configPath, data, &raw); err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}
	if raw["version"] == nil {
		raw["version"] = 1
	}
	if err := upsertSimpleHookMap(raw, "hooks", event, hook); err != nil {
		return fmt.Errorf("patch %s: %w", configPath, err)
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", configPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(configPath), err)
	}
	if err := os.WriteFile(configPath, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", configPath, err)
	}
	return nil
}
