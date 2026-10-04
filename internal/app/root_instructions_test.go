package app

import (
	"os"
	"path/filepath"
	"testing"
)

// droidRootInstructions returns the rootInstructionsCapability for the droid harness.
// Used by tests to exercise inspectRootInstructions with droid-specific paths.
func droidRootInstructions() *rootInstructionsCapability {
	h := harnessFor(agentDroid)
	if h == nil || h.RootInstructions == nil {
		panic("droid harness missing RootInstructions")
	}
	return h.RootInstructions
}

func TestInspectDroidRootInstructionsMissing(t *testing.T) {
	home := t.TempDir()
	report := agentReport{}

	if err := inspectRootInstructions(&report, droidRootInstructions(), filepath.Join(home, ".agents"), home); err != nil {
		t.Fatal(err)
	}

	if report.RootState != stateMissing {
		t.Fatalf("RootState = %q, want %q", report.RootState, stateMissing)
	}
	if report.RootExpected != filepath.Join(home, ".agents", "AGENTS.md") {
		t.Fatalf("RootExpected = %q", report.RootExpected)
	}
}

func TestInspectDroidRootInstructionsSynced(t *testing.T) {
	home := t.TempDir()
	expected := filepath.Join(home, ".agents", "AGENTS.md")
	linkPath := filepath.Join(home, ".factory", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(expected), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(expected, []byte("# Shared\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(expected, linkPath); err != nil {
		t.Fatal(err)
	}

	report := agentReport{}
	if err := inspectRootInstructions(&report, droidRootInstructions(), filepath.Join(home, ".agents"), home); err != nil {
		t.Fatal(err)
	}

	if report.RootState != stateSynced {
		t.Fatalf("RootState = %q, want %q", report.RootState, stateSynced)
	}
}

func TestInspectDroidRootInstructionsConflict(t *testing.T) {
	home := t.TempDir()
	linkPath := filepath.Join(home, ".factory", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(linkPath, []byte("# Local\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report := agentReport{}
	if err := inspectRootInstructions(&report, droidRootInstructions(), filepath.Join(home, ".agents"), home); err != nil {
		t.Fatal(err)
	}

	if report.RootState != stateConflict {
		t.Fatalf("RootState = %q, want %q", report.RootState, stateConflict)
	}
	if len(report.Conflicts) != 1 {
		t.Fatalf("Conflicts = %#v, want one conflict", report.Conflicts)
	}
}

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

func TestInspectClaudeAndCodexRootInstructions(t *testing.T) {
	for _, tc := range []struct {
		agent string
		cap   func() *rootInstructionsCapability
		dir   string
		file  string
	}{
		{"amp", ampRootInstructions, ".config/amp", "AGENTS.md"},
		{agentClaudeCode, claudeCodeRootInstructions, ".claude", "CLAUDE.md"},
		{agentCodex, codexRootInstructions, ".codex", "AGENTS.md"},
	} {
		t.Run(tc.agent+"/missing", func(t *testing.T) {
			home := t.TempDir()
			report := agentReport{}
			if err := inspectRootInstructions(&report, tc.cap(), filepath.Join(home, ".agents"), home); err != nil {
				t.Fatal(err)
			}
			if report.RootState != stateMissing {
				t.Fatalf("RootState = %q, want %q", report.RootState, stateMissing)
			}
		})
		t.Run(tc.agent+"/synced", func(t *testing.T) {
			home := t.TempDir()
			expected := filepath.Join(home, ".agents", "AGENTS.md")
			linkPath := filepath.Join(home, tc.dir, tc.file)
			if err := os.MkdirAll(filepath.Dir(expected), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(expected, []byte("# Shared\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(expected, linkPath); err != nil {
				t.Fatal(err)
			}
			report := agentReport{}
			if err := inspectRootInstructions(&report, tc.cap(), filepath.Join(home, ".agents"), home); err != nil {
				t.Fatal(err)
			}
			if report.RootState != stateSynced {
				t.Fatalf("RootState = %q, want %q", report.RootState, stateSynced)
			}
		})
		t.Run(tc.agent+"/conflict", func(t *testing.T) {
			home := t.TempDir()
			linkPath := filepath.Join(home, tc.dir, tc.file)
			if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(linkPath, []byte("# Local\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			report := agentReport{}
			if err := inspectRootInstructions(&report, tc.cap(), filepath.Join(home, ".agents"), home); err != nil {
				t.Fatal(err)
			}
			if report.RootState != stateConflict {
				t.Fatalf("RootState = %q, want %q", report.RootState, stateConflict)
			}
			if len(report.Conflicts) != 1 {
				t.Fatalf("Conflicts = %#v, want one conflict", report.Conflicts)
			}
		})
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
