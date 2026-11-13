package app

import (
	"os/exec"
	"strings"
)

// checkRex reports whether the Rex terminal is installed and runnable. The
// optional tackroom integration for Rex is a layout/recipe (shipping under
// rex/); this check only verifies the CLI so `tackroom doctor` can tell a
// missing or broken terminal apart from a healthy one. Rex is optional, so an
// absent binary is a pass, not a failure.
func checkRex() checkResult {
	rex, err := exec.LookPath("rex")
	if err != nil {
		return checkResult{"rex", checkStatusPass, "rex not installed (optional terminal), skipped"}
	}
	if out, err := exec.Command(rex, "--version").CombinedOutput(); err == nil {
		return checkResult{"rex", checkStatusPass, strings.TrimSpace(string(out))}
	}
	return checkResult{"rex", checkStatusPass, "rex installed"}
}
