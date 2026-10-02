package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCronCommandForWeeklyDeps(t *testing.T) {
	cmd, interval := cronCommandForOptions("/usr/local/bin/tackroom", cronOptions{Deps: true, Interval: cronIntervalDefault, runOptions: runOptions{ConfigPath: "/custom/tackroom.yaml"}})
	if interval != cronIntervalWeekly {
		t.Fatalf("interval = %q, want %s", interval, cronIntervalWeekly)
	}
	if !strings.Contains(cmd, `tackroom" sync deps`) {
		t.Fatalf("cmd = %q, want sync deps", cmd)
	}
	legacy := legacyCronCommands(cmd)
	if len(legacy) != 1 || !strings.Contains(legacy[0], `tackroom" deps update --config`) {
		t.Fatalf("legacy = %q, want the pre-1.0 deps update spelling so install and --remove can clear it", legacy)
	}
	if got := legacyCronCommands(strings.Replace(cmd, "sync deps", "pull", 1)); got != nil {
		t.Fatalf("pull mode has no legacy spelling, got %q", got)
	}
	if !strings.Contains(cmd, "/usr/local/bin/tackroom") || strings.Contains(cmd, "go run") {
		t.Fatalf("cmd = %q, want installed CLI path without go run", cmd)
	}
	if strings.Contains(cmd, " pull") {
		t.Fatalf("cmd = %q, should not use pull mode", cmd)
	}
	if !strings.Contains(cmd, `--config "/custom/tackroom.yaml"`) {
		t.Fatalf("cmd = %q, want custom config preserved", cmd)
	}
}

func TestIntervalToScheduleWeekly(t *testing.T) {
	if got := intervalToSchedule(cronIntervalWeekly); got != "0 4 * * 1" {
		t.Fatalf("weekly schedule = %q, want Monday 04:00", got)
	}
}

func TestPatchHermesConfigAddsTackroomSkillDir(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".hermes", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("skills:\n  external_dirs:\n    - ~/keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	patched, err := patchHermesConfig(home, filepath.Join(home, ".agents"), config{})
	if err != nil {
		t.Fatal(err)
	}
	if !patched {
		t.Fatal("patchHermesConfig reported no changes")
	}

	out, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	skills := raw["skills"].(map[string]interface{})
	dirs := skills["external_dirs"].([]interface{})
	if !containsInterfaceString(dirs, "~/keep") || !containsInterfaceString(dirs, tackroomSkillsPathValue) {
		t.Fatalf("external_dirs = %#v", dirs)
	}
}

func TestPatchHermesConfigCreatesMissingConfig(t *testing.T) {
	home := t.TempDir()
	repoRoot := filepath.Join(home, "custom-agents")
	patched, err := patchHermesConfig(home, repoRoot, config{})
	if err != nil {
		t.Fatal(err)
	}
	if !patched {
		t.Fatal("patchHermesConfig reported no changes")
	}
	data, err := os.ReadFile(filepath.Join(home, ".hermes", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), filepath.Join(repoRoot, "skills")) {
		t.Fatalf("created Hermes config missing custom skill root:\n%s", data)
	}
}

func containsInterfaceString(items []interface{}, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
