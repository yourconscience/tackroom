package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
)

// skillsRegistryURL is the skills.sh search API base; tests point it at a fake.
var skillsRegistryURL = "https://skills.sh"

// minSourceCommitAge is how old a commit must be before `skill sources add`
// pins it, so a freshly pushed (possibly compromised) commit is never the
// first thing every agent loads.
const minSourceCommitAge = 72 * time.Hour

// installedSource is a skill source installed outside tackroom: a Claude Code
// plugin or a `npx skills` install.
type installedSource struct {
	Repo   string   `json:"repo"`           // owner/repo
	Path   string   `json:"path,omitempty"` // subdirectory inside the repo
	Via    string   `json:"via"`
	Skills []string `json:"skills,omitempty"`
}

func runSkillSources(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "search":
			return runSkillSourcesSearch(args[1:], os.Stdout)
		case "add":
			interactive := term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
			return runSkillSourcesAdd(args[1:], os.Stdin, os.Stdout, interactive)
		}
	}
	return runSkillSourcesList(args, os.Stdout)
}

func runSkillSourcesList(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("skill sources", flag.ContinueOnError)
	configPath := flags.String("config", "", "Path to tackroom YAML config")
	jsonOut := flags.Bool("json", false, "Print JSON")
	positional, err := parseInterleaved(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 0 {
		return fmt.Errorf("unknown skill sources subcommand %q (want search or add)", positional[0])
	}
	repoRoot, home, cfg, _, err := loadContext(runOptions{ConfigPath: *configPath})
	if err != nil {
		return err
	}
	lock, err := readLockFile(repoRoot)
	if err != nil {
		return err
	}
	type configuredSource struct {
		Name          string   `json:"name"`
		URL           string   `json:"url"`
		Commit        string   `json:"commit,omitempty"`
		Skills        []string `json:"skills,omitempty"`
		ExcludeAgents []string `json:"exclude_agents,omitempty"`
	}
	configured := []configuredSource{}
	for _, src := range cfg.ExternalSkills {
		item := configuredSource{Name: sourceName(src), URL: redactURL(src.URL), ExcludeAgents: src.ExcludeAgents}
		if pin := lockEntryFor(lock, src); pin != nil {
			item.Commit = pin.Commit
			item.Skills = pin.Materialized.Values()
		}
		if len(item.Skills) == 0 {
			item.Skills = src.Skills
		}
		configured = append(configured, item)
	}
	outside := []installedSource{}
	for _, found := range detectInstalledSources(home) {
		if !sourceConfigured(cfg, found.Repo, found.Path) {
			outside = append(outside, found)
		}
	}
	if *jsonOut {
		return encodeJSON(out, map[string]interface{}{"configured": configured, "installed_elsewhere": outside})
	}
	if len(configured) == 0 {
		fmt.Fprintln(out, "No external skill sources in tackroom.yaml.")
	} else {
		fmt.Fprintln(out, "In tackroom.yaml:")
		for _, item := range configured {
			line := fmt.Sprintf("  %-20s %s", item.Name, item.URL)
			if item.Commit != "" {
				line += " @ " + shortCommit(item.Commit)
			}
			if len(item.Skills) > 0 {
				line += " (" + pluralSkills(len(item.Skills)) + ")"
			}
			if len(item.ExcludeAgents) > 0 {
				line += ", not for " + strings.Join(item.ExcludeAgents, ", ")
			}
			fmt.Fprintln(out, line)
		}
	}
	if len(outside) > 0 {
		fmt.Fprintln(out, "\nInstalled outside tackroom:")
		for _, found := range outside {
			fmt.Fprintf(out, "  %-40s %s\n", sourceSlugWithPath(found.Repo, found.Path), found.Via)
			fmt.Fprintf(out, "    add: tackroom skill sources add %s %s\n", shellArg(sourceSlugWithPath(found.Repo, found.Path)), suggestedSkillsFlag(found.Skills))
		}
	}
	fmt.Fprintln(out, "\nFind more: tackroom skill sources search <query>")
	return nil
}

func pluralSkills(n int) string {
	if n == 1 {
		return "1 skill"
	}
	return strconv.Itoa(n) + " skills"
}

func suggestedSkillsFlag(skills []string) string {
	if len(skills) == 0 {
		return "--all"
	}
	return "--skills " + shellArg(strings.Join(skills, ","))
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9._/,:@=+-]+$`)

// shellArg makes a suggested command safe to paste, whatever names the plugin
// registry or skill lock held.
func shellArg(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return shellQuote(s)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// redactURL drops credentials and query strings from a source URL before it
// is printed.
func redactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.User == nil && parsed.RawQuery == "") {
		return raw
	}
	parsed.User = nil
	parsed.RawQuery = ""
	return parsed.String()
}

func sourceSlugWithPath(repo, sub string) string {
	if sub == "" {
		return repo
	}
	return repo + "/" + sub
}

// sourceConfigured reports whether tackroom.yaml already has a source from
// this GitHub repo covering sub (any path of it when sub is empty).
func sourceConfigured(cfg config, repo string, sub string) bool {
	for _, src := range cfg.ExternalSkills {
		if slug := githubSlug(src.URL); slug != "" && strings.EqualFold(slug, repo) && (sub == "" || overlapsPath(externalSkillDirs(src), sub)) {
			return true
		}
	}
	return false
}

// detectInstalledSources lists skill sources installed by Claude Code plugins
// and by `npx skills`. Unreadable or missing registries are skipped.
func detectInstalledSources(home string) []installedSource {
	found := append(detectClaudePluginSources(home), detectNpxSkillSources(home)...)
	sort.SliceStable(found, func(i, j int) bool { return found[i].Repo < found[j].Repo })
	return found
}

func detectClaudePluginSources(home string) []installedSource {
	pluginsDir := filepath.Join(home, ".claude", "plugins")
	var installed struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if readJSONFile(filepath.Join(pluginsDir, "installed_plugins.json"), &installed) != nil {
		return nil
	}
	var marketplaces map[string]struct {
		Source struct {
			Source string `json:"source"`
			Repo   string `json:"repo"`
			URL    string `json:"url"`
		} `json:"source"`
		InstallLocation string `json:"installLocation"`
	}
	_ = readJSONFile(filepath.Join(pluginsDir, "known_marketplaces.json"), &marketplaces)
	var result []installedSource
	keys := make([]string, 0, len(installed.Plugins))
	for key := range installed.Plugins {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		plugin, marketplace, ok := strings.Cut(key, "@")
		if !ok {
			continue
		}
		// Only plugins that ship skills are skill sources.
		hasSkills := false
		for _, install := range installed.Plugins[key] {
			if countSkillDirs(install.InstallPath) > 0 {
				hasSkills = true
			}
		}
		if !hasSkills {
			continue
		}
		market, known := marketplaces[marketplace]
		if !known {
			continue
		}
		marketRepo := market.Source.Repo
		if marketRepo == "" {
			marketRepo = githubSlug(market.Source.URL)
		}
		repo, sub := claudePluginRepo(market.InstallLocation, plugin, marketRepo)
		if repo == "" {
			continue
		}
		result = append(result, installedSource{Repo: repo, Path: sub, Via: "Claude Code plugin " + key})
	}
	return result
}

// claudePluginRepo resolves where a marketplace plugin comes from: a GitHub
// URL in its marketplace entry, or a path inside the marketplace's own repo.
func claudePluginRepo(marketDir string, plugin string, marketRepo string) (string, string) {
	var manifest struct {
		Plugins []struct {
			Name   string          `json:"name"`
			Source json.RawMessage `json:"source"`
		} `json:"plugins"`
	}
	if readJSONFile(filepath.Join(marketDir, ".claude-plugin", "marketplace.json"), &manifest) != nil {
		return "", ""
	}
	for _, entry := range manifest.Plugins {
		if entry.Name != plugin {
			continue
		}
		var rel string
		if json.Unmarshal(entry.Source, &rel) == nil {
			if marketRepo == "" {
				return "", ""
			}
			sub := strings.Trim(path.Clean(strings.TrimPrefix(rel, "./")), "/")
			if sub == "." {
				sub = ""
			}
			return marketRepo, sub
		}
		var src struct {
			Source string `json:"source"`
			Repo   string `json:"repo"`
			URL    string `json:"url"`
			Path   string `json:"path"`
		}
		if json.Unmarshal(entry.Source, &src) != nil {
			return "", ""
		}
		if src.Repo != "" {
			return src.Repo, strings.Trim(src.Path, "/")
		}
		if slug := githubSlug(src.URL); slug != "" {
			return slug, strings.Trim(src.Path, "/")
		}
	}
	return "", ""
}

// detectNpxSkillSources reads the `npx skills` lock, which lives at
// $XDG_STATE_HOME/skills/.skill-lock.json or ~/.agents/.skill-lock.json.
func detectNpxSkillSources(home string) []installedSource {
	lockPath := filepath.Join(home, ".agents", ".skill-lock.json")
	if state := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); state != "" {
		lockPath = filepath.Join(state, "skills", ".skill-lock.json")
	}
	var lock struct {
		Skills map[string]struct {
			Source     string `json:"source"`
			SourceType string `json:"sourceType"`
		} `json:"skills"`
	}
	if readJSONFile(lockPath, &lock) != nil {
		return nil
	}
	bySource := map[string][]string{}
	for name, entry := range lock.Skills {
		if entry.SourceType == "github" && strings.Count(entry.Source, "/") == 1 {
			bySource[entry.Source] = append(bySource[entry.Source], name)
		}
	}
	var result []installedSource
	for repo, skills := range bySource {
		sort.Strings(skills)
		result = append(result, installedSource{Repo: repo, Via: "npx skills (" + strings.Join(skills, ", ") + ")", Skills: skills})
	}
	return result
}

func readJSONFile(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func countSkillDirs(root string) int {
	count := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "SKILL.md" {
			count++
		}
		return nil
	})
	return count
}

var githubURLPattern = regexp.MustCompile(`^(?:https?://|ssh://git@|git@)github\.com[:/]([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?/?$`)

// githubSlug returns owner/repo for a GitHub clone URL, else "".
func githubSlug(raw string) string {
	match := githubURLPattern.FindStringSubmatch(strings.TrimSpace(raw))
	if match == nil {
		return ""
	}
	return match[1] + "/" + match[2]
}

// registrySource groups skills.sh search hits by their source repo.
type registrySource struct {
	Repo      string   `json:"repo"`
	Installs  int      `json:"installs"`
	Skills    []string `json:"skills"`
	Installed string   `json:"installed,omitempty"`
}

func runSkillSourcesSearch(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("skill sources search", flag.ContinueOnError)
	configPath := flags.String("config", "", "Path to tackroom YAML config")
	jsonOut := flags.Bool("json", false, "Print JSON")
	positional, err := parseInterleaved(flags, args)
	if err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(positional, " "))
	if len(query) < 2 {
		return errors.New("usage: tackroom skill sources search <query> (at least 2 characters)")
	}
	_, home, cfg, _, err := loadContext(runOptions{ConfigPath: *configPath})
	if err != nil {
		return err
	}
	sources, err := searchSkillsRegistry(query)
	if err != nil {
		return err
	}
	installed := map[string]string{}
	for _, found := range detectInstalledSources(home) {
		installed[strings.ToLower(found.Repo)] = found.Via
	}
	for i := range sources {
		if sourceConfigured(cfg, sources[i].Repo, "") {
			sources[i].Installed = "in tackroom.yaml"
		} else if via, ok := installed[strings.ToLower(sources[i].Repo)]; ok {
			sources[i].Installed = via
		}
	}
	if *jsonOut {
		return encodeJSON(out, sources)
	}
	if len(sources) == 0 {
		fmt.Fprintf(out, "No skill sources on skills.sh match %q.\n", query)
		return nil
	}
	fmt.Fprintf(out, "Skill sources on skills.sh matching %q:\n", query)
	for _, src := range sources {
		skills := src.Skills
		more := ""
		if len(skills) > 5 {
			more = fmt.Sprintf(", +%d more", len(skills)-5)
			skills = skills[:5]
		}
		fmt.Fprintf(out, "  %-32s %10s  %s%s\n", src.Repo, formatInstalls(src.Installs), strings.Join(skills, ", "), more)
		if src.Installed != "" {
			fmt.Fprintf(out, "  %-32s %10s  installed: %s\n", "", "", src.Installed)
		}
	}
	fmt.Fprintln(out, "\nAdd one: tackroom skill sources add <owner/repo> [--skills a,b | --all]")
	return nil
}

// searchSkillsRegistry queries skills.sh and groups hits by GitHub repo,
// most-installed first.
func searchSkillsRegistry(query string) ([]registrySource, error) {
	endpoint := strings.TrimSuffix(skillsRegistryURL, "/") + "/api/search?" + url.Values{"q": {query}, "limit": {"50"}}.Encode()
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("search skills.sh: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search skills.sh: %s", resp.Status)
	}
	var body struct {
		Skills []struct {
			Name     string `json:"name"`
			Source   string `json:"source"`
			Installs int    `json:"installs"`
		} `json:"skills"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("search skills.sh: decode: %w", err)
	}
	sort.SliceStable(body.Skills, func(i, j int) bool { return body.Skills[i].Installs > body.Skills[j].Installs })
	index := map[string]int{}
	var sources []registrySource
	for _, hit := range body.Skills {
		// Only GitHub owner/repo sources can be added.
		if strings.Count(hit.Source, "/") != 1 || githubSlug("https://github.com/"+hit.Source) == "" {
			continue
		}
		key := strings.ToLower(hit.Source)
		i, ok := index[key]
		if !ok {
			i = len(sources)
			index[key] = i
			sources = append(sources, registrySource{Repo: hit.Source})
		}
		sources[i].Installs += hit.Installs
		sources[i].Skills = append(sources[i].Skills, hit.Name)
	}
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].Installs > sources[j].Installs })
	return sources, nil
}

func formatInstalls(n int) string {
	switch {
	case n >= 1_000_000:
		return strconv.FormatFloat(float64(n)/1_000_000, 'f', 1, 64) + "M"
	case n >= 1_000:
		return strconv.FormatFloat(float64(n)/1_000, 'f', 1, 64) + "K"
	default:
		return strconv.Itoa(n)
	}
}

// sourceSkill is a skill directory found in a source repo; Dir is its parent,
// relative to the repo root, which becomes a skill_dirs entry. Root marks the
// repo itself as the skill (a SKILL.md at its root): Dir is "." and the skill
// is named by rootSkillName.
type sourceSkill struct {
	Name string
	Dir  string
	Root bool
}

// path is the skill's own directory relative to the repo root.
func (s sourceSkill) path() string {
	if s.Root {
		return "."
	}
	return path.Join(s.Dir, s.Name)
}

func runSkillSourcesAdd(args []string, in io.Reader, out io.Writer, interactive bool) error {
	flags := flag.NewFlagSet("skill sources add", flag.ContinueOnError)
	configPath := flags.String("config", "", "Path to tackroom YAML config")
	skillsCSV := flags.String("skills", "", "Comma-separated skills to add")
	all := flags.Bool("all", false, "Add every skill in the source, including ones added upstream later")
	branch := flags.String("branch", "", "Branch to track (default: the repo's default branch)")
	name := flags.String("name", "", "Source name in tackroom.yaml (default: the repo name)")
	latest := flags.Bool("latest", false, "Pin the newest commit instead of one at least 3 days old")
	noSync := flags.Bool("no-sync", false, "Only write tackroom.yaml and tackroom.lock")
	var fresh runOptions
	addAllowFreshFlags(flags, &fresh)
	positional, err := parseInterleaved(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New("usage: tackroom skill sources add <owner/repo[/path] | git URL> [--skills a,b | --all]")
	}
	if *all && strings.TrimSpace(*skillsCSV) != "" {
		return errors.New("use either --skills or --all")
	}
	if *name != "" && !safeSourceName(*name) {
		return fmt.Errorf("--name %q: use a plain directory name", *name)
	}
	repoURL, sub, urlBranch, err := parseSourceArg(positional[0])
	if err != nil {
		return err
	}
	if *branch == "" {
		*branch = urlBranch
	}

	repoRoot, home, cfg, _, err := loadContext(runOptions{ConfigPath: *configPath})
	if err != nil {
		return err
	}
	// Before the fetch and the lock write, with or without --no-sync: a second
	// config that does not list what the shared lock pins would otherwise reuse
	// a pinned source's checkout and replace its lock entry.
	if err := checkSharedLock(*configPath, home, cfg); err != nil {
		return err
	}
	if *branch == "" {
		if *branch, err = gitDefaultBranch(repoURL); err != nil {
			return err
		}
	}
	for _, existing := range cfg.ExternalSkills {
		if existing.URL == repoURL && (sub == "" || overlapsPath(externalSkillDirs(existing), sub)) {
			return fmt.Errorf("%s is already in tackroom.yaml as %q; edit its skills there, then run tackroom sync", repoURL, sourceName(existing))
		}
	}
	src := externalSkillSource{URL: repoURL, Branch: *branch, Materialize: true}
	if *name != "" {
		src.Name = *name
	} else if base := path.Base(sub); sub != "" && base != "skills" {
		src.Name = base
	}
	if taken(cfg, sourceName(src)) && *name == "" {
		owner := strings.SplitN(githubSlug(repoURL)+"/", "/", 2)[0]
		src.Name = strings.Trim(owner+"-"+sourceName(src), "-")
	}
	if taken(cfg, sourceName(src)) {
		return fmt.Errorf("tackroom.yaml already has a source named %q; pass --name", sourceName(src))
	}

	cachePath := filepath.Join(externalCacheDir(home), sourceName(src))
	commit, err := fetchSourceForPin(src, cachePath, *latest)
	if err != nil {
		return err
	}
	found, err := findSourceSkills(cachePath, sub)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		return fmt.Errorf("no skills (directories with SKILL.md) found in %s", sourceSlugWithPath(repoURL, sub))
	}
	selected, err := chooseSourceSkills(found, *skillsCSV, *all, in, out, interactive)
	if err != nil {
		return err
	}
	var names []string
	for _, skill := range selected {
		names = append(names, skill.Name)
	}
	src.SkillDirs = sourceSkillDirs(found, selected, !*all, hasFile(filepath.Join(cachePath, "SKILL.md")))
	sort.Strings(names)
	if !*all {
		src.Skills = names
	}
	for _, existing := range cfg.ExternalSkills {
		if existing.URL == src.URL && overlaps(externalSkillDirs(existing), src.SkillDirs) {
			return fmt.Errorf("%s is already in tackroom.yaml as %q; edit its skills there, then run tackroom sync", repoURL, sourceName(existing))
		}
	}

	if plugin := matchingClaudePlugin(home, githubSlug(repoURL), sub); plugin != "" && agentEnabled(cfg, agentClaudeCode) {
		src.ExcludeAgents = []string{agentClaudeCode}
		fmt.Fprintf(out, "Claude Code already has this source as plugin %s; tackroom adds it for the other agents only.\n", plugin)
	}

	// A --latest head is in cooldown like any new pin until it is 72 hours old; an
	// older commit never is, and a local source has no publish time.
	freshGate := gateOptionsFor(fresh)
	var gate *gateOptions
	if *latest && !isLocalSourceURL(src.URL) {
		gate = &freshGate
	}
	verdict, gated, err := writeSourceEntry(*configPath, repoRoot, home, src, commit, names, selected, cachePath, gate)
	if err != nil {
		return err
	}
	inCooldown := map[string]gatedItem{}
	if gated {
		inCooldown[sourceName(src)] = verdict
	}
	warnUnmatchedAllowFresh(os.Stderr, freshGate, inCooldown)
	label := repoURL
	if sub != "" {
		label += " (" + sub + ")"
	}
	if gated {
		fmt.Fprintf(out, "added %s as %q: %s at %s (%s), not pinned (%s)\n", label, sourceName(src), pluralSkills(len(names)), shortCommit(commit), gitCommitDate(cachePath, commit), verdict.Kind)
		fmt.Fprintln(out, gatedLines(map[string]gatedItem{sourceName(src): verdict})[0])
	} else {
		fmt.Fprintf(out, "added %s as %q: %s pinned at %s (%s)\n", label, sourceName(src), pluralSkills(len(names)), shortCommit(commit), gitCommitDate(cachePath, commit))
	}
	if *noSync {
		fmt.Fprintln(out, "next: tackroom sync")
		return nil
	}
	return runSync(runOptions{ConfigPath: *configPath, Stdout: out})
}

// writeSourceEntry re-reads tackroom.yaml and tackroom.lock (a clone takes long
// enough for another add or a sync to change them), writes the lock pin first
// so the source is never configured without it, then the config entry. A
// failed config write restores the previous lock. A commit with an unaccepted
// CRITICAL finding, or (with gate set) in cooldown, gets no pin, so sync gates
// the source like any new unpinned one; the gate verdict is returned.
func writeSourceEntry(configPath string, repoRoot string, home string, src externalSkillSource, commit string, names []string, selected []sourceSkill, cachePath string, gate *gateOptions) (gatedItem, bool, error) {
	shared, sharedPath, err := loadEditableMCPConfig(configPath)
	if err != nil {
		return gatedItem{}, false, err
	}
	for _, existing := range shared.ExternalSkills {
		if strings.EqualFold(sourceName(existing), sourceName(src)) || (existing.URL == src.URL && overlaps(externalSkillDirs(existing), src.SkillDirs)) {
			return gatedItem{}, false, fmt.Errorf("tackroom.yaml changed meanwhile and already has %q; run the add again", sourceName(existing))
		}
	}
	lock, err := readLockFile(repoRoot)
	if err != nil {
		return gatedItem{}, false, err
	}
	if err := checkCanonicalSkillTargets(selected, cachePath, repoRoot, lock, sourceName(src)); err != nil {
		return gatedItem{}, false, err
	}
	// The same audit gate sync applies to new content, then the cooldown.
	item := externalSourceItem(src, home)
	verdict, gated := auditBlock(item, lock)
	if !gated && gate != nil {
		verdict, gated = gateSourceFreshness(src, home, item.ID(), &lock, *gate)
	}
	// Drop an entry left by a source of this name removed without a sync.
	kept := lock.ExternalSkills[:0]
	for _, entry := range lock.ExternalSkills {
		if entry.Name != sourceName(src) {
			kept = append(kept, entry)
		}
	}
	lock.ExternalSkills = kept
	if !gated {
		lock.ExternalSkills = append(kept, externalLockEntry{
			Name: sourceName(src), URL: src.URL, Branch: src.Branch, Commit: commit,
			Materialized: newMaterializedSkillNames(names),
		})
	}
	lockPath := lockFilePath(repoRoot)
	previous, readErr := os.ReadFile(lockPath)
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return gatedItem{}, false, readErr
	}
	if err := writeLockFile(repoRoot, lock); err != nil {
		return gatedItem{}, false, err
	}
	shared.ExternalSkills = append(shared.ExternalSkills, src)
	if err := writeEditableMCPConfig(sharedPath, shared); err != nil {
		var restoreErr error
		if readErr != nil {
			restoreErr = os.Remove(lockPath)
		} else {
			restoreErr = atomicConfigWrite(lockPath, previous, 0o644)
		}
		if restoreErr != nil {
			restoreErr = fmt.Errorf("restore %s: %w", lockPath, restoreErr)
		}
		return gatedItem{}, false, errors.Join(err, restoreErr)
	}
	return verdict, gated, nil
}

// parseInterleaved parses flags that may also follow positional arguments
// (`search grill --json`) and returns the positional ones.
func parseInterleaved(flags *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := flags.Parse(args); err != nil {
			return nil, err
		}
		if flags.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, flags.Arg(0))
		args = flags.Args()[1:]
	}
}

func safeSourceName(name string) bool {
	return name != "." && name != ".." && !strings.ContainsAny(name, `/\`) && strings.TrimSpace(name) == name && name != ""
}

func taken(cfg config, name string) bool {
	for _, src := range cfg.ExternalSkills {
		if strings.EqualFold(sourceName(src), name) {
			return true
		}
	}
	return false
}

func agentEnabled(cfg config, name string) bool {
	for _, agent := range cfg.Agents {
		if normalizeAgentName(agent.Name) == name && agent.Enabled {
			return true
		}
	}
	return false
}

// overlapsPath reports whether sub is one of dirs, inside one, or contains one.
func overlapsPath(dirs []string, sub string) bool {
	for _, dir := range dirs {
		if dir == sub || strings.HasPrefix(dir, sub+"/") || strings.HasPrefix(sub, dir+"/") {
			return true
		}
	}
	return false
}

func overlaps(a, b []string) bool {
	for _, x := range a {
		if stringInSlice(x, b) {
			return true
		}
	}
	return false
}

// parseSourceArg accepts owner/repo[/path], a GitHub URL (optionally a
// /tree/<branch>/<path> link) or any other git URL.
func parseSourceArg(arg string) (repoURL string, sub string, branch string, err error) {
	arg = strings.TrimSuffix(strings.TrimSpace(arg), "/")
	if strings.Contains(arg, "://") || strings.HasPrefix(arg, "git@") {
		if slug := githubSlug(arg); slug != "" {
			return "https://github.com/" + slug, "", "", nil
		}
		parsed, perr := url.Parse(arg)
		if perr == nil && parsed.Host == "github.com" {
			parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
			if len(parts) >= 4 && parts[2] == "tree" {
				if !safeSubpath(parts[4:]) {
					return "", "", "", fmt.Errorf("source %q: bad path", arg)
				}
				return "https://github.com/" + parts[0] + "/" + strings.TrimSuffix(parts[1], ".git"), strings.Join(parts[4:], "/"), parts[3], nil
			}
		}
		return arg, "", "", nil
	}
	parts := strings.Split(arg, "/")
	if len(parts) < 2 || githubSlug("https://github.com/"+parts[0]+"/"+parts[1]) == "" {
		return "", "", "", fmt.Errorf("source %q: want owner/repo[/path] or a git URL", arg)
	}
	if !safeSubpath(parts[2:]) {
		return "", "", "", fmt.Errorf("source %q: bad path", arg)
	}
	return "https://github.com/" + parts[0] + "/" + parts[1], strings.Join(parts[2:], "/"), "", nil
}

func safeSubpath(parts []string) bool {
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.Contains(part, `\`) {
			return false
		}
	}
	return true
}

func gitDefaultBranch(repoURL string) (string, error) {
	output, err := gitRemote("ls-remote", "--symref", repoURL, "HEAD")
	if err != nil {
		return "", fmt.Errorf("read default branch of %s: %w", repoURL, err)
	}
	for _, line := range strings.Split(output, "\n") {
		if ref, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok {
			if branch, _, ok := strings.Cut(ref, "\tHEAD"); ok {
				return branch, nil
			}
		}
	}
	return "", fmt.Errorf("read default branch of %s: no HEAD ref; pass --branch", repoURL)
}

// fetchSourceForPin brings the cache to the newest commit at least
// minSourceCommitAge old (or the branch head with latest) and returns it.
func fetchSourceForPin(src externalSkillSource, cachePath string, latest bool) (string, error) {
	ref := "HEAD"
	if hasDir(filepath.Join(cachePath, ".git")) {
		// Left behind by a source removed from tackroom.yaml: reuse it.
		if err := exec.Command("git", "-C", cachePath, "remote", "set-url", "origin", src.URL).Run(); err != nil {
			return "", fmt.Errorf("update remote URL for %s: %w", sourceName(src), err)
		}
		fetchArgs := []string{"-C", cachePath, "fetch", "--quiet", "origin", src.Branch}
		if gitIsShallow(cachePath) {
			fetchArgs = []string{"-C", cachePath, "fetch", "--quiet", "--unshallow", "origin", src.Branch}
		}
		if _, err := gitRemote(fetchArgs...); err != nil {
			return "", fmt.Errorf("fetch %s: %w", src.URL, err)
		}
		ref = "FETCH_HEAD"
	} else {
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
			return "", err
		}
		// History without blobs: enough to pick an older commit cheaply.
		if _, err := gitRemote("clone", "--quiet", "--filter=blob:none", "--no-checkout", "--branch", src.Branch, src.URL, cachePath); err != nil {
			return "", fmt.Errorf("clone %s: %w", src.URL, err)
		}
	}
	revArgs := []string{"-C", cachePath, "rev-list", "-1"}
	if !latest {
		revArgs = append(revArgs, "--before="+strconv.FormatInt(time.Now().Add(-minSourceCommitAge).Unix(), 10))
	}
	output, err := exec.Command("git", append(revArgs, ref)...).Output()
	if err != nil {
		return "", fmt.Errorf("pick a commit of %s: %w", src.URL, err)
	}
	commit := strings.TrimSpace(string(output))
	if commit == "" {
		return "", fmt.Errorf("%s has no commit older than 3 days on %s; pass --latest to pin the newest one", src.URL, src.Branch)
	}
	// A blob-less clone fetches file contents here.
	if _, err := gitRemote("-C", cachePath, "reset", "--quiet", "--hard", commit); err != nil {
		return "", fmt.Errorf("check out %s: %w", shortCommit(commit), err)
	}
	return commit, nil
}

// gitRemoteTimeout bounds git commands that talk to the remote, so a stalled
// server cannot block `add` forever.
const gitRemoteTimeout = 5 * time.Minute

func gitRemote(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitRemoteTimeout)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return "", fmt.Errorf("timed out after %s", gitRemoteTimeout)
	}
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return string(output), nil
}

func gitCommitDate(repoPath string, commit string) string {
	output, err := exec.Command("git", "-C", repoPath, "show", "-s", "--format=%cs", commit).Output()
	if err != nil {
		return "date unknown"
	}
	return strings.TrimSpace(string(output))
}

// findSourceSkills lists skill directories under sub (the whole repo when
// empty), skipping hidden directories and anything nested inside a skill. A
// SKILL.md at the repo root makes the repo itself the skill, and nothing else is
// listed, as for any skill directory.
func findSourceSkills(repoPath string, sub string) ([]sourceSkill, error) {
	start := filepath.Join(repoPath, filepath.FromSlash(sub))
	if !hasDir(start) {
		return nil, fmt.Errorf("%s not found in the source", sub)
	}
	var found []sourceSkill
	err := filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if p != start && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if !hasFile(filepath.Join(p, "SKILL.md")) {
			return nil
		}
		if p == repoPath {
			found = append(found, sourceSkill{Name: rootSkillName(repoPath), Dir: ".", Root: true})
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(repoPath, filepath.Dir(p))
		if err != nil {
			return err
		}
		found = append(found, sourceSkill{Name: d.Name(), Dir: filepath.ToSlash(rel)})
		return filepath.SkipDir
	})
	sort.Slice(found, func(i, j int) bool {
		if found[i].Dir != found[j].Dir {
			return found[i].Dir < found[j].Dir
		}
		return found[i].Name < found[j].Name
	})
	return found, err
}

// rootSkillName names a skill that is the repo root, which has no folder name
// of its own: the name in its SKILL.md frontmatter, else the directory's name,
// which for a source's cache is the source name.
func rootSkillName(repoPath string) string {
	fm, err := parseSkillFrontmatter(filepath.Join(repoPath, "SKILL.md"))
	name := strings.TrimSpace(fm.Name)
	if err != nil || !safeSourceName(name) || strings.HasPrefix(name, ".") {
		return filepath.Base(repoPath)
	}
	return name
}

// sourceSkillDirs is the skill_dirs list that makes discovery yield the selected
// skills. A folder is listed whole when all its skills are selected, or when a
// skills allowlist narrows it, so skills added there upstream arrive on update.
// Otherwise each selected skill is listed by its own path, so a skipped or
// unselected neighbour is not pulled in. A SKILL.md at the repo root makes "."
// that one skill, so folders at the root are listed by path then.
func sourceSkillDirs(found, selected []sourceSkill, allowlisted bool, rootSkill bool) []string {
	inDir := map[string]int{}
	for _, skill := range found {
		inDir[skill.Dir]++
	}
	selectedInDir := map[string]int{}
	for _, skill := range selected {
		selectedInDir[skill.Dir]++
	}
	entries := map[string]bool{}
	for _, skill := range selected {
		whole := (allowlisted || selectedInDir[skill.Dir] == inDir[skill.Dir]) && (!rootSkill || skill.Dir != ".")
		if whole {
			entries[skill.Dir] = true
		} else {
			entries[skill.path()] = true
		}
	}
	dirs := make([]string, 0, len(entries))
	for dir := range entries {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs
}

func chooseSourceSkills(found []sourceSkill, csv string, all bool, in io.Reader, out io.Writer, interactive bool) ([]sourceSkill, error) {
	var picks []string
	switch {
	case all:
		return uniqueSkills(found)
	case strings.TrimSpace(csv) != "":
		picks = strings.Split(csv, ",")
	case interactive:
		fmt.Fprintln(out, "Skills in this source:")
		for i, skill := range found {
			fmt.Fprintf(out, "  %3d. %-36s %s\n", i+1, skill.Name, skill.Dir)
		}
		fmt.Fprint(out, "Add which? Numbers or names, comma-separated, or all: ")
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && line == "" {
			return nil, errors.New("no skills chosen")
		}
		if strings.TrimSpace(line) == "all" {
			return uniqueSkills(found)
		}
		picks = strings.Split(line, ",")
	default:
		var names []string
		for _, skill := range found {
			names = append(names, skill.Name)
		}
		return nil, fmt.Errorf("choose skills with --skills a,b or --all; this source has %d: %s", len(found), strings.Join(names, ", "))
	}
	var selected []sourceSkill
	seen := map[string]bool{}
	for _, pick := range picks {
		pick = strings.TrimSpace(pick)
		if pick == "" {
			continue
		}
		var matches []sourceSkill
		if n, err := strconv.Atoi(pick); err == nil && n >= 1 && n <= len(found) {
			matches = []sourceSkill{found[n-1]}
		} else {
			for _, skill := range found {
				if skill.Name == pick {
					matches = append(matches, skill)
				}
			}
		}
		switch {
		case len(matches) == 0:
			return nil, fmt.Errorf("no skill %q in this source", pick)
		case len(matches) > 1:
			return nil, fmt.Errorf("skill %q exists in %s and %s; add the source by path to pick one", pick, matches[0].Dir, matches[1].Dir)
		}
		if !seen[matches[0].Name] {
			seen[matches[0].Name] = true
			selected = append(selected, matches[0])
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("no skills chosen")
	}
	return selected, nil
}

// uniqueSkills is the selection for --all: every skill whose name is in one
// folder only. Each name found in several folders is skipped, with one line on
// stderr naming the folders. It fails, with the first such name, only when
// nothing is left to add.
func uniqueSkills(found []sourceSkill) ([]sourceSkill, error) {
	dirs := map[string][]string{}
	for _, skill := range found {
		dirs[skill.Name] = append(dirs[skill.Name], skill.Dir)
	}
	var unique []sourceSkill
	var skipped []string
	for _, skill := range found {
		switch len(dirs[skill.Name]) {
		case 1:
			unique = append(unique, skill)
		default:
			if skill.Dir == dirs[skill.Name][0] {
				skipped = append(skipped, skill.Name)
			}
		}
	}
	if len(unique) == 0 && len(skipped) > 0 {
		return nil, fmt.Errorf("skill %q exists in both %s and %s; pick skills with --skills, or add the source by path", skipped[0], dirs[skipped[0]][0], dirs[skipped[0]][1])
	}
	for _, name := range skipped {
		folders := append([]string(nil), dirs[name]...)
		for i, dir := range folders {
			if dir == "." {
				folders[i] = "the repository root"
			}
		}
		fmt.Fprintf(os.Stderr, "skipped skill %q: it is in %s; --skills cannot choose between them, add the source by path to pick one\n", name, strings.Join(folders, ", "))
	}
	return unique, nil
}

// checkCanonicalSkillTargets refuses to overwrite ~/.agents/skills entries the
// new source does not own. An identical copy (say, from `npx skills add`) is
// adopted; a differing one stops the add.
func checkCanonicalSkillTargets(selected []sourceSkill, cachePath string, repoRoot string, lock lockFile, self string) error {
	owners := map[string]string{}
	for _, entry := range lock.ExternalSkills {
		if entry.Name == self {
			continue // a leftover entry of this source is replaced
		}
		for _, name := range entry.Materialized.Values() {
			owners[name] = entry.Name
		}
	}
	for _, skill := range selected {
		if owner, ok := owners[skill.Name]; ok {
			return fmt.Errorf("skill %q already comes from source %q; leave it out with --skills", skill.Name, owner)
		}
		target := filepath.Join(repoRoot, "skills", skill.Name)
		if _, err := os.Lstat(target); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		same, err := externalTreesEqual(filepath.Join(cachePath, filepath.FromSlash(skill.path())), target, true)
		if err != nil || !same {
			return fmt.Errorf("%s already exists and differs from this source; move it away or leave %s out with --skills", target, skill.Name)
		}
	}
	return nil
}

// matchingClaudePlugin names the Claude Code plugin that installs the same
// repo (and path), or "".
func matchingClaudePlugin(home string, repo string, sub string) string {
	if repo == "" {
		return ""
	}
	for _, found := range detectClaudePluginSources(home) {
		if !strings.EqualFold(found.Repo, repo) {
			continue
		}
		if found.Path == "" || sub == "" || found.Path == sub || strings.HasPrefix(sub, found.Path+"/") || strings.HasPrefix(found.Path, sub+"/") {
			return strings.TrimPrefix(found.Via, "Claude Code plugin ")
		}
	}
	return ""
}
