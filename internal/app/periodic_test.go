package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// No test may install into or read the real scheduler.
func TestMain(m *testing.M) {
	installPeriodicSync = func(cronOptions) error { return nil }
	readPeriodicSync = func() (*cronJob, bool) { return nil, false }
	os.Exit(m.Run())
}

func TestBuiltinJobRunsTheBinaryDirectlyEvery15MinutesWithThePath(t *testing.T) {
	t.Setenv("PATH", "/opt/agents/bin:/usr/bin")
	job := cronJobForOptions("/usr/local/bin/tackroom", cronOptions{runOptions: runOptions{ConfigPath: "/home/u/.agents/tackroom.yaml"}})
	want := []string{"/usr/local/bin/tackroom", "pull", "--config", "/home/u/.agents/tackroom.yaml"}
	if strings.Join(job.Command, "|") != strings.Join(want, "|") || job.Schedule != "*/15 * * * *" {
		t.Fatalf("job = %+v", job)
	}
	for backend, wants := range map[string][]string{
		cronBackendLaunchd: {"<string>/usr/local/bin/tackroom</string>", "<string>pull</string>", "<key>EnvironmentVariables</key>", "<string>/opt/agents/bin:/usr/bin</string>", "<integer>45</integer>"},
		cronBackendSystemd: {"OnCalendar=*-*-* *:00,15,30,45:00", `Environment="PATH=/opt/agents/bin:/usr/bin"`, `ExecStart="/usr/local/bin/tackroom" "pull"`},
		cronBackendCrontab: {"*/15 * * * * PATH='/opt/agents/bin:/usr/bin' '/usr/local/bin/tackroom' 'pull'"},
	} {
		unit, err := renderOwnedCron(job, backend, "/home/u")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(unit, "/bin/sh") || strings.Contains(unit, ".profile") {
			t.Fatalf("%s unit must not use a shell:\n%s", backend, unit)
		}
		for _, want := range wants {
			if !strings.Contains(unit, want) {
				t.Fatalf("%s unit lacks %q:\n%s", backend, want, unit)
			}
		}
	}
}

func TestPullSyncsWhenTheGitStepFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("PATH", fakePath(t, "claude")+string(os.PathListSeparator)+os.Getenv("PATH"))
	repoRoot := filepath.Join(home, ".agents") // not a git repository
	writeMinimalConfig(t, filepath.Join(repoRoot, "tackroom.yaml"), "claude")
	writeSyncTestFile(t, filepath.Join(repoRoot, "AGENTS.md"), []byte("# Shared\n"))

	// Capture stderr, where the one warning line goes.
	orig := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	err := runPull(runOptions{ConfigPath: filepath.Join(repoRoot, "tackroom.yaml"), Stdout: &bytes.Buffer{}})
	w.Close()
	os.Stderr = orig
	var got bytes.Buffer
	got.ReadFrom(r)

	if err != nil {
		t.Fatalf("pull should exit 0 when only git failed: %v", err)
	}
	if strings.Count(got.String(), "\n") != 1 || !strings.HasPrefix(got.String(), "warning: git pull --ff-only failed") {
		t.Fatalf("want one warning line, got %q", got.String())
	}
	if target, err := os.Readlink(filepath.Join(home, ".claude", "CLAUDE.md")); err != nil || target != filepath.Join(repoRoot, "AGENTS.md") {
		t.Fatalf("the sync should still have run: %q %v", target, err)
	}
}

func fixtureProbe(plist, launchctl, disabled string) []probeSection {
	const path = "/home/u/Library/LaunchAgents/dev.tackroom.tackroom-pull.plist"
	sections := []probeSection{{Kind: "os", Body: "Darwin"}, {Kind: "launchd-disabled", Body: disabled}, {Kind: "crontab", RC: 1, Body: "crontab: no crontab for u"}}
	if plist != "" {
		sections = append(sections, probeSection{Kind: "plist", Key: path, Body: plist}, probeSection{Kind: "launchctl", Key: path, Body: launchctl})
	}
	return sections
}

func TestPeriodicSyncWarningByState(t *testing.T) {
	const plist = `{"Label":"dev.tackroom.tackroom-pull","ProgramArguments":["/usr/local/bin/tackroom","pull"],"StartCalendarInterval":[{"Minute":0},{"Minute":15}]}`
	const loaded = "\tstate = not running\n\truns = 3\n\tlast exit code = "
	for _, tc := range []struct {
		name     string
		sections []probeSection
		want     string // substring of the warning; "" means no warning
	}{
		{"missing", fixtureProbe("", "", ""), "periodic sync is off, so edited root instruction files reach Codex, Pi and OMP only on a manual `tackroom sync`. Fix: tackroom cron"},
		{"paused", fixtureProbe(plist, loaded+"0", `"dev.tackroom.tackroom-pull" => disabled`), "paused or disabled. Fix: tackroom cron resume tackroom-pull"},
		{"failed", fixtureProbe(plist, loaded+"1", ""), "last run failed (exit 1). Fix: run tackroom pull"},
		{"healthy", fixtureProbe(plist, loaded+"0", ""), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job, ok := periodicSyncFromProbe(tc.sections)
			if !ok {
				t.Fatal("fixture state should be readable")
			}
			readPeriodicSync = func() (*cronJob, bool) { return job, true }
			defer func() { readPeriodicSync = func() (*cronJob, bool) { return nil, false } }()
			got, check := periodicSyncProblem(), checkPeriodicSync()
			if tc.want == "" {
				if got != "" || check.status != checkStatusPass {
					t.Fatalf("healthy job warned: %q %+v", got, check)
				}
				return
			}
			if !strings.Contains(got, tc.want) || check.status != checkStatusWarn || check.name != "periodic sync" || check.detail != got {
				t.Fatalf("problem %q, check %+v, want %q", got, check, tc.want)
			}
		})
	}
}

func TestPeriodicSyncUnreadableStateIsSilent(t *testing.T) {
	// An unreadable crontab with no launchd unit must not read as "not installed".
	sections := fixtureProbe("", "", "")
	sections[2] = probeSection{Kind: "crontab", RC: 1, Body: "crontab: permission denied"}
	if _, ok := periodicSyncFromProbe(sections); ok {
		t.Fatal("unreadable scheduler state must not be reported as a missing job")
	}
	if got := periodicSyncProblem(); got != "" {
		t.Fatalf("status must print nothing, got %q", got)
	}
}
