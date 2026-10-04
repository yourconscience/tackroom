package app

import (
	"os"
	"path/filepath"
	"strings"
)

// grokHome resolves Grok Build's config directory, honoring $GROK_HOME when set
// (per the Grok Build README).
func grokHome(home string) string {
	if env := strings.TrimSpace(os.Getenv("GROK_HOME")); env != "" {
		return env
	}
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
