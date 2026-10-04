package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func coexistenceFixture(t *testing.T) (repoRoot, home string, cfg config) {
	t.Helper()
	home = t.TempDir()
	repoRoot = filepath.Join(home, ".agents")
	if err := os.MkdirAll(filepath.Join(repoRoot, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg = config{Agents: []agentConfig{{Name: agentClaudeCode, Enabled: true, SkillRoot: "~/.claude/skills"}}}
	return repoRoot, home, cfg
}

func TestCoexistencePassesWhenNoOtherManagers(t *testing.T) {
	repoRoot, home, cfg := coexistenceFixture(t)
	got := checkAgentManagerCoexistence(repoRoot, home, cfg)
	if got.status != checkStatusPass || got.detail != "no other agent managers found" {
		t.Fatalf("got %+v", got)
	}
}

func TestCoexistenceWarnsOnSentryDotagentsState(t *testing.T) {
	repoRoot, home, cfg := coexistenceFixture(t)
	writeSyncTestFile(t, filepath.Join(repoRoot, "agents.toml"), []byte("version = 1\n"))
	got := checkAgentManagerCoexistence(repoRoot, home, cfg)
	if got.status != checkStatusWarn || !strings.Contains(got.detail, "Sentry dotagents") || !strings.Contains(got.detail, "agents.toml") {
		t.Fatalf("got %+v", got)
	}
}

func TestCoexistenceWarnsWhenCCSwitchStoresSkillsInAgentsFolder(t *testing.T) {
	repoRoot, home, cfg := coexistenceFixture(t)
	writeSyncTestFile(t, filepath.Join(home, ".cc-switch", "settings.json"), []byte(`{"skillStorageLocation":"unified"}`))
	got := checkAgentManagerCoexistence(repoRoot, home, cfg)
	if got.status != checkStatusWarn || !strings.Contains(got.detail, "cc-switch") || !strings.Contains(got.detail, "~/.agents/skills") {
		t.Fatalf("got %+v", got)
	}
}

func TestCoexistenceWarnsOnCCSwitchSkillLinksInAgentRoots(t *testing.T) {
	repoRoot, home, cfg := coexistenceFixture(t)
	writeSyncTestFile(t, filepath.Join(home, ".cc-switch", "settings.json"), []byte(`{"skillStorageLocation":"cc_switch"}`))
	target := filepath.Join(home, ".cc-switch", "skills", "pdf-tools")
	writeSyncTestFile(t, filepath.Join(target, "SKILL.md"), []byte("---\nname: pdf-tools\n---\n"))
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".claude", "skills", "pdf-tools")); err != nil {
		t.Fatal(err)
	}
	got := checkAgentManagerCoexistence(repoRoot, home, cfg)
	if got.status != checkStatusWarn || !strings.Contains(got.detail, "claude: pdf-tools") {
		t.Fatalf("got %+v", got)
	}
}

func TestCoexistenceNotesManagersWithoutOverlap(t *testing.T) {
	repoRoot, home, cfg := coexistenceFixture(t)
	writeSyncTestFile(t, filepath.Join(home, ".cc-switch", "settings.json"), []byte(`{"skillStorageLocation":"cc_switch"}`))
	if err := os.MkdirAll(filepath.Join(home, ".harnesskit", "kits"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := checkAgentManagerCoexistence(repoRoot, home, cfg)
	if got.status != checkStatusPass || !strings.Contains(got.detail, "cc-switch") || !strings.Contains(got.detail, "HarnessKit") {
		t.Fatalf("got %+v", got)
	}
}
