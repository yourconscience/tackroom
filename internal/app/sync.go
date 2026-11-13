package app

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func runStatus(opts runOptions) error {
	err := statusReport(opts)
	if !opts.ExitCode {
		return err
	}
	return mapStatusExit(err)
}

func statusReport(opts runOptions) error {
	repoRoot, home, cfg, selected, err := loadContext(opts)
	if err != nil {
		return err
	}
	injectPluginMCPServers(&cfg, home)

	repoReport, err := inspectRepoLink(repoRoot, home)
	if err != nil {
		return err
	}

	expected, err := expectedSkills(repoRoot, home, cfg)
	if err != nil {
		return err
	}
	reports, err := inspectAgents(selected, expected, repoRoot, home, cfg)
	if err != nil {
		return err
	}

	if opts.JSONOutput {
		view := buildStatusJSON(repoRoot, repoReport, reports)
		if err := encodeJSON(setupStreams(opts).out, view); err != nil {
			return err
		}
		if !view.Synced {
			return notSyncedError{"tackroom is not fully synced"}
		}
		return nil
	}
	printStatusReport(setupStreams(opts).out, repoRoot, repoReport, reports, home, cfg, opts.Verbose)
	if err := agentFailures(reports); err != nil {
		return err
	}
	if repoReport.State != stateSynced {
		return notSyncedError{"tackroom is not fully synced"}
	}
	for _, report := range reports {
		if report.Detected && !report.Synced {
			return notSyncedError{report.Name + " is not synced"}
		}
	}

	return nil
}

// notSyncedError marks status drift: the machine is out of sync but nothing
// errored. `status --exit-code` maps it to exit code 1; any other failure is 2.
type notSyncedError struct{ msg string }

func (e notSyncedError) Error() string { return e.msg }

// statusExitError carries the process exit code `status --exit-code` should
// end with: 1 for drift, 2 for a genuine error.
type statusExitError struct {
	code int
	err  error
}

func (e statusExitError) Error() string { return e.err.Error() }
func (e statusExitError) Unwrap() error { return e.err }

func mapStatusExit(err error) error {
	if err == nil {
		return nil
	}
	var drift notSyncedError
	if errors.As(err, &drift) {
		return statusExitError{code: 1, err: err}
	}
	return statusExitError{code: 2, err: err}
}

// ExitCode maps a command error to a process exit code: 0 for nil, the carried
// code for a status --exit-code result, and 1 for any other error.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var se statusExitError
	if errors.As(err, &se) {
		return se.code
	}
	return 1
}

func runSync(opts runOptions) error {
	if opts.DryRun {
		return runSyncDryRun(opts)
	}
	repoRoot, home, cfg, selected, err := loadContext(opts)
	if err != nil {
		return err
	}

	repoReport, err := inspectRepoLink(repoRoot, home)
	if err != nil {
		return err
	}
	if repoReport.State == stateConflict {
		printReport(setupStreams(opts).out, false, repoReport, nil, home, cfg)
		return fmt.Errorf("sync aborted due to conflicts: repo link: %s", repoReport.Path)
	}
	linkingRepo := repoReport.State != stateSynced
	if err := applyRepoLink(repoReport); err != nil {
		return err
	}

	if err := syncExternalRepos(cfg.ExternalSkills, home, repoRoot); err != nil {
		return err
	}
	// Re-inject after clone so first sync picks up newly fetched plugins.
	injectPluginMCPServers(&cfg, home)
	if err := renderCommittedArtifacts(repoRoot); err != nil {
		return err
	}

	streams := setupStreams(opts)
	jsonOut := streams.out
	if opts.JSONOutput {
		// The text report is replaced by one JSON document at the end.
		streams.out = io.Discard
	}
	starterChanges, err := reconcileStarterFiles(repoRoot, streams, opts.ConfirmRemovals)
	if err != nil {
		return err
	}
	starterChanges.report(streams.out)

	toolInstalls, err := installMemoryTools(repoRoot)
	if err != nil {
		return err
	}
	if len(toolInstalls) > 0 {
		fmt.Fprintf(streams.out, "memory tools installed: %s\n", strings.Join(toolInstalls, ", "))
	}

	expected, err := expectedSkills(repoRoot, home, cfg)
	if err != nil {
		return err
	}
	inspected, err := inspectAgents(selected, expected, repoRoot, home, cfg)
	if err != nil {
		return err
	}
	reports := readableReports(inspected)
	if opts.ReplaceConflicts {
		if err := replaceAllConflicts(reports, home, streams, opts.ConfirmRemovals); err != nil {
			return err
		}
	}
	if opts.ConfirmRemovals {
		confirmDestructiveSyncActions(reports, streams)
	}
	preflight := cloneReports(reports)

	// An agent with conflicts is left untouched; every other agent still syncs.
	var conflicts []string
	runnable := make([]agentReport, 0, len(reports))
	for _, report := range reports {
		if len(report.Conflicts) == 0 {
			runnable = append(runnable, report)
			continue
		}
		for _, conflict := range report.Conflicts {
			conflicts = append(conflicts, fmt.Sprintf("%s: %s", report.Name, conflict))
		}
	}
	reports = runnable
	if err := applyAgentSync(reports, cfg, repoRoot, home); err != nil {
		return err
	}
	if err := applyAgentRoleSync(reports, selected, repoRoot); err != nil {
		return err
	}
	if err := applyAgentMCPSync(reports, cfg, home); err != nil {
		return err
	}
	if err := applyAgentHookSync(reports, cfg, home); err != nil {
		return err
	}
	if err := applyAgentRootInstructionSync(reports); err != nil {
		return err
	}
	if err := applyAgentPackageSync(reports, selected, home); err != nil {
		return err
	}
	if err := applyClaudePluginSync(reports, repoRoot, home); err != nil {
		return err
	}

	repoReport, err = inspectRepoLink(repoRoot, home)
	if err != nil {
		return err
	}
	reports, err = inspectAgents(selected, expected, repoRoot, home, cfg)
	if err != nil {
		return err
	}
	restoreSyncActions(reports, preflight)
	repoReport.Linked = linkingRepo && repoReport.State == stateSynced

	printReport(streams.out, true, repoReport, reports, home, cfg)
	if opts.JSONOutput {
		view := buildStatusJSON(repoRoot, repoReport, reports)
		for _, c := range conflicts {
			view.Synced = false
			view.Agents = appendConflict(view.Agents, c)
		}
		if len(conflicts) > 0 {
			view.Hint = conflictHint(home)
		}
		if err := encodeJSON(jsonOut, view); err != nil {
			return err
		}
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("left %d conflict(s) in place; the other agents synced: %s\n%s",
			len(conflicts), strings.Join(conflicts, "; "), conflictHint(home))
	}
	return agentFailures(reports)
}

// runSyncDryRun computes the same plan a real sync would apply, through the
// same inspection code, and prints it without writing anything or fetching
// external sources.
func runSyncDryRun(opts runOptions) error {
	repoRoot, home, cfg, selected, err := loadContext(opts)
	if err != nil {
		return err
	}

	// A dry run never fetches or clones. External sources whose cache is
	// absent are dropped from the plan and reported as "not fetched" below.
	var notFetched []string
	cfg, notFetched = withoutMissingSources(cfg, home)

	injectPluginMCPServers(&cfg, home)

	repoReport, err := inspectRepoLink(repoRoot, home)
	if err != nil {
		return err
	}

	expected, err := expectedSkills(repoRoot, home, cfg)
	if err != nil {
		return err
	}
	reports, err := inspectAgents(selected, expected, repoRoot, home, cfg)
	if err != nil {
		return err
	}

	streams := setupStreams(opts)
	printReport(streams.out, false, repoReport, reports, home, cfg)

	for _, name := range notFetched {
		fmt.Fprintf(streams.out, "not fetched: %s\n", name)
	}
	if opts.ReplaceConflicts {
		listReplaceableConflicts(streams.out, reports, home)
	}

	changes, harnesses := countSyncChanges(repoReport, reports)
	fmt.Fprintf(streams.out, "\ndry run: %d changes across %d harnesses; nothing written\n", changes, harnesses)
	return nil
}

// withoutMissingSources drops non-materialized external sources whose cache is
// absent (a real sync would clone them) and returns their names. Materialized
// sources' skills are committed in the repo, so they stay in the plan.
func withoutMissingSources(cfg config, home string) (config, []string) {
	var missing []string
	kept := make([]externalSkillSource, 0, len(cfg.ExternalSkills))
	for _, src := range cfg.ExternalSkills {
		if !src.Materialize && !hasDir(filepath.Join(externalCacheDir(home), sourceName(src), ".git")) {
			missing = append(missing, sourceName(src))
			continue
		}
		kept = append(kept, src)
	}
	cfg.ExternalSkills = kept
	return cfg, missing
}

// listReplaceableConflicts prints what `sync --replace-conflicts` would move
// into a backup, without moving anything.
func listReplaceableConflicts(w io.Writer, reports []agentReport, home string) {
	var paths []string
	for _, report := range reports {
		for _, item := range report.Replaceable {
			paths = append(paths, item.Path)
		}
	}
	if len(paths) == 0 {
		return
	}
	backupDir := newConflictBackupDir(home)
	fmt.Fprintf(w, "\n%d existing file(s) differ from the shared copy and would be moved to %s and replaced with links:\n", len(paths), backupDir)
	for _, path := range paths {
		fmt.Fprintf(w, "  %s\n", path)
	}
}

// countSyncChanges tallies the individual changes a sync would apply, and the
// number of harnesses they touch. Conflicts are blockers, not changes, and are
// reported separately by the plan.
func countSyncChanges(repoReport repoLinkReport, reports []agentReport) (changes, harnesses int) {
	if repoReport.State != stateSynced {
		changes++
	}
	for _, report := range reports {
		if report.Error != "" || !report.Detected {
			continue
		}
		n := len(report.Adds) + len(report.Updates) + len(report.Removes) +
			len(report.AddsAgent) + len(report.UpdatesAgent) + len(report.RemovesAgent) +
			len(report.AddsMCP) + len(report.UpdatesMCP) + len(report.RemovesMCP) +
			len(report.AddsHook) + len(report.UpdatesHook) +
			len(report.UpdatesPackage) + len(report.RemovesPackage) +
			len(report.AddsPlugin) + len(report.RemovesPlugin)
		if report.RootState != "" && report.RootState != stateSynced {
			n++
		}
		changes += n
		if n > 0 {
			harnesses++
		}
	}
	return changes, harnesses
}

// replaceAllConflicts backs up and clears every replaceable conflict. When
// confirm is set (setup), it lists them and asks first; --yes accepts.
func replaceAllConflicts(reports []agentReport, home string, streams setupIO, confirm bool) error {
	var pending []string
	for _, report := range reports {
		for _, item := range report.Replaceable {
			pending = append(pending, item.Path)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	backupDir := newConflictBackupDir(home)
	if confirm {
		fmt.Fprintf(streams.out, "\n%d existing file(s) differ from the shared copy and will be replaced with links:\n", len(pending))
		for _, path := range pending {
			fmt.Fprintf(streams.out, "  %s\n", path)
		}
		if !promptYesNo(streams, fmt.Sprintf("Move them to %s and link the shared copies?", backupDir)) {
			fmt.Fprintln(streams.out, "keeping them; those agents are skipped until the conflicts are resolved")
			return nil
		}
	}
	var moved []string
	for i := range reports {
		paths, err := replaceConflicts(&reports[i], backupDir, home)
		moved = append(moved, paths...)
		if err != nil {
			return err
		}
	}
	fmt.Fprintf(streams.out, "backed up %d file(s) to %s\n", len(moved), backupDir)
	return nil
}

func applyAgentRootInstructionSync(reports []agentReport) error {
	for _, report := range reports {
		if !report.Detected || report.RootPath == "" {
			continue
		}
		switch report.RootState {
		case stateSynced:
			continue
		case stateMissing:
			if err := os.MkdirAll(filepath.Dir(report.RootPath), 0o755); err != nil {
				return fmt.Errorf("create %s: %w", filepath.Dir(report.RootPath), err)
			}
		case stateDrifted:
			// Removing first means a write never follows an old link into
			// AGENTS.md itself.
			if err := os.Remove(report.RootPath); err != nil {
				return fmt.Errorf("remove %s before relink: %w", report.RootPath, err)
			}
		case stateConflict:
			return fmt.Errorf("%s is not a symlink", report.RootPath)
		default:
			return fmt.Errorf("unsupported root instruction state %q for %s", report.RootState, report.Name)
		}
		if report.RootContent != nil {
			// 0600: a copy of a private source must not be readable by others.
			if err := os.WriteFile(report.RootPath, report.RootContent, 0o600); err != nil {
				return fmt.Errorf("write %s: %w", report.RootPath, err)
			}
			continue
		}
		if err := os.Symlink(report.RootExpected, report.RootPath); err != nil {
			return fmt.Errorf("symlink %s -> %s: %w", report.RootPath, report.RootExpected, err)
		}
	}
	return nil
}

func applyRepoLink(report repoLinkReport) error {
	switch report.State {
	case stateSynced:
		return nil
	case stateMissing:
		return os.Symlink(report.ExpectedTarget, report.Path)
	case stateDrifted:
		if err := os.Remove(report.Path); err != nil {
			return fmt.Errorf("remove %s: %w", report.Path, err)
		}
		return os.Symlink(report.ExpectedTarget, report.Path)
	case stateConflict:
		return fmt.Errorf("%s is not a symlink", report.Path)
	default:
		return fmt.Errorf("unsupported repo link state %q", report.State)
	}
}

func applyAgentSync(reports []agentReport, cfg config, repoRoot string, home string) error {
	for _, report := range reports {
		if !report.Detected {
			continue
		}
		if len(report.Conflicts) > 0 {
			return fmt.Errorf("%s has conflicts", report.Name)
		}
		h := harnessFor(report.Name)
		if h != nil && h.Skills == skillsConfigDriven {
			if err := applyConfigDrivenSkillDrift(report, h, cfg, repoRoot, home); err != nil {
				return err
			}
			continue
		}
		if h != nil && h.SkillsNativeRoot != nil && h.SkillsNativeRoot(repoRoot, home) {
			// Skills are read directly from the config root; no mirror to write.
			continue
		}

		if err := os.MkdirAll(report.SkillRoot, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", report.SkillRoot, err)
		}

		for _, name := range report.Removes {
			path := filepath.Join(report.SkillRoot, name)
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("remove %s: %w", path, err)
			}
		}

		if err := linkExpectedSkills(report); err != nil {
			return err
		}
	}
	return nil
}

func linkExpectedSkills(report agentReport) error {
	if len(report.Adds)+len(report.Updates) == 0 {
		return nil
	}
	if err := os.MkdirAll(report.SkillRoot, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", report.SkillRoot, err)
	}
	for _, name := range append(append([]string{}, report.Adds...), report.Updates...) {
		path := filepath.Join(report.SkillRoot, name)
		if info, err := os.Lstat(path); err == nil {
			remove := os.Remove
			if info.IsDir() {
				// Only identical copies reach here as directories (inspect
				// reports differing ones as conflicts), so nothing is lost.
				remove = os.RemoveAll
			}
			if err := remove(path); err != nil {
				return fmt.Errorf("remove %s before relink: %w", path, err)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
		target, ok := report.ExpectedSkills[name]
		if !ok {
			return fmt.Errorf("%s expected skill %q has no target", report.Name, name)
		}
		if err := os.Symlink(target, path); err != nil {
			return fmt.Errorf("symlink %s -> %s: %w", path, target, err)
		}
	}
	return nil
}

func applyConfigDrivenSkillDrift(report agentReport, h *harness, cfg config, repoRoot string, home string) error {
	if h.Setup == nil || (len(report.Adds) == 0 && len(report.Updates) == 0 && len(report.Removes) == 0) {
		return nil
	}
	if _, err := h.Setup(home, repoRoot, cfg); err != nil {
		return fmt.Errorf("%s config-driven skill sync: %w", report.Name, err)
	}

	return nil
}
