package app

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func runDeps(args []string) error {
	if len(args) == 0 {
		return errors.New("deps requires subcommand: check, update")
	}
	switch args[0] {
	case "check":
		opts, err := parseDepsFlags("deps check", args[1:])
		if err != nil {
			return err
		}
		return runDepsCheck(opts)
	case "update":
		opts, err := parseDepsFlags("deps update", args[1:])
		if err != nil {
			return err
		}
		return runDepsUpdate(opts)
	default:
		return fmt.Errorf("unknown deps subcommand %q", args[0])
	}
}

func parseDepsFlags(name string, args []string) (runOptions, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var opts runOptions
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to tackroom YAML config")
	fs.BoolVar(&opts.SkipPackageAge, "skip-package-age", false, "Skip external package publish-age checks")
	if err := fs.Parse(args); err != nil {
		return runOptions{}, err
	}
	if fs.NArg() != 0 {
		return runOptions{}, fmt.Errorf("%s does not accept positional arguments", name)
	}
	return opts, nil
}

func runDepsCheck(opts runOptions) error {
	repoRoot, _, cfg, _, err := loadContext(opts)
	if err != nil {
		return err
	}
	result := checkExternalPackageAge(repoRoot, cfg, opts.SkipPackageAge, time.Now())
	fmt.Printf("package age: %s (%s)\n", result.status, result.detail)
	if result.status != checkStatusPass {
		return errors.New(result.detail)
	}
	return nil
}

func runDepsUpdate(opts runOptions) error {
	repoRoot, _, _, _, err := loadContext(opts)
	if err != nil {
		return err
	}
	if err := updateRepoDependencies(repoRoot); err != nil {
		return err
	}
	return runDepsCheck(opts)
}

func updateRepoDependencies(repoRoot string) error {
	if err := walkNamedFile(repoRoot, "go.mod", func(path string) error {
		dir := filepath.Dir(path)
		if err := runLoggedCommand(dir, "go", "get", "-u=patch", "./..."); err != nil {
			return err
		}
		return runLoggedCommand(dir, "go", "mod", "tidy")
	}); err != nil {
		return err
	}
	if hasFile(filepath.Join(repoRoot, "pnpm-lock.yaml")) || hasFile(filepath.Join(repoRoot, "package.json")) {
		if err := runLoggedCommand(repoRoot, "pnpm", "update"); err != nil {
			return err
		}
	}
	if hasFile(filepath.Join(repoRoot, "uv.lock")) || hasFile(filepath.Join(repoRoot, "pyproject.toml")) {
		if err := runLoggedCommand(repoRoot, "uv", "lock", "--upgrade"); err != nil {
			return err
		}
	}
	return nil
}

func walkNamedFile(root string, name string, visit func(string) error) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".worktrees", "node_modules", ".venv":
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != name {
			return nil
		}
		return visit(path)
	})
}

func runLoggedCommand(dir string, name string, args ...string) error {
	fmt.Printf("deps update: (%s) %s %s\n", dir, name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}
