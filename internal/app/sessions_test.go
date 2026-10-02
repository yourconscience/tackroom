package app

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseSessionsArgs(t *testing.T) {
	opts, passthrough, err := parseSessionsArgs([]string{"--no-open", "--ssh-host=m4", "--port", "9090", "--no-sync"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.NoOpen || opts.SSHHost != "m4" {
		t.Fatalf("parseSessionsArgs options = %+v", opts)
	}
	want := []string{"--port", "9090", "--no-sync"}
	if !reflect.DeepEqual(passthrough, want) {
		t.Fatalf("parseSessionsArgs passthrough = %v, want %v", passthrough, want)
	}
}

func TestParseSessionsArgsRequiresSSHHost(t *testing.T) {
	if _, _, err := parseSessionsArgs([]string{"--ssh-host"}); err == nil {
		t.Fatal("expected missing --ssh-host value to fail")
	}
}

func TestAgentsViewServeArgs(t *testing.T) {
	tests := []struct {
		name        string
		opts        sessionsOptions
		passthrough []string
		remote      bool
		want        []string
	}{
		{name: "local defaults", want: []string{"serve"}},
		{name: "no open", opts: sessionsOptions{NoOpen: true}, want: []string{"serve", "--no-browser"}},
		{name: "remote", remote: true, passthrough: []string{"--port", "9090"}, want: []string{"serve", "--no-browser", "--port", "9090"}},
		{name: "existing no browser", remote: true, passthrough: []string{"--no-browser"}, want: []string{"serve", "--no-browser"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentsViewServeArgs(tt.opts, tt.passthrough, tt.remote)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("agentsViewServeArgs = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAgentsViewPort(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{want: "8080"},
		{args: []string{"--port", "9090"}, want: "9090"},
		{args: []string{"--port=7070"}, want: "7070"},
	} {
		if got := agentsViewPort(tt.args); got != tt.want {
			t.Fatalf("agentsViewPort(%v) = %q, want %q", tt.args, got, tt.want)
		}
	}
}

func TestRunSessionsMissingBinary(t *testing.T) {
	orig := agentsViewLookPath
	t.Cleanup(func() { agentsViewLookPath = orig })
	agentsViewLookPath = func(string) (string, error) { return "", errors.New("not found") }

	err := runSessions(nil)
	if err == nil {
		t.Fatal("expected error when agentsview binary is missing")
	}
	if !strings.Contains(err.Error(), "AgentsView") || !strings.Contains(err.Error(), "github.com/kenn-io/agentsview") {
		t.Fatalf("error should guide install, got: %v", err)
	}
	if !strings.Contains(err.Error(), "tackroom sessions") {
		t.Fatalf("install hint should point at the sessions command, got: %v", err)
	}
}

func TestRunSessionsHelpSkipsRemoteBanner(t *testing.T) {
	orig := agentsViewLookPath
	t.Cleanup(func() { agentsViewLookPath = orig })
	agentsViewLookPath = func(string) (string, error) { return "/bin/echo", nil }

	stdout, stderr, err := captureCLIOutput(t, func() error {
		return runSessions([]string{"--help"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, "Launching AgentsView on the remote host.") {
		t.Fatalf("help should not trigger remote banner:\n%s", stdout)
	}
	if strings.Contains(stdout, "Launching AgentsView for session") {
		t.Fatalf("help should not print the launch banner:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("help wrote stderr: %q", stderr)
	}
}
