package app

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// acceptedFinding records one accepted audit finding. Matching is on
// (item, rule, evidence); the evidence hash has no line number, so unrelated
// edits keep the acceptance and a changed payload does not.
type acceptedFinding struct {
	Item     string `yaml:"item"`
	Rule     string `yaml:"rule"`
	Evidence string `yaml:"evidence"`
	Severity string `yaml:"severity"`
	Reason   string `yaml:"reason"`
	At       string `yaml:"at"`
}

// gatedItem is an external item sync skipped: blocked by an audit finding or
// in cooldown.
type gatedItem struct {
	Item    string
	Kind    string // "blocked" or "held" (in cooldown)
	Reason  string
	Until   string // cooldown end, RFC 3339; empty for blocked
	Command string // the exact command that overrides it
}

func isAccepted(lock lockFile, item string, f ruleFinding) bool {
	for _, a := range lock.Accepted {
		if a.Item == item && a.Rule == f.Rule && a.Evidence == f.Evidence {
			return true
		}
	}
	return false
}

func markAccepted(items []auditItem, lock lockFile) {
	for i := range items {
		for j := range items[i].Findings {
			items[i].Findings[j].Accepted = isAccepted(lock, items[i].ID(), items[i].Findings[j])
		}
	}
}

// unacceptedCritical returns the findings that block an external item.
func unacceptedCritical(item auditItem, lock lockFile) []ruleFinding {
	var out []ruleFinding
	for _, f := range item.Findings {
		if f.Severity == sevCritical && !isAccepted(lock, item.ID(), f) {
			out = append(out, f)
		}
	}
	return out
}

// isLocalSourceURL is true for a source on this machine; it is audited like any
// external source but has no publish time for a cooldown.
func isLocalSourceURL(url string) bool {
	u := strings.TrimSpace(url)
	return strings.HasPrefix(u, "/") || strings.HasPrefix(u, "./") || strings.HasPrefix(u, "../") ||
		strings.HasPrefix(u, "~") || strings.HasPrefix(u, "file://")
}

// gateExternalSources decides, for every external source whose pin is new or
// would change, whether sync may use it. A source already pinned in the lock is
// never gated here. The candidate content is the checkout under home's external
// cache, so a caller staging a candidate passes the staging home.
func gateExternalSources(sources []externalSkillSource, home string, lock *lockFile, opts gateOptions) map[string]gatedItem {
	gated := map[string]gatedItem{}
	for _, src := range sources {
		if lockEntryFor(*lock, src) != nil {
			continue
		}
		name := sourceName(src)
		if !hasDir(filepath.Join(externalCacheDir(home), name, ".git")) {
			continue
		}
		item := externalSourceItem(src, home)
		if h, ok := auditBlock(item, *lock); ok {
			gated[name] = h
			continue
		}
		if isLocalSourceURL(src.URL) {
			continue // freshness applies to remote content only
		}
		if h, ok := gateSourceFreshness(src, home, item.ID(), lock, opts); ok {
			gated[name] = h
		}
	}
	return gated
}

// auditBlock blocks an external item with an unaccepted CRITICAL finding.
func auditBlock(item auditItem, lock lockFile) (gatedItem, bool) {
	blockers := unacceptedCritical(item, lock)
	if len(blockers) == 0 {
		return gatedItem{}, false
	}
	return gatedItem{
		Item:    item.ID(),
		Kind:    "blocked",
		Reason:  fmt.Sprintf("%s in %s", blockers[0].Rule, blockers[0].File),
		Command: fmt.Sprintf("tackroom doctor audit accept %s %s --reason \"...\"", item.ID(), blockers[0].Rule),
	}, true
}

// filterGatedSources drops gated sources from the list sync works with.
func filterGatedSources(sources []externalSkillSource, gated map[string]gatedItem) []externalSkillSource {
	if len(gated) == 0 {
		return sources
	}
	var out []externalSkillSource
	for _, src := range sources {
		if _, ok := gated[sourceName(src)]; !ok {
			out = append(out, src)
		}
	}
	return out
}

// keepGatedLockEntries restores the previous lock entry of every gated source
// and drops the new pin of sources that were never pinned.
func keepGatedLockEntries(entries []externalLockEntry, prev lockFile, gated map[string]gatedItem) []externalLockEntry {
	if len(gated) == 0 {
		return entries
	}
	var out []externalLockEntry
	for _, e := range entries {
		if _, ok := gated[e.Name]; !ok {
			out = append(out, e)
		}
	}
	for name := range gated {
		if e := findLockEntry(prev, name); e != nil {
			out = append(out, *e)
		}
	}
	sortLockEntries(out)
	return out
}

// restoreGatedCaches puts each gated source's cache back on its previous pin so
// the shared cache never serves a pin the gate rejected.
func restoreGatedCaches(sources []externalSkillSource, home string, prev lockFile, gated map[string]gatedItem) {
	for _, src := range sources {
		if _, ok := gated[sourceName(src)]; !ok {
			continue
		}
		if e := findLockEntry(prev, sourceName(src)); e != nil {
			_ = gitCheckoutCommit(filepath.Join(externalCacheDir(home), sourceName(src)), e.Commit, src.Branch)
		}
	}
}

// pendingCandidateItems returns the audit items of external sources whose
// rejected candidate is still staged. The shared cache only ever holds accepted
// content, so a candidate the gate blocked is inspectable here and nowhere else.
func pendingCandidateItems(home string, cfg config) []auditItem {
	candidateHome := externalCandidateHome(home)
	var items []auditItem
	for _, src := range cfg.ExternalSkills {
		if !hasDir(filepath.Join(externalCandidatePath(home, sourceName(src)), ".git")) {
			continue
		}
		items = append(items, externalSourceItem(src, candidateHome))
	}
	return items
}

// acceptAuditItems is collectAuditItems with each pending candidate checkout
// standing in for the shared checkout it would replace once accepted.
func acceptAuditItems(repoRoot, home string, cfg config) []auditItem {
	candidates := pendingCandidateItems(home, cfg)
	if len(candidates) == 0 {
		return collectAuditItems(repoRoot, home, cfg)
	}
	pending := make(map[string]bool, len(candidates))
	for _, item := range candidates {
		pending[item.Name] = true
	}
	var items []auditItem
	for _, item := range collectAuditItems(repoRoot, home, cfg) {
		if item.External && pending[item.Name] {
			continue
		}
		items = append(items, item)
	}
	return append(items, candidates...)
}

func printGatedSummary(w io.Writer, gated map[string]gatedItem) {
	if len(gated) == 0 {
		return
	}
	blocked, cooldownN := 0, 0
	for _, h := range gated {
		if h.Kind == "blocked" {
			blocked++
		} else {
			cooldownN++
		}
	}
	fmt.Fprintf(w, "skipped %d blocked and %d held item(s); see tackroom status\n", blocked, cooldownN)
}

// gatedNote describes external sources in cooldown or blocked in one line and
// points at the action that resolves each; empty when nothing is in cooldown
// or blocked. Used by the doctor external-skill checks so a source in cooldown or blocked
// is reported as such instead of as an unpinned or missing-materialization
// defect.
func gatedNote(gated map[string]gatedItem) string {
	cooldownN, blockedN := 0, 0
	for _, h := range gated {
		if h.Kind == "blocked" {
			blockedN++
		} else {
			cooldownN++
		}
	}
	if cooldownN+blockedN == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d held, %d blocked (skipped by sync); see tackroom status", cooldownN, blockedN)
	if cooldownN > 0 {
		b.WriteString("; a hold ends on its own or with sync --allow-fresh=<item>")
	}
	if blockedN > 0 {
		b.WriteString("; a blocked item needs tackroom doctor audit accept ... or a fix upstream")
	}
	return b.String()
}

func runAuditAccept(args []string) error {
	fs := flag.NewFlagSet("doctor audit accept", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var cfgPath, reason string
	fs.StringVar(&cfgPath, "config", "", "Path to tackroom YAML config")
	fs.StringVar(&reason, "reason", "", "Why the finding is acceptable (required)")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return fmt.Errorf("usage: tackroom doctor audit accept <item> <rule> --reason \"...\"")
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("--reason is required")
	}
	repoRoot, home, cfg, _, err := loadContext(runOptions{ConfigPath: cfgPath})
	if err != nil {
		return err
	}
	lock, err := readLockFile(repoRoot)
	if err != nil {
		return err
	}
	n := 0
	for _, item := range acceptAuditItems(repoRoot, home, cfg) {
		if item.ID() != pos[0] {
			continue
		}
		for _, f := range item.Findings {
			if f.Rule != pos[1] || isAccepted(lock, item.ID(), f) {
				continue
			}
			lock.Accepted = append(lock.Accepted, acceptedFinding{
				Item: item.ID(), Rule: f.Rule, Evidence: f.Evidence, Severity: f.Severity,
				Reason: reason, At: timeNow().UTC().Format(time.RFC3339),
			})
			n++
		}
	}
	if n == 0 {
		return fmt.Errorf("no unaccepted %s finding on %s (see tackroom doctor audit)", pos[1], pos[0])
	}
	if err := writeLockFile(repoRoot, lock); err != nil {
		return err
	}
	fmt.Printf("accepted %d %s finding(s) on %s\n", n, pos[1], pos[0])
	return nil
}

func runAuditRevoke(args []string) error {
	fs := flag.NewFlagSet("doctor audit revoke", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var cfgPath string
	fs.StringVar(&cfgPath, "config", "", "Path to tackroom YAML config")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return fmt.Errorf("usage: tackroom doctor audit revoke <item> <rule>")
	}
	repoRoot, _, _, _, err := loadContext(runOptions{ConfigPath: cfgPath})
	if err != nil {
		return err
	}
	lock, err := readLockFile(repoRoot)
	if err != nil {
		return err
	}
	var kept []acceptedFinding
	for _, a := range lock.Accepted {
		if a.Item != pos[0] || a.Rule != pos[1] {
			kept = append(kept, a)
		}
	}
	if len(kept) == len(lock.Accepted) {
		return fmt.Errorf("no accepted %s finding on %s", pos[1], pos[0])
	}
	removed := len(lock.Accepted) - len(kept)
	lock.Accepted = kept
	if err := writeLockFile(repoRoot, lock); err != nil {
		return err
	}
	fmt.Printf("revoked %d %s finding(s) on %s\n", removed, pos[1], pos[0])
	return nil
}

// printRuleFindings lists rule findings per item and returns how many
// unaccepted CRITICAL findings sit on external items.
func printRuleFindings(w io.Writer, items []auditItem, lock lockFile, asJSON bool) int {
	blocked := 0
	for _, item := range items {
		if item.External {
			blocked += len(unacceptedCritical(item, lock))
		}
	}
	if asJSON {
		type jsonItem struct {
			Item     string        `json:"item"`
			External bool          `json:"external"`
			Findings []ruleFinding `json:"findings"`
		}
		out := struct {
			Items   []jsonItem `json:"items"`
			Blocked int        `json:"blocked"`
		}{Items: []jsonItem{}, Blocked: blocked}
		for _, item := range items {
			out.Items = append(out.Items, jsonItem{item.ID(), item.External, append([]ruleFinding{}, item.Findings...)})
		}
		_ = encodeJSON(w, out)
		return blocked
	}
	fmt.Fprintln(w, "rule findings by item")
	total := 0
	for _, item := range items {
		if len(item.Findings) == 0 {
			continue
		}
		scope := "local"
		if item.External {
			scope = "external"
		}
		fmt.Fprintf(w, "%s (%s)\n", item.ID(), scope)
		for _, f := range item.Findings {
			total++
			tags := ""
			if f.Lowered {
				tags += " lowered"
			}
			if f.Accepted {
				tags += " accepted"
			}
			fmt.Fprintf(w, "  %-8s %s%s  %s  %s\n", f.Severity, f.Rule, tags, f.File, f.Match)
		}
	}
	fmt.Fprintf(w, "%d rule finding(s); %d unaccepted CRITICAL on external items\n", total, blocked)
	return blocked
}

// lockRow is one line of the lock as the TUI and the view show it: an external
// source with its pin, gate state and audit, or a package in cooldown.
type lockRow struct {
	Name     string `json:"name"`
	Source   string `json:"source,omitempty"` // the source URL; empty for a package in cooldown
	Commit   string `json:"commit,omitempty"` // the short pin, or the cloned head while unpinned
	Pinned   bool   `json:"pinned"`
	State    string `json:"state"`            // pinned, held (in cooldown), blocked, or a pin issue such as "unpinned"
	Reason   string `json:"reason,omitempty"` // why it is in cooldown or blocked, or the whole pin issue
	Until    string `json:"until,omitempty"`  // cooldown end, RFC 3339
	Command  string `json:"command,omitempty"`
	Audited  bool   `json:"audited"` // false until the source is cloned
	Findings int    `json:"findings"`
	Critical int    `json:"critical"`
}

// lockRows lists every configured external source, then every package in cooldown,
// with what status settled for each: no registry lookup, no write.
func lockRows(g statusGates) []lockRow {
	rows := []lockRow{}
	cacheRoot := externalCacheDir(g.home)
	for _, src := range g.sources {
		name := sourceName(src)
		cachePath := filepath.Join(cacheRoot, name)
		row := lockRow{Name: name, Source: src.URL, State: "pinned"}
		pin, issue := sourcePinIssue(g.lock, src, cacheRoot)
		cloned := hasDir(filepath.Join(cachePath, ".git"))
		switch {
		case pin != nil:
			row.Commit, row.Pinned = shortCommit(pin.Commit), true
		case cloned:
			row.Commit = externalSkillCommit(cachePath)
		}
		if h, ok := g.gated[name]; ok {
			row.State, row.Reason, row.Until, row.Command = h.Kind, h.Reason, h.Until, h.Command
		} else if issue != "" {
			row.State, _, _ = strings.Cut(issue, " (")
			row.Reason, row.Command = issue, "tackroom sync"
		} else if h, ok := g.gated["update-gate:"+name]; ok {
			row.State, row.Reason, row.Until, row.Command = h.Kind, h.Reason, h.Until, h.Command
		}
		if cloned {
			item := externalSourceItem(src, g.home)
			row.Audited = true
			for _, f := range item.Findings {
				if isAccepted(g.lock, item.ID(), f) {
					continue
				}
				row.Findings++
				if f.Severity == sevCritical {
					row.Critical++
				}
			}
		}
		rows = append(rows, row)
	}
	var packages []string
	for key := range g.gated {
		if strings.HasPrefix(key, "package-gate:") {
			packages = append(packages, key)
		}
	}
	sort.Strings(packages)
	for _, key := range packages {
		h := g.gated[key]
		rows = append(rows, lockRow{Name: h.Item, State: h.Kind, Reason: h.Reason, Until: h.Until, Command: h.Command})
	}
	return rows
}

// gatedLines renders one status line per item in cooldown or blocked.
func gatedLines(gated map[string]gatedItem) []string {
	var lines []string
	for _, h := range gated {
		line := fmt.Sprintf("%s %s: %s", h.Kind, h.Item, h.Reason)
		if h.Until != "" {
			line += "; hold ends " + h.Until
		}
		lines = append(lines, line+"; override: "+h.Command)
	}
	sort.Strings(lines)
	return lines
}
