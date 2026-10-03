package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func printReport(mode string, repoRoot string, repoReport repoLinkReport, reports []agentReport, home string, cfg config) {
	fmt.Printf("tackroom %s\n", mode)
	fmt.Printf("repo: %s\n", repoRoot)
	fmt.Printf("~/.agents: %s", repoReport.State)
	if repoReport.State == stateSynced {
		fmt.Printf(" -> %s\n", repoReport.ExpectedTarget)
	} else {
		fmt.Printf(" (expected %s", repoReport.ExpectedTarget)
		if repoReport.ActualTarget != "" {
			fmt.Printf(", actual %s", repoReport.ActualTarget)
		}
		fmt.Printf(")\n")
	}
	if len(cfg.ExternalSkills) > 0 {
		cacheRoot := externalCacheDir(home)
		fmt.Println()
		fmt.Println("external sources:")
		for _, src := range cfg.ExternalSkills {
			name := repoName(src.URL)
			cachePath := filepath.Join(cacheRoot, name)
			state := "not cloned"
			if hasDir(filepath.Join(cachePath, ".git")) {
				commit := externalSkillCommit(cachePath)
				state = fmt.Sprintf("synced (%s)", commit)
			}
			fmt.Printf("  %s  %s  %s\n", name, src.URL, state)
		}
	}
	fmt.Println()

	for _, report := range reports {
		fmt.Printf("%s\n", report.Name)
		if report.Error != "" {
			fmt.Printf("  error: %s\n", report.Error)
			fmt.Println("  left unchanged; fix the file and run tackroom sync again")
			fmt.Println()
			continue
		}
		if !report.Detected {
			fmt.Println("  not detected (binary not on PATH)")
			fmt.Println()
			continue
		}
		fmt.Printf("  skill root: %s\n", report.SkillRoot)
		if report.AgentRoot != "" {
			fmt.Printf("  agent root: %s\n", report.AgentRoot)
		}
		if h := harnessFor(report.Name); h != nil && h.IntegrationNote != "" {
			fmt.Printf("  integration: %s\n", h.IntegrationNote)
		}
		if report.RootPath != "" {
			fmt.Printf("  root instructions: %s", report.RootState)
			if report.RootState == stateSynced {
				fmt.Printf(" -> %s\n", report.RootExpected)
			} else {
				fmt.Printf(" (expected %s", report.RootExpected)
				if report.RootActual != "" {
					fmt.Printf(", actual %s", report.RootActual)
				}
				fmt.Printf(")\n")
			}
		}
		if report.Synced {
			fmt.Println("  sync: synced")
		} else {
			fmt.Println("  sync: drifted")
		}
		if mode == "status" {
			contextSkills := contextSkillsForReport(cfg, repoRoot, home, report)
			listingBytes := skillListingBytes(contextSkills)
			fmt.Printf("  skill listing context: %d skills, %d bytes name+desc, %s\n", len(contextSkills), listingBytes, formatTokenEstimate(estimateTokens(listingBytes)))
		}
		fmt.Printf("  managed (%d): %s\n", len(report.Managed), displayList(report.Managed))
		if len(report.ManagedAgent)+len(report.MissingAgent)+len(report.DriftedAgent) > 0 {
			fmt.Printf("  agent managed (%d): %s\n", len(report.ManagedAgent), displayList(report.ManagedAgent))
		}
		fmt.Printf("  external (%d): %s\n", len(report.External), displayList(report.External))
		if len(report.ManagedMCP)+len(report.MissingMCP)+len(report.DriftedMCP) > 0 {
			fmt.Printf("  mcp managed (%d): %s\n", len(report.ManagedMCP), displayList(report.ManagedMCP))
		}
		if len(report.ManagedHook)+len(report.MissingHook)+len(report.DriftedHook)+len(report.UnsupportedHook) > 0 {
			fmt.Printf("  hook managed (%d): %s\n", len(report.ManagedHook), displayList(report.ManagedHook))
		}
		if len(report.ManagedPackage)+len(report.DriftedPackage) > 0 {
			fmt.Printf("  package managed (%d): %s\n", len(report.ManagedPackage), displayList(report.ManagedPackage))
		}
		if len(report.Missing) > 0 {
			fmt.Printf("  missing (%d): %s\n", len(report.Missing), displayList(report.Missing))
		}
		if len(report.MissingAgent) > 0 {
			fmt.Printf("  agent missing (%d): %s\n", len(report.MissingAgent), displayList(report.MissingAgent))
		}
		if len(report.MissingMCP) > 0 {
			fmt.Printf("  mcp missing (%d): %s\n", len(report.MissingMCP), displayList(report.MissingMCP))
		}
		if len(report.MissingHook) > 0 {
			fmt.Printf("  hook missing (%d): %s\n", len(report.MissingHook), displayList(report.MissingHook))
		}
		if len(report.Drifted) > 0 {
			fmt.Printf("  drifted (%d): %s\n", len(report.Drifted), displayList(report.Drifted))
		}
		if len(report.DriftedAgent) > 0 {
			fmt.Printf("  agent drifted (%d): %s\n", len(report.DriftedAgent), displayList(report.DriftedAgent))
		}
		if len(report.DriftedMCP) > 0 {
			fmt.Printf("  mcp drifted (%d): %s\n", len(report.DriftedMCP), displayList(report.DriftedMCP))
		}
		if len(report.DriftedHook) > 0 {
			fmt.Printf("  hook drifted (%d): %s\n", len(report.DriftedHook), displayList(report.DriftedHook))
		}
		if len(report.DriftedPackage) > 0 {
			fmt.Printf("  package drifted (%d): %s\n", len(report.DriftedPackage), displayList(report.DriftedPackage))
		}
		if len(report.UnsupportedHook) > 0 {
			fmt.Printf("  hook unsupported (%d): %s\n", len(report.UnsupportedHook), displayList(report.UnsupportedHook))
		}
		if len(report.StaleManaged) > 0 {
			fmt.Printf("  stale managed (%d): %s\n", len(report.StaleManaged), displayList(report.StaleManaged))
		}
		if len(report.Conflicts) > 0 {
			fmt.Printf("  conflicts (%d): %s\n", len(report.Conflicts), displayList(report.Conflicts))
		}
		if mode == "sync" {
			fmt.Printf("  sync actions: add=%d update=%d remove=%d agent-add=%d agent-update=%d agent-remove=%d mcp-add=%d mcp-update=%d hook-add=%d hook-update=%d package-update=%d package-remove=%d\n", len(report.Adds), len(report.Updates), len(report.Removes), len(report.AddsAgent), len(report.UpdatesAgent), len(report.RemovesAgent), len(report.AddsMCP), len(report.UpdatesMCP), len(report.AddsHook), len(report.UpdatesHook), len(report.UpdatesPackage), len(report.RemovesPackage))
		}
		fmt.Println()
	}
}

func displayList(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	return strings.Join(items, ", ")
}

func sortReportLists(report *agentReport) {
	sort.Strings(report.Managed)
	sort.Strings(report.ManagedAgent)
	sort.Strings(report.ManagedMCP)
	sort.Strings(report.ManagedHook)
	sort.Strings(report.ManagedPackage)
	sort.Strings(report.Drifted)
	sort.Strings(report.DriftedAgent)
	sort.Strings(report.DriftedMCP)
	sort.Strings(report.DriftedHook)
	sort.Strings(report.DriftedPackage)
	sort.Strings(report.Missing)
	sort.Strings(report.MissingAgent)
	sort.Strings(report.MissingMCP)
	sort.Strings(report.MissingHook)
	sort.Strings(report.UnsupportedHook)
	sort.Strings(report.UpdatesPackage)
	sort.Strings(report.RemovesPackage)
	sort.Strings(report.Conflicts)
	sort.Strings(report.StaleManaged)
	sort.Strings(report.External)
	sort.Strings(report.Adds)
	sort.Strings(report.AddsAgent)
	sort.Strings(report.AddsMCP)
	sort.Strings(report.AddsHook)
	sort.Strings(report.Updates)
	sort.Strings(report.UpdatesAgent)
	sort.Strings(report.UpdatesMCP)
	sort.Strings(report.UpdatesHook)
	sort.Strings(report.Removes)
	sort.Strings(report.RemovesAgent)
}

func cloneReports(reports []agentReport) []agentReport {
	cloned := make([]agentReport, len(reports))
	copy(cloned, reports)
	return cloned
}

func restoreSyncActions(current []agentReport, preflight []agentReport) {
	index := make(map[string]agentReport, len(preflight))
	for _, report := range preflight {
		index[report.Name] = report
	}
	for i := range current {
		if original, ok := index[current[i].Name]; ok {
			current[i].Adds = append([]string{}, original.Adds...)
			current[i].AddsAgent = append([]string{}, original.AddsAgent...)
			current[i].AddsMCP = append([]string{}, original.AddsMCP...)
			current[i].AddsHook = append([]string{}, original.AddsHook...)
			current[i].Updates = append([]string{}, original.Updates...)
			current[i].UpdatesAgent = append([]string{}, original.UpdatesAgent...)
			current[i].RemovesAgent = append([]string{}, original.RemovesAgent...)
			current[i].UpdatesMCP = append([]string{}, original.UpdatesMCP...)
			current[i].UpdatesHook = append([]string{}, original.UpdatesHook...)
			current[i].UpdatesPackage = append([]string{}, original.UpdatesPackage...)
			current[i].RemovesPackage = append([]string{}, original.RemovesPackage...)
			current[i].Removes = append([]string{}, original.Removes...)
		}
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func hasDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func hasFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// palette renders status markers and emphasis with ANSI color when stdout is a
// TTY (and NO_COLOR is unset). Piped output — tests, files, other tools — stays
// plain so it never carries escape codes.
type palette struct{ on bool }

func statusPalette() palette {
	// NO_COLOR opts out when present, regardless of value (https://no-color.org).
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return palette{on: false}
	}
	info, err := os.Stdout.Stat()
	if err != nil {
		return palette{on: false}
	}
	return palette{on: info.Mode()&os.ModeCharDevice != 0}
}

func (p palette) wrap(code, s string) string {
	if !p.on {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p palette) bold(s string) string   { return p.wrap("1", s) }
func (p palette) dim(s string) string    { return p.wrap("2", s) }
func (p palette) green(s string) string  { return p.wrap("32", s) }
func (p palette) yellow(s string) string { return p.wrap("33", s) }
func (p palette) red(s string) string    { return p.wrap("31", s) }

// mark returns a colored status glyph for one of pass/warn/fail states.
func (p palette) mark(kind string) string {
	switch kind {
	case "ok":
		return p.green("✓")
	case "warn":
		return p.yellow("!")
	default:
		return p.red("✗")
	}
}

// printStatusReport renders a human-scannable `tackroom status`: overall health
// first, then a compact per-harness block that leads with sync state and shows
// only actionable drift. The identical multi-harness managed lists collapse to
// counts; --verbose (verbose=true) restores the full lists and native roots.
func printStatusReport(repoRoot string, repoReport repoLinkReport, reports []agentReport, home string, cfg config, verbose bool) {
	p := statusPalette()
	fmt.Println(p.bold("tackroom status"))
	fmt.Println()

	if repoReport.State == stateSynced {
		fmt.Printf("repo   %s ~/.agents %s %s\n", p.mark("ok"), p.dim("->"), repoReport.ExpectedTarget)
	} else {
		detail := fmt.Sprintf("expected %s", repoReport.ExpectedTarget)
		if repoReport.ActualTarget != "" {
			detail += fmt.Sprintf(", actual %s", repoReport.ActualTarget)
		}
		fmt.Printf("repo   %s ~/.agents %s (%s)\n", p.mark("fail"), repoReport.State, detail)
	}

	if len(cfg.ExternalSkills) > 0 {
		cacheRoot := externalCacheDir(home)
		fmt.Println(p.dim("sources"))
		for _, src := range cfg.ExternalSkills {
			name := repoName(src.URL)
			state := p.mark("fail") + " not cloned"
			if hasDir(filepath.Join(cacheRoot, name, ".git")) {
				state = fmt.Sprintf("%s %s", p.mark("ok"), externalSkillCommit(filepath.Join(cacheRoot, name)))
			}
			fmt.Printf("  %s  %s  %s\n", state, name, p.dim(src.URL))
		}
	}

	var drifted, failed []string
	for _, report := range reports {
		fmt.Println()
		printHarnessStatus(p, report, repoRoot, home, cfg, verbose)
		switch {
		case report.Error != "":
			failed = append(failed, report.Name)
		case report.Detected && !report.Synced:
			drifted = append(drifted, report.Name)
		}
	}

	fmt.Println()
	fmt.Println(p.dim("checks"))
	checks := []checkResult{checkExternalSkillLock(repoRoot, cfg, home)}
	if memsearchSetUp(repoRoot, home) {
		checks = append(checks, checkMemsearchIndex(repoRoot, home))
	}
	width := 0
	for _, chk := range checks {
		if len(chk.name) > width {
			width = len(chk.name)
		}
	}
	for _, chk := range checks {
		fmt.Printf("  %s %-*s  %s\n", p.mark(checkMarkKind(chk.status)), width, chk.name, p.dim(chk.detail))
	}

	fmt.Println()
	if len(failed) > 0 {
		fmt.Printf("%s %s (config could not be read; fix it, then run %s)\n", p.red("failed:"), strings.Join(failed, ", "), p.bold("tackroom sync"))
	}
	if len(drifted) == 0 && len(failed) == 0 && repoReport.State == stateSynced {
		fmt.Println(p.green("Everything is synced."))
		return
	}
	if len(drifted) == 0 && repoReport.State == stateSynced {
		return
	}
	if repoReport.State != stateSynced {
		fmt.Printf("%s ~/.agents repo link is not synced.\n", p.yellow("drift:"))
	}
	if len(drifted) > 0 {
		fmt.Printf("%s %s\n", p.yellow("drifted:"), strings.Join(drifted, ", "))
	}
	fmt.Printf("run %s to reconcile.\n", p.bold("tackroom sync"))
}

// checkMarkKind maps a checkResult status to a palette mark kind.
func checkMarkKind(status string) string {
	switch status {
	case checkStatusPass:
		return "ok"
	case checkStatusWarn:
		return "warn"
	default:
		return "fail"
	}
}

func printHarnessStatus(p palette, report agentReport, repoRoot string, home string, cfg config, verbose bool) {
	if report.Error != "" {
		fmt.Printf("%s   %s %s\n", p.bold(report.Name), p.mark("fail"), p.red("config unreadable"))
		fmt.Printf("  error       %s\n", report.Error)
		return
	}
	if !report.Detected {
		fmt.Printf("%s   %s\n", p.bold(report.Name), p.dim("not detected (binary not on PATH)"))
		return
	}
	if report.Synced {
		fmt.Printf("%s   %s %s\n", p.bold(report.Name), p.mark("ok"), p.green("synced"))
	} else {
		fmt.Printf("%s   %s %s\n", p.bold(report.Name), p.mark("fail"), p.yellow("drifted"))
	}

	if verbose {
		fmt.Printf("  skill root  %s\n", p.dim(report.SkillRoot))
		if report.AgentRoot != "" {
			fmt.Printf("  agent root  %s\n", p.dim(report.AgentRoot))
		}
	}
	if h := harnessFor(report.Name); h != nil && h.IntegrationNote != "" {
		fmt.Printf("  integration %s\n", p.dim(h.IntegrationNote))
	}
	if report.RootPath != "" {
		if report.RootState == stateSynced {
			fmt.Printf("  root doc    %s synced %s %s\n", p.mark("ok"), p.dim("->"), report.RootExpected)
		} else {
			detail := fmt.Sprintf("expected %s", report.RootExpected)
			if report.RootActual != "" {
				detail += fmt.Sprintf(", actual %s", report.RootActual)
			}
			fmt.Printf("  root doc    %s %s (%s)\n", p.mark("fail"), report.RootState, detail)
		}
	}

	fmt.Printf("  managed     %s\n", surfaceCounts(report))
	contextSkills := contextSkillsForReport(cfg, repoRoot, home, report)
	listingBytes := skillListingBytes(contextSkills)
	fmt.Printf("  context     %s\n", p.dim(fmt.Sprintf("%d skills, %d bytes, %s", len(contextSkills), listingBytes, formatTokenEstimate(estimateTokens(listingBytes)))))

	if verbose {
		printVerboseSurfaceLists(report)
	}

	for _, b := range driftBuckets(report) {
		if len(b.items) == 0 {
			continue
		}
		marker := p.mark("fail")
		if b.label == "conflicts" {
			marker = p.red("✗")
		}
		fmt.Printf("  %s %s: %s\n", marker, b.label, displayList(b.items))
	}
}

// surfaceCounts summarizes the managed surfaces a harness carries, omitting
// categories a harness does not support.
func surfaceCounts(r agentReport) string {
	parts := []string{fmt.Sprintf("%d skills", len(r.Managed))}
	if n := len(r.ManagedAgent); n > 0 {
		parts = append(parts, fmt.Sprintf("%d agents", n))
	}
	if n := len(r.ManagedMCP); n > 0 {
		parts = append(parts, fmt.Sprintf("%d mcp", n))
	}
	if n := len(r.ManagedHook); n > 0 {
		parts = append(parts, fmt.Sprintf("%d hooks", n))
	}
	if n := len(r.ManagedPackage); n > 0 {
		parts = append(parts, fmt.Sprintf("%d packages", n))
	}
	out := strings.Join(parts, " · ")
	if n := len(r.External); n > 0 {
		out += fmt.Sprintf("  (+%d external)", n)
	}
	return out
}

type driftBucket struct {
	label string
	items []string
}

// driftBuckets lists the actionable, non-synced surfaces for a harness in a
// stable order so the concise status view can render only what needs a sync.
func driftBuckets(r agentReport) []driftBucket {
	return []driftBucket{
		{"skills drifted", r.Drifted},
		{"skills missing", r.Missing},
		{"skills stale", r.StaleManaged},
		{"agents drifted", r.DriftedAgent},
		{"agents missing", r.MissingAgent},
		{"mcp drifted", r.DriftedMCP},
		{"mcp missing", r.MissingMCP},
		{"hooks drifted", r.DriftedHook},
		{"hooks missing", r.MissingHook},
		{"hooks unsupported", r.UnsupportedHook},
		{"packages drifted", r.DriftedPackage},
		{"packages removed", r.RemovesPackage},
		{"conflicts", r.Conflicts},
	}
}

// printVerboseSurfaceLists restores the full managed and external skill lists
// that the concise view collapses to counts.
func printVerboseSurfaceLists(report agentReport) {
	fmt.Printf("  skills (%d):  %s\n", len(report.Managed), displayList(report.Managed))
	if len(report.ManagedAgent) > 0 {
		fmt.Printf("  agents (%d):  %s\n", len(report.ManagedAgent), displayList(report.ManagedAgent))
	}
	if len(report.ManagedMCP) > 0 {
		fmt.Printf("  mcp (%d):     %s\n", len(report.ManagedMCP), displayList(report.ManagedMCP))
	}
	if len(report.ManagedHook) > 0 {
		fmt.Printf("  hooks (%d):   %s\n", len(report.ManagedHook), displayList(report.ManagedHook))
	}
	if len(report.ManagedPackage) > 0 {
		fmt.Printf("  packages (%d): %s\n", len(report.ManagedPackage), displayList(report.ManagedPackage))
	}
	if len(report.External) > 0 {
		fmt.Printf("  external (%d): %s\n", len(report.External), displayList(report.External))
	}
}
