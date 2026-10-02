package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

// openInBrowser opens url in the user's default browser. Indirected so tests
// can assert the launch without spawning a real browser. Shared with the
// config web server (config_web.go).
var openInBrowser = func(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap the short-lived launcher (open/xdg-open/start) so it does not linger
	// as a zombie for the lifetime of the foreground config server session.
	go func() { _ = cmd.Wait() }()
	return nil
}

// legacyInspectFlags are the HarnessKit-only flags that `tackroom view`
// forwarded to `hk serve` before the cutover. They no longer control `view`
// (which now opens the config UI); using one prints concise rename guidance
// pointing at HarnessKit itself instead of silently launching a browser.
var legacyInspectFlags = []string{"--port", "--host", "--no-token", "--name"}

// runView launches the canonical tackroom configuration experience: the
// loopback-only web UI for authoring the canonical YAML (shared/local layers,
// read-only effective view, revision-guarded saves, explicit sync preview and
// confirmation). It is the only interactive config editor.
func runView(args []string) error {
	if flag := firstLegacyInspectFlag(args); flag != "" {
		return fmt.Errorf("tackroom view no longer launches HarnessKit, so %s is not a view flag; run HarnessKit directly (\"hk serve --port N\") for the harness inspector. \"tackroom view\" now opens the config UI — set its loopback bind with --addr", flag)
	}
	opts, err := parseViewFlags(args)
	if err != nil {
		return err
	}
	return runConfigServe(opts)
}

// firstLegacyInspectFlag returns the first HarnessKit-era flag in args, matching
// both `--flag` and `--flag=value` forms, or "" when none is present.
func firstLegacyInspectFlag(args []string) string {
	for _, arg := range args {
		for _, legacy := range legacyInspectFlags {
			if arg == legacy || strings.HasPrefix(arg, legacy+"=") {
				return legacy
			}
		}
	}
	return ""
}

// parseViewFlags parses the flags for `tackroom view`. It serves the config
// web UI, so it shares the web server's flags (--config, --addr,
// --secure-cookie, --no-open) plus --ssh-host for a remote tunnel hint.
func parseViewFlags(args []string) (configServeOptions, error) {
	fs := flag.NewFlagSet("view", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var opts configServeOptions
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to tackroom YAML config")
	fs.StringVar(&opts.Addr, "addr", "127.0.0.1:8765", "Loopback listen address")
	fs.BoolVar(&opts.NoOpen, "no-open", false, "Do not open the browser")
	fs.BoolVar(&opts.SecureCookie, "secure-cookie", false, "Mark the session cookie Secure for HTTPS loopback access")
	fs.StringVar(&opts.SSHHost, "ssh-host", "", "Print an ssh -L tunnel command for this host (user@host)")
	fs.StringVar(&opts.TokenFile, "token-file", "", "Stable session token file (created on first use); keeps the URL fixed across restarts")
	if err := fs.Parse(args); err != nil {
		return configServeOptions{}, err
	}
	if fs.NArg() != 0 {
		return configServeOptions{}, errors.New("view does not accept positional arguments")
	}
	return opts, nil
}

var urlPortRe = regexp.MustCompile(`https?://[^/]*:(\d+)`)

// portFromURL extracts the TCP port from an http(s) URL, or "" if none.
func portFromURL(url string) string {
	m := urlPortRe.FindStringSubmatch(url)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// resolveSSHHost picks the host for the tunnel hint: the explicit --ssh-host
// value wins; otherwise, inside an SSH session, it derives `user@server-ip`
// from SSH_CONNECTION so a remote `tackroom view` prints a usable tunnel.
func resolveSSHHost(explicit string, env func(string) string) string {
	if explicit != "" {
		return explicit
	}
	fields := strings.Fields(env("SSH_CONNECTION"))
	if len(fields) < 3 {
		return ""
	}
	serverIP := fields[2]
	if user := env("USER"); user != "" {
		return user + "@" + serverIP
	}
	return serverIP
}

// tunnelCommand builds the `ssh -L PORT:localhost:PORT host` command for
// reaching a loopback-bound config UI from another machine.
func tunnelCommand(url, host string) (string, bool) {
	port := portFromURL(url)
	if port == "" || host == "" {
		return "", false
	}
	return fmt.Sprintf("ssh -L %s:localhost:%s %s", port, port, host), true
}

// announceView prints the config UI access block once the server URL is known:
// the bare URL on its own line, an optional tunnel command for remote hosts,
// and (locally, unless suppressed) a default-browser launch.
func announceView(w io.Writer, url string, opts configServeOptions, remote bool, sshHost string) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "tackroom config UI ready:")
	fmt.Fprintf(w, "  %s\n", url)

	if tunnel, ok := tunnelCommand(url, sshHost); ok {
		fmt.Fprintf(w, "  from your machine:  %s\n", tunnel)
		fmt.Fprintln(w, "  then open the URL above locally.")
	} else if remote {
		fmt.Fprintln(w, "  running on a remote host; add --ssh-host user@host for a ready tunnel command.")
	}

	if opts.NoOpen || remote {
		fmt.Fprintln(w)
		return
	}
	if err := openInBrowser(url); err != nil {
		fmt.Fprintf(w, "  (could not open a browser automatically: %v)\n", err)
	} else {
		fmt.Fprintln(w, "  opening in your default browser...")
	}
	fmt.Fprintln(w)
}
