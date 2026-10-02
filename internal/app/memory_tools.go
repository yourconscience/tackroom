package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// installMemoryTools builds every Go package under <repoRoot>/memory/tools/<name>
// and installs the binary into the user's bin directory. Source checkouts use
// the root module; setup scaffolds a module file beside each copied tool. This
// is how the default memory system (rem, knowledge-sync)
// follows a machine: tackroom sync provisions it after reconciling files.
//
// installed binary, so frequent syncs stay cheap. GOBIN overrides the
// destination directory; the default is $HOME/.local/bin. Requires the Go
// toolchain; without one this is a no-op so syncs never fail on plain hosts.
func installMemoryTools(repoRoot string) ([]string, error) {
	if _, err := exec.LookPath("go"); err != nil {
		fmt.Println("memory tools skipped: go toolchain not found in PATH")
		return nil, nil
	}
	toolsDir := filepath.Join(repoRoot, "memory", "tools")
	entries, err := os.ReadDir(toolsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", toolsDir, err)
	}

	destDir := os.Getenv("GOBIN")
	if destDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		destDir = filepath.Join(home, ".local", "bin")
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, err
	}

	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		matches, err := filepath.Glob(filepath.Join(toolsDir, entry.Name(), "*.go"))
		if err != nil {
			return nil, fmt.Errorf("find Go source for %s: %w", entry.Name(), err)
		}
		for _, match := range matches {
			if !strings.HasSuffix(match, "_test.go") {
				names = append(names, entry.Name())
				break
			}
		}
	}
	sort.Strings(names)

	var installed []string
	for _, name := range names {
		changed, err := buildMemoryTool(filepath.Join(toolsDir, name), filepath.Join(destDir, name))
		if err != nil {
			return installed, fmt.Errorf("memory tool %s: %w", name, err)
		}
		if changed {
			installed = append(installed, name)
		}
	}
	return installed, nil
}

func buildMemoryTool(srcDir, dest string) (bool, error) {
	var newest time.Time
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".go") && entry.Name() != "go.mod") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return false, err
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	if destInfo, err := os.Stat(dest); err == nil && !newest.After(destInfo.ModTime()) {
		return false, nil
	}

	tmpDest := dest + ".tmp"
	cmd := exec.Command("go", "build", "-o", tmpDest, ".")
	cmd.Dir = srcDir
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmpDest)
		return false, fmt.Errorf("go build: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	if err := os.Rename(tmpDest, dest); err != nil {
		os.Remove(tmpDest)
		return false, err
	}
	return true, nil
}
