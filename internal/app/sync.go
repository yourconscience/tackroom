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
			return errors.New("tackroom is not fully synced")
		}
		return nil
	}
	printStatusReport(setupStreams(opts).out, repoRoot, repoReport, reports, home, cfg, opts.Verbose)
	if err := agentFailures(reports); err != nil {
		return err
	}
	if repoReport.State != stateSynced {
		return errors.New("tackroom is not fully synced")
	}
	for _, report := range reports {
		if report.Detected && !report.Synced {
			return fmt.Errorf("%s is not synced", report.Name)
		}
	}

	return nil
}

func runSync(opts runOptions) error {
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
			if err := os.Remove(report.RootPath); err != nil {
				return fmt.Errorf("remove %s before relink: %w", report.RootPath, err)
			}
		case stateConflict:
			return fmt.Errorf("%s is not a symlink", report.RootPath)
		default:
			return fmt.Errorf("unsupported root instruction state %q for %s", report.RootState, report.Name)
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
