package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func copilotHome(home string) string {
	return filepath.Join(home, ".copilot")
}

func copilotMCPConfigPath(home string) string {
	return filepath.Join(copilotHome(home), "mcp-config.json")
}

// copilotHooksConfigPath is the hook file tackroom owns inside Copilot's
// user hooks directory; other files there are left alone.
func copilotHooksConfigPath(home string) string {
	return filepath.Join(copilotHome(home), "hooks", "tackroom.json")
}

// copilotHookEvents are the Claude-style event names Copilot CLI accepts; for
// these it sends Claude-style payloads.
var copilotHookEvents = map[string]bool{
	"SessionStart": true, "SessionEnd": true, "UserPromptSubmit": true, "PreToolUse": true,
	"PostToolUse": true, "PostToolUseFailure": true, "Stop": true, "SubagentStop": true,
	"ErrorOccurred": true, "PreCompact": true,
}

func inspectCopilotHook(hook hookConfig, home string) (string, error) {
	if !copilotHookEvents[hook.Event] {
		return stateUnsupported, nil
	}
	return inspectVersionedHookFile(copilotHooksConfigPath(home), hook.Event, hook)
}

func patchCopilotHook(hook hookConfig, home string) error {
	if !copilotHookEvents[hook.Event] {
		return nil
	}
	return patchVersionedHookFile(copilotHooksConfigPath(home), hook.Event, hook)
}

// patchCopilotMCPServer writes the shared JSON shape, then adds the `tools`
// allowlist Copilot's documented examples carry when the entry has none.
func patchCopilotMCPServer(target mcpTarget, server mcpServerConfig, home string) error {
	if err := patchJSONMCPServer(target, server, home); err != nil {
		return err
	}
	configPath := target.configPath(home)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}
	var raw map[string]interface{}
	if err := parseJSONConfig(configPath, data, &raw); err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}
	entry, ok := mapMCPEntry(raw, target.rootKey, server.Name)
	if !ok {
		return nil
	}
	if _, exists := entry["tools"]; exists {
		return nil
	}
	entry["tools"] = []interface{}{"*"}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", configPath, err)
	}
	return os.WriteFile(configPath, append(out, '\n'), 0o644)
}
