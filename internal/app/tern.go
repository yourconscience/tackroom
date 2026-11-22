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
// dash` pane, and shows that session in the window. Tern splits only right and
// down, with no size option, so here the shell is the upper pane and the two
// share the height. It is the optional Tern terminal integration; an absent
// Tern is an error, not a silent no-op.
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

	dash := []string{"tackroom", "dash"}
	if *config != "" {
		dash = append(dash, "--config", configPath)
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
	// The dash goes below the shell: Tern splits only right and down.
	if _, err := step("", append(append([]string{"split", shell, "down"}, launch...), append([]string{"--"}, dash...)...)...); err != nil {
		return err
	}
	// The window shows the new session only once one of its blocks is focused.
	_, err = step("", "focus", shell, "--json")
	return err
}
