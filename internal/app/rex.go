package app

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// rexSessionLabel is the label of the session `tackroom rex` opens; a second
// run finds the session by it instead of opening another.
const rexSessionLabel = "tackroom"

// runRexCommand opens a tackroom workspace in the Rex terminal: a session
// labelled "tackroom" in the config root, with a shell pane, a `tackroom
// status` pane on the right, and a `tackroom cron collect --table` pane below
// it, then asks the Rex app to show that session. It is the optional Rex
// terminal integration; an absent Rex is an error, not a silent no-op.
func runRexCommand(args []string) error {
	fs := flag.NewFlagSet("rex", flag.ContinueOnError)
	config := fs.String("config", "", "Path to tackroom YAML config")
	dryRun := fs.Bool("dry-run", false, "print the rex commands without running them")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: tackroom rex [--config PATH] [--dry-run]")
	}

	rex, err := exec.LookPath("rex")
	if err != nil {
		return fmt.Errorf("rex not found on PATH; install the Rex terminal first")
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

	// run prints a rex command under --dry-run, and otherwise runs it and
	// returns what it printed.
	run := func(args ...string) ([]byte, error) {
		cmd := append([]string{rex}, args...)
		if *dryRun {
			fmt.Println(strings.Join(cmd, " "))
			return nil, nil
		}
		out, err := exec.Command(rex, args...).Output()
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				out = append(out, exit.Stderr...)
			}
			return nil, fmt.Errorf("%s: %w\n%s", strings.Join(cmd, " "), err, out)
		}
		return out, nil
	}
	// Showing a session is an action of the Rex app, which it takes only while
	// it is open with Remote Control turned on. A refusal becomes one line for
	// the person, with Rex's own reason, not an error.
	show := func(session string) string {
		_, err := run("do", "session.select", "session_id="+session)
		if err == nil {
			return ""
		}
		reason := ""
		for _, line := range strings.Split(err.Error(), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				reason = strings.TrimPrefix(line, "Error: ")
			}
		}
		return "Open it from Rex's sidebar; turning on Remote Control in Rex's settings lets tackroom switch to it (rex: " + reason + ")."
	}

	listed, err := run("ls", "--json")
	if err != nil {
		return err
	}
	if !*dryRun {
		var sessions struct {
			Sessions []struct {
				ID    string `json:"session_id"`
				Label string `json:"label"`
			} `json:"sessions"`
		}
		if err := json.Unmarshal(listed, &sessions); err != nil {
			return fmt.Errorf("rex ls --json: unexpected output: %s", listed)
		}
		for _, s := range sessions.Sessions {
			if s.Label != rexSessionLabel {
				continue
			}
			if hint := show(s.ID); hint != "" {
				fmt.Println("A Rex session labelled tackroom already exists. " + hint)
			} else {
				fmt.Println("A Rex session labelled tackroom already exists; showing it.")
			}
			return nil
		}
	}

	// Blocks are addressed by the ids rex prints, never by the focused one: a
	// new session is not the one the app shows, so the focused block would be
	// the pane the person is working in.
	session, shell := "<session>", "<shell>"
	created, err := run("new", rexSessionLabel, "-c", root, "--window", rexSessionLabel, "--json")
	if err != nil {
		return err
	}
	if !*dryRun {
		var made struct {
			SessionID      string `json:"session_id"`
			InitialWindows []struct {
				BlockIDs []string `json:"block_ids"`
			} `json:"initial_windows"`
		}
		if err := json.Unmarshal(created, &made); err != nil || made.SessionID == "" || len(made.InitialWindows) == 0 || len(made.InitialWindows[0].BlockIDs) == 0 {
			return fmt.Errorf("rex new: no session or block id in output: %s", created)
		}
		session, shell = made.SessionID, made.InitialWindows[0].BlockIDs[0]
	}
	split := func(placeholder, anchor, direction string, command []string) (string, error) {
		out, err := run(append([]string{"split", "-s", session, "-b", anchor, "--split", direction, "-c", root, "--keep-open", "--focus=false", "--json", "--"}, command...)...)
		if err != nil || *dryRun {
			return placeholder, err
		}
		var made struct {
			BlockIDs []string `json:"block_ids"`
		}
		if err := json.Unmarshal(out, &made); err != nil || len(made.BlockIDs) == 0 || made.BlockIDs[0] == "" {
			return "", fmt.Errorf("rex split: no block id in output: %s", out)
		}
		return made.BlockIDs[0], nil
	}
	statusBlock, err := split("<status>", shell, "right", status)
	if err != nil {
		return err
	}
	if _, err := split("", statusBlock, "below", crons); err != nil {
		return err
	}
	if hint := show(session); hint != "" {
		fmt.Println("Opened a Rex session labelled tackroom. " + hint)
	}
	return nil
}
