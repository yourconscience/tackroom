package app

import (
	"os/exec"
	"strings"
)

// checkTern reports whether the Tern multiplexing terminal is installed and
// runnable. The optional tackroom integration for Tern is a layout/recipe
// (shipping under tern/); this check only verifies the CLI so `tackroom doctor`
// can tell a missing or broken terminal apart from a healthy one. Tern is
// optional, so an absent binary is a pass, not a failure.
func checkTern() checkResult {
	tern, err := exec.LookPath("tern")
	if err != nil {
		return checkResult{"tern", checkStatusPass, "tern not installed (optional terminal), skipped"}
	}
	if out, err := exec.Command(tern, "--version").CombinedOutput(); err == nil {
		return checkResult{"tern", checkStatusPass, strings.TrimSpace(string(out))}
	}
	return checkResult{"tern", checkStatusWarn, "tern present but not runnable"}
}
