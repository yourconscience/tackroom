package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yourconscience/tackroom/internal/agentrole"
)

// setupSyncResult is what the setup-driven sync did, for the setup summary.
type setupSyncResult struct {
	done      bool // the sync ran to the end; a returned error is then only its leftovers
	repo      repoLinkReport
	reports   []agentReport // final state, with this run's actions restored
	conflicts []string      // "agent: message" for conflicts left in place
	changes   int
	backupDir string
	moved     []string // native paths moved into backupDir
	held      int
}

// errSetupNeedsAttention ends a setup whose summary lists what is left to fix.
var errSetupNeedsAttention = errors.New("tackroom setup: some items need your attention; see the list above")

// setupSummary collects what one setup run did and prints it in a few plain
// lines; `setup --verbose` prints the full sync report before it.
type setupSummary struct {
	home     string
	root     string
	agents   []string
	fresh    bool       // the config root had no tackroom.yaml before this run
	from     *setupFrom // --from: what the clone or pull did
	gitNew   bool       // this run created the git repository
	gitNote  string     // the first commit is still to do (git has no identity)
	imported map[string]int
	// warnings name harness configs detection could not read; what they
	// hold was left out of the import.
	warnings []string
	// keptRoles are roles each agent keeps as its own, with the reason.
	keptRoles map[string]string
	// differs holds native skill copies whose content differs from the
	// shared one, taken before the sync moves them.
	differs  map[string]bool
	differed []string
	memory   string // the memory line, for example "basic (hooks at session start and end)"
	rootDocs []rootInstructionSource
	changed  bool // setup changed a harness config itself (not through sync)
	sync     setupSyncResult
	periodic string
	cronNew  bool
	problems []string
	// hookProblems are memory hook cleanup failures. They come from an
	// unreadable harness config, which the sync reports too, so they are only
	// listed when the sync read every config.
	hookProblems []string
}

func (s *setupSummary) problem(format string, args ...any) {
	s.problems = append(s.problems, fmt.Sprintf(format, args...))
}

// canonicalItems names the skills, roles and MCP servers the config root holds,
// keyed like detected items ("skill:name"), so setup can tell what it imported.
func canonicalItems(root string, cfg config) map[string]bool {
	items := map[string]bool{}
	if entries, err := os.ReadDir(filepath.Join(root, "skills")); err == nil {
		for _, e := range entries {
			if e.IsDir() && hasFile(filepath.Join(root, "skills", e.Name(), "SKILL.md")) {
				items["skill:"+e.Name()] = true
			}
		}
	}
	if entries, err := os.ReadDir(filepath.Join(root, "agents")); err == nil {
		for _, e := range entries {
			if !e.IsDir() && filepath.Ext(e.Name()) == agentrole.MarkdownExtension {
				items["role:"+strings.TrimSuffix(e.Name(), agentrole.MarkdownExtension)] = true
			}
		}
	}
	for _, server := range cfg.MCPServers {
		items["mcp:"+server.Name] = true
	}
	return items
}

// noteImports counts what the import added to the config root.
func (s *setupSummary) noteImports(before, after map[string]bool) {
	s.imported = map[string]int{}
	for key := range after {
		if !before[key] {
			surface, _, _ := strings.Cut(key, ":")
			s.imported[surface]++
		}
	}
}

// noteDiffering records, before the sync, which native skill copies differ
// from the shared copy the config root now has.
func (s *setupSummary) noteDiffering(detection *detectionResult) {
	s.differs = map[string]bool{}
	for _, item := range append(append([]detectedItem(nil), detection.Items...), detection.Resolved...) {
		if item.Surface != "skill" {
			continue
		}
		shared := filepath.Join(s.root, "skills", item.Name)
		for _, src := range item.Sources {
			if same, err := treesEqual(src.Path, shared); err == nil && !same {
				s.differs[src.Path] = true
			}
		}
	}
}

// noteDiffered runs after the sync: a native skill copy that differed and was
// moved into the backup lost to the shared one. Roles are shared only when
// every copy is the same role, and an MCP server is imported for the agent
// it came from, so neither has a losing copy.
func (s *setupSummary) noteDiffered(before map[string]bool, detection *detectionResult, decisions []reviewDecision) {
	moved := map[string]bool{}
	for _, path := range s.sync.moved {
		moved[path] = true
	}
	for _, item := range append(append([]detectedItem(nil), detection.Items...), detection.Resolved...) {
		if item.Surface != "skill" {
			continue
		}
		existed := before[item.Surface+":"+item.Name]
		if !existed && (len(item.Sources) < 2 || item.Identical) {
			continue
		}
		winner, kept := "", ""
		if existed {
			kept = "the one already in " + displayPath(s.root, s.home)
		} else {
			for _, d := range decisions {
				if d.Surface == item.Surface && d.Name == item.Name && d.Action == actionShare {
					winner = d.Source.Harness
					kept = winner + "'s"
				}
			}
		}
		// Losers are copies that differed; the winner's own copy (identical
		// to what was kept) is backed up too, and the line says so.
		var losers, backedUp []string
		for _, src := range item.Sources {
			if !moved[src.Path] {
				continue
			}
			backedUp = append(backedUp, src.Harness+"'s")
			if src.Harness != winner && s.differs[src.Path] {
				losers = append(losers, src.Harness)
			}
		}
		if kept == "" || len(losers) == 0 {
			continue
		}
		label := item.Surface + " " + item.Name
		if !existed {
			label += " differed"
		}
		copies := "copy is"
		if len(backedUp) > 1 {
			copies = "copies are"
		}
		s.differed = append(s.differed, fmt.Sprintf("%s: kept %s; %s %s backed up", label, kept, joinAnd(backedUp), copies))
	}
}

// noop reports a rerun that found nothing to do.
func (s *setupSummary) noop() bool {
	if s.fresh || s.gitNew || s.changed || s.cronNew || len(s.rootDocs) > 0 || (s.from != nil && s.from.changed) {
		return false
	}
	for _, n := range s.imported {
		if n > 0 {
			return false
		}
	}
	return s.sync.changes == 0 && len(s.sync.moved) == 0
}

// syncProblems lists what the sync left for the user, each with its fix.
func (s *setupSummary) syncProblems() []string {
	var out []string
	if s.sync.repo.State != "" && s.sync.repo.State != stateSynced {
		out = append(out, fmt.Sprintf("~/.agents does not point to %s (%s). Fix: tackroom sync", s.sync.repo.ExpectedTarget, s.sync.repo.State))
	}
	for _, conflict := range s.sync.conflicts {
		out = append(out, conflict+". Resolve it, then run: tackroom setup")
	}
	for _, r := range s.sync.reports {
		switch {
		case r.Error != "":
			out = append(out, fmt.Sprintf("%s: could not read its config (%s). Fix the file, then run: tackroom setup", r.Name, r.Error))
		case r.Detected && !r.Synced && len(r.Conflicts) == 0:
			// Setup never removes or overwrites what an earlier tackroom run
			// wrote without asking; plain sync does.
			left := countParts(len(r.StaleManaged), "old skill link", len(r.DriftedAgent), "role rewrite", len(r.StaleMCP), "old MCP server", len(r.StalePlugin), "old plugin setting", len(r.DriftedPackage), "Pi package change")
			if len(left) > 0 {
				out = append(out, fmt.Sprintf("%s: setup left %s for you, since it does not remove or rewrite what an earlier tackroom run wrote. Apply with: tackroom sync", r.Name, joinAnd(left)))
				continue
			}
			out = append(out, fmt.Sprintf("%s is not fully synced (tackroom status shows what is left). Fix: tackroom sync", r.Name))
		}
	}
	return out
}

// short shows paths under the home directory as ~/...
func (s *setupSummary) short(text string) string {
	return strings.ReplaceAll(text, s.home+string(filepath.Separator), "~"+string(filepath.Separator))
}

// print writes the summary and returns how many items need the user.
func (s *setupSummary) print(w io.Writer) int {
	problems := append([]string(nil), s.problems...)
	unreadable := false
	for _, r := range s.sync.reports {
		unreadable = unreadable || r.Error != ""
	}
	if !unreadable {
		problems = append(problems, s.hookProblems...)
	}
	// Listed on every run until the imported sections are merged: AGENTS.md
	// itself says whether they still are.
	if paths := unmergedInstructionSources(s.root); len(paths) > 0 {
		agentsMD := filepath.Join(displayPath(s.root, s.home), "AGENTS.md")
		problems = append(problems, fmt.Sprintf("%s still holds the separate sections setup imported from %s, and every agent reads all of them. Merge them into one set of rules and delete the \"## From ...\" headings: %s", agentsMD, joinAnd(paths), agentsMD))
	}
	problems = append(problems, s.syncProblems()...)
	if len(problems) == 0 && s.noop() {
		fmt.Fprintln(w, "Already set up; everything is synced.")
		return 0
	}
	root := displayPath(s.root, s.home)
	synced := map[string]bool{}
	for _, r := range s.sync.reports {
		synced[r.Name] = r.Detected
	}
	found := make([]string, len(s.agents))
	for i, name := range s.agents {
		found[i] = name
		if s.sync.done && !synced[name] {
			found[i] += " (off in tackroom.local.yaml)"
		}
	}
	fmt.Fprintf(w, "Found %s: %s\n", plural(len(s.agents), "coding agent"), strings.Join(found, ", "))
	fmt.Fprintf(w, "Config: %s%s\n", root, s.configNote())
	if s.gitNote != "" {
		fmt.Fprintf(w, "  %s\n", strings.ReplaceAll(s.gitNote, s.root, root))
	}
	if s.memory != "" {
		fmt.Fprintln(w, "Memory: "+s.memory)
	}
	for _, warning := range s.warnings {
		fmt.Fprintln(w, "Not imported: "+s.short(warning))
	}

	indent := ""
	if parts := countParts(s.imported["skill"], "skill", s.imported["role"], "role", s.imported["mcp"], "MCP server"); len(parts) > 0 {
		fmt.Fprintf(w, "Imported %s from them (copied into %s)\n", joinAnd(parts), root)
		indent = "  "
	}
	for _, line := range s.differed {
		fmt.Fprintln(w, indent+line)
	}
	if len(s.keptRoles) > 0 {
		fmt.Fprintln(w, "Not shared; each agent keeps its own:")
		for _, name := range sortedKeys(s.keptRoles) {
			fmt.Fprintf(w, "  role %s %s\n", name, s.short(s.keptRoles[name]))
		}
	}
	if len(s.rootDocs) > 0 {
		paths := make([]string, len(s.rootDocs))
		for i, src := range s.rootDocs {
			paths[i] = displayPath(src.Path, s.home)
		}
		fmt.Fprintf(w, "Imported your instructions from %s into %s\n", joinAnd(paths), filepath.Join(root, "AGENTS.md"))
	}
	if len(s.sync.moved) > 0 {
		them := "them"
		if len(s.sync.moved) == 1 {
			them = "it"
		}
		fmt.Fprintf(w, "Backed up %s before replacing %s: %s (undo: tackroom restore latest)\n", plural(len(s.sync.moved), "original"), them, displayPath(s.sync.backupDir, s.home))
	}
	s.printLinked(w)
	if s.sync.held > 0 {
		fmt.Fprintf(w, "Held back %s from external sources (too new or blocked); see: tackroom status\n", plural(s.sync.held, "item"))
	}
	if s.periodic != "" {
		fmt.Fprintln(w, s.periodic)
	}
	if len(problems) == 0 {
		fmt.Fprintln(w, "Everything is synced.")
	} else {
		fmt.Fprintln(w, "Needs your attention:")
		for _, p := range problems {
			fmt.Fprintln(w, "  - "+s.short(p))
		}
	}
	s.printNextMachine(w)
	return len(problems)
}

func (s *setupSummary) configNote() string {
	switch {
	case s.from != nil && s.from.clone && s.fresh:
		return fmt.Sprintf(" (cloned from %s; it had no tackroom.yaml, so setup started a new config in it)", redactURL(s.from.url))
	case s.from != nil && s.from.clone:
		return fmt.Sprintf(" (cloned from %s)", redactURL(s.from.url))
	case s.from != nil && s.from.changed:
		return fmt.Sprintf(" (pulled from %s)", redactURL(s.from.url))
	case s.from != nil:
		return fmt.Sprintf(" (up to date with %s)", redactURL(s.from.url))
	case s.gitNew:
		return " (new git repository)"
	case s.fresh:
		return " (new)"
	}
	return ""
}

// printLinked says what each agent now gets from the config root, in one line
// when every agent gets the same.
func (s *setupSummary) printLinked(w io.Writer) {
	var names, lines []string
	for _, r := range s.sync.reports {
		if !r.Detected || r.Error != "" {
			continue
		}
		parts := countParts(len(r.Managed), "skill", len(r.ManagedAgent), "role", len(r.ManagedHook), "hook", len(r.ManagedMCP), "MCP server", len(r.ManagedPlugin), "plugin")
		if r.RootPath != "" && r.RootState == stateSynced {
			parts = append(parts, "AGENTS.md")
		}
		line := strings.Join(parts, ", ")
		if line == "" {
			line = "nothing yet"
		}
		names = append(names, r.Name)
		lines = append(lines, line)
	}
	if len(names) == 0 {
		return
	}
	suffix := ""
	if !s.fresh && (s.from == nil || !s.from.clone) && s.sync.changes > 0 {
		suffix = fmt.Sprintf(" (%s this run)", plural(s.sync.changes, "change"))
	}
	same := true
	for _, line := range lines[1:] {
		same = same && line == lines[0]
	}
	if same {
		fmt.Fprintf(w, "Linked into %s: %s%s\n", joinAnd(names), lines[0], suffix)
		return
	}
	fmt.Fprintf(w, "Linked%s:\n", suffix)
	for i, name := range names {
		fmt.Fprintf(w, "  %s: %s\n", name, lines[i])
	}
}

// printNextMachine says how to bring this config to another machine.
func (s *setupSummary) printNextMachine(w io.Writer) {
	root := displayPath(s.root, s.home)
	inside, err := exec.Command("git", "-C", s.root, "rev-parse", "--is-inside-work-tree").Output()
	if err != nil || strings.TrimSpace(string(inside)) != "true" {
		fmt.Fprintf(w, "\nTo use this setup on another machine, put %s in a private git repository, then run there:\n  tackroom setup --from <repository url>\n", root)
		return
	}
	origin, _ := exec.Command("git", "-C", s.root, "config", "--get", "remote.origin.url").Output()
	url := redactURL(strings.TrimSpace(string(origin)))
	push := "push " + root
	if status, err := exec.Command("git", "-C", s.root, "status", "--porcelain").Output(); err == nil && len(strings.TrimSpace(string(status))) > 0 {
		push = "commit and push " + root
	}
	if url == "" {
		fmt.Fprintf(w, "\nTo use this setup on another machine, %s to a private repository, then run there:\n  tackroom setup --from <repository url>\n", push)
		return
	}
	lead := "To use this setup on another machine, run there:"
	if strings.HasPrefix(push, "commit") {
		lead = fmt.Sprintf("To use this setup on another machine, %s, then run there:", push)
	}
	fmt.Fprintf(w, "\n%s\n  tackroom setup --from %s\n", lead, url)
}

// countParts renders count/noun pairs as "2 skills", dropping zeros.
func countParts(pairs ...any) []string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		if n := pairs[i].(int); n > 0 {
			parts = append(parts, plural(n, pairs[i+1].(string)))
		}
	}
	return parts
}

// joinAnd renders "a", "a and b", "a, b and c".
func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
