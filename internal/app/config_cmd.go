package app

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type configCommandOptions struct {
	ConfigPath string
}

type configServeOptions struct {
	ConfigPath string
	Addr       string
	// AddrSet records an explicit --addr. Without it, view first looks for a
	// running view at the address the last one recorded.
	AddrSet      bool
	NoOpen       bool
	SecureCookie bool
	// SSHHost, when set, prints a ready-to-copy `ssh -L` tunnel command for
	// reaching the loopback-bound UI from this host instead of auto-opening a
	// browser on a remote box.
	SSHHost string
	// TokenFile holds a stable session token (created on first use) so a
	// restarted `view` keeps the same access URL. Empty means view.token in
	// the tackroom state directory.
	TokenFile string
}

func runConfigCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("config requires a subcommand: validate, print, inventory or set (or edit the config with `tackroom view`)")
	}
	if isHelpArg(args[0]) {
		printGroupUsage("config")
		return nil
	}
	switch args[0] {
	case "validate":
		opts, err := parseConfigFlags(args[1:])
		if err != nil {
			return err
		}
		return runConfigValidate(opts)
	case "print":
		opts, err := parseConfigFlags(args[1:])
		if err != nil {
			return err
		}
		return runConfigPrint(opts)
	case "inventory":
		opts, err := parseConfigFlags(args[1:])
		if err != nil {
			return err
		}
		return runConfigInventory(opts)
	case "set":
		return runConfigSet(args[1:])
	default:
		return fmt.Errorf("unknown config subcommand %q: use validate, print, inventory or set (or edit the config with `tackroom view`)", args[0])
	}
}

// runConfigSet makes the web view's one-field edits from the command line:
// `config set mcp_servers/exa/enabled false` or `config set hooks/x/agents
// '["claude","codex"]'`. The value is JSON when it parses as JSON and a string
// otherwise. An entry defined in tackroom.local.yaml is edited there, as the
// web view does. Like the web view, it saves the config and leaves the sync.
func runConfigSet(args []string) error {
	fs := flag.NewFlagSet("config set", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var opts configCommandOptions
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to tackroom YAML config")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: tackroom config set [--config PATH] <section/name/field> <value>")
	}
	path, value := fs.Arg(0), json.RawMessage(fs.Arg(1))
	if !json.Valid(value) {
		value, _ = json.Marshal(fs.Arg(1))
	}
	doc, _, err := openConfigDocument(opts)
	if err != nil {
		return err
	}
	layer := configLayerShared
	if doc.definedLocally(path) {
		layer = configLayerLocal
	}
	saved, err := doc.applyOperations(layer, doc.revision(layer), []configOperation{{Op: "set", Path: path, Value: value}})
	if err != nil {
		return err
	}
	// Only the changed lines: the saved diff carries the whole file as context.
	for _, line := range strings.Split(saved.Diff, "\n") {
		if (strings.HasPrefix(line, "-") || strings.HasPrefix(line, "+")) && !strings.HasPrefix(line, "---") && !strings.HasPrefix(line, "+++") {
			fmt.Println(line)
		}
	}
	fmt.Printf("saved %s; run tackroom sync to apply it\n", doc.path(layer))
	return nil
}

// definedLocally reports whether the section entry a path names, such as
// agents/codex in agents/codex/enabled, is defined in the local overlay.
func (d *configDocument) definedLocally(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || d.localNode == nil {
		return false
	}
	section := mappingValue(rootMapping(d.localNode), parts[0])
	return section != nil && section.Kind == yaml.SequenceNode && findSequenceEntry(section, parts[0], parts[1]) >= 0
}

func parseConfigFlags(args []string) (configCommandOptions, error) {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var opts configCommandOptions
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to tackroom YAML config")
	if err := fs.Parse(args); err != nil {
		return configCommandOptions{}, err
	}
	if fs.NArg() != 0 {
		return configCommandOptions{}, errors.New("config does not accept positional arguments")
	}
	return opts, nil
}

func openConfigDocument(opts configCommandOptions) (*configDocument, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", fmt.Errorf("resolve home: %w", err)
	}
	path, err := configPathFor(runOptions{ConfigPath: opts.ConfigPath}, home)
	if err != nil {
		return nil, "", err
	}
	doc, err := newConfigDocument(path, home)
	if err != nil {
		return nil, path, err
	}
	return doc, path, nil
}

func runConfigValidate(opts configCommandOptions) error {
	doc, path, err := openConfigDocument(opts)
	if err != nil {
		return err
	}
	fmt.Printf("valid: %s\nrevision: %s\n", path, doc.revision(configLayerShared))
	if len(doc.localBytes) != 0 {
		fmt.Printf("local overlay: %s\n", doc.localPath)
	}
	return nil
}

func runConfigPrint(opts configCommandOptions) error {
	doc, path, err := openConfigDocument(opts)
	if err != nil {
		return err
	}
	fmt.Printf("shared: %s\n", path)
	fmt.Printf("local: %s\n", doc.localPath)
	fmt.Printf("shared revision: %s\n", doc.revision(configLayerShared))
	if len(doc.localBytes) != 0 {
		fmt.Printf("local revision: %s\n", doc.revision(configLayerLocal))
	}
	fmt.Println("effective:")
	data, err := yamlMarshalConfig(doc.effective)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

// runConfigInventory prints, as JSON, what the web view's Overview, Skills,
// Roles and MCP & hooks tabs draw: the document behind /api/inventory.
func runConfigInventory(opts configCommandOptions) error {
	doc, _, err := openConfigDocument(opts)
	if err != nil {
		return err
	}
	inv, err := buildViewInventory(doc)
	if err != nil {
		return err
	}
	return encodeJSON(os.Stdout, inv)
}

func yamlMarshalConfig(cfg config) ([]byte, error) {
	return yaml.Marshal(cfg)
}
