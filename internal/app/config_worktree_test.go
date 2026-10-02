package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefuseWorktreeRoot(t *testing.T) {
	repo := t.TempDir()
	if err := refuseWorktreeRoot(repo); err != nil {
		t.Fatalf("canonical root must be accepted: %v", err)
	}

	worktree := filepath.Join(repo, ".worktrees", "sync-main")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := refuseWorktreeRoot(worktree); err == nil {
		t.Fatal("worktree root must be refused")
	} else if !strings.Contains(err.Error(), "canonical checkout") {
		t.Fatalf("unexpected error: %v", err)
	}

	// A linked worktree outside .worktrees/ is caught by its .git file.
	arbitrary := filepath.Join(t.TempDir(), "agents-test")
	if err := os.MkdirAll(arbitrary, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(arbitrary, ".git"), []byte("gitdir: elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := refuseWorktreeRoot(arbitrary); err == nil {
		t.Fatal("linked worktree via .git file must be refused")
	}

	// A canonical checkout has a .git directory, which is allowed.
	canonical := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(canonical, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := refuseWorktreeRoot(canonical); err != nil {
		t.Fatalf("canonical checkout with .git dir must be accepted: %v", err)
	}

	// A symlink aliasing a worktree root resolves to the same refusal.
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(worktree, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := refuseWorktreeRoot(alias); err == nil {
		t.Fatal("symlinked worktree root must be refused")
	}
}

func TestRefuseLegacyRoot(t *testing.T) {
	root := t.TempDir()
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte("version: 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("tackroom.yaml")
	if err := checkConfigRoot(root); err != nil {
		t.Fatalf("migrated root must be accepted: %v", err)
	}

	// A machine-local overlay left behind would silently drop local overrides.
	write("dotagents.local.yaml")
	err := checkConfigRoot(root)
	if err == nil || !strings.Contains(err.Error(), "mv dotagents.local.yaml tackroom.local.yaml") {
		t.Fatalf("legacy overlay must be refused with a rename hint, got %v", err)
	}

	write("tackroom.local.yaml")
	if err := checkConfigRoot(root); err != nil {
		t.Fatalf("legacy file next to its renamed counterpart must be accepted: %v", err)
	}

	legacyOnly := t.TempDir()
	if err := os.WriteFile(filepath.Join(legacyOnly, "dotagents.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkConfigRoot(legacyOnly); err == nil || !strings.Contains(err.Error(), "mv dotagents.yaml tackroom.yaml") {
		t.Fatalf("legacy root must be refused before setup scaffolds a parallel config, got %v", err)
	}
}
