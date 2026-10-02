package app

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"
)

func TestSyncUpdatesCommittedArtifacts(t *testing.T) {
	repoRoot := t.TempDir()
	home := t.TempDir()
	t.Setenv("TACKROOM_HOME", repoRoot)
	t.Setenv("HOME", home)

	writeSyncTestFile(t, filepath.Join(repoRoot, "tackroom.yaml"), []byte(`version: 1
agents:
  - name: omp
    enabled: true
    skill_root: ~/.omp/agent/skills
    agent_root: ~/.omp/agent/agents
`))
	writeSyncTestFile(t, filepath.Join(repoRoot, "agents", "reviewer.md"), []byte(`---
name: reviewer
description: Reviews changes
---

Review the change.
`))
	writeSyncTestFile(t, filepath.Join(repoRoot, "skills", "sample", "SKILL.md"), []byte("---\nname: sample\n---\n"))
	writeSyncTestFile(t, filepath.Join(repoRoot, "README.md"), []byte("# Skills\n\n"+readmeSkillsBeginMarker+"\n0 skills ship with this repo:\n\n``\n"+readmeSkillsEndMarker+"\n"))

	if err := Run([]string{"sync"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repoRoot, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "1 skills ship with this repo:\n\n`sample`"; !bytes.Contains(data, []byte(want)) {
		t.Fatalf("sync did not render committed outputs:\n%s", data)
	}
}

func TestTackroomBinaryDoesNotCarryCodexMirrorCopyDependency(t *testing.T) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("Go build information unavailable")
	}
	for _, dep := range info.Deps {
		if dep.Path == "github.com/otiai10/copy" {
			t.Fatalf("obsolete Codex mirror dependency remains in binary: %s", dep.Path)
		}
	}
}
