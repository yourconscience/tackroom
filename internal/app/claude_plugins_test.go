package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func writeClaudePluginFixture(t *testing.T, root string, marketplace string, plugins map[string]string) {
	t.Helper()
	for name, manifest := range plugins {
		dir := filepath.Join(root, "plugins", name, ".claude-plugin")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := claudeMarketplacePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(marketplace), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readClaudeSettingsForTest(t *testing.T, home string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(claudeSettingsPath(home))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestClaudePluginSyncRegistersDirectoryMarketplace(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeClaudePluginFixture(t, root, `{
  "name": "mine",
  "owner": {"name": "me"},
  "plugins": [
    {"name": "mods", "source": "./plugins/mods"},
    {"name": "quiet", "source": "./plugins/quiet"},
    {"name": "remote", "source": {"source": "github", "repo": "o/r"}}
  ]
}`, map[string]string{
		"mods":  `{"name":"mods"}`,
		"quiet": `{"name":"quiet","defaultEnabled":false}`,
	})
	settingsPath := claudeSettingsPath(home)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{
  "theme": "dark",
  "hooks": {"Stop": []},
  "extraKnownMarketplaces": {"team": {"source": {"source": "github", "repo": "org/team"}}},
  "enabledPlugins": {"tool@team": true}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	agent := agentConfig{Name: agentClaudeCode}
	var report agentReport
	if err := augmentClaudePluginReport(&report, agent, root, home); err != nil {
		t.Fatal(err)
	}
	if want := []string{"marketplace mine", "mods"}; !reflect.DeepEqual(report.MissingPlugin, want) {
		t.Fatalf("missing = %#v, want %#v", report.MissingPlugin, want)
	}
	if want := []string{"quiet (defaultEnabled: false)"}; !reflect.DeepEqual(report.DisabledPlugin, want) {
		t.Fatalf("disabled = %#v, want %#v", report.DisabledPlugin, want)
	}
	report.Name, report.Detected = agentClaudeCode, true
	if err := applyClaudePluginSync([]agentReport{report}, root, home); err != nil {
		t.Fatal(err)
	}

	settings := readClaudeSettingsForTest(t, home)
	if settings["theme"] != "dark" || settings["hooks"] == nil {
		t.Fatalf("unrelated settings changed: %#v", settings)
	}
	known := settings["extraKnownMarketplaces"].(map[string]interface{})
	if known["team"] == nil {
		t.Fatalf("unrelated marketplace dropped: %#v", known)
	}
	source := known["mine"].(map[string]interface{})["source"].(map[string]interface{})
	if source["source"] != "directory" || source["path"] != root {
		t.Fatalf("marketplace source = %#v", source)
	}
	enabled := settings["enabledPlugins"].(map[string]interface{})
	want := map[string]interface{}{"tool@team": true, "mods@mine": true}
	if !reflect.DeepEqual(enabled, want) {
		t.Fatalf("enabledPlugins = %#v, want %#v", enabled, want)
	}

	var synced agentReport
	if err := augmentClaudePluginReport(&synced, agent, root, home); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(synced.ManagedPlugin, []string{"mods"}) || len(synced.MissingPlugin)+len(synced.StalePlugin) != 0 {
		t.Fatalf("unexpected synced report: %#v", synced)
	}
}

func TestClaudePluginSyncKeepsUserDisableAndPrunesStale(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeClaudePluginFixture(t, root, `{"name":"mine","owner":{"name":"me"},"plugins":[{"name":"a","source":"./plugins/a"},{"name":"b","source":"./plugins/b"}]}`,
		map[string]string{"a": `{"name":"a"}`, "b": `{"name":"b"}`})
	settingsPath := claudeSettingsPath(home)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := map[string]interface{}{
		"extraKnownMarketplaces": map[string]interface{}{
			"mine":    map[string]interface{}{"source": map[string]interface{}{"source": "directory", "path": root}},
			"renamed": map[string]interface{}{"source": map[string]interface{}{"source": "directory", "path": root}},
		},
		"enabledPlugins": map[string]interface{}{
			"a@mine":    false,
			"gone@mine": true,
			"x@renamed": true,
			"keep@team": false,
		},
	}
	if err := writeJSONConfig(settingsPath, settings); err != nil {
		t.Fatal(err)
	}

	agent := agentConfig{Name: agentClaudeCode}
	var report agentReport
	if err := augmentClaudePluginReport(&report, agent, root, home); err != nil {
		t.Fatal(err)
	}
	if want := []string{"gone@mine", "marketplace renamed"}; !reflect.DeepEqual(sortedCopy(report.StalePlugin), want) {
		t.Fatalf("stale = %#v, want %#v", report.StalePlugin, want)
	}
	if !reflect.DeepEqual(report.DisabledPlugin, []string{"a (disabled in settings)"}) {
		t.Fatalf("disabled = %#v", report.DisabledPlugin)
	}
	if isReportSynced(report) {
		t.Fatal("report with stale plugins must not be synced")
	}
	if err := syncClaudePlugins(root, home); err != nil {
		t.Fatal(err)
	}

	got := readClaudeSettingsForTest(t, home)
	if _, ok := got["extraKnownMarketplaces"].(map[string]interface{})["renamed"]; ok {
		t.Fatal("renamed marketplace pointing at the config root should be removed")
	}
	want := map[string]interface{}{"a@mine": false, "b@mine": true, "keep@team": false}
	if !reflect.DeepEqual(got["enabledPlugins"], want) {
		t.Fatalf("enabledPlugins = %#v, want %#v", got["enabledPlugins"], want)
	}
}

func TestClaudePluginSurfaceOffWithoutMarketplace(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	var report agentReport
	if err := augmentClaudePluginReport(&report, agentConfig{Name: agentClaudeCode}, root, home); err != nil {
		t.Fatal(err)
	}
	if len(report.MissingPlugin)+len(report.ManagedPlugin) != 0 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if err := syncClaudePlugins(root, home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(claudeSettingsPath(home)); !os.IsNotExist(err) {
		t.Fatalf("settings.json must not be created without a marketplace: %v", err)
	}
	if got := checkClaudePlugins(root); got.status != checkStatusPass {
		t.Fatalf("doctor = %#v", got)
	}
}

func TestClaudePluginSurfaceOnlyForClaude(t *testing.T) {
	root := t.TempDir()
	writeClaudePluginFixture(t, root, `{"name":"mine","owner":{"name":"me"},"plugins":[{"name":"a","source":"./plugins/a"}]}`,
		map[string]string{"a": `{"name":"a"}`})
	var report agentReport
	if err := augmentClaudePluginReport(&report, agentConfig{Name: agentCodex}, root, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if len(report.MissingPlugin) != 0 {
		t.Fatalf("codex must not get Claude plugins: %#v", report)
	}
}

func TestCheckClaudePluginsUsesClaudeValidator(t *testing.T) {
	root := t.TempDir()
	writeClaudePluginFixture(t, root, `{"name":"mine","owner":{"name":"me"},"plugins":[{"name":"a","source":"./plugins/a"}]}`,
		map[string]string{"a": `{"name":"a"}`})
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	original := claudePluginValidate
	t.Cleanup(func() { claudePluginValidate = original })

	var gotRoot string
	claudePluginValidate = func(_ string, dir string) ([]byte, error) {
		gotRoot = dir
		return []byte("√ Validation passed with warnings\n"), nil
	}
	if got := checkClaudePlugins(root); got.status != checkStatusPass || !strings.Contains(got.detail, "with warnings") || gotRoot != root {
		t.Fatalf("doctor = %#v (root %q)", got, gotRoot)
	}

	claudePluginValidate = func(string, string) ([]byte, error) {
		return []byte("✘ Found 1 error\nx Validation failed\n"), errors.New("exit status 1")
	}
	if got := checkClaudePlugins(root); got.status != checkStatusFail || !strings.Contains(got.detail, "Validation failed") {
		t.Fatalf("doctor = %#v", got)
	}
}

func sortedCopy(items []string) []string {
	out := append([]string{}, items...)
	sort.Strings(out)
	return out
}
