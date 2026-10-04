package app

import "path/filepath"

func cursorHooksConfigPath(home string) string {
	return filepath.Join(home, ".cursor", "hooks.json")
}

// cursorHookEvent maps canonical (Claude Code) events to Cursor's hook names,
// following Cursor's own Claude Code compatibility table.
func cursorHookEvent(event string) (string, bool) {
	switch event {
	case "SessionStart":
		return "sessionStart", true
	case "SessionEnd":
		return "sessionEnd", true
	case "Stop":
		return "stop", true
	case "PreToolUse":
		return "preToolUse", true
	case "PostToolUse":
		return "postToolUse", true
	case "UserPromptSubmit":
		return "beforeSubmitPrompt", true
	case "SubagentStop":
		return "subagentStop", true
	case "PreCompact":
		return "preCompact", true
	}
	return "", false
}

func inspectCursorHook(hook hookConfig, home string) (string, error) {
	event, ok := cursorHookEvent(hook.Event)
	if !ok {
		return stateUnsupported, nil
	}
	return inspectVersionedHookFile(cursorHooksConfigPath(home), event, hook)
}

func patchCursorHook(hook hookConfig, home string) error {
	event, ok := cursorHookEvent(hook.Event)
	if !ok {
		return nil
	}
	return patchVersionedHookFile(cursorHooksConfigPath(home), event, hook)
}
