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
	if marketplace == "" {
		return
	}
	path := claudeMarketplacePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(marketplace), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeClaudeSettingsForTest(t *testing.T, home string, settings string) {
	t.Helper()
	path := claudeHooksConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readClaudeSettingsForTest(t *testing.T, home string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(claudeHooksConfigPath(home))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func claudePluginReport(t *testing.T, root string, home string) agentReport {
	t.Helper()
	report := agentReport{Name: agentClaudeCode, Detected: true}
	if err := augmentClaudePluginReport(&report, agentConfig{Name: agentClaudeCode}, root, home); err != nil {
		t.Fatal(err)
	}
	return report
}

const twoPluginMarketplace = `{"name":"mine","owner":{"name":"me"},"plugins":[{"name":"a","source":"./plugins/a"},{"name":"b","source":"./plugins/b"}]}`

var twoPlugins = map[string]string{"a": `{"name":"a"}`, "b": `{"name":"b"}`}

func TestClaudePluginSyncRegistersDirectoryMarketplace(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeClaudePluginFixture(t, root, `{
  "name": "mine",
  "owner": {"name": "me"},
  "metadata": {"pluginRoot": "./plugins"},
  "plugins": [
    {"name": "mods", "source": "./plugins/mods"},
    {"name": "bare", "source": "bare"},
    {"name": "quiet", "source": "./plugins/quiet"},
    {"name": "root", "source": "."},
    {"name": "remote", "source": {"source": "github", "repo": "o/r"}}
  ]
}`, map[string]string{
		"mods":  `{"name":"mods"}`,
		"bare":  `{"name":"bare"}`,
		"quiet": `{"name":"quiet","defaultEnabled":false}`,
	})
	writeClaudeSettingsForTest(t, home, `{
  "theme": "dark",
  "hooks": {"Stop": []},
  "extraKnownMarketplaces": {"team": {"source": {"source": "github", "repo": "org/team"}}},
  "enabledPlugins": {"tool@team": true}
}`)

	report := claudePluginReport(t, root, home)
	if want := []string{"bare", "marketplace mine", "mods"}; !reflect.DeepEqual(sortedCopy(report.MissingPlugin), want) {
		t.Fatalf("missing = %#v, want %#v", report.MissingPlugin, want)
	}
	if want := []string{"quiet (defaultEnabled: false)"}; !reflect.DeepEqual(report.DisabledPlugin, want) {
		t.Fatalf("disabled = %#v, want %#v", report.DisabledPlugin, want)
	}
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
	want := map[string]interface{}{"tool@team": true, "mods@mine": true, "bare@mine": true}
	if !reflect.DeepEqual(settings["enabledPlugins"], want) {
		t.Fatalf("enabledPlugins = %#v, want %#v", settings["enabledPlugins"], want)
	}

	synced := claudePluginReport(t, root, home)
	if !reflect.DeepEqual(synced.ManagedPlugin, []string{"bare", "mods"}) || len(synced.MissingPlugin)+len(synced.StalePlugin) != 0 {
		t.Fatalf("unexpected synced report: %#v", synced)
	}
}

func TestClaudePluginSyncKeepsUserDisableAndPrunesStale(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeClaudePluginFixture(t, root, twoPluginMarketplace, twoPlugins)
	settings := map[string]interface{}{
		"extraKnownMarketplaces": map[string]interface{}{
			"mine": map[string]interface{}{"source": map[string]interface{}{"source": "directory", "path": root}},
		},
		"enabledPlugins": map[string]interface{}{
			"a@mine":    false,
			"b@mine":    nil,
			"gone@mine": true,
			"keep@team": false,
		},
	}
	if err := writeJSONConfig(claudeHooksConfigPath(home), settings); err != nil {
		t.Fatal(err)
	}

	report := claudePluginReport(t, root, home)
	if !reflect.DeepEqual(report.StalePlugin, []string{"gone@mine"}) {
		t.Fatalf("stale = %#v", report.StalePlugin)
	}
	if !reflect.DeepEqual(report.DisabledPlugin, []string{"a (disabled in settings)"}) {
		t.Fatalf("disabled = %#v", report.DisabledPlugin)
	}
	if isReportSynced(report) {
		t.Fatal("report with stale plugins must not be synced")
	}
	if err := applyClaudePluginSync([]agentReport{report}, root, home); err != nil {
		t.Fatal(err)
	}

	want := map[string]interface{}{"a@mine": false, "b@mine": true, "keep@team": false}
	if got := readClaudeSettingsForTest(t, home)["enabledPlugins"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("enabledPlugins = %#v, want %#v", got, want)
	}
	if after := claudePluginReport(t, root, home); !isReportSynced(after) {
		t.Fatalf("not synced after sync: %#v", after)
	}
}

func TestClaudePluginRenameCarriesUserDisable(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeClaudePluginFixture(t, root, strings.Replace(twoPluginMarketplace, `"mine"`, `"mine2"`, 1), twoPlugins)
	writeClaudeSettingsForTest(t, home, `{
  "extraKnownMarketplaces": {"mine": {"source": {"source": "directory", "path": "`+root+`"}}},
  "enabledPlugins": {"a@mine": false, "b@mine": true}
}`)

	report := claudePluginReport(t, root, home)
	if want := []string{"a@mine", "b@mine", "marketplace mine"}; !reflect.DeepEqual(sortedCopy(report.StalePlugin), want) {
		t.Fatalf("stale = %#v", report.StalePlugin)
	}
	if err := applyClaudePluginSync([]agentReport{report}, root, home); err != nil {
		t.Fatal(err)
	}
	got := readClaudeSettingsForTest(t, home)
	if want := map[string]interface{}{"a@mine2": false, "b@mine2": true}; !reflect.DeepEqual(got["enabledPlugins"], want) {
		t.Fatalf("enabledPlugins = %#v, want %#v", got["enabledPlugins"], want)
	}
	known := got["extraKnownMarketplaces"].(map[string]interface{})
	if _, ok := known["mine"]; ok || known["mine2"] == nil {
		t.Fatalf("marketplaces = %#v", known)
	}
}

func TestClaudePluginSyncHonorsDeclinedRemovals(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeClaudePluginFixture(t, root, twoPluginMarketplace, twoPlugins)
	writeClaudeSettingsForTest(t, home, `{"enabledPlugins": {"gone@mine": true}}`)

	report := claudePluginReport(t, root, home)
	report.RemovesPlugin = nil // setup's confirmation was declined
	if err := applyClaudePluginSync([]agentReport{report}, root, home); err != nil {
		t.Fatal(err)
	}
	want := map[string]interface{}{"gone@mine": true, "a@mine": true, "b@mine": true}
	if got := readClaudeSettingsForTest(t, home)["enabledPlugins"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("enabledPlugins = %#v, want %#v", got, want)
	}
}

func TestClaudePluginConflicts(t *testing.T) {
	cases := map[string]struct {
		marketplace string
		settings    string
		want        string
	}{
		"same name, other source": {twoPluginMarketplace, `{"extraKnownMarketplaces": {"mine": {"source": {"source": "github", "repo": "o/mine"}}}, "enabledPlugins": {"tool@mine": true}}`, `marketplace "mine" is already registered`},
		"malformed marketplace":   {`{"name":"mine","plugins":[{"name":"a"},]}`, `{}`, "marketplace.json"},
		"enabledPlugins array":    {twoPluginMarketplace, `{"enabledPlugins": []}`, "enabledPlugins is not an object"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			home := t.TempDir()
			writeClaudePluginFixture(t, root, tc.marketplace, twoPlugins)
			writeClaudeSettingsForTest(t, home, tc.settings)
			before, _ := os.ReadFile(claudeHooksConfigPath(home))

			report := claudePluginReport(t, root, home)
			if len(report.Conflicts) != 1 || !strings.Contains(report.Conflicts[0], tc.want) {
				t.Fatalf("conflicts = %#v, want one containing %q", report.Conflicts, tc.want)
			}
			if len(report.AddsPlugin)+len(report.RemovesPlugin) != 0 {
				t.Fatalf("a conflict must plan no changes: %#v", report)
			}
			if err := syncClaudePlugins(root, home, true); err == nil {
				t.Fatal("sync must refuse a conflicting plan")
			}
			if after, _ := os.ReadFile(claudeHooksConfigPath(home)); string(after) != string(before) {
				t.Fatalf("settings changed despite conflict:\n%s", after)
			}
		})
	}
}

func TestClaudePluginRemovedMarketplaceIsCleanedUp(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeClaudeSettingsForTest(t, home, `{
  "extraKnownMarketplaces": {"mine": {"source": {"source": "directory", "path": "`+root+`"}}, "team": {"source": {"source": "github", "repo": "o/t"}}},
  "enabledPlugins": {"a@mine": true, "tool@team": true}
}`)

	report := claudePluginReport(t, root, home)
	if want := []string{"a@mine", "marketplace mine"}; !reflect.DeepEqual(sortedCopy(report.StalePlugin), want) {
		t.Fatalf("stale = %#v", report.StalePlugin)
	}
	if err := applyClaudePluginSync([]agentReport{report}, root, home); err != nil {
		t.Fatal(err)
	}
	got := readClaudeSettingsForTest(t, home)
	if want := map[string]interface{}{"tool@team": true}; !reflect.DeepEqual(got["enabledPlugins"], want) {
		t.Fatalf("enabledPlugins = %#v", got["enabledPlugins"])
	}
	if known := got["extraKnownMarketplaces"].(map[string]interface{}); known["mine"] != nil || known["team"] == nil {
		t.Fatalf("marketplaces = %#v", known)
	}
}

func TestClaudePluginDefaultDisabledEnabledByUser(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeClaudePluginFixture(t, root, `{"name":"mine","owner":{"name":"me"},"plugins":[{"name":"quiet","source":"./plugins/quiet","defaultEnabled":false}]}`,
		map[string]string{"quiet": `{"name":"quiet"}`})
	writeClaudeSettingsForTest(t, home, `{"extraKnownMarketplaces": {"mine": {"source": {"source": "directory", "path": "`+root+`"}}}, "enabledPlugins": {"quiet@mine": true}}`)

	report := claudePluginReport(t, root, home)
	if !reflect.DeepEqual(report.ManagedPlugin, []string{"quiet"}) || len(report.DisabledPlugin) != 0 || !isReportSynced(report) {
		t.Fatalf("unexpected report: %#v", report)
	}
}

func TestClaudePluginSurfaceOffWithoutMarketplace(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	report := claudePluginReport(t, root, home)
	if len(report.MissingPlugin)+len(report.ManagedPlugin)+len(report.StalePlugin) != 0 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if err := syncClaudePlugins(root, home, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(claudeHooksConfigPath(home)); !os.IsNotExist(err) {
		t.Fatalf("settings.json must not be created without a marketplace: %v", err)
	}
}

func TestClaudePluginSurfaceOnlyForClaude(t *testing.T) {
	root := t.TempDir()
	writeClaudePluginFixture(t, root, twoPluginMarketplace, twoPlugins)
	var report agentReport
	if err := augmentClaudePluginReport(&report, agentConfig{Name: agentCodex}, root, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if len(report.MissingPlugin) != 0 {
		t.Fatalf("codex must not get Claude plugins: %#v", report)
	}
}

func TestCheckClaudePlugins(t *testing.T) {
	root := t.TempDir()
	writeClaudePluginFixture(t, root, twoPluginMarketplace, twoPlugins)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	withClaude := config{Agents: []agentConfig{{Name: agentClaudeCode, Detect: "claude"}}}
	codexOnly := config{Agents: []agentConfig{{Name: agentCodex, Detect: "codex"}}}

	original := claudePluginValidate
	t.Cleanup(func() { claudePluginValidate = original })

	claudePluginValidate = func(string, string) ([]byte, error) {
		t.Fatal("validator must not run when Claude is not a detected target")
		return nil, nil
	}
	if got := checkClaudePlugins(root, codexOnly); got.status != checkStatusPass {
		t.Fatalf("doctor = %#v", got)
	}

	var gotRoot string
	claudePluginValidate = func(_ string, dir string) ([]byte, error) {
		gotRoot = dir
		return []byte("√ Validation passed with warnings\n"), nil
	}
	if got := checkClaudePlugins(root, withClaude); got.status != checkStatusPass || !strings.Contains(got.detail, "with warnings") || gotRoot != root {
		t.Fatalf("doctor = %#v (root %q)", got, gotRoot)
	}

	claudePluginValidate = func(string, string) ([]byte, error) {
		return []byte("✘ Found 1 error\nx Validation failed\n"), errors.New("exit status 1")
	}
	if got := checkClaudePlugins(root, withClaude); got.status != checkStatusFail || !strings.Contains(got.detail, "Validation failed") {
		t.Fatalf("doctor = %#v", got)
	}

	claudePluginValidate = func(string, string) ([]byte, error) {
		return nil, errors.New("signal: killed")
	}
	if got := checkClaudePlugins(root, withClaude); got.status != checkStatusFail || !strings.Contains(got.detail, "signal: killed") {
		t.Fatalf("doctor = %#v", got)
	}
}

func sortedCopy(items []string) []string {
	out := append([]string{}, items...)
	sort.Strings(out)
	return out
}

func TestClaudePluginSyncWritesThroughSymlinkedSettings(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeClaudePluginFixture(t, root, twoPluginMarketplace, twoPlugins)
	target := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(target, []byte(`{"theme":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := claudeHooksConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := syncClaudePlugins(root, home, true); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("settings symlink replaced: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("target mode = %v, %v", info.Mode().Perm(), err)
	}
	if got := readClaudeSettingsForTest(t, home); got["theme"] != "dark" || got["enabledPlugins"] == nil {
		t.Fatalf("settings = %#v", got)
	}
}
