package app

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoName(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://github.com/example/single-skill", "single-skill"},
		{"https://github.com/example/single-skill.git", "single-skill"},
		{"git@github.com:example/single-skill.git", "single-skill"},
		{"https://github.com/user/repo/", "repo"},
		{"simple-name", "simple-name"},
	}
	for _, tt := range tests {
		got := repoName(tt.url)
		if got != tt.want {
			t.Errorf("repoName(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func makeGitDir(t *testing.T, cachePath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(cachePath, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverExternalSkillsSingleSkill(t *testing.T) {
	home := t.TempDir()
	cacheRoot := filepath.Join(home, ".agents", "external")

	repoDir := filepath.Join(cacheRoot, "single-skill", "skill")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	makeGitDir(t, filepath.Join(cacheRoot, "single-skill"))
	if err := os.WriteFile(filepath.Join(repoDir, "SKILL.md"), []byte("---\nname: single-skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sources := []externalSkillSource{
		{URL: "https://github.com/example/single-skill", SkillDir: "skill", Branch: "main"},
	}

	result, err := discoverExternalSkills(sources, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(result))
	}
	if _, ok := result["single-skill"]; !ok {
		t.Fatalf("expected skill named 'single-skill', got %v", result)
	}
	if result["single-skill"] != repoDir {
		t.Fatalf("expected path %s, got %s", repoDir, result["single-skill"])
	}
}

func TestDiscoverExternalSkillsMultiSkill(t *testing.T) {
	home := t.TempDir()
	cacheRoot := filepath.Join(home, ".agents", "external")

	makeGitDir(t, filepath.Join(cacheRoot, "shared-skills"))
	skillsDir := filepath.Join(cacheRoot, "shared-skills", "skills")
	for _, name := range []string{"alpha", "beta"} {
		dir := filepath.Join(skillsDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	sources := []externalSkillSource{
		{URL: "https://github.com/user/shared-skills", SkillDir: "skills", Branch: "main"},
	}

	result, err := discoverExternalSkills(sources, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 skills, got %d: %v", len(result), result)
	}
	for _, name := range []string{"alpha", "beta"} {
		if _, ok := result[name]; !ok {
			t.Errorf("missing skill %q", name)
		}
	}
}

func TestDiscoverExternalSkillsCollision(t *testing.T) {
	home := t.TempDir()
	cacheRoot := filepath.Join(home, ".agents", "external")

	for _, repo := range []string{"repo-a", "repo-b"} {
		makeGitDir(t, filepath.Join(cacheRoot, repo))
		dir := filepath.Join(cacheRoot, repo, "skills", "samename")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: samename\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	sources := []externalSkillSource{
		{URL: "https://github.com/user/repo-a", SkillDir: "skills", Branch: "main"},
		{URL: "https://github.com/user/repo-b", SkillDir: "skills", Branch: "main"},
	}

	_, err := discoverExternalSkills(sources, home)
	if err == nil {
		t.Fatal("expected collision error, got nil")
	}
	for _, origin := range []string{`external Git "https://github.com/user/repo-a"`, `external Git "https://github.com/user/repo-b"`} {
		if !strings.Contains(err.Error(), origin) {
			t.Fatalf("collision error %q does not identify %s", err, origin)
		}
	}
}

func TestDiscoverExternalSkillsNotCloned(t *testing.T) {
	home := t.TempDir()

	sources := []externalSkillSource{
		{URL: "https://github.com/user/nonexistent", SkillDir: "skill", Branch: "main"},
	}

	_, err := discoverExternalSkills(sources, home)
	if err == nil {
		t.Fatal("expected error for not-cloned external source, got nil")
	}
}

func TestDiscoverExternalSkillsBadSkillDir(t *testing.T) {
	home := t.TempDir()
	cacheRoot := filepath.Join(home, ".agents", "external")

	repoDir := filepath.Join(cacheRoot, "single-skill")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	sources := []externalSkillSource{
		{URL: "https://github.com/example/single-skill", SkillDir: "wrong-dir", Branch: "main"},
	}

	_, err := discoverExternalSkills(sources, home)
	if err == nil {
		t.Fatal("expected error for bad skill_dir, got nil")
	}
}

func TestDiscoverExternalSkillsZeroSkills(t *testing.T) {
	home := t.TempDir()
	cacheRoot := filepath.Join(home, ".agents", "external")

	repoDir := filepath.Join(cacheRoot, "empty-repo")
	makeGitDir(t, repoDir)
	if err := os.MkdirAll(filepath.Join(repoDir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}

	sources := []externalSkillSource{
		{URL: "https://github.com/user/empty-repo", SkillDir: "skills", Branch: "main"},
	}

	_, err := discoverExternalSkills(sources, home)
	if err == nil {
		t.Fatal("expected error for zero skills, got nil")
	}
}

func TestDiscoverExternalSkillsNonGitDir(t *testing.T) {
	home := t.TempDir()
	cacheRoot := filepath.Join(home, ".agents", "external")

	repoDir := filepath.Join(cacheRoot, "not-a-repo", "skill")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	sources := []externalSkillSource{
		{URL: "https://github.com/user/not-a-repo", SkillDir: "skill", Branch: "main"},
	}

	_, err := discoverExternalSkills(sources, home)
	if err == nil {
		t.Fatal("expected error for non-git directory, got nil")
	}
}

func TestIsExternalSkillLink(t *testing.T) {
	home := t.TempDir()
	extDir := filepath.Join(home, ".agents", "external", "single-skill", "skill")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatal(err)
	}

	skillRoot := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	linkPath := filepath.Join(skillRoot, "single-skill")
	if err := os.Symlink(extDir, linkPath); err != nil {
		t.Fatal(err)
	}

	rawTarget, _ := os.Readlink(linkPath)
	if !isExternalSkillLink(linkPath, rawTarget, home) {
		t.Error("expected link to be recognized as external skill link")
	}

	localDir := filepath.Join(home, ".agents", "skills", "local")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatal(err)
	}
	localLink := filepath.Join(skillRoot, "local")
	if err := os.Symlink(localDir, localLink); err != nil {
		t.Fatal(err)
	}
	rawLocal, _ := os.Readlink(localLink)
	if isExternalSkillLink(localLink, rawLocal, home) {
		t.Error("local skill link should not be recognized as external")
	}
}

func writeExternalSkillTree(t *testing.T, home string, repo string, relDir string, name string, content string) string {
	t.Helper()
	path := filepath.Join(externalCacheDir(home), repo, filepath.FromSlash(relDir), name)
	writeSyncTestFile(t, filepath.Join(path, "SKILL.md"), []byte(content))
	return path
}

func TestMaterializedMultiDirSourceWritesCanonicalCopiesAndLockOwnership(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	cachePath := filepath.Join(externalCacheDir(home), "catalog")
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		t.Fatal(err)
	}
	alphaSource := writeExternalSkillTree(t, home, "catalog", "commands", "alpha", "---\nname: alpha\n---\nalpha v1\n")
	betaSource := writeExternalSkillTree(t, home, "catalog", "workflows", "beta", "---\nname: beta\n---\nbeta v1\n")
	git(t, cachePath, "init")
	git(t, cachePath, "config", "user.name", "Test User")
	git(t, cachePath, "config", "user.email", "test@example.com")
	git(t, cachePath, "add", ".")
	git(t, cachePath, "commit", "-m", "initial")

	writeSyncTestFile(t, filepath.Join(repoRoot, "tackroom.yaml"), []byte(`version: 1
agents:
  - name: codex
    enabled: true
    skill_root: ~/.codex/skills
external_skills:
  - url: https://github.com/example/catalog
    skill_dirs:
      - commands
      - workflows
    branch: main
    materialize: true
`))
	cfg, err := loadConfig(repoRoot, home, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ExternalSkills) != 1 {
		t.Fatalf("external source count = %d, want one unified source", len(cfg.ExternalSkills))
	}
	src := cfg.ExternalSkills[0]
	plan, err := planMaterializedSkills(cfg.ExternalSkills, home, repoRoot, lockFile{}, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMaterializedSkills(plan); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		sourcePath string
	}{
		{name: "alpha", sourcePath: alphaSource},
		{name: "beta", sourcePath: betaSource},
	} {
		canonical := filepath.Join(repoRoot, "skills", tc.name)
		equal, err := externalSkillTreesEqual(tc.sourcePath, canonical)
		if err != nil {
			t.Fatal(err)
		}
		if !equal {
			t.Fatalf("canonical %s is not an exact copy of %s", canonical, tc.sourcePath)
		}
		info, err := os.Lstat(canonical)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("canonical materialization %s is a symlink", canonical)
		}
	}

	entries := rebuildLockEntries([]externalSkillSource{src}, home, lockFile{})
	if len(entries) != 1 || strings.Join(entries[0].Materialized.Values(), ",") != "alpha,beta" {
		t.Fatalf("materialized lock ownership = %+v, want catalog owning alpha and beta", entries)
	}
	if err := writeLockFile(repoRoot, lockFile{ExternalSkills: entries}); err != nil {
		t.Fatal(err)
	}
	roundTripped, err := readLockFile(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(roundTripped.ExternalSkills) != 1 || strings.Join(roundTripped.ExternalSkills[0].Materialized.Values(), ",") != "alpha,beta" {
		t.Fatalf("persisted materialized ownership = %+v", roundTripped.ExternalSkills)
	}
}

func TestMaterializedRefreshReplacesTheExactOwnedTree(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	makeGitDir(t, filepath.Join(externalCacheDir(home), "catalog"))
	source := writeExternalSkillTree(t, home, "catalog", "skills", "alpha", "---\nname: alpha\n---\nalpha v2\n")
	writeSyncTestFile(t, filepath.Join(source, "new.txt"), []byte("new\n"))
	canonical := filepath.Join(repoRoot, "skills", "alpha")
	writeSyncTestFile(t, filepath.Join(canonical, "SKILL.md"), []byte("---\nname: alpha\n---\nalpha v1\n"))
	writeSyncTestFile(t, filepath.Join(canonical, "obsolete.txt"), []byte("obsolete\n"))

	src := externalSkillSource{URL: "https://github.com/example/catalog", SkillDir: "skills", Branch: "main", Materialize: true}
	lock := lockFile{ExternalSkills: []externalLockEntry{{
		Name: "catalog", URL: src.URL, Branch: src.Branch, Commit: "deadbeef",
		Materialized: newMaterializedSkillNames([]string{"alpha"}),
	}}}
	plan, err := planMaterializedSkills([]externalSkillSource{src}, home, repoRoot, lock, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMaterializedSkills(plan); err != nil {
		t.Fatal(err)
	}
	equal, err := externalSkillTreesEqual(source, canonical)
	if err != nil {
		t.Fatal(err)
	}
	if !equal {
		t.Fatal("refresh did not replace the canonical skill with the exact source tree")
	}
	if _, err := os.Lstat(filepath.Join(canonical, "obsolete.txt")); !os.IsNotExist(err) {
		t.Fatalf("obsolete file survived exact refresh, stat error = %v", err)
	}
}

func TestMaterializedRefreshRejectsDifferentSourceIdentity(t *testing.T) {
	tests := []struct {
		name          string
		lockedURL     string
		lockedBranch  string
		currentURL    string
		currentBranch string
	}{
		{
			name:          "different URL with same repository basename",
			lockedURL:     "https://github.com/previous/catalog",
			lockedBranch:  "main",
			currentURL:    "https://github.com/replacement/catalog",
			currentBranch: "main",
		},
		{
			name:          "different branch",
			lockedURL:     "https://github.com/example/catalog",
			lockedBranch:  "main",
			currentURL:    "https://github.com/example/catalog",
			currentBranch: "release",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			repoRoot := t.TempDir()
			makeGitDir(t, filepath.Join(externalCacheDir(home), "catalog"))
			writeExternalSkillTree(t, home, "catalog", "skills", "alpha", "---\nname: alpha\n---\nreplacement\n")
			canonicalFile := filepath.Join(repoRoot, "skills", "alpha", "SKILL.md")
			writeSyncTestFile(t, canonicalFile, []byte("original owner\n"))

			src := externalSkillSource{URL: tc.currentURL, SkillDir: "skills", Branch: tc.currentBranch, Materialize: true}
			lock := lockFile{ExternalSkills: []externalLockEntry{{
				Name: "catalog", URL: tc.lockedURL, Branch: tc.lockedBranch, Commit: "deadbeef",
				Materialized: newMaterializedSkillNames([]string{"alpha"}),
			}}}

			_, err := planMaterializedSkills([]externalSkillSource{src}, home, repoRoot, lock, true, nil)
			if err == nil {
				t.Fatal("different source identity was allowed to replace the existing materialized skill")
			}
			data, readErr := os.ReadFile(canonicalFile)
			if readErr != nil || string(data) != "original owner\n" {
				t.Fatalf("rejected refresh changed the previous owner's target: data=%q err=%v", data, readErr)
			}
		})
	}
}

func TestMaterializedLockRejectsDuplicateSkillOwnership(t *testing.T) {
	repoRoot := t.TempDir()
	canonicalFile := filepath.Join(repoRoot, "skills", "alpha", "SKILL.md")
	writeSyncTestFile(t, canonicalFile, []byte("existing owner\n"))
	lock := lockFile{ExternalSkills: []externalLockEntry{
		{
			Name: "catalog-a", URL: "https://github.com/example/catalog-a", Branch: "main", Commit: "aaaa",
			Materialized: newMaterializedSkillNames([]string{"alpha"}),
		},
		{
			Name: "catalog-b", URL: "https://github.com/example/catalog-b", Branch: "main", Commit: "bbbb",
			Materialized: newMaterializedSkillNames([]string{"alpha"}),
		},
	}}

	_, err := planMaterializedSkills(nil, t.TempDir(), repoRoot, lock, true, nil)
	if err == nil {
		t.Fatal("duplicate materialized skill ownership in the lock was accepted")
	}
	data, readErr := os.ReadFile(canonicalFile)
	if readErr != nil || string(data) != "existing owner\n" {
		t.Fatalf("invalid duplicate ownership changed the canonical target: data=%q err=%v", data, readErr)
	}
}

func TestSelectedMaterializedUpdateRemovesOnlyItsStaleOwnedSkills(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	makeGitDir(t, filepath.Join(externalCacheDir(home), "catalog-a"))
	writeExternalSkillTree(t, home, "catalog-a", "skills", "current-a", "---\nname: current-a\n---\n")
	writeSyncTestFile(t, filepath.Join(repoRoot, "skills", "stale-a", "SKILL.md"), []byte("owned by a\n"))
	writeSyncTestFile(t, filepath.Join(repoRoot, "skills", "stale-b", "SKILL.md"), []byte("owned by b\n"))
	src := externalSkillSource{URL: "https://github.com/example/catalog-a", SkillDir: "skills", Branch: "main", Materialize: true}
	lock := lockFile{ExternalSkills: []externalLockEntry{
		{Name: "catalog-a", URL: src.URL, Branch: src.Branch, Commit: "aaaa", Materialized: newMaterializedSkillNames([]string{"stale-a"})},
		{Name: "catalog-b", URL: "https://github.com/example/catalog-b", Branch: "main", Commit: "bbbb", Materialized: newMaterializedSkillNames([]string{"stale-b"})},
	}}

	plan, err := planMaterializedSkills([]externalSkillSource{src}, home, repoRoot, lock, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMaterializedSkills(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(repoRoot, "skills", "stale-a")); !os.IsNotExist(err) {
		t.Fatalf("selected source's stale owned skill remains, stat error = %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(repoRoot, "skills", "stale-b", "SKILL.md")); err != nil || string(data) != "owned by b\n" {
		t.Fatalf("out-of-scope source was changed: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "skills", "current-a", "SKILL.md")); err != nil {
		t.Fatalf("selected source's current skill was not materialized: %v", err)
	}
}

func TestMaterializationConflictPreservesUnrelatedCanonicalSkill(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	makeGitDir(t, filepath.Join(externalCacheDir(home), "catalog"))
	writeExternalSkillTree(t, home, "catalog", "skills", "alpha", "---\nname: alpha\n---\nexternal\n")
	canonicalFile := filepath.Join(repoRoot, "skills", "alpha", "SKILL.md")
	writeSyncTestFile(t, canonicalFile, []byte("unrelated sentinel\n"))
	src := externalSkillSource{URL: "https://github.com/example/catalog", SkillDir: "skills", Branch: "main", Materialize: true}

	_, err := planMaterializedSkills([]externalSkillSource{src}, home, repoRoot, lockFile{}, true, nil)
	if err == nil || !strings.Contains(err.Error(), "collides with unrelated canonical skill") || !strings.Contains(err.Error(), filepath.Dir(canonicalFile)) {
		t.Fatalf("materialization conflict error = %v", err)
	}
	data, readErr := os.ReadFile(canonicalFile)
	if readErr != nil || string(data) != "unrelated sentinel\n" {
		t.Fatalf("conflict changed unrelated canonical skill: data=%q err=%v", data, readErr)
	}
}

func TestLegacyDirectExternalSourceRemainsDeliverableWithoutMaterialization(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	makeGitDir(t, filepath.Join(externalCacheDir(home), "legacy-catalog"))
	cacheSkill := filepath.Join(externalCacheDir(home), "legacy-catalog", "skill")
	writeSyncTestFile(t, filepath.Join(cacheSkill, "SKILL.md"), []byte("---\nname: legacy-catalog\n---\n"))
	src := externalSkillSource{URL: "https://github.com/example/legacy-catalog", SkillDir: "skill", Branch: "main"}

	expected, err := expectedSkills(repoRoot, home, config{ExternalSkills: []externalSkillSource{src}})
	if err != nil {
		t.Fatal(err)
	}
	if len(expected) != 1 || expected["legacy-catalog"] != cacheSkill {
		t.Fatalf("legacy direct external source = %#v, want cache path %q", expected, cacheSkill)
	}
	if _, err := os.Lstat(filepath.Join(repoRoot, "skills", "legacy-catalog")); !os.IsNotExist(err) {
		t.Fatalf("legacy direct mode unexpectedly materialized a canonical copy, stat error = %v", err)
	}
}

func TestDoctorRejectsDirectCacheLinkForMaterializedSkill(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	makeGitDir(t, filepath.Join(externalCacheDir(home), "catalog"))
	cacheSkill := writeExternalSkillTree(t, home, "catalog", "skills", "alpha", "---\nname: alpha\n---\n")
	canonicalSkill := filepath.Join(repoRoot, "skills", "alpha")
	writeSyncTestFile(t, filepath.Join(canonicalSkill, "SKILL.md"), []byte("---\nname: alpha\n---\n"))
	skillRoot := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(skillRoot, "alpha")
	if err := os.Symlink(cacheSkill, link); err != nil {
		t.Fatal(err)
	}
	src := externalSkillSource{URL: "https://github.com/example/catalog", SkillDir: "skills", Branch: "main", Materialize: true}
	cfg := config{
		Agents:         []agentConfig{{Name: agentClaudeCode, SkillRoot: skillRoot}},
		ExternalSkills: []externalSkillSource{src},
	}
	if err := writeLockFile(repoRoot, lockFile{ExternalSkills: []externalLockEntry{{
		Name: "catalog", URL: src.URL, Branch: src.Branch, Commit: "deadbeef",
		Materialized: newMaterializedSkillNames([]string{"alpha"}),
	}}}); err != nil {
		t.Fatal(err)
	}

	result := checkMaterializedExternalSkills(repoRoot, cfg, home)
	if result.status != checkStatusFail || !strings.Contains(result.detail, "claude-code/alpha is linked directly from "+externalCacheDir(home)) {
		t.Fatalf("doctor direct-cache result = %s (%s), want fail identifying direct cache link", result.status, result.detail)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(canonicalSkill, link); err != nil {
		t.Fatal(err)
	}
	result = checkMaterializedExternalSkills(repoRoot, cfg, home)
	if result.status != checkStatusPass {
		t.Fatalf("doctor rejected canonical materialized link = %s (%s)", result.status, result.detail)
	}
}

func TestSyncKeepsCommittedCopiesWhenUpstreamUnavailable(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	src := externalSkillSource{
		URL:         "file:///nonexistent/catalog",
		Branch:      "main",
		Materialize: true,
	}
	skillDir := filepath.Join(repoRoot, "skills", "alpha")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: alpha\n---\nalpha v1\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := lockFile{Version: 1, ExternalSkills: []externalLockEntry{{
		Name:         "catalog",
		URL:          src.URL,
		Branch:       src.Branch,
		Commit:       "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Materialized: newMaterializedSkillNames([]string{"alpha"}),
	}}}
	if err := writeLockFile(repoRoot, lock); err != nil {
		t.Fatal(err)
	}
	if err := syncExternalRepos([]externalSkillSource{src}, home, repoRoot); err != nil {
		t.Fatalf("sync with unavailable upstream = %v, want committed copies kept", err)
	}
	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatalf("committed copy changed: %q", data)
	}
	after, err := readLockFile(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.ExternalSkills) != 1 || after.ExternalSkills[0].Commit != lock.ExternalSkills[0].Commit {
		t.Fatalf("lock pin changed: %+v", after.ExternalSkills)
	}
}

func TestSyncFailsWhenUpstreamUnavailableAndCopiesMissing(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	src := externalSkillSource{
		URL:         "file:///nonexistent/catalog",
		Branch:      "main",
		Materialize: true,
	}
	lock := lockFile{Version: 1, ExternalSkills: []externalLockEntry{{
		Name:         "catalog",
		URL:          src.URL,
		Branch:       src.Branch,
		Commit:       "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Materialized: newMaterializedSkillNames([]string{"alpha"}),
	}}}
	if err := writeLockFile(repoRoot, lock); err != nil {
		t.Fatal(err)
	}
	err := syncExternalRepos([]externalSkillSource{src}, home, repoRoot)
	if err == nil || !strings.Contains(err.Error(), "clone external catalog") {
		t.Fatalf("sync error = %v, want clone failure", err)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	if stderr.Len() > 0 && args[0] != "init" {
		t.Logf("git %s stderr:\n%s", strings.Join(args, " "), stderr.String())
	}
	return strings.TrimSuffix(stdout.String(), "\n")
}
