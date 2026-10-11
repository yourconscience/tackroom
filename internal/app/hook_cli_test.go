package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectAndRemoveNativeHooksAcrossSupportedFormats(t *testing.T) {
	home := t.TempDir()
	writeSyncTestFile(t, claudeHooksConfigPath(home), []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"~/.orca/agent-hooks/claude-hook.sh"},{"type":"command","command":"echo keep"}]}]}}`))
	writeSyncTestFile(t, codexHooksConfigPath(home), []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"~/.orca/agent-hooks/codex-hook.sh"}]}]}}`))
	writeSyncTestFile(t, droidHooksConfigPath(home), []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"~/.orca/agent-hooks/droid-hook.sh"}]}]}}`))
	writeSyncTestFile(t, filepath.Join(home, ".hermes", "config.yaml"), []byte("hooks:\n  on_session_end:\n    - command: ~/.orca/agent-hooks/hermes-hook.sh\n"))
	writeSyncTestFile(t, qwenSettingsPath(home), []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"~/.orca/agent-hooks/qwen-hook.sh"}]}]}}`))

	selected := []agentConfig{{Name: agentClaudeCode}, {Name: agentCodex}, {Name: agentDroid}, {Name: agentHermes}, {Name: agentQwenCode}}
	entries, unsupported, err := collectNativeHooks(home, config{}, selected)
	if err != nil {
		t.Fatal(err)
	}
	if len(unsupported) != 0 || len(filterNativeHooks(entries, "orca")) != 5 {
		t.Fatalf("entries=%#v unsupported=%#v", entries, unsupported)
	}
	changed, err := removeNativeHookEntries(filterNativeHooks(entries, "orca"))
	if err != nil {
		t.Fatal(err)
	}
	if changed != 5 {
		t.Fatalf("changed configs = %d, want 5", changed)
	}
	for _, path := range []string{claudeHooksConfigPath(home), codexHooksConfigPath(home), droidHooksConfigPath(home), filepath.Join(home, ".hermes", "config.yaml"), qwenSettingsPath(home)} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(string(data)), "orca") {
			t.Fatalf("orca hook remains in %s:\n%s", path, data)
		}
	}
	data, err := os.ReadFile(claudeHooksConfigPath(home))
	if err != nil || !strings.Contains(string(data), "echo keep") {
		t.Fatalf("unrelated hook was not preserved: err=%v data=%s", err, data)
	}
}

func TestMissingHookTarget(t *testing.T) {
	home := t.TempDir()
	missing := filepath.Join(home, ".orca", "agent-hooks", "hook.sh")
	command := "if [ -f '" + missing + "' ]; then /bin/sh '" + missing + "'; fi"
	if got := missingHookTarget(command, home); got != missing {
		t.Fatalf("missingHookTarget() = %q, want %q", got, missing)
	}
	if err := os.MkdirAll(filepath.Dir(missing), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSyncTestFile(t, missing, []byte("#!/bin/sh\n"))
	if got := missingHookTarget(command, home); got != "" {
		t.Fatalf("existing target reported missing: %q", got)
	}

	// A command that chains an existing script with a missing one is still
	// stale: report the missing target rather than clearing on the first hit.
	present := filepath.Join(home, ".agents", "hooks", "present.sh")
	writeSyncTestFile(t, present, []byte("#!/bin/sh\n"))
	gone := filepath.Join(home, ".agents", "hooks", "gone.py")
	chained := "'" + present + "' && python3 '" + gone + "'"
	if got := missingHookTarget(chained, home); got != gone {
		t.Fatalf("chained missing target = %q, want %q", got, gone)
	}
}

func TestNativeHookIsManagedUsesCodexRenderedCommand(t *testing.T) {
	entry := nativeHookEntry{
		Agent:   agentCodex,
		Event:   "SessionEnd",
		Command: "DOTAGENTS_MEMORY_SOURCE=codex ~/.agents/memory/hooks/session-end.sh",
	}
	cfg := config{Hooks: []hookConfig{{
		Name:    "memory-session-end",
		Enabled: true,
		Event:   "SessionEnd",
		Command: "~/.agents/memory/hooks/session-end.sh",
		Agents:  []string{agentCodex},
	}}}
	if !nativeHookIsManaged(entry, cfg) {
		t.Fatal("rendered Codex memory hook was not recognized as managed")
	}
}

func TestRemoveNativeHookEntriesScopesRemovalToMatchedEvent(t *testing.T) {
	home := t.TempDir()
	path := codexHooksConfigPath(home)
	command := "~/.agents/hooks/shared.sh"
	writeSyncTestFile(t, path, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"`+command+`"}]}],"SessionStart":[{"hooks":[{"type":"command","command":"`+command+`"}]}]}}`))

	entries, _, err := collectNativeHooks(home, config{}, []agentConfig{{Name: agentCodex}})
	if err != nil {
		t.Fatal(err)
	}
	matches := filterNativeHooks(entries, "Stop")
	if len(matches) != 1 {
		t.Fatalf("Stop matches = %d, want 1", len(matches))
	}
	if _, err := removeNativeHookEntries(matches); err != nil {
		t.Fatal(err)
	}
	remaining, _, err := collectNativeHooks(home, config{}, []agentConfig{{Name: agentCodex}})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].Event != "SessionStart" {
		t.Fatalf("remaining hooks = %#v, want only SessionStart", remaining)
	}
}
