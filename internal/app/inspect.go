package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	stateMissing     = "missing"
	stateConflict    = "conflict"
	stateSynced      = "synced"
	stateDrifted     = "drifted"
	stateUnsupported = "unsupported"
)

func expectedSkills(repoRoot string, home string, cfg config) (map[string]string, error) {
	discovered := make(map[string]discoveredSkill)
	local, err := discoverLocalSkills(repoRoot, home)
	if err != nil {
		return nil, err
	}
	if err := mergeDiscoveredSkills(discovered, local); err != nil {
		return nil, err
	}

	directSources := make([]externalSkillSource, 0, len(cfg.ExternalSkills))
	for _, src := range cfg.ExternalSkills {
		if !src.Materialize {
			directSources = append(directSources, src)
		}
	}
	external, err := discoverExternalSkillSet(directSources, home)
	if err != nil {
		return nil, err
	}
	if err := mergeDiscoveredSkills(discovered, discoveredSkillValues(external)); err != nil {
		return nil, err
	}
	return discoveredSkillPaths(discovered), nil
}

func discoverLocalSkills(repoRoot string, _ string) ([]discoveredSkill, error) {
	skillsDir := filepath.Join(repoRoot, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", skillsDir, err)
	}

	agentsSkillRoot := filepath.Join(repoRoot, "skills")
	discovered := make([]discoveredSkill, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		name := entry.Name()
		if !hasFile(filepath.Join(skillsDir, name, "SKILL.md")) {
			continue
		}
		discovered = append(discovered, discoveredSkill{
			Name:   name,
			Path:   filepath.Join(agentsSkillRoot, name),
			Origin: "local skills",
			Root:   skillsDir,
		})
	}
	return discovered, nil
}

func expectedSkillsForAgent(base map[string]string, _ string, _ config, _ string) (map[string]string, error) {
	return base, nil
}

func inspectRepoLink(repoRoot string, home string) (repoLinkReport, error) {
	linkPath := filepath.Join(home, ".agents")
	report := repoLinkReport{
		Path:           linkPath,
		ExpectedTarget: repoRoot,
		State:          stateMissing,
	}
	if !sameCleanPath(linkPath, repoRoot) && !sameResolvedPath(linkPath, repoRoot) {
		report.ActualTarget = repoRoot
		report.State = stateSynced
		return report, nil
	}

	info, err := os.Lstat(linkPath)
	if errors.Is(err, fs.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return repoLinkReport{}, fmt.Errorf("stat %s: %w", linkPath, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		// Support the documented `git clone ... ~/.agents` install path: when
		// the repo is cloned directly to ~/.agents, the path is a real
		// directory that already *is* repoRoot, so no symlink is needed and
		// the layout is already correct. Only a foreign non-symlink path
		// (something other than the repo) is a genuine conflict.
		if info.IsDir() && sameResolvedPath(linkPath, repoRoot) {
			report.ActualTarget = linkPath
			report.State = stateSynced
			return report, nil
		}
		report.State = stateConflict
		report.ActualTarget = "non-symlink path exists"
		return report, nil
	}

	rawTarget, err := os.Readlink(linkPath)
	if err != nil {
		return repoLinkReport{}, fmt.Errorf("readlink %s: %w", linkPath, err)
	}
	report.ActualTarget = rawTarget
	if linkMatches(linkPath, rawTarget, repoRoot) {
		report.State = stateSynced
		return report, nil
	}

	report.State = stateDrifted
	return report, nil
}

func sameCleanPath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}

// sameResolvedPath reports whether a and b refer to the same location after
// resolving symlinks. Used to detect an in-place ~/.agents clone (the repo
// cloned directly to ~/.agents) where a symlink to itself is unnecessary.
func sameResolvedPath(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	return ra == rb
}

func inspectAgents(selected []agentConfig, expected map[string]string, repoRoot string, home string, cfg config) ([]agentReport, error) {
	reports := make([]agentReport, 0, len(selected))
	agentsSkillRoot := filepath.Join(repoRoot, "skills")
	for _, agent := range selected {
		agentExpected := expected
		var err error
		if isDetected(agent) {
			agentExpected, err = expectedSkillsForAgent(expected, home, cfg, agent.Name)
		}
		report := agentReport{}
		if err == nil {
			report, err = inspectAgent(agent, agentExpected, repoRoot, agentsSkillRoot, cfg, home)
		}
		// One unreadable native config (a hand-edited YAML, say) must not hide
		// every other agent: record the failure on that agent and keep going.
		if err != nil {
			report = agentReport{Name: agent.Name, SkillRoot: agent.SkillRoot, AgentRoot: agent.AgentRoot, Detected: true, Error: err.Error()}
		}
		reports = append(reports, report)
	}
	return reports, nil
}

// readableReports drops agents whose native config could not be read, so the
// sync steps only touch agents tackroom fully understands.
func readableReports(reports []agentReport) []agentReport {
	readable := make([]agentReport, 0, len(reports))
	for _, report := range reports {
		if report.Error == "" {
			readable = append(readable, report)
		}
	}
	return readable
}

// agentFailures folds every unreadable agent into one error, or returns nil.
func agentFailures(reports []agentReport) error {
	var failed []string
	for _, report := range reports {
		if report.Error != "" {
			failed = append(failed, report.Name+": "+report.Error)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return fmt.Errorf("could not read the config of %d agent(s), left them unchanged: %s", len(failed), strings.Join(failed, "; "))
}

func inspectAgent(agent agentConfig, expected map[string]string, repoRoot string, agentsSkillRoot string, cfg config, home string) (agentReport, error) {
	report := agentReport{
		Name:           agent.Name,
		SkillRoot:      agent.SkillRoot,
		AgentRoot:      agent.AgentRoot,
		ExpectedSkills: expected,
		Detected:       isDetected(agent),
	}
	if !report.Detected {
		return report, nil
	}
	h := harnessFor(agent.Name)
	if h != nil && h.Skills == skillsConfigDriven && h.InspectSkills != nil {
		return h.InspectSkills(agent, expected, agentsSkillRoot, cfg, home)
	}

	if h != nil && h.SkillsNativeRoot != nil && h.SkillsNativeRoot(repoRoot, home) {
		// Skills are consumed directly from the config root; no per-harness
		// mirror is created, so every expected skill is already managed.
		report.Managed = append(report.Managed, sortedKeys(expected)...)
	} else {
		expectedNames := sortedKeys(expected)
		rootInfo, err := os.Stat(agent.SkillRoot)
		rootMissing := false
		switch {
		case errors.Is(err, fs.ErrNotExist):
			rootMissing = true
		case err != nil:
			return agentReport{}, fmt.Errorf("stat %s: %w", agent.SkillRoot, err)
		case !rootInfo.IsDir():
			report.Conflicts = append(report.Conflicts, fmt.Sprintf("%s exists but is not a directory", agent.SkillRoot))
			report.Missing = append(report.Missing, expectedNames...)
			report.Adds = append(report.Adds, expectedNames...)
			sortReportLists(&report)
			report.Synced = false
			return report, nil
		}

		entryMap := make(map[string]fs.DirEntry)
		if !rootMissing {
			entries, err := os.ReadDir(agent.SkillRoot)
			if err != nil {
				return agentReport{}, fmt.Errorf("read %s: %w", agent.SkillRoot, err)
			}
			for _, entry := range entries {
				entryMap[entry.Name()] = entry
			}
		}

		for _, name := range expectedNames {
			linkPath := filepath.Join(agent.SkillRoot, name)
			entry, ok := entryMap[name]
			if !ok || rootMissing {
				report.Missing = append(report.Missing, name)
				report.Adds = append(report.Adds, name)
				continue
			}

			mode := entry.Type()
			if mode&os.ModeSymlink == 0 {
				matches, err := treesEqual(linkPath, expected[name])
				if err != nil {
					return agentReport{}, fmt.Errorf("compare %s with %s: %w", linkPath, expected[name], err)
				}
				if matches {
					report.Managed = append(report.Managed, name)
					continue
				}
				report.Conflicts = append(report.Conflicts, fmt.Sprintf("%s exists but differs from canonical content and is not a symlink", linkPath))
				continue
			}

			rawTarget, err := os.Readlink(linkPath)
			if err != nil {
				return agentReport{}, fmt.Errorf("readlink %s: %w", linkPath, err)
			}
			if linkMatches(linkPath, rawTarget, expected[name]) {
				report.Managed = append(report.Managed, name)
				continue
			}

			report.Drifted = append(report.Drifted, name)
			report.Updates = append(report.Updates, name)
		}

		if !rootMissing {
			for name, entry := range entryMap {
				if _, ok := expected[name]; ok {
					continue
				}
				path := filepath.Join(agent.SkillRoot, name)
				if entry.Type()&os.ModeSymlink != 0 {
					rawTarget, err := os.Readlink(path)
					if err != nil {
						return agentReport{}, fmt.Errorf("readlink %s: %w", path, err)
					}
					if isManagedSkillLink(path, rawTarget, repoRoot, agentsSkillRoot) || isExternalSkillLink(path, rawTarget, home) {
						report.StaleManaged = append(report.StaleManaged, name)
						report.Removes = append(report.Removes, name)
						continue
					}
				}
				report.External = append(report.External, name)
			}
		}
	}

	if err := augmentMCPReport(&report, agent, cfg, home); err != nil {
		return agentReport{}, err
	}
	if err := augmentHookReport(&report, agent, cfg, home); err != nil {
		return agentReport{}, err
	}
	if err := inspectAgentRoles(&report, repoRoot, agent); err != nil {
		return agentReport{}, err
	}
	if h != nil && h.RootInstructions != nil {
		if err := inspectRootInstructions(&report, h.RootInstructions, repoRoot, home); err != nil {
			return agentReport{}, err
		}
	}
	if err := augmentPiPackageReport(&report, agent, home); err != nil {
		return agentReport{}, err
	}

	sortReportLists(&report)
	report.Synced = isReportSynced(report)
	return report, nil
}
func treesEqual(left string, right string) (bool, error) {
	leftInfo, err := os.Lstat(left)
	if err != nil {
		return false, err
	}
	rightInfo, err := os.Lstat(right)
	if err != nil {
		return false, err
	}
	if leftInfo.Mode().Type() != rightInfo.Mode().Type() {
		return false, nil
	}
	if leftInfo.Mode()&os.ModeSymlink != 0 {
		leftTarget, err := os.Readlink(left)
		if err != nil {
			return false, err
		}
		rightTarget, err := os.Readlink(right)
		return leftTarget == rightTarget, err
	}
	if leftInfo.IsDir() {
		leftEntries, err := os.ReadDir(left)
		if err != nil {
			return false, err
		}
		rightEntries, err := os.ReadDir(right)
		if err != nil {
			return false, err
		}
		if len(leftEntries) != len(rightEntries) {
			return false, nil
		}
		for i := range leftEntries {
			if leftEntries[i].Name() != rightEntries[i].Name() {
				return false, nil
			}
			matches, err := treesEqual(filepath.Join(left, leftEntries[i].Name()), filepath.Join(right, rightEntries[i].Name()))
			if err != nil || !matches {
				return matches, err
			}
		}
		return true, nil
	}
	if !leftInfo.Mode().IsRegular() || leftInfo.Size() != rightInfo.Size() {
		return false, nil
	}
	leftFile, err := os.Open(left)
	if err != nil {
		return false, err
	}
	defer leftFile.Close()
	rightFile, err := os.Open(right)
	if err != nil {
		return false, err
	}
	defer rightFile.Close()
	leftBuffer := make([]byte, 32*1024)
	rightBuffer := make([]byte, len(leftBuffer))
	for {
		leftN, leftErr := leftFile.Read(leftBuffer)
		rightN, rightErr := rightFile.Read(rightBuffer)
		if leftN != rightN || !bytes.Equal(leftBuffer[:leftN], rightBuffer[:rightN]) {
			return false, nil
		}
		if leftErr == io.EOF || rightErr == io.EOF {
			return leftErr == io.EOF && rightErr == io.EOF, nil
		}
		if leftErr != nil {
			return false, leftErr
		}
		if rightErr != nil {
			return false, rightErr
		}
	}
}

func isReportSynced(report agentReport) bool {
	if len(report.Missing) > 0 || len(report.Drifted) > 0 || len(report.Conflicts) > 0 || len(report.StaleManaged) > 0 {
		return false
	}
	if len(report.MissingMCP) > 0 || len(report.DriftedMCP) > 0 || len(report.MissingAgent) > 0 || len(report.DriftedAgent) > 0 {
		return false
	}
	if len(report.MissingHook) > 0 || len(report.DriftedHook) > 0 || len(report.DriftedPackage) > 0 {
		return false
	}
	return report.RootState == "" || report.RootState == stateSynced
}

func inspectRootInstructions(report *agentReport, ri *rootInstructionsCapability, repoRoot string, home string) error {
	linkPath := ri.Path(home)
	expectedTarget := ri.Expected(repoRoot)
	report.RootPath = linkPath
	report.RootExpected = expectedTarget
	report.RootState = stateMissing

	info, err := os.Lstat(linkPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", linkPath, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		report.RootState = stateConflict
		report.RootActual = "non-symlink path exists"
		report.Conflicts = append(report.Conflicts, fmt.Sprintf("%s exists but is not a symlink", linkPath))
		return nil
	}

	rawTarget, err := os.Readlink(linkPath)
	if err != nil {
		return fmt.Errorf("readlink %s: %w", linkPath, err)
	}
	report.RootActual = rawTarget
	if linkMatches(linkPath, rawTarget, expectedTarget) {
		report.RootState = stateSynced
		return nil
	}

	report.RootState = stateDrifted
	return nil
}

func inspectAmpAgent(agent agentConfig, expected map[string]string, agentsSkillRoot string, cfg config, home string) (agentReport, error) {
	report := agentReport{
		Name:           agent.Name,
		SkillRoot:      agent.SkillRoot,
		AgentRoot:      agent.AgentRoot,
		ExpectedSkills: expected,
		Detected:       isDetected(agent),
	}
	if !report.Detected {
		return report, nil
	}

	if ok, err := ampHasSkillsPath(home, agentsSkillRoot); err != nil {
		return agentReport{}, err
	} else if !ok {
		message := "config amp.skills.path -> " + hermesExternalDirValue(home, agentsSkillRoot)
		report.Missing = append(report.Missing, message)
		report.Adds = append(report.Adds, message)
	}
	report.Managed = append(report.Managed, sortedKeys(expected)...)
	if err := augmentMCPReport(&report, agent, cfg, home); err != nil {
		return agentReport{}, err
	}
	if err := augmentHookReport(&report, agent, cfg, home); err != nil {
		return agentReport{}, err
	}
	if h := harnessFor(agent.Name); h != nil && h.RootInstructions != nil {
		if err := inspectRootInstructions(&report, h.RootInstructions, filepath.Dir(agentsSkillRoot), home); err != nil {
			return agentReport{}, err
		}
	}

	sortReportLists(&report)
	report.Synced = isReportSynced(report)
	return report, nil
}

func ampHasSkillsPath(home string, target string) (bool, error) {
	configPath := ampSettingsPath(home)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", configPath, err)
	}

	var raw map[string]interface{}
	if err := parseJSONConfig(configPath, data, &raw); err != nil {
		return false, fmt.Errorf("parse %s: %w", configPath, err)
	}
	path, _ := raw["amp.skills.path"].(string)
	return ampSkillsPathConfigured(path, home, target), nil
}

func inspectHermesAgent(agent agentConfig, expected map[string]string, agentsSkillRoot string, cfg config, home string) (agentReport, error) {
	report := agentReport{
		Name:           agent.Name,
		SkillRoot:      agent.SkillRoot,
		AgentRoot:      agent.AgentRoot,
		ExpectedSkills: expected,
		Detected:       isDetected(agent),
	}
	if !report.Detected {
		return report, nil
	}

	entries, err := os.ReadDir(agent.SkillRoot)
	if err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			report.External = append(report.External, name)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return agentReport{}, fmt.Errorf("read %s: %w", agent.SkillRoot, err)
	}

	expectedDirs, err := hermesExternalSkillDirs(agentsSkillRoot, cfg, home)
	if err != nil {
		return agentReport{}, err
	}
	ok, stale, err := hermesExternalSkillsDirsState(expectedDirs, cfg, home)
	if err != nil {
		return agentReport{}, err
	}
	if !ok {
		report.Missing = append(report.Missing, "config skills.external_dirs")
		report.Adds = append(report.Adds, "config skills.external_dirs")
	}
	for _, dir := range stale {
		report.StaleManaged = append(report.StaleManaged, "config dir "+dir)
		report.Removes = append(report.Removes, "config dir "+dir)
	}
	report.Managed = append(report.Managed, sortedKeys(report.ExpectedSkills)...)
	if err := augmentMCPReport(&report, agent, cfg, home); err != nil {
		return agentReport{}, err
	}
	if err := augmentHookReport(&report, agent, cfg, home); err != nil {
		return agentReport{}, err
	}

	sortReportLists(&report)
	report.Synced = isReportSynced(report)
	return report, nil
}

func hermesExternalSkillDirs(agentsSkillRoot string, _ config, _ string) ([]string, error) {
	return []string{agentsSkillRoot}, nil
}

func hermesExternalSkillsDirsState(expected []string, _ config, _ string) (bool, []string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, nil, fmt.Errorf("resolve home: %w", err)
	}
	configPath := filepath.Join(home, ".hermes", "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return false, nil, fmt.Errorf("read %s: %w", configPath, err)
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return false, nil, fmt.Errorf("parse %s: %w", configPath, err)
	}

	skillsRaw, ok := raw["skills"]
	if !ok {
		return false, nil, nil
	}
	skills, ok := skillsRaw.(map[string]interface{})
	if !ok {
		return false, nil, nil
	}
	dirsRaw, ok := skills["external_dirs"]
	if !ok {
		return false, nil, nil
	}
	dirs, ok := dirsRaw.([]interface{})
	if !ok {
		return false, nil, nil
	}

	expectedSet := make(map[string]bool, len(expected))
	for _, dir := range expected {
		expectedSet[dir] = true
	}

	found := make(map[string]bool, len(expected))
	for _, d := range dirs {
		s, ok := d.(string)
		if !ok {
			continue
		}
		expanded := expandPath(strings.TrimSpace(s), home)
		if expectedSet[expanded] {
			found[expanded] = true
		}
	}

	for _, dir := range expected {
		if !found[dir] {
			return false, nil, nil
		}
	}
	return true, nil, nil
}

func linkMatches(linkPath string, rawTarget string, expectedTarget string) bool {
	if rawTarget == expectedTarget {
		return true
	}

	actualAbs := absoluteTarget(linkPath, rawTarget)
	expectedAbs := absoluteTarget(linkPath, expectedTarget)
	if actualAbs == expectedAbs {
		return true
	}

	actualResolved, err := filepath.EvalSymlinks(actualAbs)
	if err != nil {
		return false
	}
	expectedResolved, err := filepath.EvalSymlinks(expectedAbs)
	if err != nil {
		return false
	}
	return actualResolved == expectedResolved
}

func isManagedSkillLink(linkPath string, rawTarget string, repoRoot string, agentsSkillRoot string) bool {
	targetAbs := absoluteTarget(linkPath, rawTarget)
	if targetAbs == agentsSkillRoot || strings.HasPrefix(targetAbs, agentsSkillRoot+string(os.PathSeparator)) {
		return true
	}
	if resolvedTarget, err := filepath.EvalSymlinks(targetAbs); err == nil {
		if resolvedAgentsRoot, err := filepath.EvalSymlinks(agentsSkillRoot); err == nil && (resolvedTarget == resolvedAgentsRoot || strings.HasPrefix(resolvedTarget, resolvedAgentsRoot+string(os.PathSeparator))) {
			return true
		}
	}

	resolved, err := filepath.EvalSymlinks(targetAbs)
	if err != nil {
		return false
	}
	repoSkillsRoot := filepath.Join(repoRoot, "skills")
	if resolved == repoSkillsRoot || strings.HasPrefix(resolved, repoSkillsRoot+string(os.PathSeparator)) {
		return true
	}
	resolvedRepoSkillsRoot, err := filepath.EvalSymlinks(repoSkillsRoot)
	if err != nil {
		return false
	}
	return resolved == resolvedRepoSkillsRoot || strings.HasPrefix(resolved, resolvedRepoSkillsRoot+string(os.PathSeparator))
}

func absoluteTarget(linkPath string, rawTarget string) string {
	if filepath.IsAbs(rawTarget) {
		return filepath.Clean(rawTarget)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(linkPath), rawTarget))
}
