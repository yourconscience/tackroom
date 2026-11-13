package app

import (
	"os"
	"strings"
	"testing"
)

func clearGatewayEnv(t *testing.T) {
	for _, key := range []string{"OPENCLAW_STATE_DIR", "OPENCLAW_CONFIG_PATH", "DSH_HOME", "DSH_AGENTS_HOME"} {
		t.Setenv(key, "")
	}
}

func TestDSHMCPGroupsTackroomRowsAndRejectsBadNames(t *testing.T) {
	home := t.TempDir()
	clearGatewayEnv(t)
	target, err := mcpTargetForHarness(agentDSH)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two"} {
		if err := target.patch(target, mcpServerConfig{Name: name, Command: name + "-mcp"}, home); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(dshPatchPath(home))
	if n := strings.Count(string(data), "- insert:"); n != 1 {
		t.Fatalf("want one insert op for tackroom rows, got %d:\n%s", n, data)
	}
	if err := target.patch(target, mcpServerConfig{Name: "bad name", Command: "x"}, home); err == nil {
		t.Fatal("server names outside [A-Za-z0-9_-]{1,32} must be rejected")
	}
}
