package app

import (
	"os"
	"path/filepath"
	"testing"
)

// brokenHermesConfig reproduces a real config.yaml with a duplicated
// plugins.enabled key, which yaml.v3 refuses to parse.
const brokenHermesConfig = "plugins:\n  enabled:\n    - herdr-agent-state\n    enabled:\n        - dashboard_auth/basic\n"

func setupBrokenHermesFixture(t *testing.T) (home string, repoRoot string) {
	t.Helper()
	home = t.TempDir()
	repoRoot = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TACKROOM_HOME", repoRoot)
	writeSyncTestFile(t, filepath.Join(repoRoot, "tackroom.yaml"), []byte(`version: 1
agents:
  - name: claude
    enabled: true
    skill_root: ~/.claude/skills
  - name: hermes
    enabled: true
    skill_root: ~/.hermes/skills
`))
	writeSyncTestFile(t, filepath.Join(repoRoot, "skills", "sample", "SKILL.md"), []byte("---\nname: sample\ndescription: Sample skill.\n---\n"))
	writeSyncTestFile(t, filepath.Join(home, ".hermes", "config.yaml"), []byte(brokenHermesConfig))
	return home, repoRoot
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
