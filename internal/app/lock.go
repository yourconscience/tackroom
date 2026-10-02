package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const lockFileName = "tackroom.lock"

type lockFile struct {
	Version         int                  `yaml:"version"`
	ExternalSkills  []externalLockEntry  `yaml:"external_skills"`
	PublishedSkills []publishedLockEntry `yaml:"published_skills,omitempty"`
}

// publishedLockEntry pins one skill pushed to a remote skill registry. It is the
// outward analogue of externalLockEntry: instead of recording an upstream commit
// we materialize inward, it records the registry id/version we published, keyed
// by (target, skill). ContentHash is the idempotency key: publish skips a skill
// whose current bundle hashes to the value already recorded here. All fields are
// comparable so entries compare with ==.
type publishedLockEntry struct {
	Target      string `yaml:"target"`
	Skill       string `yaml:"skill"`
	SkillID     string `yaml:"skill_id"`
	Version     string `yaml:"version"`
	ContentHash string `yaml:"content_hash"`
	PublishedAt string `yaml:"published_at"`
}

type externalLockEntry struct {
	Name          string                 `yaml:"name"`
	URL           string                 `yaml:"url"`
	Branch        string                 `yaml:"branch"`
	Commit        string                 `yaml:"commit"`
	Materialized  materializedSkillNames `yaml:"materialized,omitempty"`
	PluginName    string                 `yaml:"plugin_name,omitempty"`
	PluginVersion string                 `yaml:"plugin_version,omitempty"`
}

// materializedSkillNames stores a sorted set in a comparable string so lock
// entries remain comparable, while preserving the readable YAML sequence.
type materializedSkillNames string

func newMaterializedSkillNames(names []string) materializedSkillNames {
	return materializedSkillNames(strings.Join(names, "\x00"))
}

func (names materializedSkillNames) Values() []string {
	if names == "" {
		return nil
	}
	return strings.Split(string(names), "\x00")
}

func (names materializedSkillNames) MarshalYAML() (interface{}, error) {
	return names.Values(), nil
}

func (names *materializedSkillNames) UnmarshalYAML(node *yaml.Node) error {
	var values []string
	if err := node.Decode(&values); err != nil {
		return fmt.Errorf("decode materialized skill ownership: %w", err)
	}
	sort.Strings(values)
	*names = newMaterializedSkillNames(values)
	return nil
}

func lockFilePath(repoRoot string) string {
	return filepath.Join(repoRoot, lockFileName)
}

func readLockFile(repoRoot string) (lockFile, error) {
	path := lockFilePath(repoRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return lockFile{Version: 1}, nil
		}
		return lockFile{}, fmt.Errorf("read %s: %w", path, err)
	}
	var lock lockFile
	if err := yaml.Unmarshal(data, &lock); err != nil {
		return lockFile{}, fmt.Errorf("yaml decode %s: %w", path, err)
	}
	if lock.Version == 0 {
		lock.Version = 1
	}
	return lock, nil
}

func writeLockFile(repoRoot string, lock lockFile) error {
	lock.Version = 1
	data, err := yaml.Marshal(lock)
	if err != nil {
		return fmt.Errorf("yaml encode lock: %w", err)
	}
	header := []byte("# Managed by tackroom sync / tackroom skill update. Pins commits and materialized skill ownership.\n")
	return os.WriteFile(lockFilePath(repoRoot), append(header, data...), 0o644)
}

// lockEntryFor returns the lock entry pinning a source, or nil when the source
// is unpinned or the config branch/URL changed since the lock was written.
func lockEntryFor(lock lockFile, src externalSkillSource) *externalLockEntry {
	name := repoName(src.URL)
	for i := range lock.ExternalSkills {
		entry := &lock.ExternalSkills[i]
		if entry.Name != name {
			continue
		}
		if entry.URL != src.URL || entry.Branch != src.Branch {
			return nil
		}
		if strings.TrimSpace(entry.Commit) == "" {
			return nil
		}
		return entry
	}
	return nil
}

// rebuildLockEntries records the current cache HEAD for every configured
// source, keeping the existing pin for sources that are not cloned locally so
// a partial update cannot silently unpin them.
func rebuildLockEntries(sources []externalSkillSource, home string, lock lockFile) []externalLockEntry {
	cacheRoot := externalCacheDir(home)
	var entries []externalLockEntry
	for _, src := range sources {
		name := repoName(src.URL)
		commit := externalSkillCommitFull(filepath.Join(cacheRoot, name))
		pin := lockEntryFor(lock, src)
		if commit == "" && pin != nil {
			commit = pin.Commit
		}
		if commit == "" {
			continue
		}
		var materialized materializedSkillNames
		if src.Materialize {
			if skills, err := discoverExternalSourceSkills(src, home); err == nil {
				var names []string
				for _, skill := range skills {
					names = append(names, skill.Name)
				}
				sort.Strings(names)
				materialized = newMaterializedSkillNames(names)
			} else if pin != nil {
				materialized = pin.Materialized
			}
		}
		var pluginName, pluginVersion string
		cachePath := filepath.Join(cacheRoot, name)
		if hasPluginManifest(cachePath) {
			if manifest, _, ok, parseErr := parsePluginManifest(cachePath); parseErr == nil && ok {
				pluginName = manifest.Name
				pluginVersion = manifest.Version
			}
		}
		entries = append(entries, externalLockEntry{
			Name:          name,
			URL:           src.URL,
			Branch:        src.Branch,
			Commit:        commit,
			Materialized:  materialized,
			PluginName:    pluginName,
			PluginVersion: pluginVersion,
		})
	}
	// Keep the lock file order-stable so reordering config sources does not
	// rewrite it.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

func lockEntriesEqual(a, b []externalLockEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func externalSkillCommitFull(cachePath string) string {
	out, err := exec.Command("git", "-C", cachePath, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
