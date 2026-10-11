package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func testHook() hookConfig {
	return hookConfig{
		Name:    "memory-stop",
		Enabled: true,
		Event:   "Stop",
		Command: "~/.agents/memory/hooks/stop.sh",
		Timeout: 15,
	}
}

func TestClaudeHookPatchPreservesUnrelatedHooks(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {"command": "echo keep", "timeout": 1},
          {"command": "~/.agents/memory/hooks/stop.sh", "timeout": 3}
        ]
      }
    ]
  }
}`)
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if state, err := inspectClaudeHook(testHook(), home); err != nil || state != stateDrifted {
		t.Fatalf("inspect before patch = %q, %v; want drifted, nil", state, err)
	}
	if err := patchClaudeHook(testHook(), home); err != nil {
		t.Fatal(err)
	}
	if state, err := inspectClaudeHook(testHook(), home); err != nil || state != stateSynced {
		t.Fatalf("inspect after patch = %q, %v; want synced, nil", state, err)
	}

	out, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	hooksRoot := raw["hooks"].(map[string]interface{})
	groups := hooksRoot["Stop"].([]interface{})
	group := groups[0].(map[string]interface{})
	if group["matcher"] != "" {
		t.Fatalf("unrelated matcher was not preserved: %#v", group)
	}
	items := group["hooks"].([]interface{})
	foundKeep := false
	foundManaged := false
	for _, itemRaw := range items {
		item := itemRaw.(map[string]interface{})
		switch item["command"] {
		case "echo keep":
			foundKeep = true
		case "~/.agents/memory/hooks/stop.sh":
			foundManaged = item["timeout"].(float64) == 15
		}
	}
	if !foundKeep || !foundManaged {
		t.Fatalf("hooks were not preserved/patched: %#v", items)
	}
}

func TestClaudeHookPatchRendersTypeCommand(t *testing.T) {
	// Regression: Claude Code ignores grouped hook entries that lack
	// "type": "command". A type-less entry (even with a matching timeout) must
	// read as drift so a resync heals it, and the patch must write the type.
	raw := map[string]interface{}{
		"hooks": map[string]interface{}{
			"Stop": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						map[string]interface{}{"command": "~/.agents/memory/hooks/stop.sh", "timeout": 15},
					},
				},
			},
		},
	}

	if state := inspectClaudeHookMap(raw, testHook()); state != stateDrifted {
		t.Fatalf("type-less entry inspected as %q, want drifted", state)
	}

	if err := upsertClaudeHookMap(raw, testHook()); err != nil {
		t.Fatal(err)
	}

	groups := raw["hooks"].(map[string]interface{})["Stop"].([]interface{})
	items := groups[0].(map[string]interface{})["hooks"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("managed hook duplicated instead of updated in place: %#v", items)
	}
	if item := items[0].(map[string]interface{}); item["type"] != "command" {
		t.Fatalf("patched hook missing type: %#v", item)
	}

	if state := inspectClaudeHookMap(raw, testHook()); state != stateSynced {
		t.Fatalf("after patch inspected as %q, want synced", state)
	}
}

func TestClaudeHookPatchUpdatesExistingHookInLaterGroup(t *testing.T) {
	raw := map[string]interface{}{
		"hooks": map[string]interface{}{
			"Stop": []interface{}{
				map[string]interface{}{
					"matcher": "first",
					"hooks": []interface{}{
						map[string]interface{}{"command": "echo keep", "timeout": 1},
					},
				},
				map[string]interface{}{
					"matcher": "second",
					"hooks": []interface{}{
						map[string]interface{}{"command": "~/.agents/memory/hooks/stop.sh", "timeout": 3},
					},
				},
			},
		},
	}

	if err := upsertClaudeHookMap(raw, testHook()); err != nil {
		t.Fatal(err)
	}

	groups := raw["hooks"].(map[string]interface{})["Stop"].([]interface{})
	managedCount := 0
	for i, groupRaw := range groups {
		group := groupRaw.(map[string]interface{})
		items := group["hooks"].([]interface{})
		for _, itemRaw := range items {
			item := itemRaw.(map[string]interface{})
			if item["command"] != "~/.agents/memory/hooks/stop.sh" {
				continue
			}
			managedCount++
			if i != 1 {
				t.Fatalf("managed hook moved to group %d, want group 1", i)
			}
			if item["timeout"].(int) != 15 {
				t.Fatalf("managed hook timeout = %#v, want 15", item["timeout"])
			}
		}
	}
	if managedCount != 1 {
		t.Fatalf("managed hook count = %d, want 1: %#v", managedCount, groups)
	}
}

func TestHookCommandMatchingNormalizesHomePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	raw := map[string]interface{}{
		"hooks": map[string]interface{}{
			"Stop": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{
						map[string]interface{}{
							"command": filepath.Join(home, ".agents", "memory", "hooks", "stop.sh"),
							"timeout": 3,
						},
					},
				},
			},
		},
	}

	if state := inspectClaudeHookMap(raw, testHook()); state != stateDrifted {
		t.Fatalf("inspectClaudeHookMap with absolute home path = %q, want drifted", state)
	}
	if err := upsertClaudeHookMap(raw, testHook()); err != nil {
		t.Fatal(err)
	}

	groups := raw["hooks"].(map[string]interface{})["Stop"].([]interface{})
	items := groups[0].(map[string]interface{})["hooks"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("managed hook was duplicated instead of normalized: %#v", items)
	}
	item := items[0].(map[string]interface{})
	if item["command"] != "~/.agents/memory/hooks/stop.sh" || item["timeout"].(int) != 15 {
		t.Fatalf("managed hook was not updated in place: %#v", item)
	}
}

func TestHookPatchAddsMatcherFreeGroupWithoutOverwritingExistingGroups(t *testing.T) {
	raw := map[string]interface{}{
		"hooks": map[string]interface{}{
			"SessionStart": []interface{}{
				map[string]interface{}{
					"matcher": "resume",
					"hooks": []interface{}{
						map[string]interface{}{"command": "echo keep", "timeout": 1},
					},
				},
				"future-native-group",
				map[string]interface{}{
					"matcher": "startup",
					"hooks":   map[string]interface{}{"future": true},
				},
			},
		},
	}
	groupsBefore := raw["hooks"].(map[string]interface{})["SessionStart"].([]interface{})
	existingBefore, err := json.Marshal(groupsBefore)
	if err != nil {
		t.Fatal(err)
	}
	hook := hookConfig{
		Name:    "memory-session-start",
		Enabled: true,
		Event:   "SessionStart",
		Command: "~/.agents/memory/hooks/session-start.sh",
		Timeout: 15,
	}

	if err := upsertClaudeHookMap(raw, hook); err != nil {
		t.Fatal(err)
	}

	groups := raw["hooks"].(map[string]interface{})["SessionStart"].([]interface{})
	if len(groups) != 4 {
		t.Fatalf("group count = %d, want 4: %#v", len(groups), groups)
	}
	existingAfter, err := json.Marshal(groups[:3])
	if err != nil {
		t.Fatal(err)
	}
	if string(existingAfter) != string(existingBefore) {
		t.Fatalf("existing groups changed:\nbefore: %s\nafter:  %s", existingBefore, existingAfter)
	}
	added := groups[3].(map[string]interface{})
	if _, hasMatcher := added["matcher"]; hasMatcher {
		t.Fatalf("new global hook inherited a matcher: %#v", added)
	}
	items := added["hooks"].([]interface{})
	if len(items) != 1 || items[0].(map[string]interface{})["command"] != hook.Command {
		t.Fatalf("new hook group = %#v", added)
	}
}

func TestHookPatchRejectsMalformedHooksRootWithoutOverwritingIt(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".claude", "settings.json")
	writeSyncTestFile(t, configPath, []byte("{\"hooks\":[\"keep\"]}\n"))
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := patchClaudeHook(testHook(), home); err == nil || !strings.Contains(err.Error(), "hooks must be an object") {
		t.Fatalf("patch error = %v, want malformed hooks error", err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("malformed native config was overwritten:\nbefore: %s\nafter: %s", before, after)
	}
}

func TestHookTimeoutMatchesExactNumericValues(t *testing.T) {
	tests := []struct {
		name   string
		value  interface{}
		target int
		want   bool
	}{
		{name: "int64 exact", value: int64(15), target: 15, want: true},
		{name: "int64 mismatch", value: int64(16), target: 15, want: false},
		{name: "float64 exact", value: float64(15), target: 15, want: true},
		{name: "float64 fractional", value: 15.5, target: 15, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := map[string]interface{}{"timeout": test.value}
			if got := hookTimeoutMatches(item, test.target); got != test.want {
				t.Fatalf("hookTimeoutMatches(%#v, %d) = %v, want %v", test.value, test.target, got, test.want)
			}
		})
	}
}

func TestHookCommandMatchingDoesNotCleanShellArgumentsAsPaths(t *testing.T) {
	if hookCommandMatches("echo bar", "echo foo/../bar") {
		t.Fatal("different shell arguments matched after path cleaning")
	}
}

func TestCodexMemoryCleanupAcrossTierTransitions(t *testing.T) {
	for _, tier := range []string{"off", "basic"} {
		t.Run(tier, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			root := filepath.Join(home, ".agents")
			prior := config{}
			for _, event := range []string{"SessionStart", "SessionEnd"} {
				name, script := "memory-session-start", "session-start.sh"
				if event == "SessionEnd" {
					name, script = "memory-session-end", "session-end.sh"
				}
				hook := hookConfig{Name: name, Enabled: true, Event: event, Command: filepath.Join(root, "memory", "hooks", script), Timeout: 15, Agents: []string{agentCodex}}
				prior.Hooks = append(prior.Hooks, hook)
				if err := patchCodexHook(hook, home); err != nil {
					t.Fatal(err)
				}
			}
			keep := hookConfig{Name: "unrelated", Event: "SessionStart", Command: "echo keep"}
			if err := patchCodexHook(keep, home); err != nil {
				t.Fatal(err)
			}
			if _, err := removeNativeManagedMemoryHooks(home, root, prior, config{}); err != nil {
				t.Fatal(err)
			}
			if tier == "basic" {
				for _, hook := range prior.Hooks {
					hook.Command = strings.Replace(hook.Command, "session-start.sh", "basic-session-start.py", 1)
					hook.Command = strings.Replace(hook.Command, "session-end.sh", "basic-session-end.py", 1)
					if err := patchCodexHook(hook, home); err != nil {
						t.Fatal(err)
					}
				}
			}
			entries, err := readGroupedNativeHooks(agentCodex, codexHooksConfigPath(home))
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if tier == "basic" {
				want = 3
			}
			if len(entries) != want {
				t.Fatalf("hooks after %s = %#v", tier, entries)
			}
			for _, entry := range entries {
				if strings.Contains(entry.Command, "session-start.sh") || strings.Contains(entry.Command, "session-end.sh") {
					t.Fatalf("old hook survived %s: %s", tier, entry.Command)
				}
			}
		})
	}
}

func TestRemoveNativeManagedMemoryHooksCleansSupportedNativeConfigs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".agents")
	files := map[string]string{
		filepath.Join(home, ".claude", "settings.json"): `{
  "hooks": {
    "SessionStart": [{"hooks": [{"command": "echo keep"}, {"command": "~/.agents/memory/hooks/basic-session-start.py"}]}]
  }
}`,
		filepath.Join(home, ".codex", "hooks.json"): `{
  "hooks": {
    "SessionEnd": [{"hooks": [{"type": "command", "command": "~/.agents/memory/hooks/session-end.sh"}, {"type": "command", "command": "echo codex"}]}]
  }
}`,
		filepath.Join(home, ".factory", "settings.json"): `{
  "hooks": {
    "Stop": [{"hooks": [{"command": "` + filepath.Join(root, "memory", "hooks", "stop.sh") + `"}, {"command": "echo droid"}]}]
  }
}`,
		filepath.Join(home, ".hermes", "config.yaml"): `hooks:
  on_session_start:
    - command: ~/.agents/memory/hooks/sync-vault-to-memory.sh
  on_session_finalize:
    - command: ~/.agents/memory/hooks/session-end.sh
    - command: ~/.agents/memory/hooks/sync-memory-to-vault.sh
    - command: echo hermes
`,
	}
	for path, data := range files {
		writeSyncTestFile(t, path, []byte(data))
	}
	cleaned, err := removeNativeManagedMemoryHooks(home, root, config{}, config{})
	if err != nil {
		t.Fatal(err)
	}
	if cleaned != 4 {
		t.Fatalf("cleaned files = %d, want 4", cleaned)
	}
	for path, keep := range map[string]string{
		filepath.Join(home, ".claude", "settings.json"):  "echo keep",
		filepath.Join(home, ".codex", "hooks.json"):      "echo codex",
		filepath.Join(home, ".factory", "settings.json"): "echo droid",
		filepath.Join(home, ".hermes", "config.yaml"):    "echo hermes",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if strings.Contains(text, "memory/hooks") {
			t.Fatalf("%s still contains managed memory command:\n%s", path, text)
		}
		if !strings.Contains(text, keep) {
			t.Fatalf("%s did not preserve unrelated hook %q:\n%s", path, keep, text)
		}
	}
}

func TestHermesHookPatchSessionEnd(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".hermes", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`hooks:
  on_session_finalize:
    - command: echo keep
      timeout: 1
`)
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	hook := hookConfig{
		Name:    "memory-session-end",
		Enabled: true,
		Event:   "SessionEnd",
		Command: "~/.agents/memory/hooks/session-end.sh",
		Timeout: 30,
	}
	if state, err := inspectHermesHook(hook, home); err != nil || state != stateMissing {
		t.Fatalf("inspect before patch = %q, %v; want missing, nil", state, err)
	}
	if err := patchHermesHook(hook, home); err != nil {
		t.Fatal(err)
	}
	if state, err := inspectHermesHook(hook, home); err != nil || state != stateSynced {
		t.Fatalf("inspect after patch = %q, %v; want synced, nil", state, err)
	}
	out, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	root := raw["hooks"].(map[string]interface{})
	items := root["on_session_finalize"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("unexpected hermes hooks: %#v", items)
	}
}

func TestHermesHookClampsTimeoutToNativeMaximum(t *testing.T) {
	home := t.TempDir()
	hook := hookConfig{
		Name:    "long-hermes-hook",
		Enabled: true,
		Event:   "post_llm_call",
		Command: "~/.agents/hooks/long-running.sh",
		Timeout: 5000,
		Agents:  []string{agentHermes},
	}
	if err := patchHermesHook(hook, home); err != nil {
		t.Fatal(err)
	}
	if state, err := inspectHermesHook(hook, home); err != nil || state != stateSynced {
		t.Fatalf("inspect after patch = %q, %v; want synced, nil", state, err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".hermes", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	items := raw["hooks"].(map[string]interface{})["post_llm_call"].([]interface{})
	if got := items[0].(map[string]interface{})["timeout"]; got != hermesMaxHookTimeout {
		t.Fatalf("Hermes timeout = %#v, want %d", got, hermesMaxHookTimeout)
	}
}

func TestHermesUnsupportedHookEventIsNotQueuedForSync(t *testing.T) {
	cfg := config{
		Hooks: []hookConfig{{
			Name:    "memory-stop",
			Enabled: true,
			Event:   "Stop",
			Command: "~/.agents/memory/hooks/stop.sh",
			Agents:  []string{agentHermes},
		}},
	}
	report := agentReport{Name: agentHermes, Detected: true}
	if err := augmentHookReport(&report, agentConfig{Name: agentHermes}, cfg, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if len(report.UnsupportedHook) != 1 || report.UnsupportedHook[0] != "memory-stop" {
		t.Fatalf("unsupported hooks = %#v, want memory-stop", report.UnsupportedHook)
	}
	if len(report.AddsHook) != 0 || len(report.MissingHook) != 0 {
		t.Fatalf("unsupported hook should not queue sync: adds=%#v missing=%#v", report.AddsHook, report.MissingHook)
	}
}

func TestDesiredHooksUnsupportedAgent(t *testing.T) {
	cfg := config{
		Hooks: []hookConfig{{
			Name:    "cmux-amp-session-start",
			Enabled: true,
			Event:   "SessionStart",
			Command: "~/.agents/hooks/cmux/dispatch.sh amp session-start",
			Agents:  []string{agentAmp},
		}},
	}
	hooks, supported := desiredHooksForAgent(cfg, agentAmp)
	if supported {
		t.Fatal("amp hooks should be unsupported until a hook surface is verified")
	}
	if len(hooks) != 1 || hooks[0].Name != "cmux-amp-session-start" {
		t.Fatalf("desired hooks = %#v, want cmux-amp-session-start", hooks)
	}
}

func TestCodexHookPatchWritesNestedHooksAndEnablesFeature(t *testing.T) {
	home := t.TempDir()
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hooksPath, []byte(`{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {"type": "command", "command": "echo keep", "timeout": 1},
          {"command": "~/.agents/hooks/cmux/dispatch.sh codex session-start", "timeout": 1}
        ]
      }
    ]
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.WriteFile(configPath, []byte("[profiles.default]\nmodel = \"gpt-5\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	hook := hookConfig{
		Name:    "cmux-codex-session-start",
		Enabled: true,
		Event:   "SessionStart",
		Command: "~/.agents/hooks/cmux/dispatch.sh codex session-start",
		Timeout: 5,
		Agents:  []string{agentCodex},
	}
	if state, err := inspectCodexHook(hook, home); err != nil || state != stateDrifted {
		t.Fatalf("inspect before patch = %q, %v; want drifted, nil", state, err)
	}
	if err := patchCodexHook(hook, home); err != nil {
		t.Fatal(err)
	}
	if state, err := inspectCodexHook(hook, home); err != nil || state != stateSynced {
		t.Fatalf("inspect after patch = %q, %v; want synced, nil", state, err)
	}

	out, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	items := raw["hooks"].(map[string]interface{})["SessionStart"].([]interface{})[0].(map[string]interface{})["hooks"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("nested hook count = %d, want 2: %#v", len(items), items)
	}
	managed := items[1].(map[string]interface{})
	if managed["type"] != "command" || managed["command"] != hook.Command || managed["timeout"].(float64) != 5 {
		t.Fatalf("managed hook was not rendered as nested command: %#v", managed)
	}

	configOut, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(configOut); got != "[features]\nhooks = true\n\n[profiles.default]\nmodel = \"gpt-5\"\n" {
		t.Fatalf("codex config not patched under [features]:\n%s", got)
	}
}

func TestCodexHookPatchEnablesExistingDisabledFeature(t *testing.T) {
	home := t.TempDir()
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.WriteFile(configPath, []byte("[features]\nhooks = false\nexperimental = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hook := hookConfig{
		Name:    "cmux-codex-session-start",
		Enabled: true,
		Event:   "SessionStart",
		Command: "~/.agents/hooks/cmux/dispatch.sh codex session-start",
		Timeout: 5,
		Agents:  []string{agentCodex},
	}
	if err := patchCodexHook(hook, home); err != nil {
		t.Fatal(err)
	}
	if state, err := inspectCodexHook(hook, home); err != nil || state != stateSynced {
		t.Fatalf("inspect after patch = %q, %v; want synced, nil", state, err)
	}
	configOut, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(configOut); got != "[features]\nhooks = true\nexperimental = true\n\n" {
		t.Fatalf("codex config not updated in existing [features]:\n%s", got)
	}
}

func TestCodexHookPatchEnablesCommentedFeaturesHeader(t *testing.T) {
	home := t.TempDir()
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.WriteFile(configPath, []byte("[features] # enabled feature flags\r\nexperimental = true\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hook := hookConfig{
		Name:    "cmux-codex-session-start",
		Enabled: true,
		Event:   "SessionStart",
		Command: "~/.agents/hooks/cmux/dispatch.sh codex session-start",
		Timeout: 5,
		Agents:  []string{agentCodex},
	}
	if err := patchCodexHook(hook, home); err != nil {
		t.Fatal(err)
	}
	configOut, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(configOut)
	if strings.Count(got, "[features]") != 1 {
		t.Fatalf("codex config has duplicate [features] header:\n%s", got)
	}
	if !strings.Contains(got, "[features] # enabled feature flags\r\nhooks = true\r\nexperimental = true") {
		t.Fatalf("codex config did not patch existing commented [features] header:\n%s", got)
	}
}

func TestCodexSessionEndHookClampsTimeoutToNativeMaximum(t *testing.T) {
	home := t.TempDir()
	hook := hookConfig{
		Name:    "memory-session-end",
		Enabled: true,
		Event:   "SessionEnd",
		Command: "~/.agents/memory/hooks/basic-session-end.py",
		Timeout: 30,
		Agents:  []string{agentCodex},
	}
	if err := patchCodexHook(hook, home); err != nil {
		t.Fatal(err)
	}
	if state, err := inspectCodexHook(hook, home); err != nil || state != stateSynced {
		t.Fatalf("inspect after patch = %q, %v; want synced, nil", state, err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".codex", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	items := raw["hooks"].(map[string]interface{})["SessionEnd"].([]interface{})[0].(map[string]interface{})["hooks"].([]interface{})
	if got := items[0].(map[string]interface{})["timeout"]; got != float64(codexSessionEndMaxTimeout) {
		t.Fatalf("Codex SessionEnd timeout = %#v, want %d", got, codexSessionEndMaxTimeout)
	}
	if got, want := items[0].(map[string]interface{})["command"], "DOTAGENTS_MEMORY_SOURCE=codex ~/.agents/memory/hooks/basic-session-end.py"; got != want {
		t.Fatalf("Codex SessionEnd command = %#v, want %q", got, want)
	}
}

func TestCodexMemorySessionStartSetsSourceHint(t *testing.T) {
	hook := nativeCodexHook(hookConfig{
		Name:    "memory-session-start",
		Event:   "SessionStart",
		Command: "~/.agents/memory/hooks/session-start.sh",
		Timeout: 15,
	})
	if got, want := hook.Command, "DOTAGENTS_MEMORY_SOURCE=codex ~/.agents/memory/hooks/session-start.sh"; got != want {
		t.Fatalf("Codex SessionStart command = %q, want %q", got, want)
	}
}

func TestDroidHookPatchWritesNestedHooks(t *testing.T) {
	home := t.TempDir()
	hook := hookConfig{
		Name:    "cmux-droid-stop",
		Enabled: true,
		Event:   "Stop",
		Command: "~/.agents/hooks/cmux/dispatch.sh factory stop",
		Timeout: 5000,
		Agents:  []string{agentDroid},
	}
	if state, err := inspectDroidHook(hook, home); err != nil || state != stateMissing {
		t.Fatalf("inspect before patch = %q, %v; want missing, nil", state, err)
	}
	if err := patchDroidHook(hook, home); err != nil {
		t.Fatal(err)
	}
	if !hasFile(filepath.Join(home, ".factory", "hooks.json")) {
		t.Fatal("new Droid hook config was not written to ~/.factory/hooks.json")
	}
	if state, err := inspectDroidHook(hook, home); err != nil || state != stateSynced {
		t.Fatalf("inspect after patch = %q, %v; want synced, nil", state, err)
	}
}

func TestUpsertCodexHooksFeaturePreservesNestedFeatureKeys(t *testing.T) {
	content := `[features]
unified_exec = true

[features.custom]
hooks = false

[profiles.default]
model = "gpt-5"
`

	updated := upsertCodexHooksFeature(content)
	parent, ok := extractTOMLSection(updated, "[features]")
	if !ok || !strings.Contains(parent, "hooks = true") {
		t.Fatalf("parent [features] section was not enabled:\n%s", updated)
	}
	if !strings.Contains(updated, "[features.custom]\nhooks = false") {
		t.Fatalf("nested feature table was changed:\n%s", updated)
	}
}

func TestDroidHookPatchUsesLegacySettingsWithoutShadowingUnrelatedHooks(t *testing.T) {
	home := t.TempDir()
	legacyPath := filepath.Join(home, ".factory", "settings.json")
	writeSyncTestFile(t, legacyPath, []byte(`{
  "model": "keep",
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "echo keep"}]}]
  }
}`))
	hook := hookConfig{
		Name:    "memory-session-end",
		Enabled: true,
		Event:   "SessionEnd",
		Command: "~/.agents/memory/hooks/session-end.sh",
		Timeout: 30,
		Agents:  []string{agentDroid},
	}

	if err := patchDroidHook(hook, home); err != nil {
		t.Fatal(err)
	}
	if hasFile(filepath.Join(home, ".factory", "hooks.json")) {
		t.Fatal("patch created hooks.json and shadowed unrelated legacy settings hooks")
	}
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"model": "keep"`) || !strings.Contains(text, "echo keep") || !strings.Contains(text, hook.Command) {
		t.Fatalf("legacy Droid settings were not preserved and patched:\n%s", text)
	}
}

func TestDroidHookPatchUsesPrimaryConfigWhenLegacySettingsHaveNoHooks(t *testing.T) {
	home := t.TempDir()
	legacyPath := filepath.Join(home, ".factory", "settings.json")
	legacy := []byte("{\"model\":\"keep\"}\n")
	writeSyncTestFile(t, legacyPath, legacy)
	hook := hookConfig{
		Name:    "memory-session-end",
		Enabled: true,
		Event:   "SessionEnd",
		Command: "~/.agents/memory/hooks/session-end.sh",
		Timeout: 30,
		Agents:  []string{agentDroid},
	}

	if err := patchDroidHook(hook, home); err != nil {
		t.Fatal(err)
	}
	if !hasFile(filepath.Join(home, ".factory", "hooks.json")) {
		t.Fatal("patch did not create the primary Droid hooks.json")
	}
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(legacy) {
		t.Fatalf("ordinary Droid settings changed:\nbefore: %s\nafter: %s", legacy, data)
	}
}

func TestValidateConfigRejectsNegativeHookTimeout(t *testing.T) {
	cfg := config{Hooks: []hookConfig{{
		Name:    "invalid-timeout",
		Enabled: true,
		Event:   "SessionStart",
		Command: "echo nope",
		Timeout: -1,
	}}}
	if err := validateConfig(&cfg, t.TempDir(), false); err == nil || !strings.Contains(err.Error(), "negative timeout") {
		t.Fatalf("validateConfig error = %v, want negative timeout error", err)
	}
}

func TestHermesNativeCmuxHookEventsAreSupported(t *testing.T) {
	for _, event := range []string{
		"on_session_start",
		"pre_llm_call",
		"post_llm_call",
		"pre_verify",
		"pre_approval_request",
		"post_approval_response",
		"on_session_end",
		"on_session_finalize",
		"on_session_reset",
		"subagent_start",
		"subagent_stop",
		"pre_gateway_dispatch",
		"pre_tool_call",
		"post_tool_call",
		"transform_tool_result",
		"transform_terminal_output",
		"transform_llm_output",
	} {
		if got, ok := hermesHookEvent(event); !ok || got != event {
			t.Fatalf("hermesHookEvent(%q) = %q, %v; want same event, true", event, got, ok)
		}
	}
}
