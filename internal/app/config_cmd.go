package app

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type configCommandOptions struct {
	ConfigPath string
}

type configServeOptions struct {
	ConfigPath   string
	Addr         string
	NoOpen       bool
	SecureCookie bool
	// SSHHost, when set, prints a ready-to-copy `ssh -L` tunnel command for
	// reaching the loopback-bound UI from this host instead of auto-opening a
	// browser on a remote box.
	SSHHost string
	// TokenFile, when set, holds a stable session token (created on first use)
	// so a restarted persistent `view` service keeps the same access URL.
	TokenFile string
}

func runConfigCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("config requires a subcommand: validate or print (edit the config with `tackroom view`)")
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
	default:
		return fmt.Errorf("unknown config subcommand %q: use validate or print (edit the config with `tackroom view`)", args[0])
	}
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

func yamlMarshalConfig(cfg config) ([]byte, error) {
	return yaml.Marshal(cfg)
}
