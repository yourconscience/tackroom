package app

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func syncedRepo() repoLinkReport {
	return repoLinkReport{State: stateSynced, ExpectedTarget: "/home/u/.agents"}
}

func renderPlain(t *testing.T, applied bool, reports []agentReport, width int) string {
	t.Helper()
	return ansi.Strip(renderReport(applied, syncedRepo(), reports, t.TempDir(), config{}, width))
}

// hyphenatedSkills returns n generic skill names with hyphens of varied length.
func hyphenatedSkills(n int) []string {
	suffixes := []string{"a", "lint", "code-review", "x", "release-notes-draft"}
	skills := make([]string, n)
	for i := range skills {
		skills[i] = fmt.Sprintf("skill-%02d-%s", i, suffixes[i%len(suffixes)])
	}
	return skills
}

func TestRenderReportGroupsChangesAndListsEveryItem(t *testing.T) {
	skills := []string{"code-review", "grilling", "release-notes", "tackroom", "web-search"}
	reports := []agentReport{
		{Name: "claude", Detected: true, Synced: true, SkillRoot: "/h/.claude/skills", Managed: skills,
			ManagedPlugin: []string{"demo"}, Adds: []string{"web-search", "grilling"}, AddsPlugin: []string{"demo"}},
		{Name: "codex", Detected: true, Synced: true, SkillRoot: "/h/.codex/skills", Managed: skills,
			Adds: []string{"web-search", "grilling"}},
		{Name: "droid", Detected: true, Synced: true, SkillRoot: "/h/.factory/skills", Managed: skills,
			Adds: []string{"web-search", "grilling"}},
		{Name: "omp", Detected: true, Synced: true, SkillRoot: "/h/.omp/skills", Managed: skills,
			Adds: []string{"code-review", "tackroom"}},
		{Name: "pi", Detected: true, Synced: true, SkillRoot: "/h/.pi/skills", Managed: skills,
			ManagedPackage: []string{"npm:demo@1.0.0"}, Removes: []string{"old"},
			UpdatesPackage: []string{"npm:demo@1.0.0"}},
		{Name: "hermes"},
	}
	out := renderPlain(t, true, reports, 100)

	for _, want := range []string{
		"tackroom sync  ✓ synced",
		"+2 skills, +1 plugins (claude)",
		// Same counts, different skills: omp is not merged into codex's entry.
		"+2 skills (codex, droid)",
		"+2 skills (omp)",
		"-1 skills, ~1 packages (pi)",
		"│ hermes  │ not detected │",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
	// Every harness block puts its values in the same column, and a change
	// spanning two surfaces continues under the first one.
	claude := harnessBlockText(out, "claude")
	added := strings.Index(claude, "skills: web-search, grilling")
	plugins := strings.Index(claude, "plugins: demo")
	lineStart := func(i int) int { return strings.LastIndex(claude[:i], "\n") + 1 }
	if added < 0 || plugins < 0 || added-lineStart(added) != plugins-lineStart(plugins) {
		t.Errorf("added surfaces are not aligned:\n%s", claude)
	}
	column := -1
	for _, harness := range []string{"claude", "codex", "pi"} {
		block := harnessBlockText(out, harness)
		i := strings.Index(block, "/h/.")
		col := i - (strings.LastIndex(block[:i], "\n") + 1)
		if column >= 0 && col != column {
			t.Errorf("%s values start at column %d, others at %d", harness, col, column)
		}
		column = col
	}
	if !strings.Contains(harnessBlockText(out, "pi"), "skills: old") {
		t.Errorf("pi block does not list the removed skill:\n%s", out)
	}
	// Full shape: every managed skill is listed under each harness block.
	for _, harness := range []string{"claude", "codex", "droid", "omp", "pi"} {
		block := harnessBlockText(out, harness)
		for _, skill := range skills {
			if !strings.Contains(block, skill) {
				t.Errorf("%s block does not list %q:\n%s", harness, skill, block)
			}
		}
	}
	if strings.Contains(out, "\nhermes ") {
		t.Errorf("undetected harness should only appear in the table:\n%s", out)
	}
}

func TestRenderReportShowsAbortedActionsAsPlanned(t *testing.T) {
	reports := []agentReport{{Name: "claude", Detected: true, Synced: false, SkillRoot: "/h/.claude/skills",
		Managed: []string{"code-review"}, Adds: []string{"web-search"}, Missing: []string{"web-search"},
		Conflicts: []string{"skill web-search: foreign dir"}}}
	out := renderPlain(t, false, reports, 100)
	for _, want := range []string{"planned  +1 skills (claude)", "to add", "skills: web-search"} {
		if !strings.Contains(out, want) {
			t.Errorf("aborted report missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"changes ", "added "} {
		if strings.Contains(out, unwanted) {
			t.Errorf("aborted report claims the run applied changes (%q):\n%s", unwanted, out)
		}
	}
}

func TestRenderReportFlagsProblems(t *testing.T) {
	reports := []agentReport{
		{Name: "claude", Detected: true, Synced: false, SkillRoot: "/h/.claude/skills",
			Managed: []string{"code-review"}, Missing: []string{"web-search"}, Conflicts: []string{"skill web-search: foreign dir"}},
		{Name: "codex", Error: "parse ~/.codex/config.toml: bad key"},
	}
	out := renderPlain(t, true, reports, 100)
	for _, want := range []string{
		"tackroom sync  ✗ needs attention",
		"skills missing (1)  web-search",
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

func TestRenderReportFitsWidth(t *testing.T) {
	skills := hyphenatedSkills(30)
	reports := []agentReport{
		{Name: "claude", Detected: true, Synced: false, SkillRoot: "/h/.claude/skills", Managed: skills,
			Missing: skills[:12], UnsupportedHook: []string{"pre-tool-use-guard", "session-end-memory"}},
		{Name: "codex"},
	}
	for width := 40; width <= 120; width++ {
		out := renderPlain(t, true, reports, width)
		for _, line := range strings.Split(out, "\n") {
			if w := lipgloss.Width(line); w > width {
				t.Errorf("width %d: line is %d columns: %q", width, w, line)
			}
			if strings.HasSuffix(line, "-") {
				t.Errorf("width %d: line breaks inside a hyphenated name: %q", width, line)
			}
		}
		block := harnessBlockText(out, "claude")
		for _, skill := range skills {
			if !strings.Contains(block, skill) {
				t.Errorf("width %d: block lost %q", width, skill)
			}
		}
	}
}

func TestRenderReportAlignsValuesUnderTheLongestLabel(t *testing.T) {
	reports := []agentReport{{Name: "claude", Detected: true, Synced: false, SkillRoot: "/h/.claude/skills",
		Managed: hyphenatedSkills(30), UnsupportedHook: []string{"pre-tool-use-guard"}}}
	block := harnessBlockText(renderPlain(t, true, reports, 100), "claude")
	// "hooks unsupported (1)" is the longest label: 21 columns plus a 2-column
	// indent and 2-column gap put every value, and every continuation, at 25.
	const valueColumn = 25
	for _, line := range strings.Split(strings.TrimRight(block, "\n"), "\n")[1:] {
		if len(line) <= valueColumn || line[valueColumn-1] != ' ' || line[valueColumn] == ' ' {
			t.Errorf("value does not start at column %d: %q", valueColumn, line)
		}
	}
}

// harnessBlockText returns the per-harness block that starts with name. Blocks
// follow the table, whose narrow form also starts lines with the name.
func harnessBlockText(out, name string) string {
	start := strings.LastIndex(out, "\n"+name+"  ")
	if start < 0 {
		return ""
	}
	rest := out[start+1:]
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		return rest[:end+1]
	}
	return rest
}
