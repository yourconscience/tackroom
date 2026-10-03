package app

import (
	"strings"
	"testing"
)

func TestSummarizeSyncPlanShowsChangesAndBlockers(t *testing.T) {
	reports := []agentReport{
		{Name: "claude-code", Detected: true, Synced: true, RootState: stateSynced},
		{Name: "codex", Detected: true, AddsMCP: []string{"context7"}, AddsHook: []string{"notify-on-stop"}},
		{Name: "pi", Detected: true, Conflicts: []string{"release-notes"}},
		{Name: "qwen-code", Detected: true, RootState: stateMissing},
		{Name: "amp", Detected: false},
	}
	got := summarizeSyncPlan(reports)
	want := []string{
		"claude-code: up to date",
		"codex: add MCP context7, add hook notify-on-stop",
		"pi: conflict release-notes (not managed by tackroom)",
		"qwen-code: root instructions missing",
		"amp: not installed, skipped",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("summary =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestSummarizeSyncPlanNeverCallsUnsyncedAgentUpToDate(t *testing.T) {
	got := summarizeSyncPlan([]agentReport{{Name: "droid", Detected: true, Synced: false}})
	if len(got) != 1 || strings.Contains(got[0], "up to date") {
		t.Fatalf("unsynced agent rendered as up to date: %q", got)
	}
}

func TestSummarizeSyncPlanNamesUnreadableAgents(t *testing.T) {
	got := summarizeSyncPlan([]agentReport{{Name: "hermes", Detected: true, Error: "parse ~/.hermes/config.yaml: yaml: line 4"}})
	if len(got) != 1 || !strings.HasPrefix(got[0], "hermes: config unreadable, skipped (parse ~/.hermes/config.yaml") {
		t.Fatalf("summary = %q", got)
	}
}
