package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name  string
		bytes int
		want  int
	}{
		{"zero", 0, 0},
		{"negative", -10, 0},
		{"below one token", 3, 0},
		{"exactly one token", 4, 1},
		{"rounds down", 7, 1},
		{"four thousand bytes", 4000, 1000},
		{"large", 48000, 12000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := estimateTokens(tc.bytes); got != tc.want {
				t.Fatalf("estimateTokens(%d) = %d, want %d", tc.bytes, got, tc.want)
			}
		})
	}
}

func TestFormatTokenEstimate(t *testing.T) {
	tests := []struct {
		name   string
		tokens int
		want   string
	}{
		{"small", 840, "~840 tokens (est.)"},
		{"boundary", 999, "~999 tokens (est.)"},
		{"one k", 1000, "~1.0k tokens (est.)"},
		{"example", 1200, "~1.2k tokens (est.)"},
		{"twelve k", 12000, "~12.0k tokens (est.)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatTokenEstimate(tc.tokens); got != tc.want {
				t.Fatalf("formatTokenEstimate(%d) = %q, want %q", tc.tokens, got, tc.want)
			}
		})
	}
}

func TestContextNoteThreshold(t *testing.T) {
	zero := 0
	custom := 5000
	negative := -1
	tests := []struct {
		name string
		cfg  config
		want int
	}{
		{"unset uses default", config{}, contextNoteTokensDefault},
		{"zero disables", config{ContextNoteTokens: &zero}, 0},
		{"custom value", config{ContextNoteTokens: &custom}, 5000},
		{"negative disables", config{ContextNoteTokens: &negative}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := contextNoteThreshold(tc.cfg); got != tc.want {
				t.Fatalf("contextNoteThreshold = %d, want %d", got, tc.want)
			}
		})
	}
}

// writeSkill creates skills/<name>/SKILL.md under root with the given frontmatter
// and returns the skill directory path.
func writeSkill(t *testing.T, root, name, fmName, description string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + fmName + "\ndescription: " + description + "\n---\n\n# " + fmName + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSkillListingBytes(t *testing.T) {
	root := t.TempDir()
	// name(3)+desc(5)=8, name(5)+desc(3)=8 => 16 bytes total.
	a := writeSkill(t, root, "alpha", "abc", "hello")
	b := writeSkill(t, root, "bravo", "bravo", "hey")
	missing := filepath.Join(root, "nope") // no SKILL.md -> skipped

	expected := map[string]string{
		"alpha": a,
		"bravo": b,
		"nope":  missing,
	}
	if got := skillListingBytes(expected); got != 16 {
		t.Fatalf("skillListingBytes = %d, want 16", got)
	}
	if got := skillListingBytes(map[string]string{}); got != 0 {
		t.Fatalf("skillListingBytes(empty) = %d, want 0", got)
	}
}

func TestContextSkillsForReportUsesExpectedSkills(t *testing.T) {
	expected := map[string]string{"shared": filepath.Join(t.TempDir(), "shared")}
	report := agentReport{ExpectedSkills: expected}

	got := contextSkillsForReport(config{}, "", "", report)
	if got["shared"] != expected["shared"] || len(got) != 1 {
		t.Fatalf("context skills = %#v, want %#v", got, expected)
	}
}

func TestContextNoteLines(t *testing.T) {
	costs := []harnessContextCost{
		{Name: "claude", Skills: 40, Bytes: 40000, Tokens: 10000},
		{Name: "codex", Skills: 5, Bytes: 4000, Tokens: 1000},
	}
	tests := []struct {
		name      string
		threshold int
		costs     []harnessContextCost
		wantCount int
		wantName  string
	}{
		{"triggers over threshold", 8000, costs, 1, "claude"},
		{"not triggered under threshold", 20000, costs, 0, ""},
		{"disabled zero threshold", 0, costs, 0, ""},
		{"disabled negative threshold", -1, costs, 0, ""},
		{"exact equal does not trigger", 10000, costs, 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lines := contextNoteLines(tc.threshold, tc.costs)
			if len(lines) != tc.wantCount {
				t.Fatalf("got %d notes %v, want %d", len(lines), lines, tc.wantCount)
			}
			for _, l := range lines {
				if len(l) < 5 || l[:5] != "note:" {
					t.Fatalf("note not prefixed with note:: %q", l)
				}
			}
			if tc.wantName != "" {
				if want := "note: " + tc.wantName; lines[0][:len(want)] != want {
					t.Fatalf("first note = %q, want prefix %q", lines[0], want)
				}
			}
		})
	}
}

// TestDoctorExitErrorIgnoresNotes verifies the doctor exit decision depends only
// on check pass/warn/fail counts. The function takes no note input, so context
// advisory notes structurally cannot change the exit code.
func TestDoctorExitErrorIgnoresNotes(t *testing.T) {
	tests := []struct {
		name    string
		failed  int
		warned  int
		wantErr bool
	}{
		{"clean", 0, 0, false},
		{"warnings only", 0, 3, true},
		{"failures", 2, 0, true},
		{"failures and warnings", 1, 4, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := doctorExitError(tc.failed, tc.warned)
			if (err != nil) != tc.wantErr {
				t.Fatalf("doctorExitError(%d,%d) err=%v, wantErr=%v", tc.failed, tc.warned, err, tc.wantErr)
			}
		})
	}
}
