package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigWithLocalOverlay(t *testing.T) {
	repoRoot := t.TempDir()
	home := t.TempDir()

	base := `version: 1
context_note_tokens: 8000
agents:
  - name: claude-code
    enabled: true
    skill_root: ~/.claude/skills
  - name: codex
    enabled: true
    skill_root: ~/.codex/skills
external_skills:
  - url: https://github.com/example/shared-skills
    branch: main
`
	local := `context_note_tokens: 0
agents:
  - name: codex
    enabled: false
    skill_root: ~/.codex/skills
external_skills:
  - url: https://github.com/example/private-skills
    branch: main
  - url: https://github.com/example/shared-skills
    branch: dev
    skills: [alpha]
`
	if err := os.WriteFile(filepath.Join(repoRoot, "tackroom.yaml"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "tackroom.local.yaml"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfig(repoRoot, home, filepath.Join(repoRoot, "tackroom.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(cfg.Agents))
	}
	for _, agent := range cfg.Agents {
		if agent.Name == "codex" && agent.Enabled {
			t.Fatal("local overlay should disable codex")
		}
	}

	if len(cfg.ExternalSkills) != 2 {
		t.Fatalf("expected 2 external sources, got %+v", cfg.ExternalSkills)
	}
	byName := make(map[string]externalSkillSource)
	for _, src := range cfg.ExternalSkills {
		byName[repoName(src.URL)] = src
	}
	if byName["shared-skills"].Branch != "dev" || len(byName["shared-skills"].Skills) != 1 {
		t.Fatalf("local overlay should replace shared-skills entry, got %+v", byName["shared-skills"])
	}
	if _, ok := byName["private-skills"]; !ok {
		t.Fatal("local overlay should append private-skills")
	}
	if cfg.ContextNoteTokens == nil || *cfg.ContextNoteTokens != 0 {
		t.Fatalf("local overlay should override context_note_tokens, got %v", cfg.ContextNoteTokens)
	}
}

func TestLoadConfigWithoutLocalOverlay(t *testing.T) {
	repoRoot := t.TempDir()
	home := t.TempDir()
	base := `version: 1
agents:
  - name: claude-code
    enabled: true
    skill_root: ~/.claude/skills
`
	if err := os.WriteFile(filepath.Join(repoRoot, "tackroom.yaml"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(repoRoot, home, filepath.Join(repoRoot, "tackroom.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(cfg.Agents))
	}
}

func TestLoadConfigWithInvalidLocalOverlay(t *testing.T) {
	repoRoot := t.TempDir()
	home := t.TempDir()
	base := `version: 1
agents:
  - name: claude-code
    enabled: true
    skill_root: ~/.claude/skills
`
	if err := os.WriteFile(filepath.Join(repoRoot, "tackroom.yaml"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "tackroom.local.yaml"), []byte("agents: {broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(repoRoot, home, filepath.Join(repoRoot, "tackroom.yaml")); err == nil {
		t.Fatal("expected error for invalid local overlay")
	}
}
