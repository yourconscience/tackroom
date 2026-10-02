package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type fakeRegistry struct {
	creates         []string
	addVersionCalls []string
	setDefault      []string
	versions        map[string]int
}

func (f *fakeRegistry) createSkill(name string, _ []byte) (string, string, error) {
	if f.versions == nil {
		f.versions = map[string]int{}
	}
	f.creates = append(f.creates, name)
	id := "skill_" + name
	f.versions[id] = 1
	return id, "1", nil
}

func (f *fakeRegistry) addVersion(skillID string, _ []byte) (string, error) {
	f.versions[skillID]++
	f.addVersionCalls = append(f.addVersionCalls, skillID)
	return strconv.Itoa(f.versions[skillID]), nil
}

func (f *fakeRegistry) setDefaultVersion(skillID string, version string) error {
	f.setDefault = append(f.setDefault, skillID+"@"+version)
	return nil
}

func writePublishSkill(t *testing.T, repoRoot, name string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(repoRoot, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := files["SKILL.md"]; !ok {
		files["SKILL.md"] = "---\nname: " + name + "\ndescription: demo skill\n---\nbody\n"
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func writePublishConfig(t *testing.T, repoRoot, body string) string {
	t.Helper()
	p := filepath.Join(repoRoot, "tackroom.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBuildSkillBundle(t *testing.T) {
	repo := t.TempDir()
	writePublishSkill(t, repo, "demo-skill", map[string]string{
		"references/note.md": "hello\n",
	})
	dir := filepath.Join(repo, "skills", "demo-skill")

	zipData, hash, files, uncompressed, err := buildSkillBundle(dir, "demo-skill")
	if err != nil {
		t.Fatalf("buildSkillBundle: %v", err)
	}
	if files != 2 {
		t.Fatalf("files = %d, want 2", files)
	}
	if uncompressed == 0 || len(zipData) == 0 {
		t.Fatalf("empty bundle: uncompressed=%d zip=%d", uncompressed, len(zipData))
	}
	if !strings.HasPrefix(hash, "sha256:") {
		t.Fatalf("hash %q missing sha256 prefix", hash)
	}

	// Determinism: same content -> same hash.
	_, hash2, _, _, err := buildSkillBundle(dir, "demo-skill")
	if err != nil {
		t.Fatal(err)
	}
	if hash != hash2 {
		t.Fatalf("hash not deterministic: %q != %q", hash, hash2)
	}

	// Changing content changes the hash.
	if err := os.WriteFile(filepath.Join(dir, "references", "note.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, hash3, _, _, err := buildSkillBundle(dir, "demo-skill")
	if err != nil {
		t.Fatal(err)
	}
	if hash3 == hash {
		t.Fatalf("hash unchanged after content edit")
	}
}

func TestBuildSkillBundleFileLimit(t *testing.T) {
	repo := t.TempDir()
	files := map[string]string{}
	for i := 0; i < publishMaxFiles+1; i++ {
		files["f"+strconv.Itoa(i)+".txt"] = "x"
	}
	writePublishSkill(t, repo, "big-skill", files)
	_, _, _, _, err := buildSkillBundle(filepath.Join(repo, "skills", "big-skill"), "big-skill")
	if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("expected file-limit error, got %v", err)
	}
}

func TestSelectPublishTargets(t *testing.T) {
	cfg := config{PublishTargets: []publishTarget{
		{Name: "on", Enabled: true},
		{Name: "off", Enabled: false},
	}}
	got, err := selectPublishTargets(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "on" {
		t.Fatalf("default select = %+v, want [on]", got)
	}
	if _, err := selectPublishTargets(cfg, "off"); err == nil {
		t.Fatal("expected error selecting disabled target")
	}
	if _, err := selectPublishTargets(cfg, "nope"); err == nil {
		t.Fatal("expected error selecting unknown target")
	}
}

func TestHTTPRegistryRejectsResponseMissingIdentifiers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()
	reg := &httpSkillRegistry{apiKey: "k", client: srv.Client(), baseURL: srv.URL}
	if _, _, err := reg.createSkill("demo-skill", []byte("zip")); err == nil {
		t.Fatal("expected error when response omits skill_id/version")
	}
}

func TestHTTPRegistryParsesCreateResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"skill_x","version":2}`))
	}))
	defer srv.Close()
	reg := &httpSkillRegistry{apiKey: "k", client: srv.Client(), baseURL: srv.URL}
	id, version, err := reg.createSkill("demo-skill", []byte("zip"))
	if err != nil || id != "skill_x" || version != "2" {
		t.Fatalf("createSkill = (%q, %q, %v), want (skill_x, 2, nil)", id, version, err)
	}
}

func TestExtractSkillIDAndVersion(t *testing.T) {
	if id := extractSkillID(map[string]any{"skill_id": "s1"}); id != "s1" {
		t.Fatalf("skill_id fallback failed: %q", id)
	}
	if id := extractSkillID(map[string]any{"id": "s2"}); id != "s2" {
		t.Fatalf("id preferred failed: %q", id)
	}
	if v := extractVersion(map[string]any{"version": float64(3)}); v != "3" {
		t.Fatalf("numeric version = %q, want 3", v)
	}
	if v := extractVersion(map[string]any{"default_version": "latest"}); v != "latest" {
		t.Fatalf("string version = %q, want latest", v)
	}
}

func runPublishTest(t *testing.T, configPath string, reg *fakeRegistry, opts publishOptions) string {
	t.Helper()
	var out bytes.Buffer
	opts.ConfigPath = configPath
	opts.Stdout = &out
	opts.Stdin = strings.NewReader("")
	opts.AssumeYes = true
	opts.Factory = func(publishTarget) (skillRegistry, error) { return reg, nil }
	if err := runPublish(opts); err != nil {
		t.Fatalf("runPublish: %v", err)
	}
	return out.String()
}

func TestRunPublishLifecycle(t *testing.T) {
	repo := t.TempDir()
	configPath := writePublishConfig(t, repo, "version: 1\npublish_targets:\n  - name: t1\n    kind: openai-skills\n    enabled: true\n    skills:\n      - demo-skill\n")
	writePublishSkill(t, repo, "demo-skill", map[string]string{"references/a.md": "v1\n"})
	reg := &fakeRegistry{}

	// Dry run: no upload, no lock.
	out := runPublishTest(t, configPath, reg, publishOptions{DryRun: true})
	if len(reg.creates) != 0 {
		t.Fatalf("dry-run uploaded: %v", reg.creates)
	}
	if _, err := os.Stat(filepath.Join(repo, "tackroom.lock")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote a lock file")
	}
	if !strings.Contains(out, "dry-run") {
		t.Fatalf("dry-run output missing marker: %s", out)
	}

	// Real run 1: create.
	runPublishTest(t, configPath, reg, publishOptions{})
	if len(reg.creates) != 1 || reg.creates[0] != "demo-skill" {
		t.Fatalf("run1 creates = %v", reg.creates)
	}
	lock, err := readLockFile(repo)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := findPublishedLockEntry(lock, "t1", "demo-skill")
	if !ok || entry.SkillID != "skill_demo-skill" || entry.Version != "1" {
		t.Fatalf("run1 lock entry = %+v ok=%v", entry, ok)
	}

	// Run 2: unchanged content -> skip, no new uploads.
	runPublishTest(t, configPath, reg, publishOptions{})
	if len(reg.creates) != 1 || len(reg.addVersionCalls) != 0 {
		t.Fatalf("run2 should skip: creates=%v addVersion=%v", reg.creates, reg.addVersionCalls)
	}

	// Run 3: change content -> new version.
	writePublishSkill(t, repo, "demo-skill", map[string]string{"references/a.md": "v2-changed\n"})
	runPublishTest(t, configPath, reg, publishOptions{})
	if len(reg.addVersionCalls) != 1 || reg.addVersionCalls[0] != "skill_demo-skill" {
		t.Fatalf("run3 addVersion = %v", reg.addVersionCalls)
	}
	lock, _ = readLockFile(repo)
	entry, _ = findPublishedLockEntry(lock, "t1", "demo-skill")
	if entry.Version != "2" {
		t.Fatalf("run3 version = %q, want 2", entry.Version)
	}
}
