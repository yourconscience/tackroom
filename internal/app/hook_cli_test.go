package app

import (
	"os"
	"path/filepath"
	"testing"
)

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
