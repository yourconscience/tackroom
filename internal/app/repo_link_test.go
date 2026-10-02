package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInspectRepoLinkInPlaceClone covers the documented `git clone ... ~/.agents`
// install path: the repo is a real directory at ~/.agents that already is
// repoRoot. No symlink is needed, so the state must be synced (not conflict).
func TestInspectRepoLinkInPlaceClone(t *testing.T) {
	home := t.TempDir()
	linkPath := filepath.Join(home, ".agents")
	if err := os.Mkdir(linkPath, 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := inspectRepoLink(linkPath, home)
	if err != nil {
		t.Fatal(err)
	}
	if report.State != stateSynced {
		t.Fatalf("in-place clone: got state %q, want %q", report.State, stateSynced)
	}
	if report.ExpectedTarget != linkPath {
		t.Fatalf("in-place clone: got ExpectedTarget %q, want %q", report.ExpectedTarget, linkPath)
	}
}

// TestInspectRepoLinkSymlink covers the symlink layout: ~/.agents -> repoRoot
// living elsewhere (e.g. ~/Workspace/tackroom).
func TestInspectRepoLinkSymlink(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	linkPath := filepath.Join(home, ".agents")
	if err := os.Symlink(repoRoot, linkPath); err != nil {
		t.Fatal(err)
	}

	report, err := inspectRepoLink(repoRoot, home)
	if err != nil {
		t.Fatal(err)
	}
	if report.State != stateSynced {
		t.Fatalf("symlink: got state %q, want %q", report.State, stateSynced)
	}
}

// TestInspectRepoLinkCustomRootDoesNotConflict covers TACKROOM_HOME/--config:
// a canonical root outside ~/.agents is already the source, so status/sync must
// not force ~/.agents to be a symlink.
func TestInspectRepoLinkCustomRootDoesNotConflict(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	linkPath := filepath.Join(home, ".agents")
	if err := os.Mkdir(linkPath, 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := inspectRepoLink(repoRoot, home)
	if err != nil {
		t.Fatal(err)
	}
	if report.State != stateSynced {
		t.Fatalf("custom root: got state %q, want %q", report.State, stateSynced)
	}
}

// TestInspectRepoLinkCustomRootMissingAgentsDoesNotLink covers a fresh machine
// using an explicit canonical root outside ~/.agents.
func TestInspectRepoLinkCustomRootMissingAgentsDoesNotLink(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()

	report, err := inspectRepoLink(repoRoot, home)
	if err != nil {
		t.Fatal(err)
	}
	if report.State != stateSynced {
		t.Fatalf("custom root missing ~/.agents: got state %q, want %q", report.State, stateSynced)
	}
}
