package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type config struct {
	Version        int                   `yaml:"version"`
	Agents         []agentConfig         `yaml:"agents"`
	MCPServers     []mcpServerConfig     `yaml:"mcp_servers"`
	ExternalSkills []externalSkillSource `yaml:"external_skills"`
	PublishTargets []publishTarget       `yaml:"publish_targets,omitempty"`
	Hooks          []hookConfig          `yaml:"hooks,omitempty"`
	UI             *uiConfig             `yaml:"ui,omitempty"`
	// Instructions lists extra instruction files (for example SOUL.md or a
	// profile kept elsewhere) that every agent's root doc carries after
	// AGENTS.md, in order. Relative paths resolve against the config root.
	Instructions []string `yaml:"instructions,omitempty"`
	// TrustedPackages lists packages (npm:<name> or pypi:<name>) whose new
	// versions skip the cooldown. Absent (nil) uses defaultTrustedPackages;
	// present, even empty, replaces them.
	TrustedPackages *[]string `yaml:"trusted_packages,omitempty"`

	// cooldownMCP names MCP servers whose package version is in cooldown.
	// desiredMCPServersForAgent keeps what each harness already has for
	// them and writes nothing new. Set by gatePackages; never from YAML.
	cooldownMCP  map[string]bool
	cooldownHome string
	// configuredSources is every external source as configured, set by
	// applyStatusGates where ExternalSkills drops the ones in cooldown or blocked
	// that are not held at their pin, so the status lock check still counts
	// them. Never from YAML.
	configuredSources []externalSkillSource
	// codexPluginMCP names plugin MCP servers Codex loads from its native
	// plugin, so desiredMCPServersForAgent writes no [mcp_servers] row for them.
	// Set by injectPluginMCPServersMode; never from YAML.
	codexPluginMCP map[string]bool
	// retiredMemoryHook returns the retired memory script a native hook
	// command runs, or "": such a registration is stale even though no
	// configured hook names it. Set by sync when no configured hook runs a
	// memory script; never from YAML.
	retiredMemoryHook func(command string) string
	// ContextNoteTokens is the estimated skill-listing token threshold above
	// which `tackroom doctor` prints a soft context advisory note. Absent
	// (nil) uses contextNoteTokensDefault; 0 (or negative) disables the note.
	ContextNoteTokens *int `yaml:"context_note_tokens,omitempty"`
	// PeriodicSync opts out of the periodic `tackroom pull` job. Absent (nil)
	// defaults to true; only an explicit false disables it and silences the
	// "periodic sync is off" warning. The per-machine local overlay wins.
	PeriodicSync *bool `yaml:"periodic_sync,omitempty"`
}

type uiConfig struct {
	Links []uiLink `yaml:"links,omitempty"`
	// View holds the settings of the background `tackroom view` server.
	View *uiViewConfig `yaml:"view,omitempty"`
}

// uiViewConfig is ui.view. Empty fields take the defaults: 127.0.0.1:8765,
// 30m and true. A field the local overlay sets replaces the shared one.
type uiViewConfig struct {
	// Addr is the loopback address a view takes when --addr is not given. It
	// falls back to a free port when taken.
	Addr string `yaml:"addr,omitempty"`
	// IdleTimeout is a Go duration after which a background view with no
	// requests exits; "0" keeps it running until `tackroom view --stop`.
	IdleTimeout string `yaml:"idle_timeout,omitempty"`
	// OpenBrowser false makes `tackroom view` behave as with --no-open.
	OpenBrowser *bool `yaml:"open_browser,omitempty"`
}

type uiLink struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

type externalSkillSource struct {
	Name        string   `yaml:"name,omitempty"`
	URL         string   `yaml:"url"`
	SkillDir    string   `yaml:"skill_dir,omitempty"`
	SkillDirs   []string `yaml:"skill_dirs,omitempty"`
	Branch      string   `yaml:"branch"`
	Skills      []string `yaml:"skills,omitempty"`
	Materialize bool     `yaml:"materialize,omitempty"`
	MCP         bool     `yaml:"mcp,omitempty"`
	MCPAgents   []string `yaml:"mcp_agents,omitempty"`
	// ExcludeAgents lists agents that do not get this source's skills, e.g.
	// Claude Code when the same repo is already installed there as a plugin.
	ExcludeAgents []string `yaml:"exclude_agents,omitempty"`
}

type agentConfig struct {
	Name      string         `yaml:"name"`
	Enabled   bool           `yaml:"enabled"`
	SkillRoot string         `yaml:"skill_root"`
	AgentRoot string         `yaml:"agent_root,omitempty"`
	Detect    string         `yaml:"detect,omitempty"`
	RoleModel string         `yaml:"role_model,omitempty"`
	Packages  *[]string      `yaml:"packages,omitempty"`
	Settings  map[string]any `yaml:"settings,omitempty"`
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
	Linked         bool // this sync run created or repaired the link
}

type agentReport struct {
	Name string
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
	RootLinked      bool     // this sync run created or repaired the root link
	RootSources     []string // files composed into the root doc, AGENTS.md first; empty for a plain link
	RootContent     []byte   // the composed root doc sync writes
	RootMissing     []string // configured instructions files that were not found
	Managed         []string
	ManagedAgent    []string
	ManagedMCP      []string
	ManagedHook     []string
	ManagedPackage  []string
	ManagedSetting  []string
	ManagedPlugin   []string
	Drifted         []string
	DriftedAgent    []string
	DriftedMCP      []string
	DriftedHook     []string
	DriftedPackage  []string
	DriftedSetting  []string
	Missing         []string
	MissingAgent    []string
	MissingMCP      []string
	MissingHook     []string
	MissingPlugin   []string
	DisabledPlugin  []string
	StalePlugin     []string
	StaleMCP        []string // servers sync wrote earlier that are no longer wanted
	StaleHook       []string // hooks the config turned off or no longer aims here, still registered
	UnsupportedHook []string
	Conflicts       []string
	// IdenticalCopies are native files or skill folders with the shared
	// content that sync replaces with links; they are backed up first, so
	// `tackroom restore` can put them back.
	IdenticalCopies []string
	// RoleConflicts are the Conflicts entries about one role file. They leave
	// only that file alone; the rest of the agent still syncs.
	RoleConflicts []string
	// Replaceable lists conflicts that `sync --replace-conflicts` (and setup)
	// may resolve by moving the native path into a backup and linking it.
	Replaceable    []replaceableConflict
	StaleManaged   []string
	External       []string
	Adds           []string
	AddsAgent      []string
	AddsMCP        []string
	AddsHook       []string
	AddsPlugin     []string
	Updates        []string
	UpdatesAgent   []string
	UpdatesMCP     []string
	UpdatesHook    []string
	UpdatesPackage []string
	UpdatesSetting []string
	Removes        []string
	RemovesAgent   []string
	RemovesMCP     []string
	RemovesHook    []string
	RemovesPackage []string
	RemovesPlugin  []string
	Synced         bool

	UnsupportedSetting []string // a settings: block on a harness without settings support
}

// replaceableConflict is a native file or skill directory that sits where
// tackroom wants a symlink or a rendered role and whose content differs from
// the canonical copy.
type replaceableConflict struct {
	Path    string // native path to back up and replace
	Skill   string // skill name; empty for roles and root instructions
	Role    string // role name; empty for skills and root instructions
	Message string // the matching entry in agentReport.Conflicts
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
	JSONOutput     bool
	DryRun         bool
	AssumeYes      bool
	Stdin          io.Reader
	Stdout         io.Writer
	// ConfirmRemovals makes sync preview per-harness removals and role
	// overwrites and ask before applying them. Set by setup-driven syncs.
	ConfirmRemovals bool
	// ReplaceConflicts moves native files that differ from the canonical copy
	// into a timestamped backup and links them. Setup always sets it.
	ReplaceConflicts bool
	// Verbose expands `status` back to the full per-surface managed and
	// external skill lists and native root paths instead of the concise view,
	// and makes setup print every step and the full sync report.
	Verbose bool
	// NoCron makes setup skip installing the periodic sync job.
	NoCron bool
	// Review makes setup show the import review and ask before each step
	// instead of taking every default.
	Review bool
	// From is the git URL setup clones into (or pulls in) the config root.
	From string
	// Overlay names overlays/NAME.yaml in the config root, which setup copies
	// to tackroom.local.yaml before the first sync.
	Overlay string
	// setupResult, set by setup, receives what the setup-driven sync did, so
	// setup can print its short summary. The sync report itself is then
	// printed only with Verbose.
	setupResult *setupSyncResult
	// ExitCode makes `status` map drift to exit 1 and errors to exit 2
	// (instead of the default 1 for any failure).
	ExitCode bool
	// AllowFresh* skip the cooldown of items (sync --allow-fresh).
	AllowFreshAll    bool
	AllowFreshItems  []string
	AllowFreshReason string
	// PlanDigest makes sync apply only the plan `sync --dry-run --json` showed
	// with this plan_digest.
	PlanDigest string
	// configRevision identifies the config files ConfigOverride was read from;
	// the plan digest covers it.
	configRevision string
}

func Run(args []string) error {
	err := run(args)
	if errors.Is(err, flag.ErrHelp) {
		// `<command> --help` already printed its usage; that is a success.
		return nil
	}
	return err
}

func run(args []string) error {
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
	case "restore":
		return runRestore(args[1:])
	case "publish":
		return runPublishCommand(args[1:])
	case "mcp":
		return runMCP(args[1:])
	case "hook":
		return runHookCommand(args[1:])
	case "tern":
		return runTernCommand(args[1:])
	case "rex":
		return runRexCommand(args[1:])
	case "tmux":
		return runTmuxCommand(args[1:])
	case "zellij":
		return runZellijCommand(args[1:])
	case "hosts":
		return runHostsCommand(args[1:])
	case "cron":
		if len(args) > 1 && args[1] == "collect" {
			return runCronCollect(args[2:])
		}
		if len(args) > 1 && args[1] == "apply" {
			return runCronApply(args[2:])
		}
		if len(args) > 1 && (args[1] == "run" || args[1] == "pause" || args[1] == "resume" || args[1] == "schedule") {
			return runCronActionCommand(args[1], args[2:])
		}
		if len(args) > 1 && (args[1] == "add" || args[1] == "edit" || args[1] == "remove") {
			return runCronJobCommand(args[1], args[2:])
		}
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
		if args[0] == "tui" {
			fmt.Fprintln(os.Stderr, "tackroom tui was removed in 3.0. Use tackroom view, or tackroom status in a terminal.")
		}
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func runSetupCommand(args []string) error {
	opts, err := parseSetupFlags(args)
	if err != nil {
		return err
	}
	return runSetup(opts)
}

func runStatusCommand(args []string) error {
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
	fs.BoolVar(&opts.JSONOutput, "json", false, "Print the status as JSON (exit code 1 when anything is out of sync)")
	fs.BoolVar(&opts.ExitCode, "exit-code", false, "Exit 0 when in sync, 1 when anything is pending, 2 on error")

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
	if opts.DryRun && opts.Pull {
		return errors.New("sync --dry-run cannot be combined with --pull")
	}
	if opts.PlanDigest != "" {
		if opts.DryRun || opts.Pull {
			return errors.New("sync --plan-digest cannot be combined with --dry-run or --pull")
		}
		return runSyncPlanned(opts)
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
			if len(args) > 1 && args[1] == "accept" {
				return runAuditAccept(args[2:])
			}
			if len(args) > 1 && args[1] == "revoke" {
				return runAuditRevoke(args[2:])
			}
			opts, err := parseAuditFlags(args[1:])
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
		return errors.New("skill requires subcommand: new, list, info, sources, update")
	}
	if isHelpArg(args[0]) {
		printGroupUsage("skill")
		return nil
	}
	switch args[0] {
	case "new":
		return runSkillify(args[1:])
	case "update":
		return runExternalUpdate(args[1:])
	case "sources":
		return runSkillSources(args[1:])
	case "list":
		return runSkillList(args[1:])
	case "info":
		return runSkillInfo(args[1:])
	default:
		return fmt.Errorf("unknown skill subcommand %q", args[0])
	}
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
	var assumeYes bool
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to tackroom YAML config")
	fs.StringVar(&opts.Agents, "agents", "", "Comma-separated agent names to use for this run")
	// --memory chose a memory tier. tackroom no longer sets up memory, so it is
	// accepted for old scripts and does nothing.
	var memoryTier string
	fs.StringVar(&memoryTier, "memory", "", "Deprecated: does nothing. tackroom no longer sets up memory")
	fs.StringVar(&opts.From, "from", "", "Clone this git repository into the config root (or pull it there) and set up from it")
	fs.StringVar(&opts.Overlay, "overlay", "", "Copy overlays/NAME.yaml from the config root to tackroom.local.yaml before the first sync")
	fs.BoolVar(&opts.Review, "review", false, "Review what to import and confirm each step instead of taking the defaults")
	fs.BoolVar(&opts.Verbose, "verbose", false, "Print every step and the full sync report")
	fs.BoolVar(&opts.JSONOutput, "json", false, "Emit detection result as JSON and exit")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "Show detected import candidates and exit without changes")
	fs.BoolVar(&opts.NoCron, "no-cron", false, "Do not install the periodic sync job (tackroom pull every 15m)")
	// --yes was the way to skip the prompts; setup no longer asks, so it is
	// accepted for old scripts and left out of the help.
	fs.BoolVar(&assumeYes, "yes", false, "")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: tackroom setup [--from GIT_URL] [--overlay NAME] [--review] [--verbose] [--agents ...] [--no-cron] [--dry-run] [--json]")
		fs.VisitAll(func(f *flag.Flag) {
			if f.Name == "yes" {
				return
			}
			fmt.Fprintf(fs.Output(), "  --%s\n    \t%s\n", f.Name, f.Usage)
		})
	}
	if err := fs.Parse(args); err != nil {
		return runOptions{}, err
	}
	if fs.NArg() != 0 {
		return runOptions{}, errors.New("setup does not accept positional arguments")
	}
	// A name, never a path: it must not reach outside overlays/.
	if strings.ContainsAny(opts.Overlay, `/\`) || strings.Contains(opts.Overlay, "..") {
		return runOptions{}, fmt.Errorf("--overlay takes a name from overlays/ (cloud for overlays/cloud.yaml), not a path: %q", opts.Overlay)
	}
	if tier := strings.TrimSpace(memoryTier); tier != "" && !strings.EqualFold(tier, "off") {
		fmt.Fprintln(os.Stderr, "tackroom no longer sets up memory. See https://yourconscience.github.io/tackroom/docs/memory/")
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
	fs.BoolVar(&opts.DryRun, "dry-run", false, "Print the plan without writing anything, fetching external sources or querying registries")
	fs.BoolVar(&opts.ReplaceConflicts, "replace-conflicts", false, "Move native files that differ from the shared copy into a backup, then link the shared skill or instructions file, or rewrite the role, in their place")
	fs.BoolVar(&opts.JSONOutput, "json", false, "Print the resulting status as JSON instead of the text report")
	fs.StringVar(&opts.PlanDigest, "plan-digest", "", "Sync only if the plan still has this plan_digest from sync --dry-run --json")
	addAllowFreshFlags(fs, &opts)
	if err := fs.Parse(args); err != nil {
		return runOptions{}, err
	}
	if fs.NArg() != 0 {
		return runOptions{}, errors.New("sync does not accept positional arguments")
	}
	// An empty digest must not fall through to a sync that checks nothing.
	emptyDigest := false
	fs.Visit(func(f *flag.Flag) { emptyDigest = emptyDigest || (f.Name == "plan-digest" && opts.PlanDigest == "") })
	if emptyDigest {
		return runOptions{}, errors.New("sync --plan-digest needs the plan_digest that tackroom sync --dry-run --json printed; it cannot be empty")
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
	fs.BoolVar(&opts.JSONOutput, "json", false, "Print check results as JSON")
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
	fs.StringVar(&opts.Interval, "interval", "", "Pull interval: 5m, 15m, 30m, 1h, 6h, 12h, daily, weekly (default 15m; weekly with --deps)")

	if err := fs.Parse(args); err != nil {
		return cronOptions{}, err
	}
	return opts, nil
}

func printUsage() {
	fmt.Println("tackroom - manage shared skills, MCP, and canonical config")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  setup    Set up this machine; on another machine: setup --from <git url>")
	fmt.Println("  status   Show harness and external lock state")
	fmt.Println("  sync     Regenerate committed artifacts and reconcile harnesses")
	fmt.Println("  doctor   Check pins, dependencies, and local health")
	fmt.Println("  config   Validate or print the canonical YAML")
	fmt.Println("  restore  List backups or put backed-up native files back")
	fmt.Println("  view     Author the canonical YAML in a loopback web UI (browser)")
	fmt.Println("  hosts    Show tackroom on every machine in crons.hosts; fix one or set one up")
	fmt.Println()
	fmt.Println("Command groups:")
	fmt.Println("  skill    Inspect, create, and update skills")
	fmt.Println("  publish  Push canonical skills to a remote skill registry")
	fmt.Println("  mcp      Manage MCP servers")
	fmt.Println("  hook     Review and remove native hook registrations")
	fmt.Println()
	fmt.Println("Run \"tackroom help --all\" for flags and maintenance commands.")
}

// canonicalForms is the full command reference; group --help prints its lines.
var canonicalForms = []string{
	"  tackroom setup [--from GIT_URL] [--overlay NAME] [--review] [--verbose] [--agents ...] [--no-cron] [--dry-run] [--json]",
	"  tackroom status [--verbose] [--exit-code] [--agents ...]",
	"  tackroom sync [--pull] [--dry-run] [--replace-conflicts] [--agents ...] [--allow-fresh[=item,...]] [--reason ...]",
	"  tackroom sync --plan-digest DIGEST        # sync only if the plan is still the one sync --dry-run --json showed",
	"  tackroom doctor [--e2e] [--agents ...]",
	"  tackroom doctor audit [--json]            # findings per item; exit 1 on an unaccepted CRITICAL in external content",
	"  tackroom doctor audit accept <item> <rule> --reason \"...\"   # item is kind:name, e.g. skill:foo",
	"  tackroom doctor audit revoke <item> <rule>",
	"  tackroom config <validate|print|inventory> [--config PATH]   # inventory prints the web view's inventory as JSON",
	"  tackroom config set [--config PATH] <section/name/field> <value>   # e.g. mcp_servers/exa/enabled false; then tackroom sync",
	"  tackroom view [--no-open | --json | --foreground | --stop] [--addr 127.0.0.1:8765] [--secure-cookie] [--ssh-host user@host] [--token-file PATH]",
	"  tackroom skill new <name> [--description ...]",
	"  tackroom skill list [--agents ...]",
	"  tackroom skill info <name>",
	"  tackroom skill sources [--json]           # external sources: configured and installed elsewhere",
	"  tackroom skill sources search <query>     # find sources on skills.sh",
	"  tackroom skill sources add <owner/repo[/path]> [--skills a,b | --all] [--latest] [--no-sync] [--allow-fresh[=item,...]]",
	"  tackroom skill update [name ...] [--allow-fresh[=item,...]]",
	"  tackroom publish [--target NAME] [--skills a,b] [--dry-run] [--json] [--yes]",
	"  tackroom mcp <list|add|import|remove> [options]   # add: --command ... or --url ... [--header 'Name: value']",
	"  tackroom restore [<backup> | latest] [--dry-run]",
	"  tackroom hook list [--agents ...] [query]",
	"  tackroom hook remove [--dry-run] [--agents ...] <query>",
	"  tackroom tern [--config PATH] [--dry-run]         # open a tackroom workspace in the Tern terminal",
	"  tackroom rex [--config PATH] [--dry-run]          # open a tackroom workspace in the Rex terminal",
	"  tackroom tmux [--config PATH] [--dry-run]         # open a tackroom workspace in tmux",
	"  tackroom zellij [--config PATH] [--dry-run]       # open a tackroom workspace in zellij",
	"  tackroom hosts [HOST...] [--json] [--config PATH]   # tackroom on each machine in crons.hosts: version, sync state, config commit",
	"  tackroom hosts fix [HOST...] [--dry-run] [--config PATH]   # run the fix hosts names (setup, pull or sync there); never commits or pushes",
	"  tackroom hosts setup HOST [--from GIT_URL] [--overlay NAME] [--no-cron] [--install] [--dry-run] [--config PATH]   # tackroom setup --from on HOST over SSH",
	"  tackroom cron collect [--hosts local,HOST...] [--table] [--config PATH]",
	"  tackroom cron apply [--dry-run] [--config PATH]",
	"  tackroom cron run|pause|resume [--host HOST] [--config PATH] ID",
	"  tackroom cron schedule [--config PATH] ID \"MIN HOUR DOM MON DOW\"",
	"  tackroom cron add ID --schedule \"MIN HOUR DOM MON DOW\" --command CMD [--arg ARG ...] [--host NAME] [--effect TEXT] [--output PATH] [--local] [--dry-run] [--no-apply] [--config PATH]",
	"  tackroom cron edit ID [--schedule EXPR] [--command CMD [--arg ARG ...]] [--host NAME] [--effect TEXT] [--output PATH] [--dry-run] [--no-apply] [--config PATH]",
	"  tackroom cron remove ID [--dry-run] [--no-apply] [--config PATH]",
}

// printGroupUsage answers `tackroom <group> --help` with that group's forms.
func printGroupUsage(group string) {
	for _, line := range canonicalForms {
		if strings.HasPrefix(line, "  tackroom "+group+" ") {
			fmt.Println(line)
		}
	}
}

func isHelpArg(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}

func printAllUsage() {
	printUsage()
	fmt.Println()
	fmt.Println("Canonical forms:")
	for _, line := range canonicalForms {
		fmt.Println(line)
	}
	fmt.Println()
	fmt.Println("Maintenance:")
	fmt.Println("  tackroom cron [--interval 15m|--deps|--remove]   # periodic sync job, installed by setup; default 15m")
	fmt.Println("  tackroom pull [options]          # git pull --ff-only (a failure only warns), then sync; what the job runs")
	fmt.Println("  tackroom doctor <audit|deps> [options]")
	fmt.Println("  tackroom sync deps [options]")
	fmt.Println("  tackroom version                 # also --version")
}
