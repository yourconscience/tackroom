package app

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type memsearchOptions struct {
	VaultDir   string
	ConfigPath string
}

func parseMemsearchFlags(args []string) (memsearchOptions, error) {
	fs := flag.NewFlagSet("memsearch", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var opts memsearchOptions
	vaultDefault := os.Getenv("KNOWLEDGE_DIR")
	if vaultDefault == "" {
		vaultDefault = "~/Workspace/knowledge"
	}
	fs.StringVar(&opts.VaultDir, "vault", vaultDefault, "Root directory for the knowledge vault (default: $KNOWLEDGE_DIR or ~/Workspace/knowledge)")
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to tackroom.yaml (default: TACKROOM_HOME/tackroom.yaml or ~/.agents/tackroom.yaml)")

	if err := fs.Parse(args); err != nil {
		return memsearchOptions{}, err
	}
	return opts, nil
}

func runMemsearch(args []string) error {
	if len(args) == 0 {
		printMemsearchUsage()
		return fmt.Errorf("missing memsearch subcommand")
	}

	switch args[0] {
	case "setup":
		opts, err := parseMemsearchFlags(args[1:])
		if err != nil {
			return err
		}
		return runMemsearchSetup(opts)
	case "status":
		return runMemsearchStatus()
	case "-h", "--help", "help":
		printMemsearchUsage()
		return nil
	default:
		printMemsearchUsage()
		return fmt.Errorf("unknown memsearch subcommand %q", args[0])
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func memsearchConfigContent(vaultDir, home string) string {
	sessionsDir := filepath.Join(vaultDir, "sessions")
	notesDir := filepath.Join(vaultDir, "notes")
	profileDir := filepath.Join(vaultDir, "profile")
	stateDir := filepath.Join(home, ".memsearch", "state")
	return fmt.Sprintf(`# memsearch configuration (written by tackroom memsearch setup)
# Source this file from memory hooks to get portable paths.
KNOWLEDGE_DIR=%s
SESSIONS_DIR=%s
NOTES_DIR=%s
PROFILE_DIR=%s
MEMSEARCH_STATE_DIR=%s
MEMSEARCH_COLLECTION='ai'
`, shellQuote(vaultDir), shellQuote(sessionsDir), shellQuote(notesDir), shellQuote(profileDir), shellQuote(stateDir))
}
func ensureMemsearchConfig(root string, home string) error {
	vaultDir := os.Getenv("KNOWLEDGE_DIR")
	if strings.TrimSpace(vaultDir) == "" {
		vaultDir = "~/Workspace/knowledge"
	}
	vaultDir = expandPath(vaultDir, home)
	for _, dir := range []string{
		filepath.Join(vaultDir, "sessions"),
		filepath.Join(vaultDir, "notes"),
		filepath.Join(vaultDir, "profile"),
		filepath.Join(home, ".memsearch", "state"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	confPath := filepath.Join(root, "memsearch.conf")
	if _, err := os.Stat(confPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", confPath, err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", root, err)
	}
	if err := os.WriteFile(confPath, []byte(memsearchConfigContent(vaultDir, home)), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", confPath, err)
	}
	return nil
}

func runMemsearchSetup(opts memsearchOptions) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	repoRoot, _, _, _, err := loadContext(runOptions{ConfigPath: opts.ConfigPath})
	if err != nil {
		return fmt.Errorf("load context: %w", err)
	}

	vaultDir := expandPath(opts.VaultDir, home)
	sessionsDir := filepath.Join(vaultDir, "sessions")
	notesDir := filepath.Join(vaultDir, "notes")
	profileDir := filepath.Join(vaultDir, "profile")
	stateDir := filepath.Join(home, ".memsearch", "state")
	confPath := filepath.Join(repoRoot, "memsearch.conf")

	fmt.Println("tackroom memsearch setup")
	fmt.Printf("vault: %s\n\n", vaultDir)

	// 1. Check memsearch is installed
	msPath, err := exec.LookPath("memsearch")
	if err != nil {
		fmt.Println("memsearch: not found on PATH")
		fmt.Println("  install: uv tool install memsearch")
		return fmt.Errorf("memsearch not installed")
	}
	fmt.Printf("memsearch: %s\n", msPath)

	// 2. Create vault directories
	for _, dir := range []string{sessionsDir, notesDir, profileDir, stateDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	fmt.Printf("vault dirs: created\n")

	// 3. Git init vault if not already
	gitDir := filepath.Join(vaultDir, ".git")
	if !hasDir(gitDir) {
		cmd := exec.Command("git", "init", vaultDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git init vault: %w", err)
		}
	} else {
		fmt.Printf("vault git: already initialized\n")
	}

	// 4. Write config
	conf := memsearchConfigContent(vaultDir, home)

	if err := os.MkdirAll(filepath.Dir(confPath), 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	if err := os.WriteFile(confPath, []byte(conf), 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	fmt.Printf("config: %s\n", confPath)

	hookSrc := filepath.Join(repoRoot, "memory", "hooks", "session-end.sh")
	if !hasFile(hookSrc) {
		fmt.Printf("memory hooks: not found at %s (skipping)\n", hookSrc)
	} else {
		fmt.Printf("memory hooks: %s\n", filepath.Dir(hookSrc))
	}

	migrated, err := migrateLegacyMemoryHookPaths(home, repoRoot)
	if err != nil {
		return fmt.Errorf("migrate legacy hook paths: %w", err)
	}
	if migrated > 0 {
		fmt.Printf("legacy hook paths: migrated %d config file(s)\n", migrated)
	}

	fmt.Printf("\ndone. Memory hooks will read config from %s\n", confPath)
	return nil
}

func migrateLegacyMemoryHookPaths(home string, roots ...string) (int, error) {
	homeAgents := filepath.Join(home, ".agents")
	replacements := map[string]string{
		"~/.agents/bin/memsearch/hook.sh session-start":                             "~/.agents/memory/hooks/session-start.sh",
		"~/.agents/bin/memsearch/hook.sh stop":                                      "~/.agents/memory/hooks/stop.sh",
		"~/.agents/bin/memsearch/hook.sh session-end":                               "~/.agents/memory/hooks/session-end.sh",
		filepath.Join(homeAgents, "bin", "memsearch", "hook.sh") + " session-start": filepath.Join(homeAgents, "memory", "hooks", "session-start.sh"),
		filepath.Join(homeAgents, "bin", "memsearch", "hook.sh") + " stop":          filepath.Join(homeAgents, "memory", "hooks", "stop.sh"),
		filepath.Join(homeAgents, "bin", "memsearch", "hook.sh") + " session-end":   filepath.Join(homeAgents, "memory", "hooks", "session-end.sh"),
		"~/.agents/bin/memsearch/finalize.sh":                                       "~/.agents/memory/hooks/session-end.sh",
		"~/.agents/bin/memsearch/hermes-finalize.sh":                                "~/.agents/memory/hooks/session-end.sh",
		"~/.agents/bin/memsearch/sync.sh":                                           "~/.agents/memory/hooks/sync.sh",
		"~/.agents/bin/memsearch/sync-memory-to-vault.sh":                           "~/.agents/memory/hooks/sync-memory-to-vault.sh",
		"~/.agents/bin/memsearch/sync-vault-to-memory.sh":                           "~/.agents/memory/hooks/sync-vault-to-memory.sh",
		filepath.Join(homeAgents, "bin", "memsearch", "finalize.sh"):                filepath.Join(homeAgents, "memory", "hooks", "session-end.sh"),
		filepath.Join(homeAgents, "bin", "memsearch", "hermes-finalize.sh"):         filepath.Join(homeAgents, "memory", "hooks", "session-end.sh"),
		filepath.Join(homeAgents, "bin", "memsearch", "sync.sh"):                    filepath.Join(homeAgents, "memory", "hooks", "sync.sh"),
		filepath.Join(homeAgents, "bin", "memsearch", "sync-memory-to-vault.sh"):    filepath.Join(homeAgents, "memory", "hooks", "sync-memory-to-vault.sh"),
		filepath.Join(homeAgents, "bin", "memsearch", "sync-vault-to-memory.sh"):    filepath.Join(homeAgents, "memory", "hooks", "sync-vault-to-memory.sh"),
	}

	paths := []string{
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".factory", "hooks.json"),
		filepath.Join(home, ".factory", "settings.json"),
		filepath.Join(home, ".hermes", "config.yaml"),
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		paths = append(paths,
			filepath.Join(root, ".claude", "settings.json"),
			filepath.Join(root, ".claude", "settings.local.json"),
			filepath.Join(root, ".factory", "hooks.json"),
			filepath.Join(root, ".factory", "settings.json"),
			filepath.Join(root, ".factory", "settings.local.json"),
		)
	}
	migrated := 0
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return migrated, fmt.Errorf("read %s: %w", path, err)
		}
		text := string(data)
		updated := text
		for old, replacement := range replacements {
			updated = strings.ReplaceAll(updated, old, replacement)
		}
		if updated == text {
			continue
		}
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			return migrated, fmt.Errorf("write %s: %w", path, err)
		}
		migrated++
	}
	return migrated, nil
}

func runMemsearchStatus() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	confPath := filepath.Join(home, ".agents", "memsearch.conf")
	fmt.Println("tackroom memsearch status")
	fmt.Println()

	// Check memsearch binary
	msPath, err := exec.LookPath("memsearch")
	if err != nil {
		fmt.Println("memsearch binary: not found")
	} else {
		fmt.Printf("memsearch binary: %s\n", msPath)
	}

	// Check config
	data, err := os.ReadFile(confPath)
	if err != nil {
		fmt.Printf("config: not found (%s)\n", confPath)
		fmt.Println("\nrun 'tackroom memsearch setup' to configure")
		return nil
	}

	fmt.Printf("config: %s\n", confPath)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fmt.Printf("  %s\n", line)
	}

	return nil
}

func printMemsearchUsage() {
	fmt.Println("tackroom memsearch - manage knowledge vault and memsearch integration")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  tackroom memsearch setup  [--vault ~/Workspace/knowledge]  Bootstrap vault + config")
	fmt.Println("  tackroom memsearch status                                  Show current configuration")
}
