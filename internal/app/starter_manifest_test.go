package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testStarterSet() (map[string][]byte, map[string][]string) {
	shipped := map[string][]byte{
		"memory/lib/keep.py":   []byte("shipped keep v2\n"),
		"memory/hooks/keep.sh": []byte("shipped hook v2\n"),
	}
	legacy := map[string][]string{
		"memory/lib/keep.py":    {fileHash([]byte("shipped keep v1\n"))},
		"memory/lib/retired.py": {fileHash([]byte("retired v1\n"))},
	}
	return shipped, legacy
}

func readStarter(t *testing.T, root, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func writeStarter(t *testing.T, root, path, content string) {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileStarterSetScaffoldsAndRecordsManifest(t *testing.T) {
	root := t.TempDir()
	shipped, legacy := testStarterSet()

	changes, err := reconcileStarterSet(root, shipped, legacy, setupIO{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes.Scaffolded) != 2 || len(changes.Updated) != 0 || len(changes.Removed) != 0 || len(changes.KeptModified) != 0 {
		t.Fatalf("changes = %#v, want 2 scaffolded and nothing else", changes)
	}
	if got := readStarter(t, root, "memory/lib/keep.py"); got != "shipped keep v2\n" {
		t.Fatalf("scaffolded content = %q", got)
	}

	manifest, exists, err := loadStarterManifest(root)
	if err != nil || !exists {
		t.Fatalf("manifest exists = %v, err = %v", exists, err)
	}
	if manifest.Files["memory/lib/keep.py"] != fileHash([]byte("shipped keep v2\n")) {
		t.Fatalf("manifest did not record the shipped hash: %#v", manifest.Files)
	}

	// A second run is a no-op and leaves the manifest bytes untouched.
	before, err := os.ReadFile(starterManifestPath(root))
	if err != nil {
		t.Fatal(err)
	}
	changes, err = reconcileStarterSet(root, shipped, legacy, setupIO{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !changes.empty() {
		t.Fatalf("second run changes = %#v, want none", changes)
	}
	after, err := os.ReadFile(starterManifestPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("manifest was rewritten on an unchanged run")
	}
}

func TestReconcileStarterSetRefreshesManifestBaselineAndKeepsUserEdits(t *testing.T) {
	root := t.TempDir()
	shipped, legacy := testStarterSet()

	// Recorded as a previous release wrote it: unmodified, so it refreshes.
	writeStarter(t, root, "memory/lib/keep.py", "shipped keep v1\n")
	// Hand-edited by the user: kept and reported.
	writeStarter(t, root, "memory/hooks/keep.sh", "my own hook\n")
	if err := saveStarterManifest(root, starterManifest{
		Version: starterManifestVersion,
		Files: map[string]string{
			"memory/lib/keep.py":   fileHash([]byte("shipped keep v1\n")),
			"memory/hooks/keep.sh": fileHash([]byte("shipped hook v1\n")),
		},
	}); err != nil {
		t.Fatal(err)
	}

	changes, err := reconcileStarterSet(root, shipped, legacy, setupIO{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes.Updated) != 1 || changes.Updated[0] != "memory/lib/keep.py" {
		t.Fatalf("updated = %#v, want only memory/lib/keep.py", changes.Updated)
	}
	if len(changes.KeptModified) != 1 || changes.KeptModified[0] != "memory/hooks/keep.sh" {
		t.Fatalf("kept = %#v, want only memory/hooks/keep.sh", changes.KeptModified)
	}
	if got := readStarter(t, root, "memory/lib/keep.py"); got != "shipped keep v2\n" {
		t.Fatalf("unmodified file was not refreshed: %q", got)
	}
	if got := readStarter(t, root, "memory/hooks/keep.sh"); got != "my own hook\n" {
		t.Fatalf("modified file was overwritten: %q", got)
	}
}

func TestReconcileStarterSetRefreshesContentOlderReleasesShipped(t *testing.T) {
	root := t.TempDir()
	shipped, legacy := testStarterSet()

	// No manifest yet: this root predates the manifest, and the content is
	// exactly what an earlier release wrote, so the upgrade may refresh it.
	writeStarter(t, root, "memory/lib/keep.py", "shipped keep v1\n")

	changes, err := reconcileStarterSet(root, shipped, legacy, setupIO{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes.Updated) != 1 || changes.Updated[0] != "memory/lib/keep.py" {
		t.Fatalf("updated = %#v, want the pre-manifest file", changes.Updated)
	}
	if got := readStarter(t, root, "memory/lib/keep.py"); got != "shipped keep v2\n" {
		t.Fatalf("content = %q, want the shipped version", got)
	}
}

func TestReconcileStarterSetNeverTouchesUnrecognizedManagedContent(t *testing.T) {
	root := t.TempDir()
	shipped, legacy := testStarterSet()

	// Neither a recorded baseline nor a known earlier release: tackroom must
	// not overwrite it, and must keep reporting it.
	writeStarter(t, root, "memory/lib/keep.py", "def my_own_helper():\n    pass\n")

	changes, err := reconcileStarterSet(root, shipped, legacy, setupIO{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes.Updated) != 0 {
		t.Fatalf("updated = %#v, want no refresh of unrecognized content", changes.Updated)
	}
	if len(changes.KeptModified) != 1 || changes.KeptModified[0] != "memory/lib/keep.py" {
		t.Fatalf("kept = %#v, want the unrecognized file reported", changes.KeptModified)
	}
	if got := readStarter(t, root, "memory/lib/keep.py"); got != "def my_own_helper():\n    pass\n" {
		t.Fatalf("unrecognized content was overwritten: %q", got)
	}

	// The manifest must not claim ownership of it.
	manifest, _, err := loadStarterManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest.Files["memory/lib/keep.py"]; ok {
		t.Fatal("manifest recorded a file tackroom never wrote")
	}
}

func TestReconcileStarterSetRemovesRetiredFilesOnlyWhenUnmodified(t *testing.T) {
	root := t.TempDir()
	shipped, legacy := testStarterSet()

	writeStarter(t, root, "memory/lib/retired.py", "retired v1\n")
	writeStarter(t, root, "memory/lib/mine.py", "hand written by the user\n")

	changes, err := reconcileStarterSet(root, shipped, legacy, setupIO{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes.Removed) != 1 || changes.Removed[0] != "memory/lib/retired.py" {
		t.Fatalf("removed = %#v, want only the unmodified retired file", changes.Removed)
	}
	if _, err := os.Lstat(filepath.Join(root, "memory", "lib", "retired.py")); !os.IsNotExist(err) {
		t.Fatalf("retired file still present: %v", err)
	}
	if got := readStarter(t, root, "memory/lib/mine.py"); got != "hand written by the user\n" {
		t.Fatalf("unrelated user file was touched: %q", got)
	}
}

func TestReconcileStarterSetRemovesManifestTrackedFilesNoLongerShipped(t *testing.T) {
	root := t.TempDir()
	shipped, legacy := testStarterSet()

	writeStarter(t, root, "memory/hooks/old.sh", "old hook\n")
	writeStarter(t, root, "memory/hooks/edited.sh", "edited old hook\n")
	if err := saveStarterManifest(root, starterManifest{
		Version: starterManifestVersion,
		Files: map[string]string{
			"memory/hooks/old.sh":    fileHash([]byte("old hook\n")),
			"memory/hooks/edited.sh": fileHash([]byte("old hook\n")),
		},
	}); err != nil {
		t.Fatal(err)
	}

	changes, err := reconcileStarterSet(root, shipped, legacy, setupIO{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes.Removed) != 1 || changes.Removed[0] != "memory/hooks/old.sh" {
		t.Fatalf("removed = %#v, want only the untouched tracked file", changes.Removed)
	}
	if len(changes.KeptModified) != 1 || changes.KeptModified[0] != "memory/hooks/edited.sh" {
		t.Fatalf("kept = %#v, want the edited tracked file", changes.KeptModified)
	}
	if got := readStarter(t, root, "memory/hooks/edited.sh"); got != "edited old hook\n" {
		t.Fatalf("edited tracked file was removed or rewritten: %q", got)
	}
}

func TestReconcileStarterSetConfirmationKeepsRetiredFiles(t *testing.T) {
	root := t.TempDir()
	shipped, legacy := testStarterSet()
	writeStarter(t, root, "memory/lib/retired.py", "retired v1\n")

	streams := setupIO{in: strings.NewReader("n\n"), out: &bytes.Buffer{}}
	changes, err := reconcileStarterSet(root, shipped, legacy, streams, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes.Removed) != 0 || len(changes.KeptModified) != 1 {
		t.Fatalf("changes = %#v, want the declined removal kept", changes)
	}
	if got := readStarter(t, root, "memory/lib/retired.py"); got != "retired v1\n" {
		t.Fatalf("declined removal still deleted the file: %q", got)
	}
}

func TestShippedStarterFilesAreManagedPathsOnly(t *testing.T) {
	shipped, err := shippedStarterFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(shipped) == 0 {
		t.Fatal("no managed starter files found in the embedded assets")
	}
	for path := range shipped {
		if !isManagedStarterPath(path) {
			t.Fatalf("unmanaged path in the managed set: %s", path)
		}
	}
	// User content must never be treated as managed code.
	for _, path := range []string{"AGENTS.md", "tackroom.yaml", "agents/architect.md", "skills/tackroom/SKILL.md"} {
		if isManagedStarterPath(path) {
			t.Fatalf("%s must not be managed", path)
		}
	}
	// Files this release stopped shipping must be gone from the shipped set.
	for _, path := range []string{"memory/lib/amp_digest.py", "memory/lib/factory_digest.py", "memory/lib/hermes_digest.py", "memory/hooks/omp-memory.ts"} {
		if _, ok := shipped[path]; ok {
			t.Fatalf("%s is still shipped", path)
		}
	}
	// Every legacy hash must describe a managed path, so the bridge cannot be
	// pointed at user content by mistake.
	for path := range legacyStarterHashes {
		if !isManagedStarterPath(path) {
			t.Fatalf("legacy hash for unmanaged path: %s", path)
		}
	}
}

func TestRunSyncReconcilesManagedStarterFiles(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TACKROOM_HOME", repoRoot)

	writeSyncTestFile(t, filepath.Join(repoRoot, "tackroom.yaml"), []byte(`version: 1
agents:
  - name: hermes
    enabled: true
    skill_root: ~/.hermes/skills
`))
	writeSyncTestFile(t, filepath.Join(home, ".hermes", "config.yaml"), []byte("{}\n"))
	// Hand-edited managed file: sync must leave it alone.
	writeSyncTestFile(t, filepath.Join(repoRoot, "memory", "hooks", "common.sh"), []byte("#!/bin/sh\n# my own tweak\n"))

	if err := runSync(runOptions{Agents: agentHermes}); err != nil {
		t.Fatal(err)
	}

	if got := readStarter(t, repoRoot, "memory/hooks/common.sh"); got != "#!/bin/sh\n# my own tweak\n" {
		t.Fatalf("user-edited managed file was overwritten by sync: %q", got)
	}
	manifest, exists, err := loadStarterManifest(repoRoot)
	if err != nil || !exists {
		t.Fatalf("sync did not write a manifest: exists=%v err=%v", exists, err)
	}
	shipped, err := shippedStarterFiles()
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Files["memory/lib/basic_memory.py"] != fileHash(shipped["memory/lib/basic_memory.py"]) {
		t.Fatalf("manifest did not record the shipped hash for a scaffolded file: %#v", manifest.Files)
	}
	if _, ok := manifest.Files["memory/hooks/common.sh"]; ok {
		t.Fatal("manifest claimed a file tackroom never wrote")
	}
}

func TestReconcileStarterSetIgnoresUnsafeManifestEntries(t *testing.T) {
	root := t.TempDir()
	shipped, legacy := testStarterSet()

	// A hand-edited manifest must not be able to reach outside the config root.
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if err := os.WriteFile(outside, []byte("victim\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := saveStarterManifest(root, starterManifest{
		Version: starterManifestVersion,
		Files: map[string]string{
			"../outside.txt":               fileHash([]byte("victim\n")),
			"memory/lib/../../outside.txt": fileHash([]byte("victim\n")),
			"/etc/hosts":                   fileHash([]byte("victim\n")),
			"AGENTS.md":                    fileHash([]byte("victim\n")),
		},
	}); err != nil {
		t.Fatal(err)
	}

	changes, err := reconcileStarterSet(root, shipped, legacy, setupIO{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes.Removed) != 0 {
		t.Fatalf("removed = %#v, want nothing removed from an unsafe manifest", changes.Removed)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("file outside the config root was removed: %v", err)
	}

	manifest, _, err := loadStarterManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	for path := range manifest.Files {
		if !isSafeStarterManifestPath(path) {
			t.Fatalf("unsafe entry survived: %s", path)
		}
	}
	for _, unsafe := range []string{"../outside.txt", "memory/lib/../../outside.txt", "/etc/hosts", "AGENTS.md"} {
		if _, ok := manifest.Files[unsafe]; ok {
			t.Fatalf("unsafe entry %q was kept", unsafe)
		}
	}
}
