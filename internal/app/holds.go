package app

import (
	"flag"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"
)

// holdEntry records the freshness decision for one immutable external
// reference, so it is looked up at most once and every machine sharing the lock
// sees the same decision.
type holdEntry struct {
	Item        string       `yaml:"item"`
	Ref         string       `yaml:"ref"`
	PublishedAt string       `yaml:"published_at,omitempty"`
	TimeSource  string       `yaml:"time_source"`
	HoldUntil   string       `yaml:"hold_until,omitempty"`
	Allowed     *holdAllowed `yaml:"allowed,omitempty"`
}

type holdAllowed struct {
	At     string `yaml:"at"`
	Reason string `yaml:"reason,omitempty"`
}

// gateOptions carries the run clock and the --allow-fresh override.
type gateOptions struct {
	now        time.Time
	allowAll   bool
	allowItems map[string]bool
	reason     string
	// dry is set by sync --dry-run to collect what the run cannot decide
	// without a registry lookup, and what --allow-fresh would release.
	dry *dryGateNotes
}

// dryGateNotes maps item to lock ref for the two cases a dry run reports apart
// from held items.
type dryGateNotes struct {
	unchecked map[string]string
	released  map[string]string
}

func newDryGateNotes() *dryGateNotes {
	return &dryGateNotes{unchecked: map[string]string{}, released: map[string]string{}}
}

func (o gateOptions) allows(item string) bool { return o.allowAll || o.allowItems[item] }

// allowFreshFlag is `--allow-fresh` (all items) or `--allow-fresh=a,b`.
type allowFreshFlag struct{ opts *runOptions }

func (f allowFreshFlag) IsBoolFlag() bool { return true }
func (f allowFreshFlag) String() string   { return "" }
func (f allowFreshFlag) Set(v string) error {
	if v == "true" {
		f.opts.AllowFreshAll = true
		return nil
	}
	if v == "false" {
		return nil
	}
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			f.opts.AllowFreshItems = append(f.opts.AllowFreshItems, item)
		}
	}
	return nil
}

func addAllowFreshFlags(fs *flag.FlagSet, opts *runOptions) {
	fs.Var(allowFreshFlag{opts}, "allow-fresh", "Release held fresh items: all, or --allow-fresh=item[,item...]")
	fs.StringVar(&opts.AllowFreshReason, "reason", "", "Reason recorded with --allow-fresh (optional)")
}

func gateOptionsFor(opts runOptions) gateOptions {
	g := gateOptions{now: timeNow(), allowAll: opts.AllowFreshAll, reason: opts.AllowFreshReason, allowItems: map[string]bool{}}
	for _, item := range opts.AllowFreshItems {
		g.allowItems[item] = true
	}
	return g
}

// holdsChanged reports whether lock.Holds differs from what is on disk.
func holdsChanged(repoRoot string, lock lockFile) bool {
	disk, err := readLockFile(repoRoot)
	if err != nil {
		return true
	}
	if len(disk.Holds) == 0 && len(lock.Holds) == 0 {
		return false
	}
	return !reflect.DeepEqual(disk.Holds, lock.Holds)
}

const (
	timeSourceNPM     = "npm-time"
	timeSourcePyPI    = "pypi-max-upload"
	timeSourceGit     = "git-commit-date"
	timeSourceUnknown = "unknown"
)

// holdResolver looks up a package release; tests and demos can replace it.
var holdResolver packageAgeResolver = resolvePackageRelease

func holdIndex(lock lockFile, item, ref string) int {
	for i, h := range lock.Holds {
		if h.Item == item && h.Ref == ref {
			return i
		}
	}
	return -1
}

// decideHold applies the freshness rule to one reference. A reference with a
// decision in the lock costs no lookup, except "unknown", which is retried.
// lookup is nil when the caller may not reach the network (status): an
// undecided reference is then not held.
func decideHold(item, ref string, lock *lockFile, opts gateOptions, lookup func() (time.Time, string, error)) (heldItem, bool) {
	idx := holdIndex(*lock, item, ref)
	if idx < 0 && lookup == nil && opts.dry != nil {
		if opts.allows(item) {
			opts.dry.released[item] = ref
		} else {
			opts.dry.unchecked[item] = ref
		}
		return heldItem{}, false
	}
	if idx < 0 || (lock.Holds[idx].TimeSource == timeSourceUnknown && lock.Holds[idx].Allowed == nil && lookup != nil) {
		if lookup == nil {
			return heldItem{}, false
		}
		entry := holdEntry{Item: item, Ref: ref, TimeSource: timeSourceUnknown}
		if published, source, err := lookup(); err == nil && !published.IsZero() {
			entry.PublishedAt = published.UTC().Format(time.RFC3339)
			entry.TimeSource = source
			entry.HoldUntil = published.Add(defaultPackageAgeMinimum).UTC().Format(time.RFC3339)
		}
		if idx < 0 {
			lock.Holds = append(lock.Holds, entry)
			idx = len(lock.Holds) - 1
		} else {
			entry.Allowed = lock.Holds[idx].Allowed
			lock.Holds[idx] = entry
		}
	}
	entry := &lock.Holds[idx]
	if entry.Allowed == nil && opts.allows(item) {
		if opts.dry != nil && holdInForce(*entry, opts.now) {
			opts.dry.released[item] = ref
		}
		entry.Allowed = &holdAllowed{At: opts.now.UTC().Format(time.RFC3339), Reason: opts.reason}
	}
	if entry.Allowed != nil {
		return heldItem{}, false
	}
	command := "tackroom sync --allow-fresh=" + item
	if entry.TimeSource == timeSourceUnknown {
		return heldItem{Item: item, Kind: "held", Reason: "publish time unknown for " + ref, Command: command}, true
	}
	until, err := time.Parse(time.RFC3339, entry.HoldUntil)
	if err != nil || !opts.now.Before(until) {
		return heldItem{}, false
	}
	return heldItem{Item: item, Kind: "held", Reason: fmt.Sprintf("%s published %s (%s)", ref, entry.PublishedAt, entry.TimeSource),
		Until: entry.HoldUntil, Command: command}, true
}

// holdInForce reports whether an undecided-or-unexpired hold still blocks sync.
func holdInForce(entry holdEntry, now time.Time) bool {
	if entry.TimeSource == timeSourceUnknown {
		return true
	}
	until, err := time.Parse(time.RFC3339, entry.HoldUntil)
	return err == nil && now.Before(until)
}

// gateSourceFreshness holds a new external skill pin until 72 hours after the
// committer date of its commit, read from the local clone. Commit dates are
// author-controlled, so this is weaker than a registry publish time.
func gateSourceFreshness(src externalSkillSource, home string, item string, lock *lockFile, opts gateOptions) (heldItem, bool) {
	cachePath := filepath.Join(externalCacheDir(home), sourceName(src))
	commit := externalSkillCommitFull(cachePath)
	if commit == "" {
		return heldItem{}, false
	}
	ref := "git:" + src.URL + "@" + commit
	return decideHold(item, ref, lock, opts, func() (time.Time, string, error) {
		out, err := exec.Command("git", "-C", cachePath, "show", "-s", "--format=%cI", commit).Output()
		if err != nil {
			return time.Time{}, "", err
		}
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
		return t, timeSourceGit, err
	})
}

var reExactVersion = regexp.MustCompile(`^v?\d+(\.\d+)*([-+.][0-9A-Za-z.+-]*)?$`)

// exactRefs returns the exact-version registry references of a package list,
// as lock refs keyed to the package reference.
func exactRefs(refs []packageReference) []packageReference {
	var out []packageReference
	for _, r := range refs {
		if r.Version == "" || r.Version == packageVersionLatest || !reExactVersion.MatchString(r.Version) || isPackageAgeException(r) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func lockRef(r packageReference) string {
	if r.Ecosystem == ecosystemPyPI {
		return "pypi:" + r.Package + "==" + r.Version
	}
	return "npm:" + r.Package + "@" + r.Version
}

func holdLookup(r packageReference, source string) func() (time.Time, string, error) {
	return func() (time.Time, string, error) {
		rel, err := holdResolver(r)
		if err != nil {
			return time.Time{}, "", err
		}
		return rel.Released, source, nil
	}
}

func timeSourceFor(r packageReference) string {
	if r.Ecosystem == ecosystemPyPI {
		return timeSourcePyPI
	}
	return timeSourceNPM
}

// piPackageReference parses a Pi `packages` entry such as npm:pkg@1.2.3.
func piPackageReference(spec, source string) (packageReference, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(spec), "npm:")
	if !ok {
		return packageReference{}, false
	}
	pkg, version := splitNPMSpec(rest)
	return packageReference{Ecosystem: ecosystemNPM, Package: pkg, Version: version, Source: source}, true
}

// gatePackages holds new exact-version references of MCP servers and Pi
// packages. "New" means neither decided in the lock nor already present in a
// harness config, so an upgrade of tackroom never holds what is already synced.
// With resolve false (status) nothing touches the network or the lock file.
func gatePackages(cfg *config, selected []agentConfig, repoRoot, home string, opts gateOptions, resolve bool) (map[string]heldItem, error) {
	lock, err := readLockFile(repoRoot)
	if err != nil {
		return nil, err
	}
	held := map[string]heldItem{}
	newRefs := map[string]bool{}
	cfg.heldMCP = map[string]bool{}
	cfg.heldHome = home

	for _, server := range cfg.MCPServers {
		if !server.Enabled {
			continue
		}
		item := "mcp:" + server.Name
		for _, r := range exactRefs(packageReferencesFromCommand(server.Command, server.Args, item)) {
			ref := lockRef(r)
			if mcpRefSynced(cfg, server, ref, home) {
				continue
			}
			newRefs[item+"|"+ref] = true
			var lookup func() (time.Time, string, error)
			if resolve {
				lookup = holdLookup(r, timeSourceFor(r))
			}
			if h, ok := decideHold(item, ref, &lock, opts, lookup); ok {
				held[item] = h
				cfg.heldMCP[server.Name] = true
			}
		}
	}

	for i, agent := range selected {
		if agent.Packages == nil {
			continue
		}
		actual, _, _ := readPiPackages(home)
		var kept []string
		for _, spec := range *agent.Packages {
			r, ok := piPackageReference(spec, agent.Name)
			if !ok || len(exactRefs([]packageReference{r})) == 0 {
				kept = append(kept, spec)
				continue
			}
			item := "package:" + r.Package
			ref := lockRef(r)
			if containsString(actual, spec) {
				kept = append(kept, spec)
				continue
			}
			newRefs[item+"|"+ref] = true
			var lookup func() (time.Time, string, error)
			if resolve {
				lookup = holdLookup(r, timeSourceNPM)
			}
			h, isHeld := decideHold(item, ref, &lock, opts, lookup)
			if !isHeld {
				kept = append(kept, spec)
				continue
			}
			held[item] = h
			// The previously synced version of the package stays as it is.
			for _, old := range actual {
				if o, ok := piPackageReference(old, ""); ok && o.Package == r.Package {
					kept = append(kept, old)
				}
			}
		}
		pk := kept
		selected[i].Packages = &pk
	}

	// Drop decisions that expired unallowed and no longer guard anything.
	var keep []holdEntry
	for _, h := range lock.Holds {
		until, err := time.Parse(time.RFC3339, h.HoldUntil)
		expired := err == nil && !opts.now.Before(until)
		if h.Allowed == nil && expired && !newRefs[h.Item+"|"+h.Ref] && !strings.HasPrefix(h.Ref, "git:") {
			continue
		}
		keep = append(keep, h)
	}
	lock.Holds = keep
	if resolve && holdsChanged(repoRoot, lock) {
		if err := writeLockFile(repoRoot, lock); err != nil {
			return nil, err
		}
	}
	return held, nil
}

// mcpRefSynced reports whether any harness already carries this server at the
// same package reference.
func mcpRefSynced(cfg *config, server mcpServerConfig, ref, home string) bool {
	for _, agent := range cfg.Agents {
		if !hasMCPSupport(agent.Name) {
			continue
		}
		existing, err := readNativeMCPServer(agent.Name, server.Name, home)
		if err != nil {
			continue
		}
		for _, r := range exactRefs(packageReferencesFromCommand(existing.Command, existing.Args, "")) {
			if lockRef(r) == ref {
				return true
			}
		}
	}
	return false
}
