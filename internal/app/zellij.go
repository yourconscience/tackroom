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

// zellijSessionName is the name of the session `tackroom zellij` opens; a
// second run finds the session by it instead of opening another.
const zellijSessionName = "tackroom"

// runZellijCommand opens a tackroom workspace in zellij: a session named
// "tackroom" in the config root, with a `tackroom status` pane above a shell
// pane on the left, and a `tackroom cron collect --table` pane at full height
// on the right. Run inside zellij, it adds the workspace as a tab of the
// current session. It is the optional zellij integration; an absent zellij is
// an error, not a silent no-op.
func runZellijCommand(args []string) error {
	fs := flag.NewFlagSet("zellij", flag.ContinueOnError)
	config := fs.String("config", "", "Path to tackroom YAML config")
	dryRun := fs.Bool("dry-run", false, "print the layout and the zellij command without running anything")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: tackroom zellij [--config PATH] [--dry-run]")
	}

	zellij, err := exec.LookPath("zellij")
	if err != nil {
		return errors.New("zellij not found on PATH; install zellij first")
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
	layout := zellijLayout(root, status, crons)

	if *dryRun {
		fmt.Print(layout)
		if os.Getenv("ZELLIJ") != "" {
			fmt.Println("zellij action new-tab --layout <layout file> --name " + zellijSessionName)
		} else {
			fmt.Println("zellij list-sessions --short --no-formatting")
			fmt.Println("# when it lists a session named " + zellijSessionName + ":")
			fmt.Println("zellij attach " + zellijSessionName)
			fmt.Println("# otherwise:")
			fmt.Println("zellij --session " + zellijSessionName + " --new-session-with-layout <layout file>")
		}
		return nil
	}

	// The layout goes to a file of its own, which zellij reads at launch; it
	// is removed once zellij returns, unless the person has to run the command.
	dir := ""
	defer func() {
		if dir != "" {
			os.RemoveAll(dir)
		}
	}()
	layoutFile := func() (string, error) {
		var err error
		if dir, err = os.MkdirTemp("", "tackroom-zellij-"); err != nil {
			return "", err
		}
		file := filepath.Join(dir, "tackroom.kdl")
		return file, os.WriteFile(file, []byte(layout), 0o600)
	}
	interactive := term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
	launch := func(args ...string) error {
		cmd := exec.Command(zellij, args...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}

	// Inside zellij the workspace is a new tab of the session the person is
	// in; zellij needs no terminal of ours for that.
	if os.Getenv("ZELLIJ") != "" {
		file, err := layoutFile()
		if err != nil {
			return err
		}
		return launch("action", "new-tab", "--layout", file, "--name", zellijSessionName)
	}

	// No sessions is an error exit of list-sessions, and means no tackroom
	// session.
	listed, _ := exec.Command(zellij, "list-sessions", "--short", "--no-formatting").Output()
	exists := false
	for _, name := range strings.Split(string(listed), "\n") {
		if strings.TrimSpace(name) == zellijSessionName {
			exists = true
		}
	}
	command := []string{"zellij", "attach", zellijSessionName}
	if !exists {
		file, err := layoutFile()
		if err != nil {
			return err
		}
		command = []string{"zellij", "--session", zellijSessionName, "--new-session-with-layout", file}
	}
	// zellij draws its own screen, so it cannot create or attach to a session
	// without a terminal. The layout file stays for the printed command.
	if !interactive {
		fmt.Println("no terminal; run: " + shellWords(command))
		dir = ""
		return nil
	}
	if exists {
		fmt.Println("zellij session " + zellijSessionName + " already exists; attaching")
	}
	return launch(command[1:]...)
}

// zellijLayout is the KDL layout of the workspace: on the left the status pane
// above the shell, which has the focus, and on the right the crons pane.
func zellijLayout(root string, status, crons []string) string {
	pane := func(indent, name, size string, command []string) string {
		args := make([]string, len(command)-1)
		for i, a := range command[1:] {
			args[i] = kdlString(a)
		}
		return fmt.Sprintf("%spane name=%s%s command=%s { args %s; }\n", indent, kdlString(name), size, kdlString(command[0]), strings.Join(args, " "))
	}
	return "layout {\n" +
		"    cwd " + kdlString(root) + "\n" +
		"    pane split_direction=\"vertical\" {\n" +
		"        pane split_direction=\"horizontal\" {\n" +
		pane("            ", "status", " size=\"70%\"", status) +
		"            pane name=\"shell\" focus=true\n" +
		"        }\n" +
		pane("        ", "crons", "", crons) +
		"    }\n" +
		"}\n"
}

// kdlString is s as a quoted KDL string.
func kdlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u{%x}`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
