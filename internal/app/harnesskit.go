package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// hkBinary is the HarnessKit CLI that `tackroom inspect` launches as a
// cross-harness inspection surface over the materialized native harness dirs.
// The `inspect` command itself never writes. tackroom does NOT enforce
// read-only: the launched HarnessKit UI can enable/disable/deploy, and those
// writes go straight to native dirs, bypassing tackroom. The launch banner
// warns against using them on managed surfaces; reconcile any drift with
// `tackroom sync`.
const hkBinary = "hk"

// hkLookPath is indirected so tests can exercise the missing-binary path
// without depending on the host PATH.
var hkLookPath = exec.LookPath

const hkInstallHint = `HarnessKit (hk) not found on PATH.

tackroom inspect launches HarnessKit as a cross-harness inspection surface for
skills, MCP servers, hooks, and configs across every detected agent.

Install it from https://github.com/RealZST/HarnessKit, then re-run: tackroom inspect`

// inspectOptions are the tackroom-owned flags for `tackroom inspect`,
// separated from the flags forwarded verbatim to `hk serve`.
type inspectOptions struct {
	// NoOpen suppresses launching the default browser.
	NoOpen bool
	// SSHHost, when set, renders a ready-to-copy `ssh -L` tunnel command using
	// this host instead of HarnessKit's `your-server` placeholder.
	SSHHost string
}

// parseInspectArgs splits tackroom-owned flags (--no-open, --open, --ssh-host)
// from the remaining args, which are forwarded verbatim to `hk serve`.
func parseInspectArgs(args []string) (inspectOptions, []string, error) {
	var opts inspectOptions
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
				return inspectOptions{}, nil, errors.New("--ssh-host requires a value (e.g. --ssh-host user@host)")
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

// hkServeArgs builds the argv for the underlying `hk serve` invocation. Extra
// args are forwarded verbatim to hk serve (e.g. --port, --host, --no-token).
func hkServeArgs(passthrough []string) []string {
	return append([]string{"serve"}, passthrough...)
}

var serveURLRe = regexp.MustCompile(`https?://\S+`)

// extractServeURL returns the first http(s) URL on a HarnessKit banner line,
// trimming trailing punctuation. HarnessKit prints its ready URL on stderr as
// `... running at http://127.0.0.1:7070/?token=...`.
func extractServeURL(line string) (string, bool) {
	m := serveURLRe.FindString(line)
	if m == "" {
		return "", false
	}
	m = strings.TrimRight(m, ".,)]}")
	return m, true
}

// hkBannerNoise reports whether line is one of HarnessKit's startup banner
// lines that tackroom replaces with its own cleaner summary. Suppressing them
// keeps the terminal readable; any unmatched line still falls through to stderr.
func hkBannerNoise(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "Access via SSH tunnel:") || strings.HasPrefix(t, "Auth token:")
}

// announceInspect prints tackroom' clean access block once the HarnessKit URL
// is known: the bare URL on its own line, an optional tunnel command for remote
// hosts, and (locally, unless suppressed) a default-browser launch.
func announceInspect(w io.Writer, url string, opts inspectOptions, remote bool, sshHost string) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "HarnessKit inspector ready:")
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

// runInspect starts `hk serve`, watches its stderr for the ready URL, and prints
// a clean access block (bare URL, optional SSH tunnel, default-browser launch).
// The launcher itself writes nothing; the banner cautions that HarnessKit's own
// write actions bypass tackroom.
func runInspect(args []string) error {
	opts, passthrough, err := parseInspectArgs(args)
	if err != nil {
		return err
	}
	path, err := hkLookPath(hkBinary)
	if err != nil {
		return errors.New(hkInstallHint)
	}
	fmt.Fprintln(os.Stdout, "Launching HarnessKit for inspection. Note: HarnessKit can also enable/disable/deploy, and those writes bypass tackroom — avoid them on tackroom-managed skills, MCP, and hooks (reconcile drift with: tackroom sync).")

	cmd := exec.Command(path, hkServeArgs(passthrough)...) // nosemgrep: go.lang.security.audit.dangerous-exec-command
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	remote := os.Getenv("SSH_CONNECTION") != ""
	sshHost := resolveSSHHost(opts.SSHHost, os.Getenv)
	announced := false
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		if url, ok := extractServeURL(line); ok && !announced {
			announced = true
			announceInspect(os.Stdout, url, opts, remote, sshHost)
			continue // tackroom' block replaces HarnessKit's verbose URL line.
		}
		if !announced || !hkBannerNoise(line) {
			fmt.Fprintln(os.Stderr, line)
		}
	}
	return cmd.Wait()
}
