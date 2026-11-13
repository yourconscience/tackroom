package app

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runTernCommand opens a tackroom workspace in the Tern terminal: a session
// named "tackroom" in the config root, with a shell pane, a `tackroom status`
// pane on the right, and a `tackroom cron collect --table` pane below it. It
// is the optional OMP integration for Tern; an absent Tern is an error, not a
// silent no-op.
func runTernCommand(args []string) error {
	fs := flag.NewFlagSet("tern", flag.ContinueOnError)
	config := fs.String("config", "", "config root (default ~/.agents)")
	dryRun := fs.Bool("dry-run", false, "print the tern commands without running them")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: tackroom tern [--config PATH] [--dry-run]")
	}

	tern, err := exec.LookPath("tern")
	if err != nil {
		return fmt.Errorf("tern not found on PATH; install the Tern terminal first")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	root := *config
	if root == "" {
		root = filepath.Join(home, ".agents")
	}
	if root, err = filepath.Abs(root); err != nil {
		return err
	}

	status := []string{"tackroom", "status"}
	crons := []string{"tackroom", "cron", "collect", "--table"}
	if *config != "" {
		status = append(status, "--config", root)
		crons = append(crons, "--config", root)
	}

	cmds := [][]string{
		{tern, "new", "session", "tackroom", "--cwd", root, "--keep-open"},
		append([]string{tern, "split", "@focused", "right", "--cwd", root, "--keep-open", "--"}, status...),
		append([]string{tern, "split", "@focused", "down", "--cwd", root, "--keep-open", "--"}, crons...),
	}
	for _, cmd := range cmds {
		if *dryRun {
			fmt.Println(strings.Join(cmd, " "))
			continue
		}
		if out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %w\n%s", strings.Join(cmd, " "), err, out)
		}
	}
	return nil
}
