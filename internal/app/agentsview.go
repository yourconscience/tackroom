package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const agentsViewBinary = "agentsview"

var agentsViewLookPath = exec.LookPath

const agentsViewInstallHint = `AgentsView not found on PATH.

tackroom sessions launches AgentsView as an optional session search, replay,
telemetry, and usage dashboard. AgentsView remains independently installed and
owns its own local index.

Install it from https://github.com/kenn-io/agentsview, then re-run: tackroom sessions`

type sessionsOptions struct {
	NoOpen  bool
	SSHHost string
}

func parseSessionsArgs(args []string) (sessionsOptions, []string, error) {
	var opts sessionsOptions
	var passthrough []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--no-open":
			opts.NoOpen = true
		case arg == "--open":
			opts.NoOpen = false
		case arg == "--ssh-host":
			if i+1 >= len(args) {
				return sessionsOptions{}, nil, errors.New("--ssh-host requires a value (e.g. --ssh-host user@host)")
			}
			opts.SSHHost = args[i+1]
			i++
		case strings.HasPrefix(arg, "--ssh-host="):
			opts.SSHHost = strings.TrimPrefix(arg, "--ssh-host=")
		default:
			passthrough = append(passthrough, arg)
		}
	}
	return opts, passthrough, nil
}

func agentsViewServeArgs(opts sessionsOptions, passthrough []string, remote bool) []string {
	args := []string{"serve"}
	if (opts.NoOpen || remote) && !containsArg(passthrough, "--no-browser") {
		args = append(args, "--no-browser")
	}
	return append(args, passthrough...)
}

func containsArg(args []string, target string) bool {
	for _, arg := range args {
		if arg == target {
			return true
		}
	}
	return false
}

func agentsViewPort(args []string) string {
	for i := range args {
		switch {
		case args[i] == "--port" && i+1 < len(args):
			return args[i+1]
		case strings.HasPrefix(args[i], "--port="):
			return strings.TrimPrefix(args[i], "--port=")
		}
	}
	return "8080"
}

func announceSessionsRemote(port string, sshHost string) {
	url := "http://127.0.0.1:" + port
	fmt.Fprintln(os.Stdout, "Launching AgentsView on the remote host.")
	fmt.Fprintf(os.Stdout, "  %s\n", url)
	if tunnel, ok := tunnelCommand(url, sshHost); ok {
		fmt.Fprintf(os.Stdout, "  from your machine:  %s\n", tunnel)
		fmt.Fprintln(os.Stdout, "  then open the URL above locally.")
	} else {
		fmt.Fprintln(os.Stdout, "  add --ssh-host user@host for a ready tunnel command.")
	}
	fmt.Fprintln(os.Stdout)
}

func runSessions(args []string) error {
	opts, passthrough, err := parseSessionsArgs(args)
	if err != nil {
		return err
	}
	path, err := agentsViewLookPath(agentsViewBinary)
	if err != nil {
		return errors.New(agentsViewInstallHint)
	}

	remote := os.Getenv("SSH_CONNECTION") != "" || opts.SSHHost != ""
	if !isHelpRequest(passthrough) {
		if remote {
			announceSessionsRemote(agentsViewPort(passthrough), resolveSSHHost(opts.SSHHost, os.Getenv))
		} else {
			fmt.Fprintln(os.Stdout, "Launching AgentsView for session search, replay, telemetry, and usage.")
		}
	}

	cmd := exec.Command(path, agentsViewServeArgs(opts, passthrough, remote)...) // nosemgrep: go.lang.security.audit.dangerous-exec-command
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func isHelpRequest(args []string) bool {
	return containsArg(args, "--help") || containsArg(args, "-h")
}
