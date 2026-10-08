package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func syncedRepo() repoLinkReport {
	return repoLinkReport{State: stateSynced, ExpectedTarget: "/home/u/.agents"}
}

func renderPlain(t *testing.T, reports []agentReport, width int) string {
	t.Helper()
	return ansi.Strip(renderReport("sync", syncedRepo(), reports, t.TempDir(), config{}, width))
}

func TestRenderReportGroupsChangesAndListsEveryItem(t *testing.T) {
	skills := []string{"artifact", "grilling", "pr-triage", "ship", "tackroom"}
	reports := []agentReport{
		{Name: "claude", Detected: true, Synced: true, SkillRoot: "/h/.claude/skills", Managed: skills,
			ManagedPlugin: []string{"myagents"}, Adds: []string{"ship", "grilling"}, AddsPlugin: []string{"myagents"}},
		{Name: "codex", Detected: true, Synced: true, SkillRoot: "/h/.codex/skills", Managed: skills,
			Adds: []string{"ship", "grilling"}},
		{Name: "droid", Detected: true, Synced: true, SkillRoot: "/h/.factory/skills", Managed: skills,
			Adds: []string{"ship", "grilling"}},
		{Name: "pi", Detected: true, Synced: true, SkillRoot: "/h/.pi/skills", Managed: skills,
			ManagedPackage: []string{"npm:pi-subagents@0.67.0"}, Removes: []string{"old"},
			UpdatesPackage: []string{"npm:pi-subagents@0.67.0"}},
		{Name: "hermes"},
	}
	out := renderPlain(t, reports, 100)

	for _, want := range []string{
		"tackroom sync  ✓ synced",
		"+2 skills, +1 plugins (claude)",
		"+2 skills (codex, droid)",
		"-1 skills, ~1 packages (pi)",
		"│ hermes  │ not detected",
		"added          skills: ship, grilling · plugins: myagents",
		"removed        skills: old",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
	// Full shape: every managed skill is listed under each harness block.
	for _, harness := range []string{"claude", "codex", "droid", "pi"} {
		block := harnessBlockText(out, harness)
		for _, skill := range skills {
			if !strings.Contains(block, skill) {
				t.Errorf("%s block does not list %q:\n%s", harness, skill, block)
			}
		}
	}
	if strings.Contains(out, "hermes  ✓") || strings.Contains(out, "\nhermes ") {
		t.Errorf("undetected harness should only appear in the table:\n%s", out)
	}
}

func TestRenderReportFlagsProblems(t *testing.T) {
	reports := []agentReport{
		{Name: "claude", Detected: true, Synced: false, SkillRoot: "/h/.claude/skills",
			Managed: []string{"artifact"}, Missing: []string{"ship"}, Conflicts: []string{"skill ship: foreign dir"}},
		{Name: "codex", Error: "parse ~/.codex/config.toml: bad key"},
	}
	out := renderPlain(t, reports, 100)
	for _, want := range []string{
		"tackroom sync  ✗ needs attention",
		"skills missing (1)  ship",
		"conflicts (1)",
		"codex  ✗ config unreadable",
		"parse ~/.codex/config.toml: bad key",
		"needs attention: claude, codex (config unreadable)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

func TestRenderReportWrapsListsUnderTheirLabel(t *testing.T) {
	var skills []string
	for _, name := range strings.Fields("agent-insights ai-engineering-radar artifact batch-grill-me brag cua-driver decide grilling gws herdr humanizer jobs load pr-triage redesign-skill remote-access repo-eval ship spawn spec tackroom taste-skill tech-search tern tg x-cli") {
		skills = append(skills, name)
	}
	reports := []agentReport{{Name: "claude", Detected: true, Synced: true, SkillRoot: "/h/.claude/skills", Managed: skills}}
	// Sweep widths: whether a line ends at a hyphen depends on where it breaks.
	for width := 50; width <= 120; width++ {
		block := harnessBlockText(renderPlain(t, reports, width), "claude")
		continuation := 0
		for _, line := range strings.Split(strings.TrimRight(block, "\n"), "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Errorf("width %d: line is %d columns: %q", width, w, line)
			}
			if strings.HasPrefix(line, strings.Repeat(" ", 17)) {
				continuation++
			}
			if strings.HasSuffix(line, "-") {
				t.Errorf("width %d: line breaks inside a hyphenated name: %q", width, line)
			}
		}
		if continuation == 0 {
			t.Errorf("width %d: expected the skill list to wrap under its label:\n%s", width, block)
		}
		for _, skill := range skills {
			if !strings.Contains(block, skill) {
				t.Errorf("width %d: wrapped list lost %q", width, skill)
			}
		}
	}
}

// harnessBlockText returns the per-harness block that starts with name.
func harnessBlockText(out, name string) string {
	start := strings.Index(out, "\n"+name+"  ")
	if start < 0 {
		return ""
	}
	rest := out[start+1:]
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		return rest[:end+1]
	}
	return rest
}
