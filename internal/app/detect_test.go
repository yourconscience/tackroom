package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHashDirDeterministic(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	h1, err := hashDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := hashDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("same directory produced different hashes: %s vs %s", h1, h2)
	}
	if h1 == "" {
		t.Fatal("hash is empty")
	}
}

func TestHashDirDiffersOnContent(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir1, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "a.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	h1, err := hashDir(dir1)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := hashDir(dir2)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Fatal("different content produced the same hash")
	}
}

func TestMarkIdenticalSingleSource(t *testing.T) {
	item := &detectedItem{
		Sources: []detectedSource{{Hash: "abc123"}},
	}
	markIdentical(item)
	if item.Identical {
		t.Fatal("single source should not be marked identical")
	}
}

func TestMarkIdenticalMatching(t *testing.T) {
	item := &detectedItem{
		Sources: []detectedSource{
			{Harness: "claude", Hash: "abc123"},
			{Harness: "codex", Hash: "abc123"},
		},
	}
	markIdentical(item)
	if !item.Identical {
		t.Fatal("matching hashes should be marked identical")
	}
}

func TestMarkIdenticalDiffering(t *testing.T) {
	item := &detectedItem{
		Sources: []detectedSource{
			{Harness: "claude", Hash: "abc123"},
			{Harness: "codex", Hash: "def456"},
		},
	}
	markIdentical(item)
	if item.Identical {
		t.Fatal("differing hashes should not be marked identical")
	}
}

func TestDetectNativeSkillsSkipsSymlinks(t *testing.T) {
	home := t.TempDir()
	canonicalSkills := filepath.Join(home, "canonical", "skills")
	harnessSkills := filepath.Join(home, "harness", "skills")
	if err := os.MkdirAll(filepath.Join(canonicalSkills, "managed-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonicalSkills, "managed-skill", "SKILL.md"), []byte("---\nname: managed-skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(harnessSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(canonicalSkills, "managed-skill"), filepath.Join(harnessSkills, "managed-skill")); err != nil {
		t.Fatal(err)
	}
	// Also create a real (non-managed) skill.
	realSkill := filepath.Join(harnessSkills, "local-skill")
	if err := os.MkdirAll(realSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realSkill, "SKILL.md"), []byte("---\nname: local-skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	agent := agentConfig{Name: "claude"}
	found, err := detectNativeSkills(agent, harnessSkills, canonicalSkills)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 non-managed skill, got %d", len(found))
	}
	if found[0].Name != "local-skill" {
		t.Fatalf("expected local-skill, got %s", found[0].Name)
	}
}

func TestRunDetectionAutoResolvesIdentical(t *testing.T) {
	home := t.TempDir()
	repoRoot := filepath.Join(home, ".agents")
	canonicalSkills := filepath.Join(repoRoot, "skills")
	if err := os.MkdirAll(canonicalSkills, 0o755); err != nil {
		t.Fatal(err)
	}

	identical := []byte("---\nname: shared-skill\n---\nShared content\n")
	different := []byte("---\nname: unique-skill\n---\nDifferent content\n")

	// Two harnesses: one skill identical across both, one skill differs.
	harness1Skills := filepath.Join(home, ".claude", "skills")
	harness2Skills := filepath.Join(home, ".codex", "skills")
	for _, dir := range []string{harness1Skills, harness2Skills} {
		skillDir := filepath.Join(dir, "shared-skill")
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), identical, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// "unique-skill" exists in both but with different content.
	for i, dir := range []string{harness1Skills, harness2Skills} {
		skillDir := filepath.Join(dir, "unique-skill")
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := different
		if i == 1 {
			content = []byte("---\nname: unique-skill\n---\nOther version\n")
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config{Version: 1}
	detected := []agentConfig{
		{Name: "claude", Enabled: true, SkillRoot: harness1Skills},
		{Name: "codex", Enabled: true, SkillRoot: harness2Skills},
	}

	result, err := runDetection(cfg, detected, repoRoot, home)
	if err != nil {
		t.Fatal(err)
	}
	if result.AutoResolved != 1 {
		t.Fatalf("expected 1 auto-resolved (shared-skill), got %d", result.AutoResolved)
	}
	if len(result.Resolved) != 1 || result.Resolved[0].Name != "shared-skill" {
		t.Fatalf("expected shared-skill in Resolved, got %+v", result.Resolved)
	}
	if result.AutoResolved != len(result.Resolved) {
		t.Fatalf("AutoResolved (%d) must equal len(Resolved) (%d)", result.AutoResolved, len(result.Resolved))
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 review item (unique-skill), got %d", len(result.Items))
	}
	if result.Items[0].Name != "unique-skill" {
		t.Fatalf("expected unique-skill in review, got %s", result.Items[0].Name)
	}
}

func TestIsSymlinkOrResolvesTo(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if !isSymlinkOrResolvesTo(link, target) {
		t.Fatal("symlink should resolve to target")
	}
	if isSymlinkOrResolvesTo(target, filepath.Join(dir, "nonexistent")) {
		t.Fatal("non-symlink should not resolve to different path")
	}
}
