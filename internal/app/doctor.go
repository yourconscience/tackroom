package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/yourconscience/tackroom/internal/agentrole"
	"gopkg.in/yaml.v3"
)

const (
	agentAmp                = "amp"
	agentClaudeCode         = "claude-code"
	agentCodex              = "codex"
	agentDroid              = "droid"
	agentHermes             = "hermes"
	agentOpenCode           = "opencode"
	agentPi                 = "pi"
	agentOMP                = "omp"
	agentQwenCode           = "qwen-code"
	tackroomSkillsPathValue = "~/.agents/skills"
)

type checkResult struct {
	name   string
	status string // "pass", "warn", "fail"
	detail string
}

const (
	checkStatusPass = "pass"
	checkStatusWarn = "warn"
	checkStatusFail = "fail"
)

func runDoctor(opts runOptions) error {
	repoRoot, home, cfg, selected, err := loadContext(opts)
	if err != nil {
		return err
	}

	fmt.Println("tackroom doctor")
	fmt.Printf("repo: %s\n\n", repoRoot)

	var results []checkResult

	results = append(results, checkSkillFrontmatter(repoRoot))
	results = append(results, checkSkillSpec(repoRoot))
	results = append(results, checkAgentRoles(repoRoot))
	for _, name := range sortedHarnessNames() {
		for _, check := range getHarnesses()[name].doctorChecks {
			results = append(results, check.Run(repoRoot, home, cfg))
		}
	}
	results = append(results, checkAgnix(repoRoot))
	results = append(results, checkAgentsMDSize(repoRoot))
	results = append(results, checkREADMESkillInventory(repoRoot))
	results = append(results, checkMemsearchIndex(home))
	results = append(results, checkHerdrPluginHealth())
	results = append(results, checkExternalPackageAge(repoRoot, cfg, opts.SkipPackageAge, timeNow()))
	results = append(results, checkExternalSkillSources(cfg, home))
	results = append(results, checkMaterializedExternalSkills(repoRoot, cfg, home))
	results = append(results, checkExternalSkillLock(repoRoot, cfg, home))
	results = append(results, checkExternalSkillAudit(cfg, home))
	results = append(results, checkNativeHookHealth(home, cfg, selected))

	fmt.Println("checks:")
	labelWidth := 0
	for _, r := range results {
		if len(r.name) > labelWidth {
			labelWidth = len(r.name)
		}
	}

	passed, warned, failed := 0, 0, 0
	for _, r := range results {
		padding := strings.Repeat(".", labelWidth-len(r.name)+3)
		fmt.Printf("  %s %s %s (%s)\n", r.name, padding, r.status, r.detail)
		switch r.status {
		case checkStatusPass:
			passed++
		case checkStatusWarn:
			warned++
		case checkStatusFail:
			failed++
		}
	}

	fmt.Printf("\n%d passed, %d warning, %d failed\n", passed, warned, failed)

	// Context advisory notes are informational only. They are printed after the
	// summary and MUST NOT influence the exit decision below.
	printContextNotes(cfg, repoRoot, home, selected)

	return doctorExitError(failed, warned)
}

func checkNativeHookHealth(home string, cfg config, selected []agentConfig) checkResult {
	entries, unsupported, err := collectNativeHooks(home, cfg, selected)
	if err != nil {
		return checkResult{"native hooks", checkStatusFail, err.Error()}
	}
	managed := 0
	var stale []string
	for _, entry := range entries {
		if entry.Managed {
			managed++
		}
		if entry.MissingTarget != "" {
			stale = append(stale, fmt.Sprintf("%s/%s -> %s", entry.Agent, entry.Event, entry.MissingTarget))
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		return checkResult{"native hooks", checkStatusWarn, fmt.Sprintf("%d stale registration(s); first: %s; review with: tackroom hook list", len(stale), stale[0])}
	}
	detail := fmt.Sprintf("%d registrations, %d managed, %d unmanaged", len(entries), managed, len(entries)-managed)
	if len(unsupported) > 0 {
		detail += fmt.Sprintf("; unsupported: %s", strings.Join(unsupported, ", "))
	}
	return checkResult{"native hooks", checkStatusPass, detail}
}

// doctorExitError derives the doctor exit result from check outcomes only. It
// intentionally takes no note/advisory input so context notes cannot affect it.
func doctorExitError(failed, warned int) error {
	if failed > 0 {
		return fmt.Errorf("%d check(s) failed", failed)
	}
	if warned > 0 {
		return fmt.Errorf("%d warning(s)", warned)
	}
	return nil
}

// printContextNotes prints soft, clearly-labeled skill-listing context notes for
// harnesses over the configured threshold. It never affects the exit code and
// stays silent on any error (best-effort advisory).
func printContextNotes(cfg config, repoRoot string, home string, selected []agentConfig) {
	threshold := contextNoteThreshold(cfg)
	if threshold <= 0 {
		return
	}
	costs, err := harnessContextCosts(cfg, repoRoot, home, selected)
	if err != nil {
		return
	}
	notes := contextNoteLines(threshold, costs)
	if len(notes) == 0 {
		return
	}
	fmt.Println()
	for _, n := range notes {
		fmt.Println(n)
	}
}

func checkAgentRoles(repoRoot string) checkResult {
	roles, err := agentrole.Load(repoRoot)
	if err != nil {
		return checkResult{"agent roles", checkStatusFail, err.Error()}
	}
	if len(roles) == 0 {
		return checkResult{"agent roles", checkStatusWarn, "no agent roles found in agents/*.md"}
	}
	return checkResult{"agent roles", checkStatusPass, fmt.Sprintf("%d roles valid", len(roles))}
}

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func checkSkillFrontmatter(repoRoot string) checkResult {
	skillsDir := filepath.Join(repoRoot, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return checkResult{"skill frontmatter", checkStatusFail, fmt.Sprintf("cannot read skills/: %s", err)}
	}

	var invalid []string
	total := 0
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		skillMD := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
		if !hasFile(skillMD) {
			continue
		}
		total++
		fm, err := parseSkillFrontmatter(skillMD)
		if err != nil {
			invalid = append(invalid, fmt.Sprintf("%s (parse error: %v)", entry.Name(), err))
		} else if fm.Name == "" || fm.Description == "" {
			invalid = append(invalid, entry.Name())
		}
	}

	if len(invalid) > 0 {
		return checkResult{"skill frontmatter", checkStatusFail, fmt.Sprintf("%s missing name/description", strings.Join(invalid, ", "))}
	}
	return checkResult{"skill frontmatter", checkStatusPass, fmt.Sprintf("%d skills valid", total)}
}

func parseSkillFrontmatter(path string) (skillFrontmatter, error) {
	f, err := os.Open(path)
	if err != nil {
		return skillFrontmatter{}, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// First line must be ---
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return skillFrontmatter{}, fmt.Errorf("no frontmatter")
	}

	var lines []string
	foundEnd := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			foundEnd = true
			break
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return skillFrontmatter{}, err
	}
	if !foundEnd {
		return skillFrontmatter{}, fmt.Errorf("frontmatter not terminated")
	}

	var fm skillFrontmatter
	if err := yaml.Unmarshal([]byte(strings.Join(lines, "\n")), &fm); err != nil {
		return skillFrontmatter{}, err
	}
	return fm, nil
}

func isAgentDetected(cfg config, name string) bool {
	for _, agent := range cfg.Agents {
		if agent.Name == name && isDetected(agent) {
			return true
		}
	}
	return false
}

func checkHermesDirectMirrors(repoRoot string, home string, cfg config) checkResult {
	hermesDetected := isAgentDetected(cfg, agentHermes)

	if !hermesDetected {
		return checkResult{"hermes direct mirrors", checkStatusPass, agentHermes + " not detected, skipped"}
	}

	hermesSkillsDir := filepath.Join(home, ".hermes", "skills")
	hermesSkills, err := collectDirectSkillNames(hermesSkillsDir)
	if err != nil {
		return checkResult{"hermes direct mirrors", checkStatusPass, "hermes skills dir unreadable, skipped"}
	}

	skillsDir := filepath.Join(repoRoot, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return checkResult{"hermes direct mirrors", checkStatusFail, fmt.Sprintf("cannot read skills/: %s", err)}
	}

	var mirrors []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		skillMD := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
		if !hasFile(skillMD) {
			continue
		}
		fm, err := parseSkillFrontmatter(skillMD)
		if err != nil || fm.Name == "" {
			continue
		}
		if hermesPath, ok := hermesSkills[fm.Name]; ok {
			mirrors = append(mirrors, fmt.Sprintf("%s has direct hermes mirror %s at %s", entry.Name(), fm.Name, hermesPath))
		}
	}

	if len(mirrors) > 0 {
		return checkResult{"hermes direct mirrors", checkStatusWarn, strings.Join(mirrors, "; ")}
	}
	return checkResult{"hermes direct mirrors", checkStatusPass, "no direct mirrors"}
}

func collectDirectSkillNames(root string) (map[string]string, error) {
	names := make(map[string]string)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		skillMD := filepath.Join(root, entry.Name(), "SKILL.md")
		if !hasFile(skillMD) {
			continue
		}
		fm, err := parseSkillFrontmatter(skillMD)
		if err != nil || strings.TrimSpace(fm.Name) == "" {
			continue
		}
		names[fm.Name] = filepath.Join(entry.Name(), "SKILL.md")
	}
	return names, nil
}

type agnixSummary struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Info     int `json:"info"`
}

type agnixDiagnostic struct {
	Level   string `json:"level"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

type agnixReport struct {
	Summary     agnixSummary      `json:"summary"`
	Diagnostics []agnixDiagnostic `json:"diagnostics"`
}

func checkAgnix(repoRoot string) checkResult {
	if _, err := exec.LookPath("agnix"); err != nil {
		return checkResult{"agnix", checkStatusWarn, "agnix not installed (optional lint); enable with: npm install -g agnix"}
	}
	out, _ := runAgnix(repoRoot)
	report, parseErr := parseAgnixReport(out)
	if parseErr != nil {
		return checkResult{"agnix", checkStatusFail, fmt.Sprintf("could not parse agnix output: %s", parseErr)}
	}
	report, ignored := scopeAgnixToCanonical(report)
	detail := agnixDetail(report)
	if ignored > 0 {
		detail = fmt.Sprintf("%s (%d findings in external/ clones ignored; upstream sources are audited separately)", detail, ignored)
	}
	if report.Summary.Errors > 0 {
		return checkResult{"agnix", checkStatusFail, detail}
	}
	return checkResult{"agnix", checkStatusPass, detail}
}

// scopeAgnixToCanonical drops findings from external/ upstream clones: that
// content is not authored here, and external sources get their own audit
// check. Summary counts are recomputed from the kept diagnostics.
func scopeAgnixToCanonical(report agnixReport) (agnixReport, int) {
	kept := report.Diagnostics[:0]
	ignored := 0
	var summary agnixSummary
	for _, diagnostic := range report.Diagnostics {
		normalized := filepath.ToSlash(diagnostic.File)
		if strings.HasPrefix(normalized, "external/") || strings.Contains(normalized, "/external/") {
			ignored++
			continue
		}
		kept = append(kept, diagnostic)
		switch diagnostic.Level {
		case "error":
			summary.Errors++
		case "warning":
			summary.Warnings++
		default:
			summary.Info++
		}
	}
	report.Diagnostics = kept
	report.Summary = summary
	return report, ignored
}

func runAgnix(repoRoot string) ([]byte, error) {
	cmd := exec.Command("agnix", "--format", "json", ".")
	cmd.Dir = repoRoot
	return cmd.Output()
}

func parseAgnixReport(data []byte) (agnixReport, error) {
	var report agnixReport
	if err := json.Unmarshal(data, &report); err != nil {
		return agnixReport{}, err
	}
	return report, nil
}

func agnixDetail(report agnixReport) string {
	detail := fmt.Sprintf("%d errors, %d warnings, %d info", report.Summary.Errors, report.Summary.Warnings, report.Summary.Info)
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Level != "error" {
			continue
		}
		location := diagnostic.File
		if diagnostic.Line > 0 {
			location = fmt.Sprintf("%s:%d", location, diagnostic.Line)
		}
		return fmt.Sprintf("%s; first error: %s: %s", detail, location, diagnostic.Message)
	}
	return detail
}

func checkAgentsMDSize(repoRoot string) checkResult {
	const limit = 8192
	path := filepath.Join(repoRoot, "AGENTS.md")
	info, err := os.Stat(path)
	if err != nil {
		return checkResult{"AGENTS.md size", checkStatusFail, "AGENTS.md not found"}
	}
	size := info.Size()
	sizeKB := float64(size) / 1024.0
	if size > limit {
		return checkResult{"AGENTS.md size", checkStatusWarn, fmt.Sprintf("%.1fKB / 8.0KB limit", sizeKB)}
	}
	return checkResult{"AGENTS.md size", checkStatusPass, fmt.Sprintf("%.1fKB / 8.0KB limit", sizeKB)}
}

func checkREADMESkillInventory(repoRoot string) checkResult {
	const remediation = "run: tackroom sync"
	data, err := os.ReadFile(filepath.Join(repoRoot, "README.md"))
	if err != nil {
		return checkResult{"README skills", checkStatusPass, "no README.md in config root (optional)"}
	}
	expected, count, err := expectedREADMESkillsBlock(repoRoot)
	if err != nil {
		return checkResult{"README skills", checkStatusFail, fmt.Sprintf("%s; %s", err, remediation)}
	}
	content := string(data)
	if !strings.Contains(content, readmeSkillsBeginMarker) && !strings.Contains(content, readmeSkillsEndMarker) {
		return checkResult{"README skills", checkStatusPass, "README.md has no generated skills block (optional)"}
	}
	start, end, err := locateREADMESkillsBlock(content)
	if err != nil {
		return checkResult{"README skills", checkStatusFail, fmt.Sprintf("%s; %s", err, remediation)}
	}
	if content[start:end] != expected {
		return checkResult{"README skills", checkStatusFail, "generated block is stale; " + remediation}
	}
	return checkResult{"README skills", checkStatusPass, fmt.Sprintf("exact inventory of %d skills", count)}
}

func checkMemsearchIndex(home string) checkResult {
	if _, err := exec.LookPath("memsearch"); err == nil {
		out, err := exec.Command("memsearch", "stats", "--collection", "ai").CombinedOutput()
		if err == nil {
			if chunks, ok := parseMemsearchChunks(string(out)); ok {
				if chunks == 0 {
					return checkResult{"memsearch index", checkStatusWarn, "collection ai has 0 chunks"}
				}
				return checkResult{"memsearch index", checkStatusPass, fmt.Sprintf("collection ai has %d chunks", chunks)}
			}
		}
	}

	stateDir := filepath.Join(home, ".memsearch", "state")
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return checkResult{"memsearch index", checkStatusWarn, "state dir missing or unreadable"}
	}
	count := 0
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			count++
		}
	}
	if count == 0 {
		return checkResult{"memsearch index", checkStatusWarn, "state dir empty, index never built"}
	}
	return checkResult{"memsearch index", checkStatusPass, fmt.Sprintf("state dir has %d files", count)}
}

func parseMemsearchChunks(statsOutput string) (int, bool) {
	re := regexp.MustCompile(`(?m)Total indexed chunks:\s*([0-9]+)[.,]?\s*$`)
	match := re.FindStringSubmatch(statsOutput)
	if len(match) != 2 {
		return 0, false
	}
	n, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

func checkHermesHooks(home string, cfg config) checkResult {
	if !isAgentDetected(cfg, agentHermes) {
		return checkResult{"hermes hooks", checkStatusPass, agentHermes + " not detected, skipped"}
	}

	configPath := filepath.Join(home, ".hermes", "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return checkResult{"hermes hooks", checkStatusWarn, "config.yaml not readable"}
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return checkResult{"hermes hooks", checkStatusWarn, "config.yaml parse error"}
	}

	hooksRaw, ok := raw["hooks"]
	if !ok {
		return checkResult{"hermes hooks", checkStatusWarn, "no hooks section configured"}
	}

	// Count hooks if it's a list or map
	count := 0
	switch v := hooksRaw.(type) {
	case []interface{}:
		count = len(v)
	case map[string]interface{}:
		count = len(v)
	default:
		if hooksRaw != nil {
			count = 1
		}
	}

	if count == 0 {
		return checkResult{"hermes hooks", checkStatusWarn, "hooks section present but empty"}
	}
	return checkResult{"hermes hooks", checkStatusPass, fmt.Sprintf("%d hook configured", count)}
}

func checkExternalSkillSources(cfg config, home string) checkResult {
	if len(cfg.ExternalSkills) == 0 {
		return checkResult{"external skills", checkStatusPass, "none configured"}
	}
	var issues []string
	valid := 0
	for _, src := range cfg.ExternalSkills {
		if _, err := discoverExternalSourceSkills(src, home); err != nil {
			issues = append(issues, fmt.Sprintf("%s: %v", repoName(src.URL), err))
			continue
		}
		valid++
	}
	if len(issues) > 0 {
		sort.Strings(issues)
		return checkResult{"external skills", checkStatusWarn, strings.Join(issues, "; ")}
	}
	return checkResult{"external skills", checkStatusPass, fmt.Sprintf("%d sources valid", valid)}
}

func checkMaterializedExternalSkills(repoRoot string, cfg config, home string) checkResult {
	lock, err := readLockFile(repoRoot)
	if err != nil {
		return checkResult{"materialized external skills", checkStatusFail, err.Error()}
	}
	var issues []string
	checked := 0
	currentNames := make(map[string]bool)
	lockedNames := make(map[string]bool)
	var materializedCacheRoots []string
	for _, entry := range lock.ExternalSkills {
		for _, name := range entry.Materialized.Values() {
			lockedNames[name] = true
		}
		if entry.Materialized != "" {
			materializedCacheRoots = append(materializedCacheRoots, filepath.Join(externalCacheDir(home), entry.Name))
		}
	}
	for _, src := range cfg.ExternalSkills {
		if !src.Materialize {
			continue
		}
		materializedCacheRoots = append(materializedCacheRoots, filepath.Join(externalCacheDir(home), repoName(src.URL)))
		pin := lockEntryFor(lock, src)
		if pin == nil {
			issues = append(issues, fmt.Sprintf("%s has no matching lock pin", repoName(src.URL)))
			continue
		}
		skills, err := discoverExternalSourceSkills(src, home)
		if err != nil {
			issues = append(issues, fmt.Sprintf("%s: %v", repoName(src.URL), err))
			continue
		}
		owners := make(map[string]bool)
		for _, name := range pin.Materialized.Values() {
			owners[name] = true
		}
		for _, skill := range skills {
			currentNames[skill.Name] = true
			checked++
			if !owners[skill.Name] {
				issues = append(issues, fmt.Sprintf("%s is not recorded as materialized in %s", skill.Name, lockFileName))
			}
			canonical := filepath.Join(repoRoot, "skills", skill.Name)
			if _, err := os.Lstat(canonical); os.IsNotExist(err) {
				issues = append(issues, fmt.Sprintf("%s canonical copy missing", skill.Name))
			} else if err != nil {
				issues = append(issues, fmt.Sprintf("%s canonical copy unreadable: %v", skill.Name, err))
			} else if equal, err := externalSkillTreesEqual(skill.Path, canonical); err != nil {
				issues = append(issues, fmt.Sprintf("%s drift check failed: %v", skill.Name, err))
			} else if !equal {
				issues = append(issues, fmt.Sprintf("%s canonical copy drifted from lock", skill.Name))
			}
		}
	}
	for name := range lockedNames {
		if currentNames[name] {
			continue
		}
		issues = append(issues, fmt.Sprintf("%s has stale materialized ownership in %s", name, lockFileName))
		if _, err := os.Lstat(filepath.Join(repoRoot, "skills", name)); err == nil {
			issues = append(issues, fmt.Sprintf("%s stale canonical copy is still delivered", name))
		} else if err != nil && !os.IsNotExist(err) {
			issues = append(issues, fmt.Sprintf("%s stale canonical copy unreadable: %v", name, err))
		}
	}
	materializedNames := make(map[string]bool, len(currentNames)+len(lockedNames))
	for name := range currentNames {
		materializedNames[name] = true
	}
	for name := range lockedNames {
		materializedNames[name] = true
	}
	for _, agent := range cfg.Agents {
		if agent.SkillRoot == "" {
			continue
		}
		for name := range materializedNames {
			linkPath := filepath.Join(agent.SkillRoot, name)
			info, err := os.Lstat(linkPath)
			if err != nil || info.Mode()&os.ModeSymlink == 0 {
				continue
			}
			rawTarget, err := os.Readlink(linkPath)
			if err == nil && isExternalSkillLink(linkPath, rawTarget, home) {
				issues = append(issues, fmt.Sprintf("%s/%s is linked directly from %s", agent.Name, name, externalCacheDir(home)))
			}
		}
	}
	for _, dir := range hermesConfiguredExternalDirs(home) {
		for _, root := range materializedCacheRoots {
			if pathIsWithin(dir, root) {
				issues = append(issues, fmt.Sprintf("hermes skills.external_dirs links materialized skills directly from %s", dir))
				break
			}
		}
	}
	if len(issues) > 0 {
		sort.Strings(issues)
		return checkResult{"materialized external skills", checkStatusFail, strings.Join(issues, "; ")}
	}
	if checked == 0 {
		return checkResult{"materialized external skills", checkStatusPass, "none configured"}
	}
	return checkResult{"materialized external skills", checkStatusPass, fmt.Sprintf("%d canonical copies match lock", checked)}
}

func hermesConfiguredExternalDirs(home string) []string {
	data, err := os.ReadFile(filepath.Join(home, ".hermes", "config.yaml"))
	if err != nil {
		return nil
	}
	var raw struct {
		Skills struct {
			ExternalDirs []string `yaml:"external_dirs"`
		} `yaml:"skills"`
	}
	if yaml.Unmarshal(data, &raw) != nil {
		return nil
	}
	dirs := make([]string, 0, len(raw.Skills.ExternalDirs))
	for _, dir := range raw.Skills.ExternalDirs {
		dirs = append(dirs, expandPath(strings.TrimSpace(dir), home))
	}
	return dirs
}

func pathIsWithin(path string, root string) bool {
	if path == root || strings.HasPrefix(path, root+string(os.PathSeparator)) {
		return true
	}
	resolvedPath, pathErr := filepath.EvalSymlinks(path)
	resolvedRoot, rootErr := filepath.EvalSymlinks(root)
	if pathErr != nil || rootErr != nil {
		return false
	}
	return resolvedPath == resolvedRoot || strings.HasPrefix(resolvedPath, resolvedRoot+string(os.PathSeparator))
}
