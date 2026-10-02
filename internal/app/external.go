package app

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func externalCacheDir(home string) string {
	return filepath.Join(home, ".agents", "external")
}

func repoName(url string) string {
	u := strings.TrimSpace(url)
	u = strings.TrimSuffix(u, "/")
	u = strings.TrimSuffix(u, ".git")
	if idx := strings.LastIndex(u, "/"); idx >= 0 {
		u = u[idx+1:]
	}
	return strings.TrimSpace(u)
}

// syncExternalRepos checks out every source at its lock pin, records pins for
// newly configured sources, and refreshes materialized copies before harness
// reconciliation can observe them.
func syncExternalRepos(sources []externalSkillSource, home string, repoRoot string) error {
	lock, err := readLockFile(repoRoot)
	if err != nil {
		return err
	}
	cacheRoot := externalCacheDir(home)
	if len(sources) > 0 {
		if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", cacheRoot, err)
		}
	}
	unavailable := make(map[string]bool)
	for _, src := range sources {
		name := repoName(src.URL)
		cachePath := filepath.Join(cacheRoot, name)
		if err := refreshExternalCache(src, cachePath, lock); err != nil {
			if committedMaterializationCurrent(src, lock, repoRoot) {
				fmt.Fprintf(os.Stderr, "warning: external %s unavailable (%v); keeping committed skill copies\n", name, err)
				unavailable[name] = true
				continue
			}
			return err
		}
	}
	plan, err := planMaterializedSkills(sources, home, repoRoot, lock, true, unavailable)
	if err != nil {
		return err
	}
	if err := writeLockIfChanged(sources, home, repoRoot, lock); err != nil {
		return err
	}
	if err := applyMaterializedSkills(plan); err != nil {
		return restoreMaterializationLock(repoRoot, lock, err)
	}
	return nil
}

// refreshExternalCache brings the local cache for src to its lock pin (or the
// branch head when unpinned), cloning it first when absent.
func refreshExternalCache(src externalSkillSource, cachePath string, lock lockFile) error {
	name := repoName(src.URL)
	if hasDir(filepath.Join(cachePath, ".git")) {
		setURL := exec.Command("git", "-C", cachePath, "remote", "set-url", "origin", src.URL)
		if err := setURL.Run(); err != nil {
			return fmt.Errorf("update remote URL for %s: %w", name, err)
		}
	} else if err := gitClone(src.URL, src.Branch, cachePath); err != nil {
		return fmt.Errorf("clone external %s: %w", name, err)
	}
	if pin := lockEntryFor(lock, src); pin != nil {
		if err := gitCheckoutCommit(cachePath, pin.Commit, src.Branch); err != nil {
			return fmt.Errorf("pin external %s to %s: %w", name, pin.Commit, err)
		}
	} else if err := gitFetchReset(cachePath, src.Branch); err != nil {
		return fmt.Errorf("update external %s: %w", name, err)
	}
	return nil
}

// committedMaterializationCurrent reports whether every lock-owned
// materialized skill of src already exists in the repo, so sync can keep the
// committed copies when the upstream cache cannot be refreshed.
func committedMaterializationCurrent(src externalSkillSource, lock lockFile, repoRoot string) bool {
	if !src.Materialize {
		return false
	}
	pin := lockEntryFor(lock, src)
	if pin == nil || len(pin.Materialized.Values()) == 0 {
		return false
	}
	for _, name := range pin.Materialized.Values() {
		if name == "" || filepath.Base(name) != name {
			return false
		}
		if !hasDir(filepath.Join(repoRoot, "skills", name)) {
			return false
		}
	}
	return true
}

// updateExternalRepos advances the named source pins (or all when names is
// empty), writes the lock, then immediately refreshes their canonical copies.
func updateExternalRepos(sources []externalSkillSource, home string, repoRoot string, names []string) error {
	selected, err := selectExternalSources(sources, names)
	if err != nil {
		return err
	}
	lock, err := readLockFile(repoRoot)
	if err != nil {
		return err
	}
	cacheRoot := externalCacheDir(home)
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", cacheRoot, err)
	}
	for _, src := range selected {
		name := repoName(src.URL)
		cachePath := filepath.Join(cacheRoot, name)
		if !hasDir(filepath.Join(cachePath, ".git")) {
			if err := gitClone(src.URL, src.Branch, cachePath); err != nil {
				return fmt.Errorf("clone external %s: %w", name, err)
			}
		} else if err := gitFetchReset(cachePath, src.Branch); err != nil {
			return fmt.Errorf("update external %s: %w", name, err)
		}
		fmt.Printf("updated %s -> %s\n", name, externalSkillCommit(cachePath))
	}
	plan, err := planMaterializedSkills(selected, home, repoRoot, lock, false, nil)
	if err != nil {
		return err
	}
	if err := writeLockAfterUpdate(sources, home, repoRoot, lock, selected); err != nil {
		return err
	}
	if err := applyMaterializedSkills(plan); err != nil {
		return restoreMaterializationLock(repoRoot, lock, err)
	}
	return nil
}

func selectExternalSources(sources []externalSkillSource, names []string) ([]externalSkillSource, error) {
	if len(names) == 0 {
		return sources, nil
	}
	index := make(map[string]externalSkillSource, len(sources))
	for _, src := range sources {
		index[repoName(src.URL)] = src
	}
	var selected []externalSkillSource
	for _, name := range names {
		src, ok := index[strings.TrimSpace(name)]
		if !ok {
			return nil, fmt.Errorf("unknown external source %q; configured: %s", name, strings.Join(externalSourceNames(sources), ", "))
		}
		selected = append(selected, src)
	}
	return selected, nil
}

func externalSourceNames(sources []externalSkillSource) []string {
	var names []string
	for _, src := range sources {
		names = append(names, repoName(src.URL))
	}
	return names
}

func writeLockIfChanged(sources []externalSkillSource, home string, repoRoot string, lock lockFile) error {
	entries := rebuildLockEntries(sources, home, lock)
	if lockEntriesEqual(lock.ExternalSkills, entries) {
		return nil
	}
	lock.ExternalSkills = entries
	return writeLockFile(repoRoot, lock)
}

func writeLockAfterUpdate(sources []externalSkillSource, home string, repoRoot string, lock lockFile, selected []externalSkillSource) error {
	selectedNames := make(map[string]bool, len(selected))
	for _, src := range selected {
		selectedNames[repoName(src.URL)] = true
	}
	entries := rebuildLockEntries(sources, home, lock)
	for i := range entries {
		if selectedNames[entries[i].Name] {
			continue
		}
		for _, src := range sources {
			if repoName(src.URL) != entries[i].Name {
				continue
			}
			if pin := lockEntryFor(lock, src); pin != nil {
				entries[i].Commit = pin.Commit
				entries[i].Materialized = pin.Materialized
			}
			break
		}
	}
	if lockEntriesEqual(lock.ExternalSkills, entries) {
		return nil
	}
	lock.ExternalSkills = entries
	return writeLockFile(repoRoot, lock)
}

func restoreMaterializationLock(repoRoot string, previous lockFile, materializeErr error) error {
	if err := writeLockFile(repoRoot, previous); err != nil {
		return fmt.Errorf("%v; restore previous %s: %w", materializeErr, lockFileName, err)
	}
	return materializeErr
}

func gitClone(url string, branch string, dest string) error {
	cmd := exec.Command("git", "clone", "--depth", "1", "--branch", branch, url, dest)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func gitFetchReset(repoPath string, branch string) error {
	fetch := exec.Command("git", "-C", repoPath, "fetch", "origin", branch)
	fetch.Stdout = os.Stdout
	fetch.Stderr = os.Stderr
	if err := fetch.Run(); err != nil {
		return err
	}
	reset := exec.Command("git", "-C", repoPath, "reset", "--hard", "origin/"+branch)
	reset.Stdout = os.Stdout
	reset.Stderr = os.Stderr
	return reset.Run()
}

// gitCheckoutCommit hard-resets the cache to a pinned commit, fetching it when
// the shallow clone does not contain it yet.
func gitCheckoutCommit(repoPath string, commit string, branch string) error {
	if externalSkillCommitFull(repoPath) == commit {
		return nil
	}
	if !gitHasCommit(repoPath, commit) {
		// Hosts like GitHub allow shallow fetches of an exact commit.
		fetchSHA := exec.Command("git", "-C", repoPath, "fetch", "--depth", "1", "origin", commit)
		fetchSHA.Stdout = os.Stdout
		fetchSHA.Stderr = os.Stderr
		if err := fetchSHA.Run(); err != nil {
			// Fall back to fetching the branch history; --unshallow is only
			// valid while the clone is still shallow.
			fetchArgs := []string{"-C", repoPath, "fetch", "origin", branch}
			if gitIsShallow(repoPath) {
				fetchArgs = []string{"-C", repoPath, "fetch", "--unshallow", "origin", branch}
			}
			fetchAll := exec.Command("git", fetchArgs...)
			fetchAll.Stdout = os.Stdout
			fetchAll.Stderr = os.Stderr
			if fallbackErr := fetchAll.Run(); fallbackErr != nil {
				return fmt.Errorf("fetch pinned commit: sha fetch: %v; branch fetch: %w", err, fallbackErr)
			}
		}
	}
	reset := exec.Command("git", "-C", repoPath, "reset", "--hard", commit)
	reset.Stdout = os.Stdout
	reset.Stderr = os.Stderr
	return reset.Run()
}

func gitHasCommit(repoPath string, commit string) bool {
	return exec.Command("git", "-C", repoPath, "cat-file", "-e", commit+"^{commit}").Run() == nil
}

func gitIsShallow(repoPath string) bool {
	out, err := exec.Command("git", "-C", repoPath, "rev-parse", "--is-shallow-repository").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

func discoverExternalSkills(sources []externalSkillSource, home string) (map[string]string, error) {
	discovered, err := discoverExternalSkillSet(sources, home)
	if err != nil {
		return nil, err
	}
	return discoveredSkillPaths(discovered), nil
}

func discoverExternalSkillSet(sources []externalSkillSource, home string) (map[string]discoveredSkill, error) {
	result := make(map[string]discoveredSkill)
	for _, src := range sources {
		discovered, err := discoverExternalSourceSkills(src, home)
		if err != nil {
			return nil, err
		}
		if err := mergeDiscoveredSkills(result, discovered); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func discoverExternalSourceSkills(src externalSkillSource, home string) ([]discoveredSkill, error) {
	name := repoName(src.URL)
	cachePath := filepath.Join(externalCacheDir(home), name)
	if !hasDir(filepath.Join(cachePath, ".git")) {
		return nil, fmt.Errorf("external source %s not cloned; run tackroom sync", src.URL)
	}
	resolvedCache, err := filepath.EvalSymlinks(cachePath)
	if err != nil {
		return nil, fmt.Errorf("resolve external source %s: %w", src.URL, err)
	}

	origin := fmt.Sprintf("external Git %q", src.URL)
	allowed := skillAllowlist(src)
	discovered := make(map[string]discoveredSkill)
	found := make(map[string]bool)
	pluginRelaxed := isPluginRelaxedSource(src, cachePath)
	for _, relDir := range externalSkillDirs(src) {
		clean, err := cleanExternalSkillDir(relDir)
		if err != nil {
			return nil, fmt.Errorf("external source %s: %w", src.URL, err)
		}
		skillBase := filepath.Join(cachePath, filepath.FromSlash(clean))
		if !hasDir(skillBase) {
			if pluginRelaxed {
				continue
			}
			return nil, fmt.Errorf("skill directory %q not found in cloned external source %s", clean, src.URL)
		}
		resolvedBase, err := filepath.EvalSymlinks(skillBase)
		if err != nil {
			return nil, fmt.Errorf("resolve skill directory %q in external source %s: %w", clean, src.URL, err)
		}
		if resolvedBase != resolvedCache && !strings.HasPrefix(resolvedBase, resolvedCache+string(os.PathSeparator)) {
			return nil, fmt.Errorf("skill directory %q escapes the external repository through a symlink", clean)
		}
		candidates, err := discoverSkillsAtExternalRoot(src, name, skillBase, origin)
		if err != nil {
			return nil, err
		}
		for _, skill := range candidates {
			if allowed != nil && !allowed[skill.Name] {
				continue
			}
			if existing, exists := discovered[skill.Name]; exists {
				return nil, fmt.Errorf("external source %s has duplicate skill basename %q in %s and %s", src.URL, skill.Name, existing.Path, skill.Path)
			}
			discovered[skill.Name] = skill
			found[skill.Name] = true
		}
	}
	for skill := range allowed {
		if !found[skill] {
			return nil, fmt.Errorf("external source %s: allowlisted skill %q not found in configured skill directories", src.URL, skill)
		}
	}
	if len(discovered) == 0 && !pluginRelaxed {
		return nil, fmt.Errorf("external source %s: no skills found in configured skill directories", src.URL)
	}
	return discoveredSkillValues(discovered), nil
}

func discoverSkillsAtExternalRoot(src externalSkillSource, repo string, skillBase string, origin string) ([]discoveredSkill, error) {
	if hasFile(filepath.Join(skillBase, "SKILL.md")) {
		name := filepath.Base(skillBase)
		// Preserve the legacy single-skill convention, where skill_dir names the
		// repository's skill and therefore uses the repository basename.
		if len(src.SkillDirs) == 0 {
			name = repo
		}
		return []discoveredSkill{{Name: name, Path: skillBase, Origin: origin, Root: skillBase}}, nil
	}
	entries, err := os.ReadDir(skillBase)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", skillBase, err)
	}
	result := make([]discoveredSkill, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		skillPath := filepath.Join(skillBase, entry.Name())
		if !hasFile(filepath.Join(skillPath, "SKILL.md")) {
			continue
		}
		result = append(result, discoveredSkill{Name: entry.Name(), Path: skillPath, Origin: origin, Root: skillBase})
	}
	return result, nil
}

func externalSkillDirs(src externalSkillSource) []string {
	if len(src.SkillDirs) > 0 {
		return src.SkillDirs
	}
	if strings.TrimSpace(src.SkillDir) == "" {
		return []string{"skills"}
	}
	return []string{src.SkillDir}
}

func cleanExternalSkillDir(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("skill directory cannot be empty")
	}
	if filepath.IsAbs(raw) {
		return "", fmt.Errorf("skill directory %q must be relative", raw)
	}
	clean := filepath.Clean(filepath.FromSlash(raw))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("skill directory %q escapes the external repository", raw)
	}
	return filepath.ToSlash(clean), nil
}

// skillAllowlist returns the configured skill name filter, or nil when the
// source exposes all skills.
func skillAllowlist(src externalSkillSource) map[string]bool {
	if len(src.Skills) == 0 {
		return nil
	}
	allowed := make(map[string]bool, len(src.Skills))
	for _, name := range src.Skills {
		name = strings.TrimSpace(name)
		if name != "" {
			allowed[name] = true
		}
	}
	if len(allowed) == 0 {
		return nil
	}
	return allowed
}

type materializedSkill struct {
	Name       string
	SourceName string
	SourcePath string
	TargetPath string
	Remove     bool
}

// keep lists source names whose lock-owned materialized copies must stay
// untouched (neither refreshed nor removed), e.g. when the upstream cache is
// unavailable but the committed copies already match the lock.
func planMaterializedSkills(sources []externalSkillSource, home string, repoRoot string, lock lockFile, reconcileRemovedSources bool, keep map[string]bool) ([]materializedSkill, error) {
	var plan []materializedSkill
	seen := make(map[string]materializedSkill)
	sourceScope := make(map[string]bool, len(sources))
	lockedOwners := make(map[string]externalLockEntry)
	for _, entry := range lock.ExternalSkills {
		for _, name := range entry.Materialized.Values() {
			if owner, exists := lockedOwners[name]; exists {
				return nil, fmt.Errorf("%s assigns materialized skill %q to both %s and %s", lockFileName, name, owner.Name, entry.Name)
			}
			lockedOwners[name] = entry
		}
	}
	for _, src := range sources {
		sourceScope[repoName(src.URL)] = true
		if !src.Materialize || keep[repoName(src.URL)] {
			continue
		}
		skills, err := discoverExternalSourceSkills(src, home)
		if err != nil {
			return nil, err
		}
		for _, skill := range skills {
			if skill.Name == "" || skill.Name == "." || skill.Name == ".." || filepath.Base(skill.Name) != skill.Name {
				return nil, fmt.Errorf("external source %s produced unsafe materialized skill name %q", src.URL, skill.Name)
			}
			item := materializedSkill{
				Name:       skill.Name,
				SourceName: repoName(src.URL),
				SourcePath: skill.Path,
				TargetPath: filepath.Join(repoRoot, "skills", skill.Name),
			}
			if existing, ok := seen[item.Name]; ok {
				return nil, fmt.Errorf("materialized skill basename %q is duplicated by %s and %s", item.Name, existing.SourcePath, item.SourcePath)
			}
			seen[item.Name] = item
			if owner, owned := lockedOwners[item.Name]; !owned {
				if _, err := os.Lstat(item.TargetPath); err == nil {
					return nil, fmt.Errorf("materialized skill %q collides with unrelated canonical skill %s", item.Name, item.TargetPath)
				} else if !os.IsNotExist(err) {
					return nil, fmt.Errorf("stat materialized target %s: %w", item.TargetPath, err)
				}
			} else if owner.URL != src.URL || owner.Branch != src.Branch {
				return nil, fmt.Errorf("materialized skill %q is owned by %s at %s#%s, not %s#%s", item.Name, owner.Name, owner.URL, owner.Branch, src.URL, src.Branch)
			}
			plan = append(plan, item)
		}
	}
	stale := make(map[string]bool)
	for _, entry := range lock.ExternalSkills {
		if !reconcileRemovedSources && !sourceScope[entry.Name] {
			continue
		}
		if keep[entry.Name] {
			continue
		}
		for _, name := range entry.Materialized.Values() {
			if _, current := seen[name]; current || stale[name] {
				continue
			}
			if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
				return nil, fmt.Errorf("%s contains unsafe materialized skill ownership %q", lockFileName, name)
			}
			stale[name] = true
			plan = append(plan, materializedSkill{
				Name:       name,
				SourceName: entry.Name,
				TargetPath: filepath.Join(repoRoot, "skills", name),
				Remove:     true,
			})
		}
	}
	sort.Slice(plan, func(i, j int) bool { return plan[i].Name < plan[j].Name })
	return plan, nil
}

type stagedMaterialization struct {
	Skill     materializedSkill
	StagePath string
	Backup    string
	HadTarget bool
	Installed bool
}

func applyMaterializedSkills(plan []materializedSkill) error {
	changes := make([]stagedMaterialization, len(plan))
	for i, skill := range plan {
		changes[i].Skill = skill
		if skill.Remove {
			continue
		}
		parent := filepath.Dir(skill.TargetPath)
		if err := os.MkdirAll(parent, 0o755); err != nil {
			cleanupMaterializationStages(changes)
			return fmt.Errorf("create %s: %w", parent, err)
		}
		stage, err := os.MkdirTemp(parent, ".tackroom-materialize-"+skill.Name+"-")
		if err != nil {
			cleanupMaterializationStages(changes)
			return fmt.Errorf("stage materialized skill %s: %w", skill.Name, err)
		}
		changes[i].StagePath = stage
		if err := copyTreeExact(skill.SourcePath, stage); err != nil {
			cleanupMaterializationStages(changes)
			return fmt.Errorf("stage materialized skill %s: %w", skill.Name, err)
		}
	}
	defer cleanupMaterializationStages(changes)
	for i := range changes {
		change := &changes[i]
		if _, err := os.Lstat(change.Skill.TargetPath); err == nil {
			placeholder, err := os.MkdirTemp(filepath.Dir(change.Skill.TargetPath), ".tackroom-backup-"+change.Skill.Name+"-")
			if err != nil {
				rollbackMaterializedChanges(changes, i-1)
				return fmt.Errorf("prepare backup for materialized skill %s: %w", change.Skill.Name, err)
			}
			change.Backup = placeholder
			if err := os.Remove(placeholder); err != nil {
				rollbackMaterializedChanges(changes, i-1)
				return fmt.Errorf("prepare backup for materialized skill %s: %w", change.Skill.Name, err)
			}
			if err := os.Rename(change.Skill.TargetPath, change.Backup); err != nil {
				rollbackMaterializedChanges(changes, i-1)
				return fmt.Errorf("backup materialized skill %s: %w", change.Skill.Name, err)
			}
			change.HadTarget = true
		} else if !os.IsNotExist(err) {
			rollbackMaterializedChanges(changes, i-1)
			return fmt.Errorf("stat materialized skill %s: %w", change.Skill.Name, err)
		}
		if change.Skill.Remove {
			continue
		}
		if err := os.Rename(change.StagePath, change.Skill.TargetPath); err != nil {
			rollbackMaterializedChanges(changes, i)
			return fmt.Errorf("install materialized skill %s: %w", change.Skill.Name, err)
		}
		change.StagePath = ""
		change.Installed = true
	}
	for i := range changes {
		change := &changes[i]
		if change.Backup != "" {
			_ = os.RemoveAll(change.Backup)
			change.Backup = ""
		}
		if change.Skill.Remove {
			fmt.Printf("removed stale materialized skill %s\n", change.Skill.Name)
		} else {
			fmt.Printf("materialized %s -> %s\n", change.Skill.SourcePath, change.Skill.TargetPath)
		}
	}
	return nil
}

func rollbackMaterializedChanges(changes []stagedMaterialization, last int) {
	for i := last; i >= 0; i-- {
		change := &changes[i]
		if change.Installed {
			_ = os.RemoveAll(change.Skill.TargetPath)
			change.Installed = false
		}
		if change.HadTarget {
			_ = os.Rename(change.Backup, change.Skill.TargetPath)
			change.Backup = ""
			change.HadTarget = false
		}
	}
}

func cleanupMaterializationStages(changes []stagedMaterialization) {
	for _, change := range changes {
		if change.StagePath != "" {
			_ = os.RemoveAll(change.StagePath)
		}
		if change.Backup != "" {
			_ = os.RemoveAll(change.Backup)
		}
	}
}

func copyTreeExact(source string, target string) error {
	type copiedDir struct {
		Path string
		Mode fs.FileMode
	}
	var directories []copiedDir
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := target
		if rel != "." {
			destination = filepath.Join(target, rel)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			if err := os.MkdirAll(destination, 0o700); err != nil {
				return err
			}
			directories = append(directories, copiedDir{Path: destination, Mode: info.Mode().Perm()})
			return nil
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, destination)
		case info.Mode().IsRegular():
			return copyExternalFile(path, destination, info.Mode().Perm())
		default:
			return fmt.Errorf("unsupported file type in external skill: %s", path)
		}
	})
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if err := os.Chmod(directories[i].Path, directories[i].Mode); err != nil {
			return err
		}
	}
	return nil
}

func copyExternalFile(source string, target string, mode fs.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(target, mode)
}

type externalTreeEntry struct {
	Mode   fs.FileMode
	Size   int64
	Link   string
	Digest [sha256.Size]byte
}

func externalSkillTreesEqual(left string, right string) (bool, error) {
	leftTree, err := externalTree(left)
	if err != nil {
		return false, err
	}
	rightTree, err := externalTree(right)
	if err != nil {
		return false, err
	}
	if len(leftTree) != len(rightTree) {
		return false, nil
	}
	for path, expected := range leftTree {
		if actual, ok := rightTree[path]; !ok || actual != expected {
			return false, nil
		}
	}
	return true, nil
}

func externalTree(root string) (map[string]externalTreeEntry, error) {
	tree := make(map[string]externalTreeEntry)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		item := externalTreeEntry{Mode: info.Mode()}
		if info.Mode()&os.ModeSymlink != 0 {
			item.Link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		} else if info.Mode().IsRegular() {
			item.Size = info.Size()
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			hash := sha256.New()
			_, hashErr := io.Copy(hash, file)
			closeErr := file.Close()
			if hashErr != nil {
				return hashErr
			}
			if closeErr != nil {
				return closeErr
			}
			copy(item.Digest[:], hash.Sum(nil))
		}
		tree[filepath.ToSlash(rel)] = item
		return nil
	})
	return tree, err
}

func externalSkillCommit(cachePath string) string {
	out, err := exec.Command("git", "-C", cachePath, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// isPluginRelaxedSource returns true when the source has a plugin.json and uses
// the default skill location, so a missing or empty skills/ directory is not an
// error (the source may only provide MCP servers).
func isPluginRelaxedSource(src externalSkillSource, cachePath string) bool {
	if !hasPluginManifest(cachePath) {
		return false
	}
	return src.SkillDir == "skills" && len(src.SkillDirs) == 0
}

func isExternalSkillLink(linkPath string, rawTarget string, home string) bool {
	extRoot := externalCacheDir(home)
	targetAbs := absoluteTarget(linkPath, rawTarget)
	if targetAbs == extRoot || strings.HasPrefix(targetAbs, extRoot+string(os.PathSeparator)) {
		return true
	}
	resolved, err := filepath.EvalSymlinks(targetAbs)
	if err != nil {
		return false
	}
	resolvedRoot, err := filepath.EvalSymlinks(extRoot)
	if err != nil {
		return false
	}
	return resolved == resolvedRoot || strings.HasPrefix(resolved, resolvedRoot+string(os.PathSeparator))
}
