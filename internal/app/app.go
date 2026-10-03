package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
)

type config struct {
	Version        int                   `yaml:"version"`
	Agents         []agentConfig         `yaml:"agents"`
	MCPServers     []mcpServerConfig     `yaml:"mcp_servers"`
	ExternalSkills []externalSkillSource `yaml:"external_skills"`
	PublishTargets []publishTarget       `yaml:"publish_targets,omitempty"`
	Hooks          []hookConfig          `yaml:"hooks,omitempty"`
	UI             *uiConfig             `yaml:"ui,omitempty"`
	// ContextNoteTokens is the estimated skill-listing token threshold above
	// which `tackroom doctor` prints a soft context advisory note. Absent
	// (nil) uses contextNoteTokensDefault; 0 (or negative) disables the note.
	ContextNoteTokens *int `yaml:"context_note_tokens,omitempty"`
}

type uiConfig struct {
	Links []uiLink `yaml:"links,omitempty"`
}

type uiLink struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

type externalSkillSource struct {
	URL         string   `yaml:"url"`
	SkillDir    string   `yaml:"skill_dir,omitempty"`
	SkillDirs   []string `yaml:"skill_dirs,omitempty"`
	Branch      string   `yaml:"branch"`
	Skills      []string `yaml:"skills,omitempty"`
	Materialize bool     `yaml:"materialize,omitempty"`
	MCP         bool     `yaml:"mcp,omitempty"`
	MCPAgents   []string `yaml:"mcp_agents,omitempty"`
}

type agentConfig struct {
	Name      string    `yaml:"name"`
	Enabled   bool      `yaml:"enabled"`
	SkillRoot string    `yaml:"skill_root"`
	AgentRoot string    `yaml:"agent_root,omitempty"`
	Detect    string    `yaml:"detect,omitempty"`
	RoleModel string    `yaml:"role_model,omitempty"`
	Packages  *[]string `yaml:"packages,omitempty"`
}

// publishTarget declares a remote skill registry to push canonical skills to.
// It is a publish verb, not a sync entity: unlike agentConfig it has no local
// skill_root to reconcile and no detect key. Skills is an explicit allowlist —
// only named skills are ever uploaded, so a private or experimental skill is
// never shipped by accident.
type publishTarget struct {
	Name            string   `yaml:"name"`
	Kind            string   `yaml:"kind"`                       // registry kind; only "openai-skills" for now
	Enabled         bool     `yaml:"enabled"`                    // default off; opt in per target
	Skills          []string `yaml:"skills"`                     // allowlist of local skill dir names to publish
	VersionStrategy string   `yaml:"version_strategy,omitempty"` // new-version (default) | set-default
	APIKeyEnv       string   `yaml:"api_key_env,omitempty"`      // env var holding the key; default OPENAI_API_KEY
}

const (
	publishKindOpenAISkills   = "openai-skills"
	publishDefaultAPIKeyEnv   = "OPENAI_API_KEY"
	publishStrategyNewVersion = "new-version"
	publishStrategySetDefault = "set-default"
)

type repoLinkReport struct {
	Path           string
	ExpectedTarget string
	ActualTarget   string
	State          string
}

type agentReport struct {
	Name            string
	// Error is set when the agent's native config could not be read; the rest
	// of the report is empty and sync leaves the agent untouched.
	Error           string
	SkillRoot       string
	AgentRoot       string
	ExpectedSkills  map[string]string
	Detected        bool
	RootPath        string
	RootExpected    string
	RootActual      string
	RootState       string
	Managed         []string
	ManagedAgent    []string
	ManagedMCP      []string
	ManagedHook     []string
	ManagedPackage  []string
	Drifted         []string
	DriftedAgent    []string
	DriftedMCP      []string
	DriftedHook     []string
	DriftedPackage  []string
	Missing         []string
	MissingAgent    []string
	MissingMCP      []string
	MissingHook     []string
	UnsupportedHook []string
	Conflicts       []string
	StaleManaged    []string
	External        []string
	Adds            []string
	AddsAgent       []string
	AddsMCP         []string
	AddsHook        []string
	Updates         []string
	UpdatesAgent    []string
	UpdatesMCP      []string
	UpdatesHook     []string
	UpdatesPackage  []string
	Removes         []string
	RemovesAgent    []string
	RemovesPackage  []string
	Synced          bool
}

func isDetected(agent agentConfig) bool {
	if agent.Detect == "" {
		return true
	}
	executable, err := exec.LookPath(agent.Detect)
	if err != nil {
		return false
	}
	if harness := harnessFor(agent.Name); harness != nil && harness.Detect != nil {
		return harness.Detect(executable)
	}
	return true
}

type runOptions struct {
	ConfigPath     string
	ConfigOverride *config
	Agents         string
	Pull           bool
	E2E            bool
	SkipPackageAge bool
	MemoryTier     string
	JSONOutput     bool
	DryRun         bool
	AssumeYes      bool
	Stdin          io.Reader
	Stdout         io.Writer
	// ConfirmRemovals makes sync preview per-harness removals and role
	// overwrites and ask before applying them. Set by setup-driven syncs.
	ConfirmRemovals bool
	// Verbose expands `status` back to the full per-surface managed and
	// external skill lists and native root paths instead of the concise view.
	Verbose bool
}

func Run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return errors.New("missing subcommand")
	}

	switch args[0] {
	case "setup":
		return runSetupCommand(args[1:])
	case "status":
		return runStatusCommand(args[1:])
	case "sync":
		return runSyncCommand(args[1:])
	case "doctor":
		return runDoctorCommand(args[1:])
	case "config":
		return runConfigCommand(args[1:])
	case "view":
		return runView(args[1:])
	case "skill":
		return runSkillCommand(args[1:])
	case "publish":
		return runPublishCommand(args[1:])
	case "mcp":
		return runMCP(args[1:])
	case "hook":
		return runHookCommand(args[1:])
	case "cron":
		opts, err := parseCronFlags(args[1:])
		if err != nil {
			return err
		}
		return runCron(opts)
	case "pull":
		// Kept as the cron entrypoint; installed crontab lines call it.
		opts, err := parseSubcommandFlags("pull", args[1:])
		if err != nil {
			return err
		}
		return runPull(opts)
	case "memsearch":
		return runDeprecatedMemsearch(args[1:])
	case "version", "--version":
		if len(args) != 1 {
			return errors.New("usage: tackroom version")
		}
		fmt.Println("tackroom " + versionString())
		return nil
	case "help":
		if len(args) == 1 {
			printUsage()
			return nil
		}
		if len(args) == 2 && args[1] == "--all" {
			printAllUsage()
			return nil
		}
		return errors.New("usage: tackroom help [--all]")
	case "-h", "--help":
		if len(args) != 1 {
			return errors.New("usage: tackroom help [--all]")
		}
		printUsage()
		return nil
	default:
		printUsage()
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func runSetupCommand(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "memsearch":
			return runMemsearch(append([]string{"setup"}, args[1:]...))
		}
	}
	opts, err := parseSetupFlags(args)
	if err != nil {
		return err
	}
	return runSetup(opts)
}

func runStatusCommand(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "memsearch":
			printRenameNotice("status memsearch", "status")
			return runMemsearch(append([]string{"status"}, args[1:]...))
		}
	}
	opts, err := parseStatusFlags(args)
	if err != nil {
		return err
	}
	return runStatus(opts)
}

func parseStatusFlags(args []string) (runOptions, error) {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var opts runOptions
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to tackroom YAML config")
	fs.StringVar(&opts.Agents, "agents", "", "Comma-separated agent names to use for this run")
	fs.BoolVar(&opts.SkipPackageAge, "skip-package-age", false, "Skip external package publish-age checks")
	fs.BoolVar(&opts.Verbose, "verbose", false, "Show full managed/external skill lists and native root paths")
	fs.BoolVar(&opts.Verbose, "v", false, "Show full managed/external skill lists and native root paths")

	if err := fs.Parse(args); err != nil {
		return runOptions{}, err
	}
	if fs.NArg() != 0 {
		return runOptions{}, errors.New("status does not accept positional arguments")
	}
	return opts, nil
}

func runSyncCommand(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "deps":
			opts, err := parseDepsFlags("sync deps", args[1:])
			if err != nil {
				return err
			}
			return runDepsUpdate(opts)
		}
	}
	opts, err := parseSyncFlags(args)
	if err != nil {
		return err
	}
	if opts.Pull {
		opts.Pull = false
		return runPull(opts)
	}
	return runSync(opts)
}

func runDoctorCommand(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "audit":
			opts, err := parseSubcommandFlags("doctor audit", args[1:])
			if err != nil {
				return err
			}
			return runAudit(opts)
		case "deps":
			opts, err := parseDepsFlags("doctor deps", args[1:])
			if err != nil {
				return err
			}
			return runDepsCheck(opts)
		}
	}
	opts, err := parseDoctorFlags(args)
	if err != nil {
		return err
	}
	if opts.E2E {
		opts.E2E = false
		return runDogfood(opts)
	}
	return runDoctor(opts)
}

func runSkillCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("skill requires subcommand: new, list, info, update, promote")
	}
	switch args[0] {
	case "new":
		return runSkillify(args[1:])
	case "update":
		return runExternalUpdate(args[1:])
	case "list":
		return runSkillList(args[1:])
	case "info":
		return runSkillInfo(args[1:])
	case "promote":
		return runPromote(args[1:])
	default:
		return fmt.Errorf("unknown skill subcommand %q", args[0])
	}
}

func runDeprecatedMemsearch(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "setup":
			printRenameNotice("memsearch setup", "setup memsearch")
			return runSetupCommand(append([]string{"memsearch"}, args[1:]...))
		case "status":
			printRenameNotice("memsearch status", "status memsearch")
			return runStatusCommand(append([]string{"memsearch"}, args[1:]...))
		}
	}
	printRenameNotice("memsearch", "setup memsearch or status memsearch")
	return runMemsearch(args)
}

func printRenameNotice(oldCommand string, newCommand string) {
	fmt.Fprintf(os.Stderr, "tackroom: %q was renamed to %q\n", oldCommand, newCommand)
}

func parseSubcommandFlags(name string, args []string) (runOptions, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var opts runOptions
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to tackroom YAML config")
	fs.StringVar(&opts.Agents, "agents", "", "Comma-separated agent names to use for this run")
	fs.BoolVar(&opts.SkipPackageAge, "skip-package-age", false, "Skip external package publish-age checks")

	if err := fs.Parse(args); err != nil {
		return runOptions{}, err
	}
	if fs.NArg() != 0 {
		return runOptions{}, fmt.Errorf("%s does not accept positional arguments", name)
	}

	return opts, nil
}

func parseSetupFlags(args []string) (runOptions, error) {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var opts runOptions
	opts.MemoryTier = memoryTierBasic
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to tackroom YAML config")
	fs.StringVar(&opts.Agents, "agents", "", "Comma-separated agent names to use for this run")
	fs.StringVar(&opts.MemoryTier, "memory", memoryTierBasic, "Memory tier: off, basic, or memsearch")
	fs.BoolVar(&opts.JSONOutput, "json", false, "Emit detection result as JSON and exit")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "Show detected import candidates and exit without changes (overrides --yes)")
	fs.BoolVar(&opts.AssumeYes, "yes", false, "Import all detected items without prompting")
	if err := fs.Parse(args); err != nil {
		return runOptions{}, err
	}
	if fs.NArg() != 0 {
		return runOptions{}, errors.New("setup does not accept positional arguments")
	}
	return opts, nil
}

func parseSyncFlags(args []string) (runOptions, error) {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var opts runOptions
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to tackroom YAML config")
	fs.StringVar(&opts.Agents, "agents", "", "Comma-separated agent names to use for this run")
	fs.BoolVar(&opts.Pull, "pull", false, "Pull the repo before syncing")
	if err := fs.Parse(args); err != nil {
		return runOptions{}, err
	}
	if fs.NArg() != 0 {
		return runOptions{}, errors.New("sync does not accept positional arguments")
	}
	return opts, nil
}

func parseDoctorFlags(args []string) (runOptions, error) {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var opts runOptions
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to tackroom YAML config")
	fs.StringVar(&opts.Agents, "agents", "", "Comma-separated agent names to use for this run")
	fs.BoolVar(&opts.E2E, "e2e", false, "Run sync, status, and doctor end to end")
	fs.BoolVar(&opts.SkipPackageAge, "skip-package-age", false, "Skip external package publish-age checks")
	if err := fs.Parse(args); err != nil {
		return runOptions{}, err
	}
	if fs.NArg() != 0 {
		return runOptions{}, errors.New("doctor does not accept positional arguments")
	}
	return opts, nil
}

func parseCronFlags(args []string) (cronOptions, error) {
	fs := flag.NewFlagSet("cron", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var opts cronOptions
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to tackroom YAML config")
	fs.StringVar(&opts.Agents, "agents", "", "Comma-separated agent names")
	fs.BoolVar(&opts.Remove, "remove", false, "Remove the cron entry instead of installing")
	fs.BoolVar(&opts.Deps, "deps", false, "Install dependency maintenance cron instead of auto-pull")
	fs.StringVar(&opts.Interval, "interval", cronIntervalDefault, "Pull interval: 5m, 15m, 30m, 1h, 6h, 12h, daily, weekly")

	if err := fs.Parse(args); err != nil {
		return cronOptions{}, err
	}
	return opts, nil
}

func printUsage() {
	fmt.Println("tackroom - manage shared skills, MCP, and canonical config")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  setup    Set up this machine and sync configured harnesses")
	fmt.Println("  status   Show harness, external lock, and memsearch state")
	fmt.Println("  sync     Regenerate committed artifacts and reconcile harnesses")
	fmt.Println("  doctor   Check pins, dependencies, and local health")
	fmt.Println("  config   Validate or print the canonical YAML")
	fmt.Println("  view     Author the canonical YAML in a loopback web UI (browser)")
	fmt.Println()
	fmt.Println("Command groups:")
	fmt.Println("  skill    Inspect, create, update, and promote skills")
	fmt.Println("  publish  Push canonical skills to a remote skill registry")
	fmt.Println("  mcp      Manage MCP servers")
	fmt.Println("  hook     Review and remove native hook registrations")
	fmt.Println()
	fmt.Println("Run \"tackroom help --all\" for flags and maintenance commands.")
}

func printAllUsage() {
	printUsage()
	fmt.Println()
	fmt.Println("Canonical forms:")
	fmt.Println("  tackroom setup [--memory off|basic|memsearch] [--agents ...] [--yes] [--dry-run] [--json]")
	fmt.Println("  tackroom status [--verbose] [--agents ...]")
	fmt.Println("  tackroom sync [--pull] [--agents ...]")
	fmt.Println("  tackroom doctor [--e2e] [--agents ...]")
	fmt.Println("  tackroom config <validate|print> [--config PATH]")
	fmt.Println("  tackroom view [--addr 127.0.0.1:8765] [--no-open] [--secure-cookie] [--ssh-host user@host] [--token-file PATH]")
	fmt.Println("  tackroom skill new <name> [--description ...]")
	fmt.Println("  tackroom skill list [--agents ...]")
	fmt.Println("  tackroom skill info <name>")
	fmt.Println("  tackroom skill update [name ...]")
	fmt.Println("  tackroom skill promote <name-or-path> [--dry-run]")
	fmt.Println("  tackroom publish [--target NAME] [--skills a,b] [--dry-run] [--json] [--yes]")
	fmt.Println("  tackroom mcp <list|add|import|remove> [options]")
	fmt.Println("  tackroom hook list [--agents ...] [query]")
	fmt.Println("  tackroom hook remove [--dry-run] [--agents ...] <query>")
	fmt.Println()
	fmt.Println("Maintenance:")
	fmt.Println("  tackroom cron [--interval 30m|--deps|--remove]")
	fmt.Println("  tackroom pull [options]          # git pull --ff-only, then sync; what cron runs")
	fmt.Println("  tackroom doctor <audit|deps> [options]")
	fmt.Println("  tackroom sync deps [options]")
	fmt.Println("  tackroom memsearch <setup|status> [options]")
	fmt.Println("  tackroom version                 # also --version")
}
