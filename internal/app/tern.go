package app

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// runTernCommand opens a tackroom workspace in the Tern terminal: a session
// named "tackroom" in the config root, with a shell pane above a `tackroom
// status` pane on the left, and a `tackroom cron collect --table` pane at full
// height on the right, and shows that session in the window. Tern splits only
// right and down, so here the shell is the upper pane. It is the optional Tern
// terminal integration; an absent Tern is an error, not a silent no-op.
func runTernCommand(args []string) error {
	fs := flag.NewFlagSet("tern", flag.ContinueOnError)
	config := fs.String("config", "", "Path to tackroom YAML config")
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
	// --config is a config file, as for every other command; the session opens
	// in its folder.
	configPath, err := resolveConfigPath(*config, home)
	if err != nil {
		return err
	}
	root := filepath.Dir(configPath)

	status := []string{"tackroom", "status"}
	crons := []string{"tackroom", "cron", "collect", "--table"}
	if *config != "" {
		status = append(status, "--config", configPath)
		crons = append(crons, "--config", configPath)
	}

	// Blocks are addressed by the ids tern prints, never by @focused: a new
	// session does not become the shown one, so @focused would be the pane the
	// person is working in.
	step := func(placeholder string, args ...string) (string, error) {
		cmd := append([]string{tern}, args...)
		if *dryRun {
			fmt.Println(strings.Join(cmd, " "))
			return placeholder, nil
		}
		out, err := exec.Command("tern", args...).Output()
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				out = append(out, exit.Stderr...)
			}
			return "", fmt.Errorf("%s: %w\n%s", strings.Join(cmd, " "), err, out)
		}
		var made struct {
			Block int64 `json:"block"`
		}
		if err := json.Unmarshal(out, &made); err != nil || made.Block == 0 {
			return "", fmt.Errorf("%s: no block id in output: %s", strings.Join(cmd, " "), out)
		}
		return strconv.FormatInt(made.Block, 10), nil
	}

	launch := []string{"--cwd", root, "--keep-open", "--json"}
	shell, err := step("<shell>", append([]string{"new", "session", "tackroom"}, launch...)...)
	if err != nil {
		return err
	}
	// The crons pane takes the right half at full height, because its tables
	// are the longest output; the status pane then splits the left half.
	if _, err := step("", append(append([]string{"split", shell, "right"}, launch...), append([]string{"--"}, crons...)...)...); err != nil {
		return err
	}
	if _, err := step("", append(append([]string{"split", shell, "down"}, launch...), append([]string{"--"}, status...)...)...); err != nil {
		return err
	}
	// The window shows the new session only once one of its blocks is focused.
	_, err = step("", "focus", shell, "--json")
	return err
}
