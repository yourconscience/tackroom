package app

import "path/filepath"

func grokHome(home string) string {
	return filepath.Join(home, ".grok")
}

// grokHooksConfigPath is the hook file tackroom owns inside Grok Build's user
// hooks directory. Grok reads it in Claude Code's settings format.
func grokHooksConfigPath(home string) string {
	return filepath.Join(grokHome(home), "hooks", "tackroom.json")
}

func inspectGrokHook(hook hookConfig, home string) (string, error) {
	return inspectNestedJSONHook(grokHooksConfigPath(home), hook)
}

func patchGrokHook(hook hookConfig, home string) error {
	return patchNestedJSONHook(grokHooksConfigPath(home), hook)
}
