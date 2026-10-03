package app

import (
	"os"
	"path/filepath"
	"strings"
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
  - name: claude-code
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

func TestRunSyncContinuesPastBrokenAgentConfig(t *testing.T) {
	home, _ := setupBrokenHermesFixture(t)

	stdout, _, err := captureCLIOutput(t, func() error { return runSync(runOptions{}) })
	if err == nil || !strings.Contains(err.Error(), "hermes") {
		t.Fatalf("sync should fail and name hermes, got err=%v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(home, ".claude", "skills", "sample")); statErr != nil {
		t.Fatalf("claude-code was not synced past the broken Hermes config: %v\n%s", statErr, stdout)
	}
	if !strings.Contains(stdout, "config.yaml") {
		t.Fatalf("sync output should show the Hermes parse error:\n%s", stdout)
	}
	if got := readFileString(t, filepath.Join(home, ".hermes", "config.yaml")); got != brokenHermesConfig {
		t.Fatalf("sync rewrote the broken Hermes config:\n%s", got)
	}
}

func TestRunStatusReportsBrokenAgentAndOthers(t *testing.T) {
	setupBrokenHermesFixture(t)
	if _, _, err := captureCLIOutput(t, func() error { return runSync(runOptions{}) }); err == nil {
		t.Fatal("sync with a broken Hermes config should return an error")
	}

	stdout, _, err := captureCLIOutput(t, func() error { return runStatus(runOptions{}) })
	if err == nil || !strings.Contains(err.Error(), "hermes") {
		t.Fatalf("status should fail and name hermes, got err=%v", err)
	}
	if !strings.Contains(stdout, "claude-code") || !strings.Contains(stdout, "config.yaml") {
		t.Fatalf("status should report claude-code and the Hermes parse error:\n%s", stdout)
	}
	if strings.Contains(stdout, "Everything is synced.") {
		t.Fatalf("status claimed everything is synced with a broken agent:\n%s", stdout)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
