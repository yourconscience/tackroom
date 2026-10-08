package app

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/yourconscience/tackroom/internal/ui"
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
	// The changes line wraps at spaces; compare it with whitespace collapsed.
	flat := strings.Join(strings.Fields(out), " ")

	for _, want := range []string{
		"tackroom sync ✓ synced",
		"+2 skills, +1 plugins (claude)",
		// Same counts, different skills: omp is not merged into codex's entry.
		"+2 skills (codex, droid)",
		"+2 skills (omp)",
		"-1 skills, ~1 packages (pi)",
		"│ hermes │ not detected │",
	} {
		if !strings.Contains(flat, want) {
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
	for _, unwanted := range []string{"changes ", "added ", "skills missing"} {
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
		"needs attention  claude, codex (config unreadable)",
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
	// The same change in many harnesses makes the longest "changes" entry.
	for _, name := range []string{"copilot", "cursor", "droid", "grok", "hermes", "omp", "opencode", "pi", "qwen-code"} {
		reports = append(reports, agentReport{Name: name, Detected: true, Synced: true, SkillRoot: "/h/s",
			Managed: skills[:2], Adds: []string{"web-search"}, AddsAgent: []string{"reviewer"}})
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
		for _, want := range skills {
			if !strings.Contains(block, want) {
				t.Errorf("width %d: block lost %q", width, want)
			}
		}
		if !strings.Contains(strings.Join(strings.Fields(out), " "), "(copilot, cursor, droid, grok, hermes, omp, opencode, pi, qwen-code)") {
			t.Errorf("width %d: changes line lost harness names:\n%s", width, out)
		}
	}
}

// Values wrap only at spaces, so a path stays whole even past the width.
func TestRenderReportKeepsPathsWhole(t *testing.T) {
	const root = "/home/some-user/agent-config-root/skills"
	reports := []agentReport{{Name: "claude", Detected: true, Synced: true, SkillRoot: root}}
	for _, width := range []int{40, 50, 60} {
		if out := renderPlain(t, true, reports, width); !strings.Contains(out, root) {
			t.Errorf("width %d: path split across lines:\n%s", width, out)
		}
	}
}

func TestRenderReportNamesRelinks(t *testing.T) {
	repo := syncedRepo()
	repo.Linked = true
	reports := []agentReport{{Name: "claude", Detected: true, Synced: true, SkillRoot: "/h/s",
		RootPath: "/h/CLAUDE.md", RootState: stateSynced, RootExpected: "/h/.agents/AGENTS.md", RootLinked: true}}
	out := ansi.Strip(renderReport(true, repo, reports, t.TempDir(), config{}, 100))
	for _, want := range []string{"changes  ~/.agents linked · root doc linked (claude)", "/home/u/.agents (linked this run)", "AGENTS.md (linked this run)"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

func TestRestoreSyncActionsMarksRootRelink(t *testing.T) {
	preflight := []agentReport{{Name: "claude", RootPath: "/h/CLAUDE.md", RootState: stateMissing}, {Name: "codex", RootPath: "/h/AGENTS.md", RootState: stateSynced}}
	current := []agentReport{{Name: "claude", RootPath: "/h/CLAUDE.md", RootState: stateSynced}, {Name: "codex", RootPath: "/h/AGENTS.md", RootState: stateSynced}}
	restoreSyncActions(current, preflight)
	if !current[0].RootLinked || current[1].RootLinked {
		t.Fatalf("RootLinked = %v, %v; want only the relinked harness", current[0].RootLinked, current[1].RootLinked)
	}
}

func TestRenderReportShowsUnsupportedHooksAsNotice(t *testing.T) {
	reports := []agentReport{{Name: "claude", Detected: true, Synced: true, SkillRoot: "/h/s", UnsupportedHook: []string{"pre-tool-use-guard"}}}
	out := renderReport(true, syncedRepo(), reports, t.TempDir(), config{}, 100)
	if !strings.Contains(out, ui.Yellow.Render("hooks unsupported (1)")) || strings.Contains(out, ui.Red.Render("hooks unsupported (1)")) {
		t.Fatalf("unsupported hooks should be a yellow notice on a synced harness:\n%s", ansi.Strip(out))
	}
}

func TestPrintStatusReportWritesToTheGivenWriter(t *testing.T) {
	var buf bytes.Buffer
	reports := []agentReport{{Name: "claude", Detected: true, Synced: true, SkillRoot: "/h/s", Managed: hyphenatedSkills(30)}}
	printStatusReport(&buf, t.TempDir(), syncedRepo(), reports, t.TempDir(), config{}, true)
	out := buf.String()
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("status to a non-terminal carried escape codes: %q", out)
	}
	for _, want := range []string{"tackroom status", "claude   ✓ synced", "skills (30)", "skill-29-release-notes-draft", "Everything is synced."} {
		if !strings.Contains(out, want) {
			t.Errorf("status missing %q:\n%s", want, out)
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
