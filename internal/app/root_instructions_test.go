package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyAgentRootInstructionSyncCreatesMissingLink(t *testing.T) {
	home := t.TempDir()
	report := agentReport{
		Name:         agentDroid,
		Detected:     true,
		RootPath:     filepath.Join(home, ".factory", "AGENTS.md"),
		RootExpected: filepath.Join(home, ".agents", "AGENTS.md"),
		RootState:    stateMissing,
	}
	if err := os.MkdirAll(filepath.Dir(report.RootExpected), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(report.RootExpected, []byte("# Shared\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := applyAgentRootInstructionSync([]agentReport{report}); err != nil {
		t.Fatal(err)
	}

	rawTarget, err := os.Readlink(report.RootPath)
	if err != nil {
		t.Fatal(err)
	}
	if rawTarget != report.RootExpected {
		t.Fatalf("link target = %q, want %q", rawTarget, report.RootExpected)
	}
}

func claudeCodeRootInstructions() *rootInstructionsCapability {
	h := harnessFor(agentClaudeCode)
	if h == nil || h.RootInstructions == nil {
		panic("claude harness missing RootInstructions")
	}
	return h.RootInstructions
}

func codexRootInstructions() *rootInstructionsCapability {
	h := harnessFor(agentCodex)
	if h == nil || h.RootInstructions == nil {
		panic("codex harness missing RootInstructions")
	}
	return h.RootInstructions
}

func ampRootInstructions() *rootInstructionsCapability {
	h := harnessFor("amp")
	if h == nil || h.RootInstructions == nil {
		panic("amp harness missing RootInstructions")
	}
	return h.RootInstructions
}

func TestRootInstructionsNativePaths(t *testing.T) {
	home := t.TempDir()
	repoRoot := filepath.Join(home, ".agents")
	for _, tc := range []struct {
		agent    string
		cap      *rootInstructionsCapability
		wantLink string
	}{
		{"amp", ampRootInstructions(), filepath.Join(home, ".config", "amp", "AGENTS.md")},
		{agentClaudeCode, claudeCodeRootInstructions(), filepath.Join(home, ".claude", "CLAUDE.md")},
		{agentCodex, codexRootInstructions(), filepath.Join(home, ".codex", "AGENTS.md")},
	} {
		if got := tc.cap.Path(home); got != tc.wantLink {
			t.Fatalf("%s Path = %q, want %q", tc.agent, got, tc.wantLink)
		}
		if got := tc.cap.Expected(repoRoot); got != filepath.Join(repoRoot, "AGENTS.md") {
			t.Fatalf("%s Expected = %q, want %q", tc.agent, got, filepath.Join(repoRoot, "AGENTS.md"))
		}
	}
}

func TestInspectAmpAgentWiresRootInstructions(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, "AGENTS.md"), []byte("# Shared\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agent := agentConfig{
		Name:      "amp",
		Enabled:   true,
		SkillRoot: filepath.Join(home, ".agents", "skills"),
	}
	agentsSkillRoot := filepath.Join(repoRoot, "skills")

	report, err := inspectAmpAgent(agent, map[string]string{}, agentsSkillRoot, config{}, home)
	if err != nil {
		t.Fatal(err)
	}

	wantLink := filepath.Join(home, ".config", "amp", "AGENTS.md")
	wantTarget := filepath.Join(repoRoot, "AGENTS.md")
	if report.RootPath != wantLink {
		t.Fatalf("RootPath = %q, want %q", report.RootPath, wantLink)
	}
	if report.RootExpected != wantTarget {
		t.Fatalf("RootExpected = %q, want %q", report.RootExpected, wantTarget)
	}
	if report.RootState != stateMissing {
		t.Fatalf("RootState = %q, want %q", report.RootState, stateMissing)
	}

	if err := applyAgentRootInstructionSync([]agentReport{report}); err != nil {
		t.Fatal(err)
	}
	rawTarget, err := os.Readlink(wantLink)
	if err != nil {
		t.Fatalf("root instructions symlink not created: %v", err)
	}
	if rawTarget != wantTarget {
		t.Fatalf("link target = %q, want %q", rawTarget, wantTarget)
	}
}

func TestApplyAgentRootInstructionSyncClaudeAndCodex(t *testing.T) {
	for _, tc := range []struct {
		agent    string
		linkPath string
	}{
		{"amp", ".config/amp/AGENTS.md"},
		{agentClaudeCode, ".claude/CLAUDE.md"},
		{agentCodex, ".codex/AGENTS.md"},
	} {
		home := t.TempDir()
		report := agentReport{
			Name:         tc.agent,
			Detected:     true,
			RootPath:     filepath.Join(home, filepath.FromSlash(tc.linkPath)),
			RootExpected: filepath.Join(home, ".agents", "AGENTS.md"),
			RootState:    stateMissing,
		}
		if err := os.MkdirAll(filepath.Dir(report.RootExpected), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(report.RootExpected, []byte("# Shared\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		if err := applyAgentRootInstructionSync([]agentReport{report}); err != nil {
			t.Fatal(err)
		}

		rawTarget, err := os.Readlink(report.RootPath)
		if err != nil {
			t.Fatal(err)
		}
		if rawTarget != report.RootExpected {
			t.Fatalf("%s link target = %q, want %q", tc.agent, rawTarget, report.RootExpected)
		}
	}
}

// Claude Code 2.1.277+ reads a project's AGENTS.md only when no CLAUDE.md or
// .claude/CLAUDE.md sits in the working directory or above it.
func TestRepoHasNoClaudeMDShadowingAgentsMD(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, "AGENTS.md")); err != nil {
		t.Fatalf("repo AGENTS.md: %v", err)
	}
	for _, name := range []string{"CLAUDE.md", filepath.Join(".claude", "CLAUDE.md")} {
		if _, err := os.Lstat(filepath.Join(repoRoot, name)); err == nil {
			t.Fatalf("%s would stop Claude Code from reading AGENTS.md", name)
		}
	}
}
