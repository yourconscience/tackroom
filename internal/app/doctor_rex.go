package app

import "os/exec"

// checkRex reports whether the Rex terminal is installed and runnable. This
// check only verifies the CLI so `tackroom doctor` can tell a missing or
// broken terminal apart from a healthy one. Rex is optional, so an absent
// binary is a pass, not a failure.
func checkRex() checkResult {
	rex, err := exec.LookPath("rex")
	if err != nil {
		return checkResult{"rex", checkStatusPass, "rex not installed (optional terminal), skipped"}
	}
	// Rex has a `version` command and no --version flag.
	version, err := terminalVersion(rex, "version")
	if err != nil {
		return checkResult{"rex", checkStatusWarn, "rex present but not runnable"}
	}
	return checkResult{"rex", checkStatusPass, version}
}
