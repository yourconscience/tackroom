package app

import (
	"context"
	"os/exec"
)

// checkRex reports whether the Rex terminal is installed and runnable. This
// check only verifies the CLI so `tackroom doctor` can tell a missing or
// broken terminal apart from a healthy one. Rex is optional, so an absent
// binary is a pass, not a failure.
func checkRex() checkResult {
	if _, err := exec.LookPath("rex"); err != nil {
		return checkResult{"rex", checkStatusPass, "rex not installed (optional terminal), skipped"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), terminalVersionTimeout)
	defer cancel()
	// Rex has a `version` command and no --version flag.
	version, err := versionLine(exec.CommandContext(ctx, "rex", "version"))
	if err != nil {
		return checkResult{"rex", checkStatusWarn, "rex present but not runnable"}
	}
	return checkResult{"rex", checkStatusPass, version}
}
