package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Antigravity CLI keeps its user config under ~/.gemini, next to Gemini CLI:
// ~/.gemini/config holds MCP servers and hooks, ~/.gemini/antigravity-cli the
// CLI-only skills and settings (https://antigravity.google/docs/settings).

func agyConfigDir(home string) string {
	return filepath.Join(home, ".gemini", "config")
}

func agyMCPConfigPath(home string) string {
	return filepath.Join(agyConfigDir(home), "mcp_config.json")
}

func agyHooksConfigPath(home string) string {
	return filepath.Join(agyConfigDir(home), "hooks.json")
}

// agyHookEvent maps canonical (Claude Code) events to Antigravity CLI's hook
// events. The matcher-group form applies to the tool events only.
func agyHookEvent(event string) (name string, toolEvent bool, ok bool) {
	switch event {
	case "SessionStart", "Stop":
		return event, false, true
	case "PreToolUse", "PostToolUse":
		return event, true, true
	}
	return "", false, false
}

// agyHookEntry renders one tackroom hook as a named hooks.json entry:
// {"<name>": {"enabled": true, "<Event>": [...]}}.
func agyHookEntry(hook hookConfig) (map[string]interface{}, bool) {
	event, toolEvent, ok := agyHookEvent(hook.Event)
	if !ok {
		return nil, false
	}
	handler := map[string]interface{}{"type": "command", "command": hook.Command}
	if hook.Timeout > 0 {
		handler["timeout"] = hook.Timeout
	}
	var items []interface{}
	if toolEvent {
		items = []interface{}{map[string]interface{}{"matcher": "*", "hooks": []interface{}{handler}}}
	} else {
		items = []interface{}{handler}
	}
	return map[string]interface{}{"enabled": true, event: items}, true
}

func readAgyHooks(path string) (map[string]interface{}, error) {
	raw := map[string]interface{}{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return raw, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := parseJSONConfig(path, data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return raw, nil
}

func inspectAgyHook(hook hookConfig, home string) (string, error) {
	want, ok := agyHookEntry(hook)
	if !ok {
		return stateUnsupported, nil
	}
	raw, err := readAgyHooks(agyHooksConfigPath(home))
	if err != nil {
		return stateMissing, err
	}
	have, exists := raw[hook.Name]
	if !exists {
		return stateMissing, nil
	}
	a, _ := json.Marshal(have)
	b, _ := json.Marshal(want)
	if string(a) == string(b) {
		return stateSynced, nil
	}
	return stateDrifted, nil
}

func patchAgyHook(hook hookConfig, home string) error {
	entry, ok := agyHookEntry(hook)
	if !ok {
		return nil
	}
	path := agyHooksConfigPath(home)
	raw, err := readAgyHooks(path)
	if err != nil {
		return err
	}
	raw[hook.Name] = entry
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// agyHookCommands returns event -> commands for one named hooks.json entry.
func agyHookCommands(named map[string]interface{}) map[string][]string {
	out := map[string][]string{}
	for event, itemsRaw := range named {
		items, _ := itemsRaw.([]interface{})
		for _, itemRaw := range items {
			item, _ := itemRaw.(map[string]interface{})
			handlers := []interface{}{item}
			if nested, ok := item["hooks"].([]interface{}); ok {
				handlers = nested
			}
			for _, handlerRaw := range handlers {
				handler, _ := handlerRaw.(map[string]interface{})
				if command, _ := handler["command"].(string); strings.TrimSpace(command) != "" {
					out[event] = append(out[event], command)
				}
			}
		}
	}
	return out
}

// readAgyNativeHooks lists every command in hooks.json for `hook list`.
func readAgyNativeHooks(agent string, path string) ([]nativeHookEntry, error) {
	raw, err := readAgyHooks(path)
	if err != nil {
		return nil, err
	}
	var entries []nativeHookEntry
	for _, nameRaw := range raw {
		named, _ := nameRaw.(map[string]interface{})
		for event, commands := range agyHookCommands(named) {
			for _, command := range commands {
				entries = append(entries, nativeHookEntry{Agent: agent, Event: event, Command: command, ConfigPath: path})
			}
		}
	}
	return entries, nil
}

// removeAgyNativeHookEntries deletes each named hooks.json entry that holds a
// matching command.
func removeAgyNativeHookEntries(path string, entries []nativeHookEntry) (bool, error) {
	raw, err := readAgyHooks(path)
	if err != nil {
		return false, err
	}
	changed := false
	for name, nameRaw := range raw {
		named, _ := nameRaw.(map[string]interface{})
	match:
		for _, commands := range agyHookCommands(named) {
			for _, command := range commands {
				for _, entry := range entries {
					if hookCommandMatches(command, entry.Command) {
						delete(raw, name)
						changed = true
						break match
					}
				}
			}
		}
	}
	if !changed {
		return false, nil
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

// checkAgyDuplicateSkills warns when a skill sits in both skill roots the CLI
// loads that tackroom may write: ~/.gemini/skills (Gemini CLI's mirror, read
// by Antigravity CLI too) and ~/.gemini/antigravity-cli/skills.
func checkAgyDuplicateSkills(_ string, home string, cfg config) checkResult {
	const name = "agy duplicate skills"
	if !isAgentDetected(cfg, agentAgy) {
		return checkResult{name, checkStatusPass, agentAgy + " not detected, skipped"}
	}
	shared := openCodeSkillDirNames(filepath.Join(home, ".gemini", "skills"))
	own := openCodeSkillDirNames(filepath.Join(home, ".gemini", "antigravity-cli", "skills"))
	var dups []string
	for skill := range own {
		if shared[skill] {
			dups = append(dups, skill)
		}
	}
	if len(dups) > 0 {
		sort.Strings(dups)
		return checkResult{name, checkStatusWarn, fmt.Sprintf("%s listed in both ~/.gemini/skills and ~/.gemini/antigravity-cli/skills", strings.Join(dups, ", "))}
	}
	return checkResult{name, checkStatusPass, "no duplicate skill listings"}
}
