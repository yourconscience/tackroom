package app

import "path/filepath"

func geminiSettingsPath(home string) string {
	return filepath.Join(home, ".gemini", "settings.json")
}

// geminiHookEvent maps canonical (Claude Code) events to Gemini CLI's hook
// events in ~/.gemini/settings.json.
func geminiHookEvent(event string) (string, bool) {
	switch event {
	case "SessionStart", "SessionEnd", "Notification":
		return event, true
	case "UserPromptSubmit":
		return "BeforeAgent", true
	case "Stop":
		return "AfterAgent", true
	case "PreToolUse":
		return "BeforeTool", true
	case "PostToolUse":
		return "AfterTool", true
	case "PreCompact":
		return "PreCompress", true
	}
	return "", false
}

// nativeGeminiHook renames the event and converts the timeout: Gemini CLI
// hook timeouts are milliseconds, canonical ones seconds.
func nativeGeminiHook(hook hookConfig) (hookConfig, bool) {
	event, ok := geminiHookEvent(hook.Event)
	if !ok {
		return hook, false
	}
	hook.Event = event
	if hook.Timeout > 0 {
		hook.Timeout *= 1000
	}
	return hook, true
}

func inspectGeminiHook(hook hookConfig, home string) (string, error) {
	native, ok := nativeGeminiHook(hook)
	if !ok {
		return stateUnsupported, nil
	}
	return inspectNestedJSONHook(geminiSettingsPath(home), native)
}

func patchGeminiHook(hook hookConfig, home string) error {
	native, ok := nativeGeminiHook(hook)
	if !ok {
		return nil
	}
	return patchNestedJSONHook(geminiSettingsPath(home), native)
}
