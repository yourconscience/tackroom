package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// skillOrigins maps a canonical skill name to a human-readable provenance
// label ("local" or "owner/repo@commit" for external sources). Computed from
// tackroom.lock plus the configured external sources, never invented.
func skillOrigins(cfg config, repoRoot string, home string, expected map[string]string) (map[string]string, error) {
	origins := make(map[string]string)
	lock, err := readLockFile(repoRoot)
	if err != nil {
		return nil, err
	}
	for _, entry := range lock.ExternalSkills {
		label := fmt.Sprintf("%s@%s", ownerRepo(entry.URL), shortSha(entry.Commit))
		for _, name := range entry.Materialized.Values() {
			if _, ok := expected[name]; ok {
				origins[name] = label
			}
		}
	}
	directSources := make([]externalSkillSource, 0, len(cfg.ExternalSkills))
	for _, src := range cfg.ExternalSkills {
		if !src.Materialize {
			directSources = append(directSources, src)
		}
	}
	for _, src := range directSources {
		set, err := discoverExternalSourceSkills(src, home)
		if err != nil {
			// An uncloned cache only degrades the label; sync and doctor
			// report the missing clone authoritatively, so this stays
			// best-effort.
			continue
		}
		label := fmt.Sprintf("%s (unpinned)", ownerRepo(src.URL))
		if entry := lockEntryFor(lock, src); entry != nil {
			label = fmt.Sprintf("%s@%s", ownerRepo(src.URL), shortSha(entry.Commit))
		}
		for _, skill := range set {
			if _, ok := expected[skill.Name]; ok {
				origins[skill.Name] = label
			}
		}
	}
	return origins, nil
}

func shortSha(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

// ownerRepo renders "owner/repo" from a git URL; repoName alone is ambiguous
// for personal skill repos whose last path segment is just "skills".
func ownerRepo(url string) string {
	trimmed := strings.TrimSpace(url)
	if _, after, ok := strings.Cut(trimmed, "://"); ok {
		trimmed = after
	}
	trimmed = strings.TrimPrefix(trimmed, "git@")
	if _, after, found := strings.Cut(trimmed, ":"); found {
		trimmed = after
	}
	trimmed = strings.TrimSuffix(strings.TrimSuffix(trimmed, "/"), ".git")
	parts := strings.Split(trimmed, "/")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return trimmed
}

// integrationMissing returns report.Missing entries that are integration-level
// messages rather than skill names. Config-driven harnesses (Amp, Hermes,
// Qwen) record these when their skills configuration is absent, while still
// listing every expected skill as managed.
func integrationMissing(report agentReport) []string {
	expectedSet := make(map[string]bool, len(report.ExpectedSkills))
	for name := range report.ExpectedSkills {
		expectedSet[name] = true
	}
	var out []string
	for _, item := range report.Missing {
		if !expectedSet[item] {
			out = append(out, item)
		}
	}
	return out
}

// skillProvenance renders one detail line for a skill in a harness skill root,
// based on the inspect report plus the symlink target where it matters.
func skillProvenance(name string, report agentReport, origins map[string]string, home string) string {
	origin := origins[name]
	linkPath := filepath.Join(report.SkillRoot, name)
	switch {
	case containsString(report.Managed, name):
		if origin != "" {
			return fmt.Sprintf("managed (external: %s)", origin)
		}
		return "managed (local)"
	case containsString(report.Drifted, name):
		return "drifted symlink -> " + symlinkTarget(linkPath)
	case containsString(report.Missing, name):
		return "missing (not linked)"
	case containsString(report.StaleManaged, name):
		return "stale managed (links into the store but is not expected)"
	case containsString(report.External, name):
		return describeExternalSkillEntry(linkPath, home)
	case conflictMentions(report.Conflicts, linkPath):
		return "conflict (real dir, differs from canonical)"
	default:
		return "unclassified"
	}
}

// conflictMentions reports whether any conflict detail names the skill's path
// in this harness root; inspect stores full sentences, not bare names.
func conflictMentions(conflicts []string, linkPath string) bool {
	for _, conflict := range conflicts {
		if strings.Contains(conflict, linkPath) {
			return true
		}
	}
	return false
}

func describeExternalSkillEntry(path string, home string) string {
	info, err := os.Lstat(path)
	if err != nil {
		return "unreadable"
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "unmanaged dir"
	}
	target := symlinkTarget(path)
	if _, err := os.Stat(path); err != nil {
		return "broken symlink -> " + target
	}
	if isExternalSkillLink(path, target, home) {
		return "external cache link -> " + target
	}
	return "foreign symlink -> " + target
}

func symlinkTarget(linkPath string) string {
	raw, err := os.Readlink(linkPath)
	if err != nil {
		return "?"
	}
	return raw
}

func containsString(list []string, needle string) bool {
	for _, item := range list {
		if item == needle {
			return true
		}
	}
	return false
}

// runSkillList prints, per detected harness, every entry in its skill root
// with provenance: where tackroom put it, where anything else came from,
// and which links are drifted, stale, or broken. Read-only.
func runSkillList(args []string) error {
	opts, err := parseSubcommandFlags("skill list", args)
	if err != nil {
		return err
	}
	repoRoot, home, cfg, selected, err := loadContext(opts)
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
	origins, err := skillOrigins(cfg, repoRoot, home, expected)
	if err != nil {
		return err
	}

	localCount := 0
	for name := range expected {
		if origins[name] == "" {
			localCount++
		}
	}
	fmt.Printf("tackroom skill list\n")
	fmt.Printf("repo: %s (%d canonical skills: %d local, %d external)\n", repoRoot, len(expected), localCount, len(expected)-localCount)

	for _, report := range reports {
		fmt.Println()
		fmt.Printf("%s (%s)\n", report.Name, report.SkillRoot)
		if !report.Detected {
			fmt.Println("  not detected (binary not on PATH)")
			continue
		}
		h := harnessFor(report.Name)
		if h != nil && h.Skills == skillsConfigDriven {
			if missing := integrationMissing(report); len(missing) > 0 {
				fmt.Printf("  integration missing: %s\n", displayList(missing))
			}
			if h.IntegrationNote != "" {
				fmt.Printf("  integration: %s\n", h.IntegrationNote)
			}
			fmt.Printf("  managed (%d): %s\n", len(report.Managed), displayList(report.Managed))
		} else {
			names := sortedKeys(report.ExpectedSkills)
			names = append(names, report.External...)
			names = append(names, report.StaleManaged...)
			sort.Strings(names)
			names = dedupeStrings(names)
			printed := 0
			for _, name := range names {
				if strings.HasPrefix(name, ".") {
					continue
				}
				fmt.Printf("  %-24s %s\n", name, skillProvenance(name, report, origins, home))
				printed++
			}
			if printed == 0 {
				fmt.Println("  (empty skill root)")
			}
		}
		listingBytes := skillListingBytes(report.ExpectedSkills)
		fmt.Printf("  skill listing context: %d skills, %d bytes name+desc, %s\n", len(report.ExpectedSkills), listingBytes, formatTokenEstimate(estimateTokens(listingBytes)))
	}
	return nil
}

// runSkillInfo prints canonical provenance and per-harness state for one
// skill: where the canonical copy lives, which source pinned it, and how
// every detected harness currently sees it.
func runSkillInfo(args []string) error {
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		return errors.New("skill info requires a skill name")
	}
	name := args[0]
	opts, err := parseSubcommandFlags("skill info", args[1:])
	if err != nil {
		return err
	}
	repoRoot, home, cfg, selected, err := loadContext(opts)
	if err != nil {
		return err
	}
	expected, err := expectedSkills(repoRoot, home, cfg)
	if err != nil {
		return err
	}
	canonical, ok := expected[name]
	if !ok {
		return fmt.Errorf("skill %q is not in the canonical skill set", name)
	}
	origins, err := skillOrigins(cfg, repoRoot, home, expected)
	if err != nil {
		return err
	}

	fmt.Printf("tackroom skill info %s\n", name)
	origin := origins[name]
	if origin != "" {
		fmt.Printf("canonical: %s (external: %s)\n", canonical, origin)
	} else {
		fmt.Printf("canonical: %s (local)\n", canonical)
	}

	single := map[string]string{name: canonical}
	listingBytes := skillListingBytes(single)
	fmt.Printf("SKILL.md listing: %d bytes name+desc, %s\n", listingBytes, formatTokenEstimate(estimateTokens(listingBytes)))

	reports, err := inspectAgents(selected, single, repoRoot, home, cfg)
	if err != nil {
		return err
	}
	for _, report := range reports {
		fmt.Printf("  %-14s ", report.Name)
		if !report.Detected {
			fmt.Println("not detected")
			continue
		}
		if missing := integrationMissing(report); len(missing) > 0 {
			fmt.Printf("%s: integration missing: %s\n", report.SkillRoot, displayList(missing))
			continue
		}
		fmt.Printf("%s: %s\n", report.SkillRoot, skillProvenance(name, report, origins, home))
	}
	return nil
}

func dedupeStrings(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := items[:0]
	for _, item := range items {
		if seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}
