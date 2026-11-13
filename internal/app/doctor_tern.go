package app

import (
	"context"
	"os/exec"
	"regexp"
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

// ternVersion matches the Tern terminal's `--version` line, its version and
// commit ("tern 0.9.18 (6f1fa0f)"); `tern --help` documents `--version` as
// "this build's version and commit".
var ternVersion = regexp.MustCompile(`^tern \S+ \([0-9a-f]+\)$`)

// checkTern reports whether the Tern terminal (Stencil Labs) is installed and
// runnable. The optional tackroom integration for Tern is the workspace command
// and the crons plugin (shipping under tern/); this check only verifies the CLI
// so `tackroom doctor` can tell a missing or broken terminal apart from a
// healthy one. Tern is optional, so an absent binary, or a `tern` on PATH that
// is not the Tern terminal, is a pass, not a failure.
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
	if !ternVersion.MatchString(version) {
		return checkResult{"tern", checkStatusPass, "tern on PATH is not the Tern terminal, skipped"}
	}
	return checkResult{"tern", checkStatusPass, version}
}
