package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yourconscience/tackroom/internal/agentrole"
)

func testDetection() *detectionResult {
	return &detectionResult{
		Harnesses: []string{"claude-code", "codex"},
		Items: []detectedItem{
			{
				Surface: "skill",
				Name:    "linter",
				Sources: []detectedSource{
					{Harness: "claude-code", Path: "/tmp/a", Hash: "h1"},
					{Harness: "codex", Path: "/tmp/b", Hash: "h2"},
				},
			},
			{
				Surface: "mcp",
				Name:    "tavily",
				Sources: []detectedSource{{Harness: "codex", Path: "tavily", Hash: "h3"}},
			},
		},
		Resolved: []detectedItem{
			{
				Surface:   "skill",
				Name:      "shared",
				Identical: true,
				Sources: []detectedSource{
					{Harness: "claude-code", Path: "/tmp/s", Hash: "h4"},
					{Harness: "codex", Path: "/tmp/s2", Hash: "h4"},
				},
			},
		},
		AutoResolved: 1,
	}
}

func press(t *testing.T, m reviewModel, keys ...string) reviewModel {
	t.Helper()
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case " ":
			msg = tea.KeyMsg{Type: tea.KeySpace}
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "left":
			msg = tea.KeyMsg{Type: tea.KeyLeft}
		case "right":
			msg = tea.KeyMsg{Type: tea.KeyRight}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		next, _ := m.Update(msg)
		m = next.(reviewModel)
	}
	return m
}

func TestReviewModelCycleAction(t *testing.T) {
	m := newReviewModel(testDetection())
	if m.rows[0].action != actionShare {
		t.Fatalf("default action should be share, got %s", m.rows[0].action)
	}
	m = press(t, m, " ")
	if m.rows[0].action != actionKeep {
		t.Fatalf("expected keep after one cycle, got %s", m.rows[0].action)
	}
	m = press(t, m, " ", " ")
	if m.rows[0].action != actionShare {
		t.Fatalf("expected share after full cycle, got %s", m.rows[0].action)
	}
}

func TestReviewModelBulkKeys(t *testing.T) {
	m := newReviewModel(testDetection())
	m = press(t, m, "s")
	for i, row := range m.rows {
		if row.action != actionSkip {
			t.Fatalf("row %d: expected skip, got %s", i, row.action)
		}
	}
	m = press(t, m, "a")
	for i, row := range m.rows {
		if row.action != actionShare {
			t.Fatalf("row %d: expected share, got %s", i, row.action)
		}
	}
}

func TestReviewModelNavigationBounds(t *testing.T) {
	m := newReviewModel(testDetection())
	m = press(t, m, "up")
	if m.cursor != 0 {
		t.Fatalf("cursor should clamp at 0, got %d", m.cursor)
	}
	m = press(t, m, "down", "down", "down")
	if m.cursor != 1 {
		t.Fatalf("cursor should clamp at last row, got %d", m.cursor)
	}
}

func TestReviewModelSourceCycle(t *testing.T) {
	m := newReviewModel(testDetection())
	m = press(t, m, "right")
	if m.rows[0].sourceIdx != 1 {
		t.Fatalf("expected sourceIdx 1, got %d", m.rows[0].sourceIdx)
	}
	m = press(t, m, "right")
	if m.rows[0].sourceIdx != 0 {
		t.Fatalf("expected wrap to 0, got %d", m.rows[0].sourceIdx)
	}
	// Single-source row must not cycle.
	m = press(t, m, "down", "right")
	if m.rows[1].sourceIdx != 0 {
		t.Fatalf("single-source row should stay at 0, got %d", m.rows[1].sourceIdx)
	}
}

func TestReviewModelAbort(t *testing.T) {
	detection := testDetection()
	m := newReviewModel(detection)
	m = press(t, m, "q")
	if !m.aborted {
		t.Fatal("q should abort")
	}
	if got := m.decisions(detection); got != nil {
		t.Fatalf("aborted model should return nil decisions, got %d", len(got))
	}
}

func TestReviewDecisionsIncludeResolved(t *testing.T) {
	detection := testDetection()
	m := newReviewModel(detection)
	m = press(t, m, " ", "enter", "enter") // first row -> keep, apply via confirm
	if !m.applied {
		t.Fatal("enter through confirm should apply")
	}
	decisions := m.decisions(detection)
	if len(decisions) != 3 {
		t.Fatalf("expected 3 decisions (2 rows + 1 resolved), got %d", len(decisions))
	}
	if decisions[0].Action != actionKeep {
		t.Fatalf("row 0 should be keep, got %s", decisions[0].Action)
	}
	last := decisions[len(decisions)-1]
	if last.Name != "shared" || last.Action != actionShare {
		t.Fatalf("resolved item should be auto-shared, got %s/%s", last.Name, last.Action)
	}
}

func TestReviewModelConfirmGate(t *testing.T) {
	m := newReviewModel(testDetection())
	m = press(t, m, "enter")
	if m.applied || !m.confirming {
		t.Fatal("first enter should enter confirm state, not apply")
	}
	m = press(t, m, "esc")
	if m.confirming || m.aborted {
		t.Fatal("esc in confirm state should return to review, not abort")
	}
	m = press(t, m, "enter", "y")
	if !m.applied {
		t.Fatal("y in confirm state should apply")
	}
}

func TestShareAllDecisions(t *testing.T) {
	detection := testDetection()
	decisions := shareAllDecisions(detection)
	if len(decisions) != 3 {
		t.Fatalf("expected 3 decisions, got %d", len(decisions))
	}
	for _, d := range decisions {
		if d.Action != actionShare {
			t.Fatalf("%s should be share, got %s", d.Name, d.Action)
		}
	}
	if decisions[0].Source.Harness != "claude-code" {
		t.Fatalf("differing item should pick first source, got %s", decisions[0].Source.Harness)
	}
}

func TestRenderDetectionSummary(t *testing.T) {
	out := renderDetectionSummary(testDetection())
	for _, want := range []string{"needs review:", "linter", "(differ)", "identical everywhere", "shared"} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary missing %q:\n%s", want, out)
		}
	}
	empty := renderDetectionSummary(&detectionResult{})
	if !strings.Contains(empty, "no import candidates") {
		t.Fatalf("empty summary unexpected: %s", empty)
	}
}

func TestApplyReviewDecisionsSkill(t *testing.T) {
	root := t.TempDir()
	srcRoot := t.TempDir()
	srcSkill := filepath.Join(srcRoot, "linter")
	if err := os.MkdirAll(srcSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcSkill, "SKILL.md"), []byte("---\nname: linter\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config{Version: 1}
	decisions := []reviewDecision{
		{Surface: "skill", Name: "linter", Action: actionShare, Source: detectedSource{Harness: "claude-code", Path: srcSkill}},
		{Surface: "skill", Name: "other", Action: actionSkip, Source: detectedSource{Harness: "codex", Path: filepath.Join(srcRoot, "missing")}},
	}
	shared, err := applyReviewDecisions(root, &cfg, decisions, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if shared != 1 {
		t.Fatalf("expected 1 shared, got %d", shared)
	}
	if !hasFile(filepath.Join(root, "skills", "linter", "SKILL.md")) {
		t.Fatal("shared skill should be copied into root")
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "other")); err == nil {
		t.Fatal("skipped skill must not be imported")
	}
}

func TestApplyReviewDecisionsRoleAndMCP(t *testing.T) {
	root := t.TempDir()
	cfg := config{Version: 1}
	roles := []nativeRoleCandidate{
		{
			nativeImportCandidate: nativeImportCandidate{Name: "reviewer", Origin: "codex"},
			TargetName:            "reviewer",
			Convert:               func() ([]byte, error) { return []byte("# reviewer\n"), nil },
		},
	}
	mcps := []nativeMCPCandidate{
		{
			nativeImportCandidate: nativeImportCandidate{Name: "tavily", Origin: "codex"},
			Server:                mcpServerConfig{Name: "tavily", Command: "npx", Args: []string{"tavily"}},
		},
	}
	decisions := []reviewDecision{
		{Surface: "role", Name: "reviewer", Action: actionShare, Source: detectedSource{Harness: "codex"}},
		{Surface: "mcp", Name: "tavily", Action: actionShare, Source: detectedSource{Harness: "codex"}},
	}
	shared, err := applyReviewDecisions(root, &cfg, decisions, nil, roles, mcps)
	if err != nil {
		t.Fatal(err)
	}
	if shared != 2 {
		t.Fatalf("expected 2 shared, got %d", shared)
	}
	if !hasFile(filepath.Join(root, "agents", "reviewer"+agentrole.MarkdownExtension)) {
		t.Fatal("role file should be written")
	}
	if len(cfg.MCPServers) != 1 || cfg.MCPServers[0].Name != "tavily" {
		t.Fatalf("MCP server should be upserted, got %+v", cfg.MCPServers)
	}
}
