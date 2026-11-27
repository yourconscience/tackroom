package app

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// `tackroom hosts fix` runs the fix `tackroom hosts` names for a machine, and
// `tackroom hosts setup` sets up a machine that has no config yet; both reach
// another machine over SSH as the hosts probe does.

// hostsUsage is the --help of hosts, hosts fix and hosts setup.
func hostsUsage(fs *flag.FlagSet) func() {
	return func() {
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintln(os.Stderr, "  tackroom hosts [HOST...] [--json] [--config PATH]")
		fmt.Fprintln(os.Stderr, "  tackroom hosts fix [HOST...] [--dry-run] [--config PATH]")
		fmt.Fprintln(os.Stderr, "  tackroom hosts setup HOST [--from GIT_URL] [--overlay NAME] [--no-cron] [--install] [--dry-run] [--config PATH]")
		fmt.Fprintln(os.Stderr, "\nFlags:")
		fs.PrintDefaults()
	}
}

func runHostsFix(args []string) error {
	fs := flag.NewFlagSet("hosts fix", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = hostsUsage(fs)
	configPath := fs.String("config", "", "Path to tackroom YAML config")
	dryRun := fs.Bool("dry-run", false, "Print the command each host would run and run nothing")
	hosts, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	result, err := collectHosts(*configPath, hosts)
	if err != nil {
		return err
	}
	var notFixed []string
	for _, h := range result.Hosts {
		switch {
		case h.Fix == "" && h.Health == "ok":
			fmt.Printf("%s: nothing to fix\n", h.Host)
		case h.Fix == "":
			fmt.Printf("%s: nothing to fix with a command: %s\n", h.Host, h.Summary)
		case !h.FixRunnable:
			fmt.Printf("%s: run this yourself; tackroom does not:\n  %s\n", h.Host, h.Fix)
			notFixed = append(notFixed, h.Host)
		case *dryRun:
			fmt.Printf("%s: would run %s\n", h.Host, h.Fix)
		default:
			fmt.Printf("%s: running %s\n", h.Host, h.Fix)
			runErr := runOnHost(h.Host, h.fixSteps)
			if runErr != nil {
				fmt.Fprintf(os.Stderr, "%s: the fix failed: %v\n", h.Host, runErr)
			}
			after, err := collectHosts(*configPath, []string{h.Host})
			if err != nil {
				return err
			}
			fmt.Println()
			printHostsTable(os.Stdout, after)
			// Fixed means the command worked and the host names no further
			// fix; a warning without one, such as uncommitted changes, stays.
			if runErr != nil || after.Hosts[0].Fix != "" {
				notFixed = append(notFixed, h.Host)
			}
		}
	}
	if len(notFixed) > 0 && !*dryRun {
		return fmt.Errorf("not fixed: %s", strings.Join(notFixed, ", "))
	}
	return nil
}

func runHostsSetup(args []string) error {
	fs := flag.NewFlagSet("hosts setup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = hostsUsage(fs)
	configPath := fs.String("config", "", "Path to tackroom YAML config")
	from := fs.String("from", "", "Git URL the host sets up from (default: the origin of this machine's config repo)")
	overlay := fs.String("overlay", "", "Passed to setup on the host as --overlay")
	noCron := fs.Bool("no-cron", false, "Passed to setup on the host: do not install the periodic sync job")
	install := fs.Bool("install", false, "When the host has no tackroom, install it with brew, else npm")
	dryRun := fs.Bool("dry-run", false, "Print the commands for the host and run nothing there but the read-only probe")
	positional, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New("usage: tackroom hosts setup HOST [--from GIT_URL] [--overlay NAME] [--no-cron] [--install] [--dry-run]")
	}
	host := positional[0]
	if host == "local" {
		return errors.New("hosts setup sets up another machine over SSH; on this one run tackroom setup")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path, err := resolveConfigPath(*configPath, home)
	if err != nil {
		return err
	}
	root := filepath.Dir(path)
	if *from == "" {
		out, _ := exec.Command("git", "-C", root, "remote", "get-url", "origin").Output()
		if *from = strings.TrimSpace(string(out)); *from == "" {
			return fmt.Errorf("%s has no git remote origin for %s to set up from; pass --from GIT_URL", displayPath(root, home), host)
		}
	}
	// The URL may carry a token, so what is printed has it redacted.
	setupArgv := func(bin, url string) []string {
		argv := []string{bin, "setup", "--from", url}
		if *overlay != "" {
			argv = append(argv, "--overlay", *overlay)
		}
		if *noCron {
			argv = append(argv, "--no-cron")
		}
		return argv
	}

	h := probeHost(host, "", "")
	if !h.Reachable {
		return fmt.Errorf("%s: unreachable: %s", host, h.Error)
	}
	if h.bin == "" {
		if !*install {
			return fmt.Errorf("tackroom is not installed on %s. Install it there with one of\n  brew install yourconscience/tap/tackroom\n  npm install -g tackroom\nor run this again with --install", host)
		}
		var with string
		var argv []string
		switch {
		case h.brew != "":
			with, argv = "Homebrew", []string{h.brew, "install", "yourconscience/tap/tackroom"}
		case h.npm != "":
			with, argv = "npm", []string{h.npm, "install", "-g", "tackroom"}
		default:
			return fmt.Errorf("tackroom is not installed on %s, and neither brew nor npm was found there on PATH or in ~/.local/bin, ~/go/bin, /opt/homebrew/bin or /usr/local/bin", host)
		}
		if *dryRun {
			fmt.Printf("%s: would install tackroom with %s: %s\n", host, with, shellJoin(argv))
			// Where the install puts tackroom is known only after it ran.
			fmt.Printf("%s: would run: %s\n", host, shellJoin(setupArgv("tackroom", redactURL(*from))))
			return nil
		}
		fmt.Printf("%s: installing tackroom with %s: %s\n", host, with, shellJoin(argv))
		if err := runOnHost(host, [][]string{argv}); err != nil {
			return fmt.Errorf("%s: the install failed: %w", host, err)
		}
		if h = probeHost(host, "", ""); h.bin == "" {
			return fmt.Errorf("%s: after the install: %s", host, h.Error)
		}
	}
	// Status there read a tackroom.yaml, or failed on one: set up already.
	if !strings.Contains(h.Error, "run tackroom setup") {
		msg := fmt.Sprintf("%s already has a tackroom config in %s; use tackroom hosts fix %s", host, h.ConfigRoot, host)
		if h.Error != "" {
			msg += " (its status: " + h.Error + ")"
		}
		return errors.New(msg)
	}
	shown := shellJoin(setupArgv(h.bin, redactURL(*from)))
	if *dryRun {
		fmt.Printf("%s: would run: %s\n", host, shown)
		return nil
	}
	fmt.Printf("%s: running %s\n", host, shown)
	if err := runOnHost(host, [][]string{setupArgv(h.bin, *from)}); err != nil {
		return fmt.Errorf("%s: tackroom setup failed: %w", host, err)
	}
	result, err := collectHosts(*configPath, []string{host})
	if err != nil {
		return err
	}
	fmt.Println()
	printHostsTable(os.Stdout, result)

	manifest, err := loadCronsManifest(*configPath)
	if err != nil {
		return err
	}
	if !slices.Contains(manifest.Hosts, host) {
		// tackroom.local.yaml's crons.hosts replaces the shared list, so it
		// names every host, and local when no list was set.
		hosts := manifest.Hosts
		if len(hosts) == 0 {
			hosts = []string{"local"}
		}
		seq := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
		for _, name := range append(hosts, host) {
			seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name})
		}
		var b bytes.Buffer
		enc := yaml.NewEncoder(&b)
		enc.SetIndent(2)
		if err := enc.Encode(map[string]any{"crons": map[string]any{"hosts": seq}}); err != nil {
			return err
		}
		fmt.Printf("\n%s is not in crons.hosts. For tackroom hosts and cron collect to include it, add this to %s (into its crons: block if it has one):\n\n%s",
			host, displayPath(filepath.Join(root, "tackroom.local.yaml"), home), b.String())
	}
	return nil
}

// runOnHost runs steps on host in order, each only after the one before it
// succeeded, with their output going straight to this terminal: here for
// "local", else over ssh with the options runHostProbe uses, as one command
// line quoted by shellJoin.
func runOnHost(host string, steps [][]string) error {
	if host == "local" {
		for _, argv := range steps {
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := cmd.Run(); err != nil {
				return err
			}
		}
		return nil
	}
	lines := make([]string, len(steps))
	for i, argv := range steps {
		lines[i] = shellJoin(argv)
	}
	cmd := exec.Command("ssh", append(sshOptions(host), host, strings.Join(lines, " && "))...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
