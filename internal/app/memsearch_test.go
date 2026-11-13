package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMemsearchFlagsDefaultsToDocumentedKnowledgeDir(t *testing.T) {
	oldKnowledgeDir, hadKnowledgeDir := os.LookupEnv("KNOWLEDGE_DIR")
	if err := os.Unsetenv("KNOWLEDGE_DIR"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if hadKnowledgeDir {
			_ = os.Setenv("KNOWLEDGE_DIR", oldKnowledgeDir)
		} else {
			_ = os.Unsetenv("KNOWLEDGE_DIR")
		}
	})

	opts, err := parseMemsearchFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.VaultDir != "~/Workspace/knowledge" {
		t.Fatalf("VaultDir = %q, want documented default", opts.VaultDir)
	}

	if err := os.Setenv("KNOWLEDGE_DIR", "/tmp/custom-knowledge"); err != nil {
		t.Fatal(err)
	}
	opts, err = parseMemsearchFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.VaultDir != "/tmp/custom-knowledge" {
		t.Fatalf("VaultDir = %q, want env override", opts.VaultDir)
	}
}

func TestMemsearchConfigPOSIXShellQuotesArbitraryPaths(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home with $dollar `tick` \"double\" and 'single'")
	vault := filepath.Join(home, "vault with shell chars")
	confPath := filepath.Join(t.TempDir(), "memsearch.conf")
	if err := os.WriteFile(confPath, []byte(memsearchConfigContent(vault, home)), 0o644); err != nil {
		t.Fatal(err)
	}

	script := `. "$CONF"; printf '%s\n' "$KNOWLEDGE_DIR" "$SESSIONS_DIR" "$NOTES_DIR" "$PROFILE_DIR" "$MEMSEARCH_STATE_DIR" "$MEMSEARCH_COLLECTION"`
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Env = []string{"CONF=" + confPath, "PATH=" + os.Getenv("PATH")}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("source generated config: %v: %s", err, output)
	}
	got := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	want := []string{
		vault,
		filepath.Join(vault, "sessions"),
		filepath.Join(vault, "notes"),
		filepath.Join(vault, "profile"),
		filepath.Join(home, ".memsearch", "state"),
		"ai",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("sourced config values = %#v, want %#v\nconfig:\n%s", got, want, memsearchConfigContent(vault, home))
	}
	if strings.Contains(memsearchConfigContent(vault, home), "$dollar") && !strings.Contains(memsearchConfigContent(vault, home), `KNOWLEDGE_DIR='`) {
		t.Fatal("generated path is not single-quoted")
	}
}

func TestMemsearchConfigFieldsDriveCommonShToSelectedVault(t *testing.T) {
	home := t.TempDir()
	vault := filepath.Join(home, "selected-vault")
	confPath := filepath.Join(home, ".agents", "memsearch.conf")
	if err := os.MkdirAll(filepath.Dir(confPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(confPath, []byte(memsearchConfigContent(vault, home)), 0o644); err != nil {
		t.Fatal(err)
	}

	commonPath := filepath.Join("..", "..", "memory", "hooks", "common.sh")
	script := `. "$COMMON_SH"; load_memory_config; printf '%s\n' "$KNOWLEDGE_DIR" "$SESSIONS_DIR" "$NOTES_DIR" "$PROFILE_DIR" "$MEMSEARCH_STATE_DIR" "$MEMSEARCH_COLLECTION"`
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = []string{
		"HOME=" + home,
		"KNOWLEDGE_CONF=" + confPath,
		"COMMON_SH=" + commonPath,
		"PATH=" + os.Getenv("PATH"),
	}
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("source common.sh: %v", err)
	}

	got := strings.Split(strings.TrimSpace(string(output)), "\n")
	want := []string{
		vault,
		filepath.Join(vault, "sessions"),
		filepath.Join(vault, "notes"),
		filepath.Join(vault, "profile"),
		filepath.Join(home, ".memsearch", "state"),
		"ai",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("common.sh values = %#v, want %#v", got, want)
	}
}

func TestCustomRootMemoryHookLoadsRootRelativeMemsearchConfig(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "private-config")
	hooksDir := filepath.Join(root, "memory", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	commonSource := filepath.Join("..", "..", "memory", "hooks", "common.sh")
	commonData, err := os.ReadFile(commonSource)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "common.sh"), commonData, 0o755); err != nil {
		t.Fatal(err)
	}
	vault := filepath.Join(home, "custom-vault")
	if err := os.WriteFile(filepath.Join(root, "memsearch.conf"), []byte(memsearchConfigContent(vault, home)), 0o644); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(hooksDir, "probe.sh")
	if err := os.WriteFile(wrapper, []byte("#!/usr/bin/env bash\n. \"$(dirname \"$0\")/common.sh\"\nload_memory_config\nprintf '%s\\n' \"$KNOWLEDGE_DIR\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(wrapper)
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("run custom-root memory hook: %v", err)
	}
	if strings.TrimSpace(string(output)) != vault {
		t.Fatalf("KNOWLEDGE_DIR = %q, want %q", strings.TrimSpace(string(output)), vault)
	}
}

func TestEnsureMemsearchConfigWritesCustomRoot(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "private-config")
	vault := filepath.Join(home, "vault")
	t.Setenv("KNOWLEDGE_DIR", vault)
	if err := ensureMemsearchConfig(root, home); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "memsearch.conf")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "KNOWLEDGE_DIR="+shellQuote(vault)) {
		t.Fatalf("custom-root memsearch config = %s", data)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "memsearch.conf")); !os.IsNotExist(err) {
		t.Fatalf("default-root memsearch config unexpectedly exists: %v", err)
	}
}
