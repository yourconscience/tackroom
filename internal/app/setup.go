package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	streams := setupStreams(opts)

	cfg, err := loadSetupConfig(configPath, home)
	if err != nil {
		return err
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
	if opts.JSONOutput {
		detection, err := runDetection(cfg, detected, repoRoot, home)
		if err != nil {
			return err
		}
		detection.PlannedActions = setupPlannedActions(opts)
		enc := json.NewEncoder(streams.out)
		enc.SetIndent("", "  ")
		return enc.Encode(detection)
	}

	if opts.DryRun {
		detection, err := runDetection(cfg, detected, repoRoot, home)
		if err != nil {
			return err
		}
		fmt.Fprint(streams.out, renderDetectionSummary(detection))
		for _, action := range setupPlannedActions(opts) {
			fmt.Fprintf(streams.out, "\nplanned: %s\n", action)
		}
		if sources := rootInstructionSources(repoRoot, detected, home); len(sources) > 0 {
			fmt.Fprintln(streams.out, "\nroot instructions to import into AGENTS.md (originals are backed up, then linked):")
			for _, src := range sources {
				fmt.Fprintf(streams.out, "  %s (%s)\n", src.Path, src.Agent)
			}
		}
		return nil
	}

	if err := applyMemoryTier(&cfg, opts.MemoryTier, repoRoot, home); err != nil {
		return err
	}

	fmt.Fprintln(streams.out, "tackroom setup")
	fmt.Fprintf(streams.out, "config root: %s\n\n", repoRoot)

	skills, roles, mcps, err := scanNativeImports(cfg, detected, repoRoot, home)
	if err != nil {
		return err
	}
	if opts.AssumeYes {
		detection, err := runDetection(cfg, detected, repoRoot, home)
		if err != nil {
			return err
		}
		shared, err := applyReviewDecisions(repoRoot, &cfg, shareAllDecisions(detection), skills, roles, mcps)
		if err != nil {
			return err
		}
		if shared > 0 {
			fmt.Fprintf(streams.out, "imported %d item(s) without prompting (--yes)\n\n", shared)
		}
	} else if reviewTTYAvailable(streams) {
		detection, err := runDetection(cfg, detected, repoRoot, home)
		if err != nil {
			return err
		}
		if err := runReviewImport(repoRoot, &cfg, detection, skills, roles, mcps, streams); err != nil {
			if !errors.Is(err, errReviewAborted) {
				return err
			}
			fmt.Fprintln(streams.out, "import review aborted; continuing setup without importing")
		}
	} else if err := importNativeContent(repoRoot, &cfg, skills, roles, mcps, streams); err != nil {
		return err
	}
	if err := importRootInstructions(repoRoot, detected, home, streams); err != nil {
		return err
	}
	if err := ensureStarterAssets(repoRoot, configPath, opts.MemoryTier); err != nil {
		return err
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
			fmt.Fprintf(streams.out, "%s: config patch failed: %v\n", agent.Name, err)
			continue
		}
		if patched {
			fmt.Fprintf(streams.out, "%s: config patched (added %s)\n", agent.Name, filepath.Join(repoRoot, "skills"))
		} else {
			fmt.Fprintf(streams.out, "%s: config already set\n", agent.Name)
		}
	}
	migrated, err := migrateLegacyMemoryHookPaths(home, repoRoot)
	if err != nil {
		fmt.Fprintf(streams.out, "memory hooks: legacy path migration failed: %v\n", err)
	} else if migrated > 0 {
		fmt.Fprintf(streams.out, "memory hooks: migrated legacy paths in %d config file(s)\n\n", migrated)
	}
	cleaned, err := removeNativeManagedMemoryHooks(home, repoRoot, priorCfg, cfg)
	if err != nil {
		fmt.Fprintf(streams.out, "memory hooks: native cleanup failed: %v\n", err)
	} else if cleaned > 0 {
		fmt.Fprintf(streams.out, "memory hooks: removed previous managed commands from %d native config file(s)\n\n", cleaned)
	}

	offerGitInit(repoRoot, streams)

	syncOpts := opts
	syncOpts.ConfigPath = configPath
	syncOpts.ConfirmRemovals = true
	// Setup adopts this machine: native files that differ from the shared
	// copy are backed up and linked (after a prompt; --yes accepts).
	syncOpts.ReplaceConflicts = true
	if err := runSync(syncOpts); err != nil {
		return err
	}
	if opts.NoCron {
		// --no-cron is an explicit opt-out, recorded so doctor does not warn
		// about a job the user chose not to install.
		if err := setLocalPeriodicSync(configPath, home, false); err != nil {
			fmt.Fprintf(streams.out, "warning: could not record periodic_sync: false: %v\n", err)
		}
		return nil
	}
	// A persisted periodic_sync: false (tackroom.yaml or the local overlay) is
	// the same opt-out; setup must not reinstall the job and reverse it.
	if effective, err := loadConfig(repoRoot, home, ""); err == nil && !periodicSyncEnabled(effective) {
		return nil
	}
	// Setup is the one place besides `tackroom cron` that installs the job.
	// A scheduler that cannot take it must not fail the setup. An installed
	// job is left alone, so a chosen --interval survives.
	if job, _ := readPeriodicSync(); job != nil {
		return nil
	}
	if err := installPeriodicSync(cronOptions{runOptions: syncOpts}); err != nil {
		fmt.Fprintf(streams.out, "warning: periodic sync not installed: %v\nfix and retry: tackroom cron\n", err)
	}
	return nil
}

// offerGitInit proposes versioning a fresh config root. Skipped when the root
// is already inside a git work tree, git is unavailable, or stdin gives no
// answer (non-interactive runs).
func offerGitInit(repoRoot string, streams setupIO) {
	if _, err := exec.LookPath("git"); err != nil {
		return
	}
	inside := exec.Command("git", "-C", repoRoot, "rev-parse", "--is-inside-work-tree")
	if out, err := inside.Output(); err == nil && strings.TrimSpace(string(out)) == "true" {
		return
	}
	if !promptYesNo(streams, fmt.Sprintf("Initialize a git repository in %s to version your configuration?", repoRoot)) {
		return
	}
	if out, err := exec.Command("git", "-C", repoRoot, "init", "-b", "main").CombinedOutput(); err != nil {
		// -b needs git >= 2.28; retry without it before giving up.
		if out2, err2 := exec.Command("git", "-C", repoRoot, "init").CombinedOutput(); err2 != nil {
			fmt.Fprintf(streams.out, "git init failed: %v: %s\n", err, strings.TrimSpace(string(append(out, out2...))))
			return
		}
	}
	if out, err := exec.Command("git", "-C", repoRoot, "add", "-A").CombinedOutput(); err != nil {
		fmt.Fprintf(streams.out, "git add failed: %v: %s\n", err, strings.TrimSpace(string(out)))
		return
	}
	if out, err := exec.Command("git", "-C", repoRoot, "commit", "-m", "initialize agent configuration").CombinedOutput(); err != nil {
		fmt.Fprintf(streams.out, "git commit skipped: %v: %s\n", err, strings.TrimSpace(string(out)))
		fmt.Fprintln(streams.out, "commit manually once git user.name/user.email are configured")
		return
	}
	fmt.Fprintf(streams.out, "initialized git repository in %s; add a private remote to sync across machines\n\n", repoRoot)
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
	if err := runCronPlan(plan, backend, home, legacy, false); err != nil {
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
