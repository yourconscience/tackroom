package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func runSetup(opts runOptions) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	configPath, err := resolveConfigPath(opts.ConfigPath, home)
	if err != nil {
		return err
	}
	repoRoot := filepath.Dir(configPath)
	if err := checkConfigRoot(repoRoot); err != nil {
		return err
	}
	// Setup takes every default without reading stdin unless asked to review.
	opts.AssumeYes = !opts.Review
	streams := setupStreams(opts)

	var from *setupFrom
	if opts.From != "" {
		plan, err := planSetupFrom(opts.From, repoRoot, home)
		if err != nil {
			return err
		}
		from = &plan
	}
	if from != nil && !opts.JSONOutput && !opts.DryRun {
		if err := from.apply(); err != nil {
			return err
		}
		if err := checkConfigRoot(repoRoot); err != nil {
			return err
		}
	}
	// --dry-run and --json plan against what the real run would see. For a
	// clone that is not made yet, that is a throwaway clone in a temp dir.
	planRoot, planConfig := repoRoot, configPath
	if from != nil && from.clone && (opts.JSONOutput || opts.DryRun) {
		tmp, err := os.MkdirTemp("", "tackroom-from-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		planRoot = filepath.Join(tmp, "config")
		planConfig = filepath.Join(planRoot, filepath.Base(configPath))
		if _, err := gitRemote("clone", "--quiet", "--depth", "1", "--", from.url, planRoot); err != nil {
			return fmt.Errorf("git clone %s failed: %v", redactURL(from.url), err)
		}
	}
	configExisted := hasFile(planConfig)

	cfg, err := loadSetupConfig(planConfig, home)
	if err != nil {
		return err
	}
	// Without --memory, an existing config keeps the tier it already uses, and
	// setup never changes it. A machine that lacks memsearch for a memsearch
	// config leaves the hooks as they are and says what to install.
	tier := opts.MemoryTier
	keepTier := false
	if tier == "" && configExisted {
		tier = configuredMemoryTier(cfg)
		if tier == memoryTierMemsearch {
			if _, err := exec.LookPath("memsearch"); err != nil {
				keepTier = true
			}
		}
	}
	detected, err := detectDefaultAgents(opts.Agents)
	if err != nil {
		return err
	}
	if len(detected) == 0 {
		return errors.New("no supported agents detected on PATH")
	}
	priorCfg := cfg
	upsertSetupAgents(&cfg, detected)

	// --json and --dry-run must exit before any mutation, including the
	// memory-tier setup below which creates directories and config files.
	planned := setupPlannedActions(opts)
	if from != nil {
		planned = append([]string{from.action()}, planned...)
	}
	if opts.JSONOutput {
		detection, err := runDetection(cfg, detected, planRoot, home)
		if err != nil {
			return err
		}
		detection.PlannedActions = planned
		enc := json.NewEncoder(streams.out)
		enc.SetIndent("", "  ")
		return enc.Encode(detection)
	}

	if opts.DryRun {
		detection, err := runDetection(cfg, detected, planRoot, home)
		if err != nil {
			return err
		}
		if opts.Review {
			fmt.Fprint(streams.out, renderDetectionSummary(detection))
		} else {
			// What the default run would do, with the same decisions.
			_, roles, _, err := scanNativeImports(cfg, detected, planRoot, home)
			if err != nil {
				return err
			}
			decisions, kept := setupImportDecisions(detection, roles, cfg.Agents, home)
			fmt.Fprint(streams.out, renderSetupPlan(detection, decisions, kept, cfg.Agents, planRoot, displayPath(repoRoot, home), home))
		}
		for _, action := range planned {
			fmt.Fprintf(streams.out, "\nplanned: %s\n", action)
		}
		if sources := rootInstructionSources(planRoot, detected, home); len(sources) > 0 {
			fmt.Fprintln(streams.out, "\nroot instructions to import into AGENTS.md (originals are backed up, then linked):")
			for _, src := range sources {
				fmt.Fprintf(streams.out, "  %s (%s)\n", displayPath(src.Path, home), src.Agent)
			}
		}
		return nil
	}

	// chatter is the step-by-step log --verbose shows. prompts carries the
	// questions --review asks; without it every question takes its default
	// and nothing about it is printed.
	chatter := streams.out
	prompts := streams
	if !opts.Verbose {
		chatter = io.Discard
		if !opts.Review {
			prompts.out = io.Discard
		}
	}
	summary := setupSummary{home: home, root: repoRoot, fresh: !configExisted, from: from}
	for _, agent := range detected {
		summary.agents = append(summary.agents, agent.Name)
	}

	if keepTier {
		summary.problem("this config's memory hooks use memsearch, which is not installed here. Install it (uv tool install memsearch), then run: tackroom setup")
	} else if err := applyMemoryTier(&cfg, tier, repoRoot, home); err != nil {
		return err
	}

	fmt.Fprintln(chatter, "tackroom setup")
	fmt.Fprintf(chatter, "config root: %s\n\n", repoRoot)

	before := canonicalItems(repoRoot, cfg)
	skills, roles, mcps, err := scanNativeImports(cfg, detected, repoRoot, home)
	if err != nil {
		return err
	}
	detection, err := runDetection(cfg, detected, repoRoot, home)
	if err != nil {
		return err
	}
	summary.warnings = detection.Warnings
	var decisions []reviewDecision
	switch {
	case !opts.Review:
		decisions, summary.keptRoles = setupImportDecisions(detection, roles, cfg.Agents, home)
		shared, err := applyReviewDecisions(repoRoot, &cfg, decisions, skills, roles, mcps)
		if err != nil {
			return err
		}
		if shared > 0 {
			fmt.Fprintf(chatter, "imported %d item(s)\n\n", shared)
		}
	case reviewTTYAvailable(streams):
		decisions, err = runReviewImport(repoRoot, &cfg, detection, skills, roles, mcps, prompts)
		if err != nil {
			if !errors.Is(err, errReviewAborted) {
				return err
			}
			fmt.Fprintln(prompts.out, "import review aborted; continuing setup without importing")
		}
	default:
		if err := importNativeContent(repoRoot, &cfg, skills, roles, mcps, prompts); err != nil {
			return err
		}
	}
	after := canonicalItems(repoRoot, cfg)
	summary.noteImports(before, after)
	for name := range summary.keptRoles {
		if before["role:"+name] {
			// The config already shares this role; the sync reports the
			// agent's own copy if it cannot be replaced.
			delete(summary.keptRoles, name)
		}
	}
	// An agent's own role that was not imported keeps its name: no starter
	// role of that name is written over it.
	skipRoles := map[string]bool{}
	for _, item := range append(append([]detectedItem(nil), detection.Items...), detection.Resolved...) {
		if item.Surface == "role" && !after["role:"+item.Name] {
			skipRoles[item.Name] = true
		}
	}
	summary.rootDocs = rootInstructionSources(repoRoot, detected, home)
	if err := importRootInstructions(repoRoot, detected, home, setupIO{in: streams.in, out: chatter}); err != nil {
		return err
	}
	// A config root cloned with --from is adopted as it is: no starter files.
	if from == nil || !configExisted {
		if err := ensureStarterAssetsExcept(repoRoot, configPath, tier, skipRoles); err != nil {
			return err
		}
	}
	if err := ensureMemoryHookExecutables(repoRoot); err != nil {
		return err
	}
	if err := writeSetupConfig(configPath, cfg, home); err != nil {
		return err
	}

	for _, agent := range detected {
		patched, err := patchAgentConfig(agent, home, repoRoot, cfg)
		if err != nil {
			fmt.Fprintf(chatter, "%s: config patch failed: %v\n", agent.Name, err)
			summary.problem("%s: could not update its config: %v. Fix the file, then run: tackroom setup", agent.Name, err)
			continue
		}
		if patched {
			summary.changed = true
			fmt.Fprintf(chatter, "%s: config patched (added %s)\n", agent.Name, filepath.Join(repoRoot, "skills"))
		} else {
			fmt.Fprintf(chatter, "%s: config already set\n", agent.Name)
		}
	}
	migrated, err := migrateLegacyMemoryHookPaths(home, repoRoot)
	if err != nil {
		fmt.Fprintf(chatter, "memory hooks: legacy path migration failed: %v\n", err)
		summary.hookProblems = append(summary.hookProblems, fmt.Sprintf("could not update old memory hook paths: %v. Fix the file, then run: tackroom setup", err))
	} else if migrated > 0 {
		summary.changed = true
		fmt.Fprintf(chatter, "memory hooks: migrated legacy paths in %d config file(s)\n\n", migrated)
	}
	// The config the sync below applies, local overlay included; when it does
	// not load, nothing is kept and every managed memory hook is cleaned.
	effective, _ := loadConfig(repoRoot, home, configPath)
	cleaned, err := removeNativeManagedMemoryHooks(home, repoRoot, priorCfg, cfg, effective)
	if err != nil {
		fmt.Fprintf(chatter, "memory hooks: native cleanup failed: %v\n", err)
		summary.hookProblems = append(summary.hookProblems, fmt.Sprintf("could not remove old memory hooks: %v. Fix the file, then run: tackroom setup", err))
	} else if cleaned > 0 {
		summary.changed = true
		fmt.Fprintf(chatter, "memory hooks: removed previous managed commands from %d native config file(s)\n\n", cleaned)
	}

	syncOpts := opts
	syncOpts.ConfigPath = configPath
	syncOpts.ConfirmRemovals = true
	// Setup adopts this machine: native files that differ from the shared
	// copy are backed up and replaced (after a prompt with --review).
	syncOpts.ReplaceConflicts = true
	syncOpts.Stdout = prompts.out
	syncOpts.setupResult = &summary.sync
	summary.noteDiffering(detection)
	syncErr := runSync(syncOpts)
	if syncErr != nil && !summary.sync.done {
		return syncErr
	}
	summary.noteDiffered(before, detection, decisions)
	summary.memory = memorySummary(cfg, tier, keepTier)
	// After the sync, so the first commit holds everything sync wrote too.
	// Without --review, git init is only for a first setup: no tackroom.yaml
	// before this run (and offerGitInit skips a root already under git). A
	// config whose owner declined git keeps going without it.
	if !configExisted || opts.Review {
		var gitProblem string
		summary.gitNew, gitProblem = offerGitInit(repoRoot, prompts)
		if gitProblem != "" {
			summary.problem("%s", gitProblem)
		}
	}
	summary.periodic, summary.cronNew = setupPeriodicSync(syncOpts, configPath, repoRoot, home, &summary)
	if problems := summary.print(streams.out); problems > 0 || syncErr != nil {
		return errSetupNeedsAttention
	}
	return nil
}

// setupPeriodicSync installs the periodic `tackroom pull` job unless the user
// opted out, and returns the summary line and whether it was installed now.
func setupPeriodicSync(opts runOptions, configPath, repoRoot, home string, summary *setupSummary) (string, bool) {
	if opts.NoCron {
		// --no-cron is an explicit opt-out, recorded so doctor does not warn
		// about a job the user chose not to install.
		if err := setLocalPeriodicSync(configPath, home, false); err != nil {
			summary.problem("could not record periodic_sync: false in tackroom.local.yaml: %v", err)
		}
		return "Periodic sync is off (--no-cron); turn it on with: tackroom cron", false
	}
	// A persisted periodic_sync: false (tackroom.yaml or the local overlay) is
	// the same opt-out; setup must not reinstall the job and reverse it.
	if effective, err := loadConfig(repoRoot, home, ""); err == nil && !periodicSyncEnabled(effective) {
		return "Periodic sync is off (periodic_sync: false); turn it on with: tackroom cron", false
	}
	// Setup is the one place besides `tackroom cron` that installs the job.
	// A scheduler that cannot take it must not fail the setup. An installed
	// job is left alone, so a chosen --interval survives.
	if job, _ := readPeriodicSync(); job != nil {
		return "Periodic sync (tackroom pull) was already installed", false
	}
	if err := installPeriodicSync(cronOptions{runOptions: opts}); err != nil {
		summary.problem("periodic sync was not installed: %v. Fix: tackroom cron", err)
		return "", false
	}
	return "Sync runs every 15 minutes (tackroom pull)", true
}

// memorySummary is the setup summary's memory line.
func memorySummary(cfg config, tier string, notInstalled bool) string {
	tier = normalizeMemoryTier(tier)
	if notInstalled {
		return tier + " (memsearch is not installed here)"
	}
	var events []string
	for _, hook := range cfg.Hooks {
		if _, managed := managedMemoryHookNames[hook.Name]; managed && hook.Enabled {
			events = append(events, map[string]string{"SessionStart": "session start", "Stop": "stop", "SessionEnd": "session end"}[hook.Event])
		}
	}
	if tier == memoryTierOff || len(events) == 0 {
		return "off"
	}
	return fmt.Sprintf("%s (hooks at %s)", tier, joinAnd(events))
}

// configuredMemoryTier reads the memory tier an existing config uses from its
// managed memory hooks, so a rerun or a second machine keeps it.
func configuredMemoryTier(cfg config) string {
	tier := memoryTierOff
	for _, hook := range cfg.Hooks {
		if _, managed := managedMemoryHookNames[hook.Name]; !managed {
			continue
		}
		switch {
		case strings.Contains(hook.Command, "basic-session-"):
			return memoryTierBasic
		case strings.Contains(hook.Command, "session-start.sh"), strings.Contains(hook.Command, "session-end.sh"), strings.Contains(hook.Command, "stop.sh"):
			tier = memoryTierMemsearch
		}
	}
	return tier
}

// offerGitInit versions a fresh config root: it asks with --review and
// otherwise just does it. Skipped when the root is already inside a git work
// tree or git is unavailable. It reports whether it created the repository and
// a problem line for the summary when a step failed.
func offerGitInit(repoRoot string, streams setupIO) (bool, string) {
	if _, err := exec.LookPath("git"); err != nil {
		return false, ""
	}
	inside := exec.Command("git", "-C", repoRoot, "rev-parse", "--is-inside-work-tree")
	if out, err := inside.Output(); err == nil && strings.TrimSpace(string(out)) == "true" {
		return false, ""
	}
	if !promptYesNo(streams, fmt.Sprintf("Initialize a git repository in %s to version your configuration?", repoRoot)) {
		return false, ""
	}
	if out, err := exec.Command("git", "-C", repoRoot, "init", "-b", "main").CombinedOutput(); err != nil {
		// -b needs git >= 2.28; retry without it before giving up.
		if out2, err2 := exec.Command("git", "-C", repoRoot, "init").CombinedOutput(); err2 != nil {
			fmt.Fprintf(streams.out, "git init failed: %v: %s\n", err, strings.TrimSpace(string(append(out, out2...))))
			return false, fmt.Sprintf("could not create a git repository in %s: %s. Fix: git -C %s init", repoRoot, gitErrorLine(string(out2)), repoRoot)
		}
	}
	if out, err := exec.Command("git", "-C", repoRoot, "add", "-A").CombinedOutput(); err != nil {
		fmt.Fprintf(streams.out, "git add failed: %v: %s\n", err, strings.TrimSpace(string(out)))
		return true, fmt.Sprintf("could not add your config to git: %s. Fix: git -C %s add -A", gitErrorLine(string(out)), repoRoot)
	}
	if out, err := exec.Command("git", "-C", repoRoot, "commit", "-m", "initialize agent configuration").CombinedOutput(); err != nil {
		fmt.Fprintf(streams.out, "git commit skipped: %v: %s\n", err, strings.TrimSpace(string(out)))
		fmt.Fprintln(streams.out, "commit manually once git user.name/user.email are configured")
		return true, fmt.Sprintf("could not make the first commit in %s (%s). Set git user.name and user.email, then run: git -C %s commit -m \"initialize agent configuration\"", repoRoot, gitErrorLine(string(out)), repoRoot)
	}
	fmt.Fprintf(streams.out, "initialized git repository in %s; add a private remote to sync across machines\n\n", repoRoot)
	return true, ""
}

func patchAgentConfig(agent agentConfig, home string, repoRoot string, cfg config) (bool, error) {
	h := harnessFor(agent.Name)
	if h != nil && h.Setup != nil {
		return h.Setup(home, repoRoot, cfg)
	}
	return false, nil
}

func patchAmpConfig(home string, repoRoot string) (bool, error) {
	configPath := ampSettingsPath(home)
	data, err := os.ReadFile(configPath)
	raw := map[string]interface{}{}
	if err != nil {
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("read %s: %w", configPath, err)
		}
	} else if err := parseJSONConfig(configPath, data, &raw); err != nil {
		return false, fmt.Errorf("parse %s: %w", configPath, err)
	}

	target := filepath.Join(repoRoot, "skills")
	current, _ := raw["amp.skills.path"].(string)
	if ampSkillsPathConfigured(current, home, target) {
		return false, nil
	}
	raw["amp.skills.path"] = appendAmpSkillsPath(current, configSkillsDirValue(home, target))

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal %s: %w", configPath, err)
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return false, fmt.Errorf("create %s: %w", filepath.Dir(configPath), err)
	}
	if err := os.WriteFile(configPath, out, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", configPath, err)
	}
	return true, nil
}

func ampSettingsPath(home string) string {
	repoRoot, _, err := findRoots()
	if err == nil {
		if path := existingAmpSettingsPath(filepath.Join(repoRoot, ".amp")); path != "" {
			return path
		}
	}
	return ampSettingsPathForRoots("", home)
}

func ampSettingsPathForRoots(repoRoot string, home string) string {
	if repoRoot != "" {
		if path := existingAmpSettingsPath(filepath.Join(repoRoot, ".amp")); path != "" {
			return path
		}
	}
	return defaultAmpSettingsPath(filepath.Join(home, ".config", "amp"))
}

func existingAmpSettingsPath(configDir string) string {
	jsonPath := filepath.Join(configDir, "settings.json")
	jsoncPath := filepath.Join(configDir, "settings.jsonc")
	if hasFile(jsonPath) {
		return jsonPath
	}
	if hasFile(jsoncPath) {
		return jsoncPath
	}
	return ""
}

func defaultAmpSettingsPath(configDir string) string {
	jsonPath := filepath.Join(configDir, "settings.json")
	jsoncPath := filepath.Join(configDir, "settings.jsonc")
	if hasFile(jsonPath) || !hasFile(jsoncPath) {
		return jsonPath
	}
	return jsoncPath
}

func ampSkillsPathConfigured(raw string, home string, target string) bool {
	for _, part := range strings.Split(raw, ":") {
		if expandPath(strings.TrimSpace(part), home) == target {
			return true
		}
	}
	return false
}

func appendAmpSkillsPath(raw string, target string) string {
	if strings.TrimSpace(raw) == "" {
		return target
	}
	return strings.TrimRight(raw, ":") + ":" + target
}

func configSkillsDirValue(home string, target string) string {
	if target == filepath.Join(home, ".agents", "skills") {
		return tackroomSkillsPathValue
	}
	return target
}

func runPull(opts runOptions) error {
	repoRoot, home, _, _, err := loadContext(opts)
	if err != nil {
		return err
	}

	// The git step is skipped quietly when it cannot do anything: no git
	// repository, no remote, or no upstream. A pull that cannot fast-forward
	// (offline, diverged, dirty tree) must still not stop the sync: the local
	// copy has to reach the harnesses, and the exit code reflects the sync
	// only. Either way the outcome is recorded so status and doctor can
	// surface a machine that has stopped receiving updates.
	if shouldPull(repoRoot) {
		now := time.Now().UTC().Format(time.RFC3339)
		ctx, cancel := context.WithTimeout(context.Background(), gitRemoteTimeout)
		cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "pull", "--ff-only")
		cmd.WaitDelay = 5 * time.Second
		out, err := cmd.CombinedOutput()
		cancel()

		prev, _ := readPullState(home)
		state := pullState{LastAttempt: now, LastSuccess: prev.LastSuccess}
		if err != nil {
			state.Failed = true
			state.Error = gitErrorLine(string(out))
			if state.Error == "" {
				state.Error = err.Error()
			}
			fmt.Fprintf(os.Stderr, "warning: git pull --ff-only failed, syncing the local copy: %s\n", state.Error)
		} else {
			state.Failed = false
			state.Error = ""
			state.LastSuccess = now
			os.Stdout.Write(out)
		}
		// A failed state write must not fail the pull.
		_ = writePullState(home, state)
	}

	return runSync(opts)
}

type cronOptions struct {
	runOptions
	Remove   bool
	Deps     bool
	Interval string
}

const (
	cronIntervalDefault = "15m"
	cronIntervalWeekly  = "weekly"
)

func runCron(opts cronOptions) error {
	_, home, _, _, err := loadContext(opts.runOptions)
	if err != nil {
		return err
	}

	binaryPath, err := exec.LookPath("tackroom")
	if err != nil {
		binaryPath, err = os.Executable()
		if err != nil {
			return fmt.Errorf("locate tackroom executable: %w", err)
		}
	}

	// The self-update and dependency jobs are built-in owned jobs: installed
	// on the selected scheduler like `cron apply` installs declared ones. The
	// scheduler does not see this shell's config choice, so the job always
	// names the config by absolute path.
	unlock, err := lockCronApply(home)
	if err != nil {
		return err
	}
	defer unlock()
	configPath, err := resolveConfigPath(opts.ConfigPath, home)
	if err != nil {
		return err
	}
	if opts.ConfigPath, err = filepath.Abs(configPath); err != nil {
		return err
	}
	job := cronJobForOptions(binaryPath, opts)
	id, mode := job.ID, "pull"
	if opts.Deps {
		mode = "sync deps"
	}
	manifest, err := loadCronsManifest(opts.ConfigPath)
	if err != nil {
		return err
	}
	backend, err := selectCronBackend(manifest.Scheduler)
	if err != nil {
		return err
	}
	var desired []ownedCronJob
	if !opts.Remove {
		desired = append(desired, job)
	}
	installed, err := readOwnedCronUnits(home)
	if err != nil {
		return err
	}
	plan, err := planOwnedCrons(desired, backend, installed, home, func(owned string) bool { return owned == id })
	if err != nil {
		return err
	}
	legacy, err := legacyCronLines(mode)
	if err != nil {
		return err
	}
	if err := runCronPlan(setupStreams(opts.runOptions).out, plan, backend, home, legacy, false); err != nil {
		return err
	}
	if !opts.Remove && !opts.Deps {
		// Installing the pull job opts back into periodic sync: drop an
		// earlier `periodic_sync: false` in the local overlay so the default
		// (true) applies again.
		if err := setLocalPeriodicSync(configPath, home, true); err != nil {
			return fmt.Errorf("re-enable periodic sync in %s: %w", filepath.Join(filepath.Dir(configPath), "tackroom.local.yaml"), err)
		}
	}
	return nil
}

// cronJobForOptions is the built-in job. It runs the tackroom binary directly
// with the installing shell's PATH, so harness executables are found without
// a shell and without sourcing a profile.
func cronJobForOptions(binaryPath string, opts cronOptions) ownedCronJob {
	id, args, interval := "tackroom-pull", []string{"pull"}, opts.Interval
	if opts.Deps {
		id, args = "tackroom-deps", []string{"sync", "deps"}
		if interval == "" {
			interval = cronIntervalWeekly
		}
	}
	if interval == "" {
		interval = cronIntervalDefault
	}
	command := append([]string{binaryPath}, args...)
	if opts.ConfigPath != "" {
		command = append(command, "--config", opts.ConfigPath)
	}
	return ownedCronJob{ID: id, Schedule: intervalToSchedule(interval), Command: command, Path: os.Getenv("PATH")}
}

func intervalToSchedule(interval string) string {
	switch interval {
	case "5m":
		return "*/5 * * * *"
	case "15m":
		return "*/15 * * * *"
	case "30m":
		return "*/30 * * * *"
	case "1h", "hourly":
		return "0 * * * *"
	case "6h":
		return "0 */6 * * *"
	case "12h":
		return "0 */12 * * *"
	case "daily":
		return "0 4 * * *"
	case cronIntervalWeekly:
		return "0 4 * * 1"
	default:
		return "*/15 * * * *"
	}
}
