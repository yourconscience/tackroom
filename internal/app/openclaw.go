package app

import (
	"os"
	"path/filepath"
	"strings"
)

// openClawStateDir resolves OpenClaw's state directory, honoring
// $OPENCLAW_STATE_DIR (per OpenClaw's environment docs).
func openClawStateDir(home string) string {
	if env := strings.TrimSpace(os.Getenv("OPENCLAW_STATE_DIR")); env != "" {
		return env
	}
	return filepath.Join(home, ".openclaw")
}

// openClawConfigPath resolves openclaw.json; $OPENCLAW_CONFIG_PATH wins over
// the state directory.
func openClawConfigPath(home string) string {
	if env := strings.TrimSpace(os.Getenv("OPENCLAW_CONFIG_PATH")); env != "" {
		return env
	}
	return filepath.Join(openClawStateDir(home), "openclaw.json")
}

// openClawReadsAgentsSkills reports whether OpenClaw already loads tackroom
// skills from ~/.agents/skills. OpenClaw only scans that root in its default
// state; a custom $OPENCLAW_STATE_DIR drops it, so tackroom mirrors instead.
func openClawReadsAgentsSkills(repoRoot string, home string) bool {
	if strings.TrimSpace(os.Getenv("OPENCLAW_STATE_DIR")) != "" {
		return false
	}
	return readsAgentsSkillsRoot(repoRoot, home)
}
