package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsSyncPatchesDeclaredKeysOnly(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	home := t.TempDir()
	path := piSettingsPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := `{
  "defaultProvider": "openai-codex",
  "defaultModel": "gpt-5.5",
  "theme": "tokyonight",
  "terminal": {"images": "kitty"}
}
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	agent := agentConfig{Name: agentPi, Settings: map[string]any{
		"defaultProvider":      "deepseek",
		"defaultModel":         "deepseek-flash",
		"defaultThinkingLevel": "high",
		"terminal.images":      "kitty",
	}}
	report := agentReport{Name: agentPi, Detected: true}
	if err := augmentSettingsReport(&report, agent, home); err != nil {
		t.Fatal(err)
	}
	if len(report.ManagedSetting) != 1 || report.ManagedSetting[0] != "terminal.images" {
		t.Fatalf("managed settings = %#v", report.ManagedSetting)
	}
	if len(report.DriftedSetting) != 3 || len(report.UpdatesSetting) != 1 {
		t.Fatalf("drifted settings = %#v updates = %#v", report.DriftedSetting, report.UpdatesSetting)
	}

	if err := applyAgentSettingsSync([]agentReport{report}, []agentConfig{agent}, home); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings["defaultProvider"] != "deepseek" || settings["defaultModel"] != "deepseek-flash" || settings["defaultThinkingLevel"] != "high" {
		t.Fatalf("declared settings not written: %#v", settings)
	}
	if settings["theme"] != "tokyonight" {
		t.Fatalf("unrelated setting changed: %#v", settings)
	}
	terminal, ok := settings["terminal"].(map[string]any)
	if !ok || terminal["images"] != "kitty" {
		t.Fatalf("nested settings damaged: %#v", settings["terminal"])
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("settings mode = %v, want 0600", info.Mode().Perm())
	}

	backups, _ := filepath.Glob(filepath.Join(home, ".local", "state", "tackroom", "backups", "*", ".pi", "agent", "settings.json"))
	if len(backups) != 1 {
		t.Fatalf("expected one settings backup, found %v", backups)
	}
	if backed, _ := os.ReadFile(backups[0]); string(backed) != original {
		t.Fatalf("backup content differs from original")
	}

	after := string(data)
	// A second sync with nothing changed must not rewrite or back up again.
	report = agentReport{Name: agentPi, Detected: true}
	if err := augmentSettingsReport(&report, agent, home); err != nil {
		t.Fatal(err)
	}
	if len(report.DriftedSetting) != 0 || len(report.UpdatesSetting) != 0 {
		t.Fatalf("second pass still drifted: %#v", report)
	}
	if err := applyAgentSettingsSync([]agentReport{report}, []agentConfig{agent}, home); err != nil {
		t.Fatal(err)
	}
	if after2, _ := os.ReadFile(path); string(after2) != after {
		t.Fatalf("second sync rewrote the file")
	}
	if backups2, _ := filepath.Glob(filepath.Join(home, ".local", "state", "tackroom", "backups", "*", ".pi", "agent", "settings.json")); len(backups2) != 1 {
		t.Fatalf("second sync created another backup: %v", backups2)
	}
}

func TestSettingsSyncWritesOMPYAMLPreservingOrderAndComments(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	home := t.TempDir()
	path := ompConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("theme:\n  dark: titanium\n# keep this comment\nhideThinkingBlock: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	agent := agentConfig{Name: agentOMP, Settings: map[string]any{"hideThinkingBlock": false}}
	report := agentReport{Name: agentOMP, Detected: true}
	if err := augmentSettingsReport(&report, agent, home); err != nil {
		t.Fatal(err)
	}
	if len(report.DriftedSetting) != 1 {
		t.Fatalf("drifted settings = %#v", report.DriftedSetting)
	}
	if err := applyAgentSettingsSync([]agentReport{report}, []agentConfig{agent}, home); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if !strings.Contains(out, "theme:") || !strings.Contains(out, "dark: titanium") {
		t.Fatalf("unrelated theme setting damaged: %s", out)
	}
	if !strings.Contains(out, "# keep this comment") {
		t.Fatalf("comment lost: %s", out)
	}
	if !strings.Contains(out, "hideThinkingBlock: false") {
		t.Fatalf("declared setting not written: %s", out)
	}
	if strings.Index(out, "theme:") > strings.Index(out, "hideThinkingBlock:") {
		t.Fatalf("key order changed: %s", out)
	}
}

func TestSettingsSyncWritesClaudeJSON(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	home := t.TempDir()
	path := claudeHooksConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{\"permissions\": {\"allow\": [\"Bash\"]}, \"theme\": \"dark\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	agent := agentConfig{Name: agentClaudeCode, Settings: map[string]any{"model": "sonnet"}}
	report := agentReport{Name: agentClaudeCode, Detected: true}
	if err := augmentSettingsReport(&report, agent, home); err != nil {
		t.Fatal(err)
	}
	if len(report.DriftedSetting) != 1 {
		t.Fatalf("drifted settings = %#v", report.DriftedSetting)
	}
	if err := applyAgentSettingsSync([]agentReport{report}, []agentConfig{agent}, home); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings["model"] != "sonnet" {
		t.Fatalf("declared setting not written: %#v", settings)
	}
	if settings["theme"] != "dark" {
		t.Fatalf("unrelated setting changed: %#v", settings)
	}
	if _, ok := settings["permissions"]; !ok {
		t.Fatalf("unrelated nested setting damaged: %#v", settings)
	}
}

func TestSettingsRejectsLiteralSecret(t *testing.T) {
	cfg := config{Agents: []agentConfig{{
		Name:      agentPi,
		Enabled:   true,
		SkillRoot: t.TempDir(),
		Settings:  map[string]any{"github.token": "literal-secret"},
	}}}
	if err := validateConfig(&cfg, t.TempDir(), true); err == nil {
		t.Fatal("literal secret accepted")
	}
}

func TestSettingsRejectsManagedKey(t *testing.T) {
	cfg := config{Agents: []agentConfig{{
		Name:      agentPi,
		Enabled:   true,
		SkillRoot: t.TempDir(),
		Settings:  map[string]any{"packages": []any{"pkg"}},
	}}}
	if err := validateConfig(&cfg, t.TempDir(), true); err == nil {
		t.Fatal("tackroom-managed key accepted")
	}
}

func TestSettingsRejectsUnsupportedAgent(t *testing.T) {
	cfg := config{Agents: []agentConfig{{
		Name:      "amp",
		Enabled:   true,
		SkillRoot: t.TempDir(),
		Settings:  map[string]any{"theme": "dark"},
	}}}
	if err := validateConfig(&cfg, t.TempDir(), true); err == nil {
		t.Fatal("settings accepted for a harness without settings support")
	}
}

func TestSettingsExpandsEnvReference(t *testing.T) {
	t.Setenv("TACKROOM_TEST_TOKEN", "secret-value")
	value, err := expandSettingValue("${TACKROOM_TEST_TOKEN}")
	if err != nil || value != "secret-value" {
		t.Fatalf("expand = %v, %v", value, err)
	}
	if _, err := expandSettingValue("${TACKROOM_TEST_MISSING}"); err == nil {
		t.Fatal("missing environment variable accepted")
	}
}
