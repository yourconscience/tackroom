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

func TestHiddenAliasesRouteWithOneRenameNotice(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantErr     string
		containsErr string
		notice      string
	}{
		{name: "pull", args: []string{"pull", "unexpected"}, wantErr: "pull does not accept positional arguments", notice: `tackroom: "pull" was renamed to "sync --pull"`},
		{name: "deps", args: []string{"deps", "unexpected"}, wantErr: `unknown deps subcommand "unexpected"`, notice: `tackroom: "deps" was renamed to "doctor deps or sync deps"`},
		{name: "memsearch", args: []string{"memsearch", "unexpected"}, wantErr: `unknown memsearch subcommand "unexpected"`, notice: `tackroom: "memsearch" was renamed to "setup memsearch or status memsearch"`},
		{name: "skillify", args: []string{"skillify"}, wantErr: "skillify requires a skill name: tackroom skillify <name>", notice: `tackroom: "skillify" was renamed to "skill new"`},
		{name: "render", args: []string{"render", "unexpected"}, wantErr: "render does not accept positional arguments", notice: `tackroom: "render" was renamed to "sync"`},
		{name: "audit", args: []string{"audit", "unexpected"}, wantErr: "audit does not accept positional arguments", notice: `tackroom: "audit" was renamed to "doctor"`},
		{name: "external", args: []string{"external"}, wantErr: "usage: tackroom external <list|update> [name ...]", notice: `tackroom: "external" was renamed to "status or skill update"`},
		{name: "promote", args: []string{"promote"}, wantErr: "promote requires a skill name or path: tackroom promote <name-or-path>", notice: `tackroom: "promote" was renamed to "skill promote"`},
		{name: "dogfood", args: []string{"dogfood", "unexpected"}, wantErr: "dogfood does not accept positional arguments", notice: `tackroom: "dogfood" was renamed to "doctor --e2e"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, err := captureCLIOutput(t, func() error { return Run(tc.args) })
			if err == nil {
				t.Fatalf("Run(%q) unexpectedly succeeded", tc.args)
			}
			if tc.wantErr != "" && err.Error() != tc.wantErr {
				t.Fatalf("Run(%q) error = %q, want %q", tc.args, err, tc.wantErr)
			}
			if tc.containsErr != "" && !strings.Contains(err.Error(), tc.containsErr) {
				t.Fatalf("Run(%q) error = %q, want substring %q", tc.args, err, tc.containsErr)
			}
			if strings.Count(stderr, "tackroom: ") != 1 || !strings.Contains(stderr, tc.notice+"\n") {
				t.Fatalf("Run(%q) rename output = %q, want exactly one %q notice", tc.args, stderr, tc.notice)
			}
		})
	}
}

func TestSkillUpdateIsCanonicalAndExternalUpdateRemainsCompatible(t *testing.T) {
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

	_, legacyStderr, legacyErr := captureCLIOutput(t, func() error {
		return Run([]string{"external", "update", "missing"})
	})
	if legacyErr == nil || legacyErr.Error() != wantErr {
		t.Fatalf("external update error = %v, want routed error %q", legacyErr, wantErr)
	}
	const wantNotice = "tackroom: \"external update\" was renamed to \"skill update\"\n"
	if legacyStderr != wantNotice {
		t.Fatalf("external update rename notice = %q, want %q", legacyStderr, wantNotice)
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
	if got, want := strings.Join(families, ","), "setup,status,sync,doctor,config,view,inspect,sessions,skill,publish,mcp,hook"; got != want {
		t.Fatalf("short-help families = %q, want %q:\n%s", got, want, stdout)
	}
	if !strings.Contains(stdout, `Run "tackroom help --all" for flags, maintenance commands, and compatibility aliases.`) {
		t.Fatalf("short help does not direct users to the complete surface:\n%s", stdout)
	}
}
