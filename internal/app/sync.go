package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// statusGates is what status settles before it looks at any harness: the lock
// and the external sources and packages sync would put in cooldown or block.
type statusGates struct {
	repoRoot string
	home     string
	cfg      config // gated sources dropped, MCP servers in cooldown marked
	selected []agentConfig
	sources  []externalSkillSource // as configured, gated ones included
	lock     lockFile
	gated    map[string]gatedItem // gated sources by name; packages in cooldown as "package-gate:"+item
}

// loadStatusGates reads the config and the lock and applies the cooldown and
// audit gates with no registry lookup and no lock write. createPluginData
// keeps the plugin data directories status has always created; a read-only
// caller passes false.
func loadStatusGates(opts runOptions, createPluginData bool) (statusGates, error) {
	repoRoot, home, cfg, selected, err := loadContext(opts)
	if err != nil {
		return statusGates{}, err
	}
	return applyStatusGates(repoRoot, home, cfg, selected, createPluginData)
}

// applyStatusGates is loadStatusGates for a config already loaded, such as the
// view's snapshot, so the view shows what status shows.
func applyStatusGates(repoRoot, home string, cfg config, selected []agentConfig, createPluginData bool) (statusGates, error) {
	statusLock, err := readLockFile(repoRoot)
	if err != nil {
		return statusGates{}, err
	}
	sources := cfg.ExternalSkills
	gated := gateExternalSources(sources, home, &statusLock, gateOptions{now: timeNow()})
	cfg.ExternalSkills = filterGatedSources(sources, gated)
	cfg.configuredSources = sources
	injectPluginMCPServersMode(&cfg, home, createPluginData)
	pkgGated, err := gatePackages(&cfg, selected, repoRoot, home, gateOptions{now: timeNow()}, false)
	if err != nil {
		return statusGates{}, err
	}
	for k, v := range pkgGated {
		gated["package-gate:"+k] = v
	}
	for k, v := range pendingUpdateGates(sources, home, statusLock, timeNow()) {
		gated[k] = v
	}
	return statusGates{repoRoot: repoRoot, home: home, cfg: cfg, selected: selected, sources: sources, lock: statusLock, gated: gated}, nil
}

// inspectStatus compares the repo link and every selected harness with what
// sync would write.
func inspectStatus(g statusGates) (repoLinkReport, []agentReport, error) {
	repoReport, err := inspectRepoLink(g.repoRoot, g.home)
	if err != nil {
		return repoLinkReport{}, nil, err
	}
	inspectCfg, err := configForInspection(g.cfg, g.sources, g.gated, g.repoRoot, g.home)
	if err != nil {
		return repoLinkReport{}, nil, err
	}
	expected, err := expectedSkills(g.repoRoot, g.home, inspectCfg)
	if err != nil {
		return repoLinkReport{}, nil, err
	}
	reports, err := inspectAgents(g.selected, expected, g.repoRoot, g.home, inspectCfg)
	if err != nil {
		return repoLinkReport{}, nil, err
	}
	return repoReport, reports, nil
}

func statusReport(opts runOptions) error {
	g, err := loadStatusGates(opts, true)
	if err != nil {
		return err
	}
	repoReport, reports, err := inspectStatus(g)
	if err != nil {
		return err
	}
	repoRoot, home, cfg, gated := g.repoRoot, g.home, g.cfg, g.gated

	if opts.JSONOutput {
		view := buildStatusJSON(repoRoot, repoReport, reports)
		view.Skipped = skippedJSON(gated)
		if err := encodeJSON(setupStreams(opts).out, view); err != nil {
			return err
		}
		if err := agentFailures(reports); err != nil {
			return err
		}
		if !view.Synced {
			return notSyncedError{"tackroom is not fully synced"}
		}
		return nil
	}
	printStatusReport(setupStreams(opts).out, repoRoot, repoReport, reports, home, cfg, opts.Verbose)
	printGatedSection(setupStreams(opts).out, gated)
	for _, problem := range periodicSyncProblems(repoRoot, home, cfg) {
		fmt.Fprintln(setupStreams(opts).out, "warning: "+problem)
	}
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

// printGatedSection lists items in cooldown and blocked items; status and sync --dry-run
// share it so both show the same lines.
func printGatedSection(w io.Writer, gated map[string]gatedItem) {
	lines := gatedLines(gated)
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(w, "\nin cooldown or blocked (skipped by sync)")
	for _, line := range lines {
		fmt.Fprintf(w, "  %s\n", line)
	}
}

// printDryGateNotes reports what a dry run could not decide without a registry
// lookup and what --allow-fresh would let through.
func printDryGateNotes(w io.Writer, notes *dryGateNotes) {
	if len(notes.unchecked) > 0 {
		fmt.Fprintln(w, "\nminimum release age not checked (a dry run makes no registry lookup; a real sync may put these in cooldown)")
		for _, item := range sortedKeys(notes.unchecked) {
			fmt.Fprintf(w, "  %s %s\n", item, notes.unchecked[item])
		}
	}
	if len(notes.released) > 0 {
		fmt.Fprintln(w, "\nwould skip the cooldown with --allow-fresh (nothing recorded)")
		for _, item := range sortedKeys(notes.released) {
			fmt.Fprintf(w, "  %s %s\n", item, notes.released[item])
		}
	}
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
	if err := checkSharedLock(opts.ConfigPath, home, cfg); err != nil {
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

	sources := cfg.ExternalSkills
	gate := gateOptionsFor(opts)
	gated, err := syncExternalReposGated(sources, home, repoRoot, ptrGate(gate))
	if err != nil {
		return err
	}
	cfg.ExternalSkills = filterGatedSources(sources, gated)
	// Re-inject after clone so first sync picks up newly fetched plugins.
	injectPluginMCPServers(&cfg, home)
	pkgGated, err := gatePackages(&cfg, selected, repoRoot, home, gate, true)
	if err != nil {
		return err
	}
	for k, v := range pkgGated {
		gated["package-gate:"+k] = v
	}
	lock, err := readLockFile(repoRoot)
	if err != nil {
		return err
	}
	for k, v := range pendingUpdateGates(sources, home, lock, timeNow()) {
		gated[k] = v
	}
	warnUnmatchedAllowFresh(os.Stderr, gate, gated)
	if err := renderCommittedArtifacts(repoRoot); err != nil {
		return err
	}

	streams := setupStreams(opts)
	jsonOut := streams.out
	if opts.JSONOutput {
		// The text report is replaced by one JSON document at the end.
		streams.out = io.Discard
	}
	reportOut := streams.out
	if opts.setupResult != nil && !opts.Verbose {
		// Setup prints its own summary instead of the report.
		reportOut = io.Discard
	}
	keepMemory := memoryHooksInUse(cfg, repoRoot, home)
	starterChanges, err := reconcileStarterFiles(repoRoot, streams, opts.ConfirmRemovals, keepMemory)
	if err != nil {
		return err
	}
	if !keepMemory {
		cfg.retiredMemoryHook = retiredMemoryScripts(repoRoot, home, starterChanges.Removed)
	}
	starterChanges.report(reportOut)
	if starterChanges.Released {
		fmt.Fprintf(os.Stderr, "memory/ in %s is yours now: tackroom no longer ships or updates it. See https://yourconscience.github.io/tackroom/docs/memory/\n", displayPath(repoRoot, home))
	}

	inspectCfg, err := configForInspection(cfg, sources, gated, repoRoot, home)
	if err != nil {
		return err
	}
	expected, err := expectedSkills(repoRoot, home, inspectCfg)
	if err != nil {
		return err
	}
	inspected, err := inspectAgents(selected, expected, repoRoot, home, inspectCfg)
	if err != nil {
		return err
	}
	reports := readableReports(inspected)
	var backupDir string
	var moved []string
	if opts.ReplaceConflicts {
		if backupDir, moved, err = replaceAllConflicts(reports, home, streams, opts.ConfirmRemovals); err != nil {
			return err
		}
	}
	if opts.ConfirmRemovals {
		confirmDestructiveSyncActions(reports, streams)
	}
	preflight := cloneReports(reports)
	changes, _ := countSyncChanges(repoReport, preflight, false)
	changes += len(starterChanges.Scaffolded) + len(starterChanges.Updated) + len(starterChanges.Removed)

	// An agent with conflicts is left untouched and every other agent still
	// syncs; a role conflict leaves only that role file alone.
	var conflicts, skipped []string
	replaceableLeft := false
	runnable := make([]agentReport, 0, len(reports))
	for _, report := range reports {
		replaceableLeft = replaceableLeft || len(report.Replaceable) > 0
		for _, conflict := range report.Conflicts {
			conflicts = append(conflicts, fmt.Sprintf("%s: %s", report.Name, conflict))
		}
		if len(blockingConflicts(report)) == 0 {
			runnable = append(runnable, report)
		} else {
			skipped = append(skipped, report.Name)
		}
	}
	reports = runnable
	if identical := len(replaceableIdenticalPaths(reports)); identical > 0 {
		if backupDir == "" {
			backupDir = newConflictBackupDir(home)
		}
		copies, err := backupIdenticalCopies(reports, backupDir, home)
		moved = append(moved, copies...)
		if err != nil {
			return err
		}
		fmt.Fprintf(streams.out, "backed up %d identical file(s) to %s before linking them\n", len(copies), backupDir)
	}
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
	if err := applyAgentSettingsSync(reports, selected, home); err != nil {
		return err
	}
	if err := applyClaudePluginSync(reports, repoRoot, home); err != nil {
		return err
	}
	if err := applyCodexPluginSync(reports, inspectCfg, home); err != nil {
		return err
	}

	repoReport, err = inspectRepoLink(repoRoot, home)
	if err != nil {
		return err
	}
	reports, err = inspectAgents(selected, expected, repoRoot, home, inspectCfg)
	if err != nil {
		return err
	}
	// An agent sync skipped changed nothing; its report must not say it did.
	for i := range preflight {
		if stringInSlice(preflight[i].Name, skipped) {
			preflight[i] = agentReport{Name: preflight[i].Name}
		}
	}
	restoreSyncActions(reports, preflight)
	repoReport.Linked = linkingRepo && repoReport.State == stateSynced

	printReport(reportOut, true, repoReport, reports, home, cfg)
	printGatedSummary(reportOut, gated)
	if opts.setupResult != nil {
		*opts.setupResult = setupSyncResult{
			done: true, repo: repoReport, reports: reports, conflicts: conflicts,
			changes: changes, backupDir: backupDir, moved: moved, gated: len(gated),
		}
	}
	if opts.JSONOutput {
		view := buildStatusJSON(repoRoot, repoReport, reports)
		view.Skipped = skippedJSON(gated)
		for _, c := range conflicts {
			view.Synced = false
			view.Agents = appendConflict(view.Agents, c)
		}
		if replaceableLeft {
			view.Hint = conflictHint(home)
		}
		if err := encodeJSON(jsonOut, view); err != nil {
			return err
		}
	}
	if len(conflicts) > 0 {
		hint := ""
		if replaceableLeft {
			// --replace-conflicts would resolve at least one of them.
			hint = "\n" + conflictHint(home)
		}
		// Say exactly what did not sync: a role conflict leaves only that
		// role file; any other conflict skips the whole agent.
		outcome := "synced everything else"
		if len(skipped) > 0 {
			outcome = fmt.Sprintf("skipped %s entirely (a conflicting skill folder, instructions file or plugin setting keeps sync away from the whole agent) and synced everything else", joinAnd(skipped))
		}
		return fmt.Errorf("left %d conflict(s) in place: %s\n%s%s",
			len(conflicts), strings.Join(conflicts, "; "), outcome, hint)
	}
	return agentFailures(reports)
}

// runSyncDryRun computes the same plan a real sync would apply, through the
// same inspection code, and prints it without writing anything or fetching
// external sources.
func runSyncDryRun(opts runOptions) error {
	if opts.JSONOutput && opts.ConfigOverride == nil {
		// The digest below covers the config files this plan was read from.
		var err error
		if opts, err = withPlanConfig(opts); err != nil {
			return err
		}
	}
	repoRoot, home, cfg, selected, err := loadContext(opts)
	if err != nil {
		return err
	}
	if err := checkSharedLock(opts.ConfigPath, home, cfg); err != nil {
		return err
	}

	// A dry run never fetches or clones. External sources whose cache is
	// absent are dropped from the plan and reported as "not fetched" below.
	var notFetched []string
	cfg, notFetched = withoutMissingSources(cfg, home)

	// Items in cooldown and blocked items come from the local cache and the lock only: a
	// dry run reads the lock, never writes it, and makes no registry lookup.
	lock, err := readLockFile(repoRoot)
	if err != nil {
		return err
	}
	gate := gateOptionsFor(opts)
	gate.dry = newDryGateNotes()
	sources := cfg.ExternalSkills
	gated := gateExternalSources(sources, home, &lock, gate)
	cfg.ExternalSkills = filterGatedSources(sources, gated)
	injectPluginMCPServersMode(&cfg, home, false)
	pkgGated, err := gatePackages(&cfg, selected, repoRoot, home, gate, false)
	if err != nil {
		return err
	}
	for k, v := range pkgGated {
		gated["package-gate:"+k] = v
	}
	for k, v := range pendingUpdateGates(sources, home, lock, gate.now) {
		gated[k] = v
	}
	if opts.PlanDigest == "" {
		// With --plan-digest this is the plan check; the sync that follows warns.
		warnUnmatchedAllowFresh(os.Stderr, gate, gated)
	}

	repoReport, err := inspectRepoLink(repoRoot, home)
	if err != nil {
		return err
	}
	if repoReport.State == stateConflict {
		if opts.JSONOutput {
			// The reason rides on stderr; stdout stays one JSON document.
			if err := encodeJSON(setupStreams(opts).out, buildStatusJSON(repoRoot, repoReport, nil)); err != nil {
				return err
			}
		} else {
			printReport(setupStreams(opts).out, false, repoReport, nil, home, cfg)
		}
		return fmt.Errorf("sync aborted due to conflicts: repo link: %s", repoReport.Path)
	}

	repoReport.Linked = repoReport.State != stateSynced

	// Starter files a real sync would scaffold, refresh or retire. Read before
	// the harnesses, since a retired memory script retires its hooks too.
	keepMemory := memoryHooksInUse(cfg, repoRoot, home)
	starterPlan, err := previewStarterFiles(repoRoot, keepMemory)
	if err != nil {
		return err
	}
	if !keepMemory {
		cfg.retiredMemoryHook = retiredMemoryScripts(repoRoot, home, starterPlan.Removed)
	}

	inspectCfg, err := configForInspection(cfg, sources, gated, repoRoot, home)
	if err != nil {
		return err
	}
	expected, err := expectedSkills(repoRoot, home, inspectCfg)
	if err != nil {
		return err
	}
	expected = withPendingMaterialized(expected, cfg.ExternalSkills, home)
	reports, err := inspectAgents(selected, expected, repoRoot, home, inspectCfg)
	if err != nil {
		return err
	}
	for i := range reports {
		// A real sync links or rewrites the root doc; say so in the plan headline.
		r := &reports[i]
		r.RootLinked = r.Detected && len(blockingConflicts(*r)) == 0 && r.RootPath != "" && (r.RootState == stateMissing || r.RootState == stateDrifted)
	}

	changes, harnesses := countSyncChanges(repoReport, reports, opts.ReplaceConflicts)
	changes += len(starterPlan.Scaffolded) + len(starterPlan.Updated) + len(starterPlan.Removed)

	streams := setupStreams(opts)
	if opts.JSONOutput {
		// One JSON document replaces the report and every plan section.
		var replaceable []string
		if opts.ReplaceConflicts {
			replaceable = replaceableConflictPaths(reports)
		}
		view := buildDryRunJSON(repoRoot, repoReport, reports, starterPlan, gated, gate.dry, notFetched, replaceable, changes, harnesses)
		view.Plan = nonNil(summarizeSyncPlan(reports))
		view.Destructive = nonNil(destructiveSyncItems(reports))
		// Starter files count as changes, so the plan names them too.
		var starter []string
		for _, path := range starterPlan.Scaffolded {
			starter = append(starter, "add "+path)
		}
		for _, path := range starterPlan.Updated {
			starter = append(starter, "update "+path)
		}
		for _, path := range starterPlan.Removed {
			starter = append(starter, "remove "+path)
			view.Destructive = append(view.Destructive, "starter files: remove "+path)
		}
		if len(starter) > 0 {
			view.Plan = append(view.Plan, "starter files: "+strings.Join(starter, ", "))
		}
		// The digest covers the whole document and the config files it was
		// planned from, so `sync --plan-digest` applies only the plan that was
		// shown, from that config. Skill and role file bodies are not covered.
		data, err := json.Marshal(view)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(append(data, opts.configRevision...))
		view.PlanDigest = hex.EncodeToString(digest[:])
		return encodeJSON(streams.out, view)
	}

	starterPlan.report(streams.out)
	printReport(streams.out, false, repoReport, reports, home, cfg)

	printGatedSection(streams.out, gated)
	printDryGateNotes(streams.out, gate.dry)
	for _, name := range notFetched {
		fmt.Fprintf(streams.out, "not fetched: %s\n", name)
	}
	if opts.ReplaceConflicts {
		listReplaceableConflicts(streams.out, reports, home)
	}
	if identical := replaceableIdenticalPaths(reports); len(identical) > 0 {
		fmt.Fprintf(streams.out, "\n%d file(s) identical to the shared copy would be moved to %s and replaced by links:\n", len(identical), newConflictBackupDir(home))
		for _, path := range identical {
			fmt.Fprintf(streams.out, "  %s\n", path)
		}
	}

	fmt.Fprintf(streams.out, "\ndry run: %s across %s; nothing written\n", plural(changes, "change"), plural(harnesses, "harness"))
	return nil
}

// syncDryRunJSON is the `sync --dry-run --json` document: the status view plus
// what only a plan computes.
type syncDryRunJSON struct {
	ConfigRoot         string            `json:"config_root"`
	DryRun             bool              `json:"dry_run"`
	Synced             bool              `json:"synced"`
	RepoLink           string            `json:"repo_link"`
	Changes            int               `json:"changes"`
	Harnesses          int               `json:"harnesses"`
	Agents             []agentStatusJSON `json:"agents"`
	StarterFiles       starterPlanJSON   `json:"starter_files"`
	Skipped            []gatedItemJSON   `json:"skipped"`
	NotFetched         []string          `json:"not_fetched"`
	FreshnessUnchecked map[string]string `json:"freshness_unchecked,omitempty"`
	WouldRelease       map[string]string `json:"would_release_with_allow_fresh,omitempty"`
	Replaceable        []string          `json:"replaceable_conflicts"`
	// Plan is one line per agent in the web view's words; Destructive lists
	// what a sync removes or overwrites; PlanDigest is what --plan-digest takes.
	Plan        []string `json:"plan"`
	Destructive []string `json:"destructive"`
	PlanDigest  string   `json:"plan_digest"`
}

// withPlanConfig reads the config once and pins it on opts, with the revision
// of the shared file and the local overlay it came from, so a plan and the
// sync that applies it work from one config.
func withPlanConfig(opts runOptions) (runOptions, error) {
	doc, _, err := openConfigDocument(configCommandOptions{ConfigPath: opts.ConfigPath})
	if err != nil {
		return opts, err
	}
	opts.ConfigOverride = &doc.effective
	opts.configRevision = doc.revision(configLayerShared) + ":" + doc.revision(configLayerLocal)
	return opts, nil
}

// runSyncPlanned applies a plan only while it is still the one shown: it runs
// the same dry run again and syncs when its digest matches, as the web view's
// Sync does with its preview. Like the web view, it syncs the config it
// planned from, not one read again after the check.
func runSyncPlanned(opts runOptions) error {
	opts, err := withPlanConfig(opts)
	if err != nil {
		return err
	}
	var shown bytes.Buffer
	dry := opts
	dry.DryRun, dry.JSONOutput, dry.Stdout = true, true, &shown
	if err := runSyncDryRun(dry); err != nil {
		return err
	}
	var plan struct {
		PlanDigest string `json:"plan_digest"`
	}
	if err := json.Unmarshal(shown.Bytes(), &plan); err != nil {
		return err
	}
	if plan.PlanDigest != opts.PlanDigest {
		return errors.New("the sync plan or the config changed since it was shown; load it again with tackroom sync --dry-run --json")
	}
	return runSync(opts)
}

type starterPlanJSON struct {
	Scaffolded   []string `json:"scaffolded"`
	Updated      []string `json:"updated"`
	Removed      []string `json:"removed"`
	KeptModified []string `json:"kept_modified"`
}

type gatedItemJSON struct {
	Item    string `json:"item"`
	Kind    string `json:"kind"`
	Reason  string `json:"reason"`
	Until   string `json:"until,omitempty"`
	Command string `json:"command"`
}

// skippedJSON lists the items sync skips, in the order of their keys in gated;
// an empty list is [], never null.
func skippedJSON(gated map[string]gatedItem) []gatedItemJSON {
	out := []gatedItemJSON{}
	for _, name := range sortedKeysNative(gated) {
		out = append(out, gatedItemJSON(gated[name]))
	}
	return out
}

func buildDryRunJSON(repoRoot string, repoReport repoLinkReport, reports []agentReport, starter starterChanges, gated map[string]gatedItem, notes *dryGateNotes, notFetched []string, replaceable []string, changes, harnesses int) syncDryRunJSON {
	status := buildStatusJSON(repoRoot, repoReport, reports)
	out := syncDryRunJSON{
		ConfigRoot: status.ConfigRoot,
		DryRun:     true,
		Synced:     status.Synced,
		RepoLink:   status.RepoLink,
		Changes:    changes,
		Harnesses:  harnesses,
		Agents:     status.Agents,
		StarterFiles: starterPlanJSON{
			Scaffolded:   nonNil(starter.Scaffolded),
			Updated:      nonNil(starter.Updated),
			Removed:      nonNil(starter.Removed),
			KeptModified: nonNil(starter.KeptModified),
		},
		Skipped:     skippedJSON(gated),
		NotFetched:  nonNil(notFetched),
		Replaceable: nonNil(replaceable),
	}
	if len(notes.unchecked) > 0 {
		out.FreshnessUnchecked = notes.unchecked
	}
	if len(notes.released) > 0 {
		out.WouldRelease = notes.released
	}
	return out
}

// configForInspection returns cfg with the previously pinned gated sources put
// back for the inspection pass. Their caches were restored to the previous
// pin, so their skills stay in the desired set; without them an installed link
// would look stale and sync would remove it even though the gated copy is what
// got synced. A source that was never pinned installs nothing and stays out,
// and the write path keeps the filtered config: a gated source contributes no
// new content.
func configForInspection(cfg config, sources []externalSkillSource, gated map[string]gatedItem, repoRoot string, home string) (config, error) {
	if len(gated) == 0 {
		return cfg, nil
	}
	lock, err := readLockFile(repoRoot)
	if err != nil {
		return cfg, err
	}
	out := cfg
	out.ExternalSkills = append([]externalSkillSource(nil), cfg.ExternalSkills...)
	for _, src := range sources {
		if _, ok := gated[sourceName(src)]; !ok {
			continue
		}
		if findLockEntry(lock, sourceName(src)) == nil {
			continue
		}
		if !src.Materialize {
			// A gated source whose restored cache still cannot be read keeps
			// its skills out of the plan rather than failing the whole run;
			// one blocked for a missing skill directory keeps the others.
			src = withoutMissingSkillDirs(src, home)
			if _, err := discoverExternalSourceSkills(src, home); err != nil {
				continue
			}
		}
		out.ExternalSkills = append(out.ExternalSkills, src)
	}
	return out, nil
}

// withPendingMaterialized adds the skills a real sync would copy into the config
// root from a materialized source's checkout and that are not there yet, so the
// plan links them as the sync will. Their path is the checkout's, because the
// copy does not exist before the sync. A source whose checkout is absent adds
// nothing: a real sync clones it first.
func withPendingMaterialized(expected map[string]string, sources []externalSkillSource, home string) map[string]string {
	for _, src := range sources {
		if !src.Materialize {
			continue
		}
		skills, err := discoverExternalSourceSkills(src, home)
		if err != nil {
			continue
		}
		for _, skill := range skills {
			if _, ok := expected[skill.Name]; !ok {
				expected[skill.Name] = skill.Path
			}
		}
	}
	return expected
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

// replaceableIdenticalPaths lists the native copies identical to the shared
// one that sync backs up before linking.
func replaceableIdenticalPaths(reports []agentReport) []string {
	var paths []string
	for _, report := range reports {
		paths = append(paths, report.IdenticalCopies...)
	}
	return paths
}

// replaceableConflictPaths lists the native paths --replace-conflicts would
// move into a backup.
func replaceableConflictPaths(reports []agentReport) []string {
	var paths []string
	for _, report := range reports {
		for _, item := range report.Replaceable {
			paths = append(paths, item.Path)
		}
	}
	return paths
}

// listReplaceableConflicts prints what `sync --replace-conflicts` would move
// into a backup, without moving anything.
func listReplaceableConflicts(w io.Writer, reports []agentReport, home string) {
	paths := replaceableConflictPaths(reports)
	if len(paths) == 0 {
		return
	}
	backupDir := newConflictBackupDir(home)
	fmt.Fprintf(w, "\n%d existing file(s) differ from the shared copy and would be moved to %s and replaced by the shared copies:\n", len(paths), backupDir)
	for _, path := range paths {
		fmt.Fprintf(w, "  %s\n", path)
	}
}

// plural renders "1 change" or "2 changes"; "harness" takes "es".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	if strings.HasSuffix(noun, "s") || strings.HasSuffix(noun, "h") {
		return fmt.Sprintf("%d %ses", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// countSyncChanges tallies the individual changes a sync would apply, and the
// number of harnesses they touch. A harness with conflicts is left untouched by
// a real sync, so it counts nothing; with replaceConflicts, replaceable
// conflicts become links and count as changes. Other conflicts are reported
// separately by the plan.
func countSyncChanges(repoReport repoLinkReport, reports []agentReport, replaceConflicts bool) (changes, harnesses int) {
	if repoReport.State != stateSynced {
		changes++
	}
	for _, report := range reports {
		if report.Error != "" || !report.Detected {
			continue
		}
		replaced := 0
		if replaceConflicts {
			replaced = len(report.Replaceable)
		}
		// A conflict that is neither replaced nor about one role keeps the
		// whole harness untouched.
		blocked := false
		for _, conflict := range blockingConflicts(report) {
			replaceable := false
			for _, item := range report.Replaceable {
				replaceable = replaceable || item.Message == conflict
			}
			blocked = blocked || !replaceConflicts || !replaceable
		}
		if blocked {
			continue
		}
		n := replaced + len(report.Adds) + len(report.Updates) + len(report.Removes) +
			len(report.AddsAgent) + len(report.UpdatesAgent) + len(report.RemovesAgent) +
			len(report.AddsMCP) + len(report.UpdatesMCP) + len(report.RemovesMCP) +
			len(report.AddsHook) + len(report.UpdatesHook) + len(report.RemovesHook) +
			len(report.UpdatesPackage) + len(report.RemovesPackage) + len(report.UpdatesSetting) +
			len(report.AddsPlugin) + len(report.RemovesPlugin)
		if report.RootState == stateMissing || report.RootState == stateDrifted {
			n++
		}
		changes += n
		if n > 0 {
			harnesses++
		}
	}
	return changes, harnesses
}

// replaceAllConflicts backs up and clears every replaceable conflict and
// returns the backup directory and the native paths moved into it. When
// confirm is set (setup --review), it lists them and asks first.
func replaceAllConflicts(reports []agentReport, home string, streams setupIO, confirm bool) (string, []string, error) {
	var pending []string
	for _, report := range reports {
		for _, item := range report.Replaceable {
			pending = append(pending, item.Path)
		}
	}
	if len(pending) == 0 {
		return "", nil, nil
	}
	backupDir := newConflictBackupDir(home)
	if confirm {
		fmt.Fprintf(streams.out, "\n%d existing file(s) differ from the shared copy and will be replaced by the shared copies:\n", len(pending))
		for _, path := range pending {
			fmt.Fprintf(streams.out, "  %s\n", path)
		}
		if !promptYesNo(streams, fmt.Sprintf("Move them to %s and put the shared copies in their place?", backupDir)) {
			fmt.Fprintln(streams.out, "keeping them; those agents are skipped until the conflicts are resolved")
			return "", nil, nil
		}
	}
	var moved []string
	for i := range reports {
		paths, err := replaceConflicts(&reports[i], backupDir, home)
		moved = append(moved, paths...)
		if err != nil {
			return backupDir, moved, err
		}
	}
	fmt.Fprintf(streams.out, "backed up %d file(s) to %s\n", len(moved), backupDir)
	return backupDir, moved, nil
}

func applyAgentRootInstructionSync(reports []agentReport) error {
	// Gemini CLI and Antigravity CLI share ~/.gemini/GEMINI.md: write it once.
	written := map[string]bool{}
	for _, report := range reports {
		if !report.Detected || report.RootPath == "" || written[report.RootPath] {
			continue
		}
		written[report.RootPath] = true
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
		if len(blockingConflicts(report)) > 0 {
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

func ptrGate(g gateOptions) *gateOptions { return &g }
