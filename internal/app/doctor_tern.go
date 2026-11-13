package app

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// terminalVersionTimeout keeps a hung terminal binary from blocking
// `tackroom doctor`.
const terminalVersionTimeout = 5 * time.Second

// versionLine runs a version command and returns the first line it prints.
func versionLine(cmd *exec.Cmd) (string, error) {
	out, err := cmd.Output()
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
	if _, err := exec.LookPath("tern"); err != nil {
		return checkResult{"tern", checkStatusPass, "tern not installed (optional terminal), skipped"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), terminalVersionTimeout)
	defer cancel()
	version, err := versionLine(exec.CommandContext(ctx, "tern", "--version"))
	if err != nil {
		return checkResult{"tern", checkStatusWarn, "tern present but not runnable"}
	}
	return checkResult{"tern", checkStatusPass, version}
}
