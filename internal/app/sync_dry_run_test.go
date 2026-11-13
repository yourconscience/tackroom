package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncDryRunWritesNothingAndStatusExitCode(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TACKROOM_HOME", repoRoot)

	writeSyncTestFile(t, filepath.Join(repoRoot, "tackroom.yaml"), []byte(`version: 1
agents:
  - name: pi
    enabled: true
    skill_root: ~/.pi/agent/skills
    agent_root: ~/.pi/agent/agents
`))
	writeSyncTestFile(t, filepath.Join(repoRoot, "AGENTS.md"), []byte("# Shared instructions\n"))
	writeSyncTestFile(t, filepath.Join(repoRoot, "skills", "sample", "SKILL.md"), []byte("---\nname: sample\ndescription: sample\n---\n"))

	// Drift before any sync: status --exit-code must report pending (1).
	if err := runStatus(runOptions{Agents: agentPi, ExitCode: true, Stdout: &bytes.Buffer{}}); err == nil {
		t.Fatal("status --exit-code before sync returned nil, want drift")
	} else if code := ExitCode(err); code != 1 {
		t.Fatalf("status --exit-code before sync = %d, want 1 (%v)", code, err)
	}

	before := hashTree(t, home) + hashTree(t, repoRoot)

	var out bytes.Buffer
	if err := runSync(runOptions{Agents: agentPi, DryRun: true, Stdout: &out}); err != nil {
		t.Fatalf("sync --dry-run: %v", err)
	}
	if !strings.Contains(out.String(), "dry run:") {
		t.Fatalf("dry-run output missing summary line:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "change") {
		t.Fatalf("dry-run output reports no change:\n%s", out.String())
	}

	after := hashTree(t, home) + hashTree(t, repoRoot)
	if before != after {
		t.Fatal("sync --dry-run wrote files under the temp home or config root")
	}

	// A real sync applies the plan.
	if err := runSync(runOptions{Agents: agentPi, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if target, err := os.Readlink(filepath.Join(home, ".pi", "agent", "skills", "sample")); err != nil || target != filepath.Join(repoRoot, "skills", "sample") {
		t.Fatalf("sync did not link sample skill: target=%q err=%v", target, err)
	}

	// In sync now: status --exit-code returns 0.
	if err := runStatus(runOptions{Agents: agentPi, ExitCode: true, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("status --exit-code after sync = %v, want nil", err)
	}
}

func TestSyncDryRunRejectsPullAndListsNotFetched(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TACKROOM_HOME", repoRoot)

	writeSyncTestFile(t, filepath.Join(repoRoot, "tackroom.yaml"), []byte(`version: 1
agents:
  - name: pi
    enabled: true
    skill_root: ~/.pi/agent/skills
external_skills:
  - url: https://example.com/skills.git
    branch: main
    skill_dir: skills
`))

	// --dry-run and --pull cannot combine.
	if err := runSyncCommand([]string{"--dry-run", "--pull"}); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("sync --dry-run --pull = %v, want combination error", err)
	}

	var out bytes.Buffer
	if err := runSync(runOptions{Agents: agentPi, DryRun: true, Stdout: &out}); err != nil {
		t.Fatalf("sync --dry-run with uncached external source: %v", err)
	}
	if !strings.Contains(out.String(), "not fetched: skills") {
		t.Fatalf("dry-run output missing not fetched line:\n%s", out.String())
	}
}

// hashTree returns a deterministic digest of every file under root, keyed by
// its path relative to root, so any added, removed, or changed file moves it.
func hashTree(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		h.Write([]byte(rel))
		h.Write([]byte{0})
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		h.Write(data)
		h.Write([]byte{0})
		return nil
	})
	if err != nil {
		t.Fatalf("hash %s: %v", root, err)
	}
	return hex.EncodeToString(h.Sum(nil))
}
