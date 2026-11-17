package app

import (
	"flag"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// cooldownEntry records the minimum release age decision for one immutable external
// reference, so it is looked up at most once and every machine sharing the lock
// sees the same decision.
type cooldownEntry struct {
	Item          string           `yaml:"item"`
	Ref           string           `yaml:"ref"`
	PublishedAt   string           `yaml:"published_at,omitempty"`
	TimeSource    string           `yaml:"time_source"`
	CooldownUntil string           `yaml:"cooldown_until,omitempty"`
	Allowed       *cooldownAllowed `yaml:"allowed,omitempty"`
}

// UnmarshalYAML also reads hold_until, the key tackroom 2.x wrote, so an entry
// keeps its end time; cooldown_until wins when both are set.
func (e *cooldownEntry) UnmarshalYAML(node *yaml.Node) error {
	type plain cooldownEntry
	var raw struct {
		plain     `yaml:",inline"`
		HoldUntil string `yaml:"hold_until"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	*e = cooldownEntry(raw.plain)
	if e.CooldownUntil == "" {
		e.CooldownUntil = raw.HoldUntil
	}
	return nil
}

type cooldownAllowed struct {
	At     string `yaml:"at"`
	Reason string `yaml:"reason,omitempty"`
}

// gateOptions carries the run clock and the --allow-fresh override.
type gateOptions struct {
	now        time.Time
	allowAll   bool
	allowItems map[string]bool
	reason     string
	// matched collects the --allow-fresh names that met an item in cooldown, so
	// warnUnmatchedAllowFresh can name the rest. Shared by every copy of the options.
	matched map[string]bool
	// dry is set by sync --dry-run to collect what the run cannot decide
	// without a registry lookup, and what --allow-fresh would let through.
	dry *dryGateNotes
}

// dryGateNotes maps item to lock ref for the two cases a dry run reports apart
// from items in cooldown.
type dryGateNotes struct {
	unchecked map[string]string
	released  map[string]string
}

func newDryGateNotes() *dryGateNotes {
	return &dryGateNotes{unchecked: map[string]string{}, released: map[string]string{}}
}

func (o gateOptions) allows(item string) bool { return o.allowAll || o.allowItems[item] }

// noteMatch records that the --allow-fresh name for item met an item in cooldown.
func (o gateOptions) noteMatch(item string) {
	if o.matched != nil && o.allowItems[item] {
		o.matched[item] = true
	}
}

// warnUnmatchedAllowFresh prints one line per --allow-fresh name that matched no
// item in cooldown, with the items that are in cooldown. A name that is in
// cooldown but that this command cannot skip (a pending skill update, for sync)
// gets the command that does. It never fails the run: a script that names an
// item must keep working once its cooldown ends.
func warnUnmatchedAllowFresh(w io.Writer, opts gateOptions, gated map[string]gatedItem) {
	var names []string
	for name := range opts.allowItems {
		if !opts.matched[name] {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return
	}
	sort.Strings(names)
	byItem := map[string]gatedItem{}
	var inCooldown []string
	for _, h := range gated {
		if h.Kind == "cooldown" {
			byItem[h.Item] = h
			inCooldown = append(inCooldown, h.Item)
		}
	}
	list := "none"
	if len(inCooldown) > 0 {
		sort.Strings(inCooldown)
		list = strings.Join(slices.Compact(inCooldown), ", ")
	}
	for _, name := range names {
		if h, ok := byItem[name]; ok {
			fmt.Fprintf(w, "--allow-fresh: %q is in cooldown but this command does not skip it; run: %s\n", name, h.Command)
			continue
		}
		fmt.Fprintf(w, "--allow-fresh: nothing in cooldown is named %q; in cooldown: %s\n", name, list)
	}
}

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
	fs.Var(allowFreshFlag{opts}, "allow-fresh", "Skip the cooldown: all items, or --allow-fresh=item[,item...]")
	fs.StringVar(&opts.AllowFreshReason, "reason", "", "Reason recorded with --allow-fresh (optional)")
}

func gateOptionsFor(opts runOptions) gateOptions {
	g := gateOptions{now: timeNow(), allowAll: opts.AllowFreshAll, reason: opts.AllowFreshReason, allowItems: map[string]bool{}, matched: map[string]bool{}}
	for _, item := range opts.AllowFreshItems {
		g.allowItems[item] = true
	}
	return g
}

// cooldownsChanged reports whether lock.Cooldowns differs from what is on disk.
func cooldownsChanged(repoRoot string, lock lockFile) bool {
	disk, err := readLockFile(repoRoot)
	if err != nil {
		return true
	}
	if len(disk.Cooldowns) == 0 && len(lock.Cooldowns) == 0 {
		return false
	}
	return !reflect.DeepEqual(disk.Cooldowns, lock.Cooldowns)
}

const (
	timeSourceNPM     = "npm-time"
	timeSourcePyPI    = "pypi-max-upload"
	timeSourceGit     = "git-commit-date"
	timeSourceUnknown = "unknown"
)

// cooldownResolver looks up a package release; tests and demos can replace it.
var cooldownResolver packageAgeResolver = resolvePackageRelease

func cooldownIndex(lock lockFile, item, ref string) int {
	for i, h := range lock.Cooldowns {
		if h.Item == item && h.Ref == ref {
			return i
		}
	}
	return -1
}

// decideCooldown applies the minimum release age to one reference. A reference with a
// decision in the lock costs no lookup, except "unknown", which is retried.
// lookup is nil when the caller may not reach the network (status): an
// undecided reference is then not in cooldown.
func decideCooldown(item, ref string, lock *lockFile, opts gateOptions, lookup func() (time.Time, string, error)) (gatedItem, bool) {
	idx := cooldownIndex(*lock, item, ref)
	if idx < 0 && lookup == nil && opts.dry != nil {
		if opts.allows(item) {
			opts.noteMatch(item)
			opts.dry.released[item] = ref
		} else {
			opts.dry.unchecked[item] = ref
		}
		return gatedItem{}, false
	}
	if idx < 0 || (lock.Cooldowns[idx].TimeSource == timeSourceUnknown && lock.Cooldowns[idx].Allowed == nil && lookup != nil) {
		if lookup == nil {
			return gatedItem{}, false
		}
		entry := cooldownEntry{Item: item, Ref: ref, TimeSource: timeSourceUnknown}
		if published, source, err := lookup(); err == nil && !published.IsZero() {
			entry.PublishedAt = published.UTC().Format(time.RFC3339)
			entry.TimeSource = source
			entry.CooldownUntil = published.Add(defaultPackageAgeMinimum).UTC().Format(time.RFC3339)
		}
		if idx < 0 {
			// A registry reference whose cooldown already ended guards nothing;
			// recording it would only change the lock on every new machine.
			if !cooldownInForce(entry, opts.now) && !opts.allows(item) && !strings.HasPrefix(ref, "git:") {
				return gatedItem{}, false
			}
			lock.Cooldowns = append(lock.Cooldowns, entry)
			idx = len(lock.Cooldowns) - 1
		} else {
			entry.Allowed = lock.Cooldowns[idx].Allowed
			lock.Cooldowns[idx] = entry
		}
	}
	entry := &lock.Cooldowns[idx]
	if entry.Allowed == nil && opts.allows(item) {
		if cooldownInForce(*entry, opts.now) {
			opts.noteMatch(item)
			if opts.dry != nil {
				opts.dry.released[item] = ref
			}
		}
		entry.Allowed = &cooldownAllowed{At: opts.now.UTC().Format(time.RFC3339), Reason: opts.reason}
	}
	if entry.Allowed != nil {
		return gatedItem{}, false
	}
	command := "tackroom sync --allow-fresh=" + item
	if entry.TimeSource == timeSourceUnknown {
		return gatedItem{Item: item, Kind: "cooldown", Reason: "publish time unknown for " + ref, Command: command}, true
	}
	until, err := time.Parse(time.RFC3339, entry.CooldownUntil)
	if err != nil || !opts.now.Before(until) {
		return gatedItem{}, false
	}
	return gatedItem{Item: item, Kind: "cooldown", Reason: fmt.Sprintf("%s published %s (%s)", ref, entry.PublishedAt, entry.TimeSource),
		Until: entry.CooldownUntil, Command: command}, true
}

// cooldownInForce reports whether an undecided-or-unexpired cooldown still blocks sync.
func cooldownInForce(entry cooldownEntry, now time.Time) bool {
	if entry.TimeSource == timeSourceUnknown {
		return true
	}
	until, err := time.Parse(time.RFC3339, entry.CooldownUntil)
	return err == nil && now.Before(until)
}

// gateSourceFreshness keeps a new external skill pin in cooldown until 72 hours after the
// committer date of its commit, read from the local clone. Commit dates are
// author-controlled, so this is weaker than a registry publish time.
func gateSourceFreshness(src externalSkillSource, home string, item string, lock *lockFile, opts gateOptions) (gatedItem, bool) {
	cachePath := filepath.Join(externalCacheDir(home), sourceName(src))
	commit := externalSkillCommitFull(cachePath)
	if commit == "" {
		return gatedItem{}, false
	}
	ref := "git:" + src.URL + "@" + commit
	return decideCooldown(item, ref, lock, opts, func() (time.Time, string, error) {
		out, err := exec.Command("git", "-C", cachePath, "show", "-s", "--format=%cI", commit).Output()
		if err != nil {
			return time.Time{}, "", err
		}
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
		return t, timeSourceGit, err
	})
}

// cooldownSourceCommit is the commit of an unpinned source's cooldown that is still in
// force and not skipped, or "" when there is none.
func cooldownSourceCommit(lock lockFile, src externalSkillSource, now time.Time) string {
	prefix := "git:" + src.URL + "@"
	commit := ""
	for _, h := range lock.Cooldowns {
		if strings.HasSuffix(h.Item, ":"+sourceName(src)) && strings.HasPrefix(h.Ref, prefix) && h.Allowed == nil && cooldownInForce(h, now) {
			commit = strings.TrimPrefix(h.Ref, prefix)
		}
	}
	return commit
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

func cooldownLookup(r packageReference, source string) func() (time.Time, string, error) {
	return func() (time.Time, string, error) {
		rel, err := cooldownResolver(r)
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

// gatePackages puts new exact-version references of MCP servers and Pi
// packages in cooldown. "New" means neither decided in the lock nor already present in a
// harness config, so an upgrade of tackroom never puts what is already synced in cooldown.
// With resolve false (status) nothing touches the network or the lock file.
func gatePackages(cfg *config, selected []agentConfig, repoRoot, home string, opts gateOptions, resolve bool) (map[string]gatedItem, error) {
	lock, err := readLockFile(repoRoot)
	if err != nil {
		return nil, err
	}
	gated := map[string]gatedItem{}
	newRefs := map[string]bool{}
	cfg.cooldownMCP = map[string]bool{}
	cfg.cooldownHome = home

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
				lookup = cooldownLookup(r, timeSourceFor(r))
			}
			if h, ok := decideCooldown(item, ref, &lock, opts, lookup); ok {
				gated[item] = h
				cfg.cooldownMCP[server.Name] = true
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
			// Sync installs packages only for a detected agent, so one not on
			// this machine needs no lookup; a decision in the lock still applies.
			if resolve && isDetected(agent) {
				lookup = cooldownLookup(r, timeSourceNPM)
			}
			h, inCooldown := decideCooldown(item, ref, &lock, opts, lookup)
			if !inCooldown {
				kept = append(kept, spec)
				continue
			}
			gated[item] = h
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
	var keep []cooldownEntry
	for _, h := range lock.Cooldowns {
		until, err := time.Parse(time.RFC3339, h.CooldownUntil)
		expired := err == nil && !opts.now.Before(until)
		if h.Allowed == nil && expired && !newRefs[h.Item+"|"+h.Ref] && !strings.HasPrefix(h.Ref, "git:") {
			continue
		}
		keep = append(keep, h)
	}
	lock.Cooldowns = keep
	if resolve && cooldownsChanged(repoRoot, lock) {
		if err := writeLockFile(repoRoot, lock); err != nil {
			return nil, err
		}
	}
	return gated, nil
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
