package app

import (
	"flag"
	"reflect"
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

func gateSourceFreshness(src externalSkillSource, home string, item string, lock *lockFile, opts gateOptions) (heldItem, bool) {
	return heldItem{}, false
}
