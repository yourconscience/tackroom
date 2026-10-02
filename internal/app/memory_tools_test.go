package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstallMemoryToolsBuildsAndSkipsFresh(t *testing.T) {
	root := t.TempDir()
	toolDir := filepath.Join(root, "memory", "tools", "hello")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(toolDir, "go.mod"), "module hello\n\ngo 1.24.2\n")
	writeFile(t, filepath.Join(toolDir, "main.go"), "package main\n\nfunc main() { helper() }\n")
	writeFile(t, filepath.Join(toolDir, "helper.go"), "package main\n\nfunc helper() {}\n")
	writeFile(t, filepath.Join(toolDir, "main_test.go"), "package main\n")

	binDir := filepath.Join(t.TempDir(), "bin")
	t.Setenv("GOBIN", binDir)

	installed, err := installMemoryTools(root)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(installed) != 1 || installed[0] != "hello" {
		t.Fatalf("installed = %v, want [hello]", installed)
	}
	if _, err := os.Stat(filepath.Join(binDir, "hello")); err != nil {
		t.Fatalf("binary missing: %v", err)
	}

	// Second run: binary is fresh, nothing rebuilt.
	installed, err = installMemoryTools(root)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if len(installed) != 0 {
		t.Fatalf("expected no rebuilds, got %v", installed)
	}

	// Touch a source file; tool must rebuild.
	future := filepath.Join(toolDir, "main.go")
	if err := os.Chtimes(future, time.Now().Add(time.Minute), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	installed, err = installMemoryTools(root)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if len(installed) != 1 {
		t.Fatalf("expected rebuild after source change, got %v", installed)
	}
}

func TestFreshScaffoldBuildsBothMemoryTools(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "tackroom.yaml")
	if err := ensureStarterAssets(root, configPath); err != nil {
		t.Fatalf("scaffold starter assets: %v", err)
	}
	for _, tool := range []string{"knowledge-sync", "rem"} {
		toolDir := filepath.Join(root, "memory", "tools", tool)
		if _, err := os.Stat(filepath.Join(toolDir, "go.mod")); err != nil {
			t.Fatalf("scaffolded %s go.mod missing: %v", tool, err)
		}
		if _, err := os.Stat(filepath.Join(toolDir, "go.mod.template")); !os.IsNotExist(err) {
			t.Fatalf("scaffolded %s retained template name: %v", tool, err)
		}
	}

	binDir := filepath.Join(t.TempDir(), "bin")
	t.Setenv("GOBIN", binDir)
	installed, err := installMemoryTools(root)
	if err != nil {
		t.Fatalf("install freshly scaffolded memory tools: %v", err)
	}
	if got := strings.Join(installed, ","); got != "knowledge-sync,rem" {
		t.Fatalf("installed = %q, want knowledge-sync,rem", got)
	}
	for _, tool := range installed {
		if info, err := os.Stat(filepath.Join(binDir, tool)); err != nil || info.Mode()&0o111 == 0 {
			t.Fatalf("installed binary %s is not executable: info=%v err=%v", tool, info, err)
		}
	}
}

func TestInstallMemoryToolsMissingDirIsNoop(t *testing.T) {
	t.Setenv("GOBIN", filepath.Join(t.TempDir(), "bin"))
	installed, err := installMemoryTools(t.TempDir())
	if err != nil {
		t.Fatalf("noop install: %v", err)
	}
	if installed != nil {
		t.Fatalf("expected nil installs, got %v", installed)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
