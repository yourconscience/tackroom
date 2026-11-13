package app

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// terminalVersion runs a terminal's version command and returns its first
// line. The deadline keeps a hung binary from blocking `tackroom doctor`.
func terminalVersion(bin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return line, nil
}

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
	version, err := terminalVersion(tern, "--version")
	if err != nil {
		return checkResult{"tern", checkStatusWarn, "tern present but not runnable"}
	}
	return checkResult{"tern", checkStatusPass, version}
}
