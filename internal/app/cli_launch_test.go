package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureCLIOutput(t *testing.T, fn func() error) (string, string, error) {
	t.Helper()

	stdoutPath := filepath.Join(t.TempDir(), "stdout")
	stderrPath := filepath.Join(t.TempDir(), "stderr")
	stdout, err := os.Create(stdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.Create(stderrPath)
	if err != nil {
		stdout.Close()
		t.Fatal(err)
	}

	oldStdout, oldStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdout, stderr
	runErr := fn()
	os.Stdout, os.Stderr = oldStdout, oldStderr
	if err := stdout.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderr.Close(); err != nil {
		t.Fatal(err)
	}

	stdoutData, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	stderrData, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(stdoutData), string(stderrData), runErr
}

func TestCanonicalCLIRoutesToCommandFamilies(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "setup", args: []string{"setup", "unexpected"}, wantErr: "setup does not accept positional arguments"},
		{name: "status", args: []string{"status", "unexpected"}, wantErr: "status does not accept positional arguments"},
		{name: "sync", args: []string{"sync", "unexpected"}, wantErr: "sync does not accept positional arguments"},
		{name: "doctor", args: []string{"doctor", "unexpected"}, wantErr: "doctor does not accept positional arguments"},
		{name: "skill", args: []string{"skill", "unexpected"}, wantErr: `unknown skill subcommand "unexpected"`},
		{name: "mcp", args: []string{"mcp", "unexpected"}, wantErr: `unknown mcp subcommand "unexpected"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := captureCLIOutput(t, func() error { return Run(tc.args) })
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("Run(%q) error = %v, want %q", tc.args, err, tc.wantErr)
			}
		})
	}
}

func TestRemovedAliasesAreUnknownCommands(t *testing.T) {
	removed := [][]string{
		{"inspect"}, {"sessions"}, {"deps", "check"}, {"skillify", "x"}, {"render"},
		{"audit"}, {"external", "list"}, {"promote", "x"}, {"dogfood"},
	}
	for _, args := range removed {
		t.Run(args[0], func(t *testing.T) {
			_, _, err := captureCLIOutput(t, func() error { return Run(args) })
			want := `unknown subcommand "` + args[0] + `"`
			if err == nil || err.Error() != want {
				t.Fatalf("Run(%q) error = %v, want %q", args, err, want)
			}
		})
	}

	nested := []struct {
		args    []string
		wantErr string
	}{
		{args: []string{"sync", "render"}, wantErr: "sync does not accept positional arguments"},
		{args: []string{"sync", "pull"}, wantErr: "sync does not accept positional arguments"},
		{args: []string{"doctor", "dogfood"}, wantErr: "doctor does not accept positional arguments"},
		{args: []string{"skill", "external", "list"}, wantErr: `unknown skill subcommand "external"`},
		{args: []string{"config"}, wantErr: "config requires a subcommand: validate or print (edit the config with `tackroom view`)"},
	}
	for _, tc := range nested {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			_, _, err := captureCLIOutput(t, func() error { return Run(tc.args) })
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("Run(%q) error = %v, want %q", tc.args, err, tc.wantErr)
			}
		})
	}
}

func TestPullStaysForCronWithoutRenameNotice(t *testing.T) {
	_, stderr, err := captureCLIOutput(t, func() error { return Run([]string{"pull", "unexpected"}) })
	if err == nil || err.Error() != "pull does not accept positional arguments" {
		t.Fatalf("pull error = %v", err)
	}
	if stderr != "" {
		t.Fatalf("pull printed a notice that cron would log every run: %q", stderr)
	}
}

func TestSkillUpdateIsCanonical(t *testing.T) {
	home := t.TempDir()
	repoRoot := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TACKROOM_HOME", repoRoot)
	writeSyncTestFile(t, filepath.Join(repoRoot, "tackroom.yaml"), []byte(`version: 1
agents:
  - name: codex
    enabled: true
    skill_root: ~/.codex/skills
external_skills:
  - url: https://github.com/example/catalog
    skill_dir: skills
    branch: main
`))

	_, canonicalStderr, canonicalErr := captureCLIOutput(t, func() error {
		return Run([]string{"skill", "update", "missing"})
	})
	const wantErr = `unknown external source "missing"; configured: catalog`
	if canonicalErr == nil || canonicalErr.Error() != wantErr {
		t.Fatalf("skill update error = %v, want %q", canonicalErr, wantErr)
	}
	if canonicalStderr != "" {
		t.Fatalf("canonical skill update printed rename notice: %q", canonicalStderr)
	}
}

func TestRootHelpAdvertisesCanonicalDescriptiveFamilies(t *testing.T) {
	stdout, stderr, err := captureCLIOutput(t, func() error {
		return Run([]string{"--help"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if stderr != "" {
		t.Fatalf("help wrote stderr: %q", stderr)
	}

	var families []string
	for _, line := range strings.Split(stdout, "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			t.Fatalf("help family lacks a description: %q", line)
		}
		families = append(families, fields[0])
	}
	if got, want := strings.Join(families, ","), "setup,status,sync,doctor,config,view,skill,publish,mcp,hook"; got != want {
		t.Fatalf("short-help families = %q, want %q:\n%s", got, want, stdout)
	}
	if !strings.Contains(stdout, `Run "tackroom help --all" for flags and maintenance commands.`) {
		t.Fatalf("short help does not direct users to the complete surface:\n%s", stdout)
	}
}
