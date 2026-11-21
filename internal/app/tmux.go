package app

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/term"
)

// tmuxSessionName is the name of the session `tackroom tmux` opens; a second
// run finds the session by it instead of opening another.
const tmuxSessionName = "tackroom"

// plainShellWord is the characters a printed command's word may have without
// quotes. `=` is left out because zsh expands a word that starts with it.
const plainShellWord = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+:,./-"

// shellWords is a command as a shell would take it, for --dry-run and the
// hints that tell a person what to run.
func shellWords(words []string) string {
	quoted := make([]string, len(words))
	for i, w := range words {
		if w == "" || strings.Trim(w, plainShellWord) != "" {
			w = shellQuote(w)
		}
		quoted[i] = w
	}
	return strings.Join(quoted, " ")
}

// runTmuxCommand opens a tackroom workspace in tmux: a session named
// "tackroom" in the config root, with a shell pane and a `tackroom status` pane
// side by side, and a `tackroom cron collect --table` pane across the full
// width below them, then shows that session. It is the optional tmux
// integration; an absent tmux is an error, not a silent no-op.
func runTmuxCommand(args []string) error {
	fs := flag.NewFlagSet("tmux", flag.ContinueOnError)
	config := fs.String("config", "", "Path to tackroom YAML config")
	dryRun := fs.Bool("dry-run", false, "print the tmux commands without running them")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: tackroom tmux [--config PATH] [--dry-run]")
	}

	tmux, err := exec.LookPath("tmux")
	if err != nil {
		return errors.New("tmux not found on PATH; install tmux first")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	// --config is a config file, as for every other command; the session opens
	// in its folder.
	configPath, err := resolveConfigPath(*config, home)
	if err != nil {
		return err
	}
	root := filepath.Dir(configPath)

	// The panes run this tackroom, by the path it was started as, so an older
	// tackroom earlier on the login shell's PATH cannot answer for it. The path
	// is not resolved through symlinks, so a package manager's link stays
	// valid across upgrades.
	self, err := os.Executable()
	if err != nil {
		self = "tackroom"
	}
	status := []string{self, "status"}
	crons := []string{self, "cron", "collect", "--table"}
	if *config != "" {
		status = append(status, "--config", configPath)
		crons = append(crons, "--config", configPath)
	}
	// tmux runs the arguments after the pane's flags without a shell, so
	// neither the default shell's syntax nor a space in a path matters. The
	// command gets its words as "$@", and the pane then keeps a shell open to
	// read its output.
	pane := func(command []string) []string {
		return append([]string{"sh", "-c", `"$@"; exec "${SHELL:-sh}"`, "sh"}, command...)
	}

	line := func(args []string) string {
		return shellWords(append([]string{"tmux"}, args...))
	}
	// run prints a tmux command under --dry-run, and otherwise runs it and
	// returns what it printed.
	run := func(placeholder string, args ...string) (string, error) {
		if *dryRun {
			fmt.Println(line(args))
			return placeholder, nil
		}
		out, err := exec.Command(tmux, args...).Output()
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				out = append(out, exit.Stderr...)
			}
			return "", fmt.Errorf("%s: %w\n%s", line(args), err, out)
		}
		return strings.TrimSpace(string(out)), nil
	}

	target := "=" + tmuxSessionName
	exists := false
	if *dryRun {
		_, _ = run("", "has-session", "-t", target)
		fmt.Println("# the next four commands run only when has-session fails")
	} else {
		exists = exec.Command(tmux, "has-session", "-t", target).Run() == nil
	}
	if exists {
		fmt.Println("tmux session " + tmuxSessionName + " already exists; showing it")
	} else {
		// Panes are addressed by the ids tmux prints, never by their index,
		// which a person's pane-base-index setting changes.
		shell, err := run("%shell", "new-session", "-d", "-s", tmuxSessionName, "-n", tmuxSessionName, "-c", root, "-P", "-F", "#{pane_id}")
		if err != nil {
			return err
		}
		// The crons pane splits off first, so it spans the full width; the status
		// pane then splits the shell above it.
		if _, err := run("", append([]string{"split-window", "-v", "-t", shell, "-c", root}, pane(crons)...)...); err != nil {
			return err
		}
		if _, err := run("", append([]string{"split-window", "-h", "-t", shell, "-c", root}, pane(status)...)...); err != nil {
			return err
		}
		if _, err := run("", "select-pane", "-t", shell); err != nil {
			return err
		}
		if !*dryRun {
			fmt.Println("created tmux session " + tmuxSessionName + " in " + root)
		}
	}

	switch {
	case os.Getenv("TMUX") != "":
		_, err := run("", "switch-client", "-t", target)
		return err
	case *dryRun:
		_, _ = run("", "attach-session", "-t", target)
		return nil
	case term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()):
		cmd := exec.Command(tmux, "attach-session", "-t", target)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	default:
		fmt.Println("no terminal; attach with: " + line([]string{"attach-session", "-t", target}))
		return nil
	}
}
