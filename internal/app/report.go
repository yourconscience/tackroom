package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/yourconscience/tackroom/internal/ui"
)

// printReport renders the full `tackroom sync` report: what this run changed,
// the repo link and sources, one table row per harness, then a block per
// detected harness that lists every managed item and any problem. Nothing is
// collapsed; long lists wrap to the terminal width instead.
func printReport(mode string, repoRoot string, repoReport repoLinkReport, reports []agentReport, home string, cfg config) {
	_ = ui.Fprint(os.Stdout, renderReport(mode, repoReport, reports, home, cfg, ui.Width(os.Stdout)))
}

func renderReport(mode string, repoReport repoLinkReport, reports []agentReport, home string, cfg config, width int) string {
	var b strings.Builder
	problems := reportProblems(repoReport, reports)
	state := ui.Mark("ok") + " " + ui.Green.Render("synced")
	if len(problems) > 0 {
		state = ui.Mark("fail") + " " + ui.Yellow.Render("needs attention")
	}
	fmt.Fprintf(&b, "%s  %s\n\n", ui.Bold.Render("tackroom "+mode), state)

	const labelWidth = 8
	writeField(&b, 0, labelWidth, "changes", changesLine(reports), width)
	writeField(&b, 0, labelWidth, "repo", repoLine(repoReport), width)
	if len(cfg.ExternalSkills) > 0 {
		cacheRoot := externalCacheDir(home)
		nameWidth := 0
		for _, src := range cfg.ExternalSkills {
			nameWidth = max(nameWidth, len(repoName(src.URL)))
		}
		label := "sources"
		for _, src := range cfg.ExternalSkills {
			name := repoName(src.URL)
			state := ui.Mark("fail") + " not cloned"
			if hasDir(filepath.Join(cacheRoot, name, ".git")) {
				state = ui.Mark("ok") + " " + externalSkillCommit(filepath.Join(cacheRoot, name))
			}
			writeField(&b, 0, labelWidth, label, fmt.Sprintf("%-*s  %s  %s", nameWidth, name, state, ui.Dim.Render(src.URL)), width)
			label = ""
		}
	}

	if len(reports) > 0 {
		b.WriteString("\n" + harnessTable(reports) + "\n")
	}
	for _, report := range reports {
		if !report.Detected && report.Error == "" {
			continue
		}
		b.WriteString("\n")
		writeHarnessBlock(&b, report, width)
	}

	if len(problems) > 0 {
		b.WriteString("\n" + ui.Yellow.Render("needs attention:") + " " + strings.Join(problems, ", ") + "\n")
	}
	return b.String()
}

// reportProblems names what keeps a sync from being clean, in report order.
func reportProblems(repoReport repoLinkReport, reports []agentReport) []string {
	var problems []string
	if repoReport.State != stateSynced {
		problems = append(problems, "~/.agents link")
	}
	for _, report := range reports {
		switch {
		case report.Error != "":
			problems = append(problems, report.Name+" (config unreadable)")
		case report.Detected && (!report.Synced || len(report.Conflicts) > 0):
			problems = append(problems, report.Name)
		}
	}
	return problems
}

func repoLine(repoReport repoLinkReport) string {
	if repoReport.State == stateSynced {
		return fmt.Sprintf("%s ~/.agents %s %s", ui.Mark("ok"), ui.Dim.Render("->"), repoReport.ExpectedTarget)
	}
	detail := fmt.Sprintf("expected %s", repoReport.ExpectedTarget)
	if repoReport.ActualTarget != "" {
		detail += fmt.Sprintf(", actual %s", repoReport.ActualTarget)
	}
	return fmt.Sprintf("%s ~/.agents %s (%s)", ui.Mark("fail"), repoReport.State, detail)
}

// surfaceActions is what one sync run did to one surface of a harness.
type surfaceActions struct {
	surface             string
	add, update, remove []string
}

func actionsBySurface(r agentReport) []surfaceActions {
	return []surfaceActions{
		{"skills", r.Adds, r.Updates, r.Removes},
		{"agents", r.AddsAgent, r.UpdatesAgent, r.RemovesAgent},
		{"mcp", r.AddsMCP, r.UpdatesMCP, nil},
		{"hooks", r.AddsHook, r.UpdatesHook, nil},
		{"packages", nil, r.UpdatesPackage, r.RemovesPackage},
		{"plugins", r.AddsPlugin, nil, r.RemovesPlugin},
	}
}

// actionPhrase counts a harness's changes, e.g. "+2 -1 skills, ~1 packages".
func actionPhrase(r agentReport) string {
	var parts []string
	for _, s := range actionsBySurface(r) {
		var counts []string
		if n := len(s.add); n > 0 {
			counts = append(counts, ui.Green.Render(fmt.Sprintf("+%d", n)))
		}
		if n := len(s.update); n > 0 {
			counts = append(counts, ui.Yellow.Render(fmt.Sprintf("~%d", n)))
		}
		if n := len(s.remove); n > 0 {
			counts = append(counts, ui.Red.Render(fmt.Sprintf("-%d", n)))
		}
		if len(counts) > 0 {
			parts = append(parts, strings.Join(counts, " ")+" "+s.surface)
		}
	}
	return strings.Join(parts, ", ")
}

// changesLine groups harnesses that changed the same way, so a skill added to
// five harnesses reads as one entry instead of five.
func changesLine(reports []agentReport) string {
	var order []string
	groups := map[string][]string{}
	for _, report := range reports {
		phrase := actionPhrase(report)
		if phrase == "" {
			continue
		}
		if _, seen := groups[phrase]; !seen {
			order = append(order, phrase)
		}
		groups[phrase] = append(groups[phrase], report.Name)
	}
	if len(order) == 0 {
		return ui.Dim.Render("none")
	}
	parts := make([]string, 0, len(order))
	for _, phrase := range order {
		parts = append(parts, phrase+" "+ui.Dim.Render("("+strings.Join(groups[phrase], ", ")+")"))
	}
	return strings.Join(parts, " · ")
}

// harnessTable is the at-a-glance view: state and managed counts per harness.
func harnessTable(reports []agentReport) string {
	count := func(items []string) string {
		if len(items) == 0 {
			return ui.Dim.Render("-")
		}
		return fmt.Sprint(len(items))
	}
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(ui.Dim).
		Headers("harness", "state", "skills", "agents", "mcp", "hooks", "plugins", "packages", "external").
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return s.Inherit(ui.Bold)
			}
			return s
		})
	for _, r := range reports {
		switch {
		case r.Error != "":
			t.Row(r.Name, ui.Mark("fail")+" "+ui.Red.Render("error"), "", "", "", "", "", "", "")
		case !r.Detected:
			t.Row(ui.Dim.Render(r.Name), ui.Dim.Render("not detected"), "", "", "", "", "", "", "")
		default:
			state := ui.Mark("ok") + " synced"
			if !r.Synced || len(r.Conflicts) > 0 {
				state = ui.Mark("fail") + " " + ui.Yellow.Render("drifted")
			}
			t.Row(r.Name, state, count(r.Managed), count(r.ManagedAgent), count(r.ManagedMCP), count(r.ManagedHook), count(r.ManagedPlugin), count(r.ManagedPackage), count(r.External))
		}
	}
	return t.String()
}

// writeHarnessBlock lists everything sync knows about one harness: roots,
// every managed item, this run's changes, and each problem bucket.
func writeHarnessBlock(b *strings.Builder, r agentReport, width int) {
	const indent, labelWidth = 2, 13
	field := func(label, value string) { writeField(b, indent, labelWidth, label, value, width) }
	list := func(label string, items []string) {
		if len(items) > 0 {
			field(fmt.Sprintf("%s (%d)", label, len(items)), strings.Join(items, ", "))
		}
	}

	if r.Error != "" {
		fmt.Fprintf(b, "%s  %s %s\n", ui.Bold.Render(r.Name), ui.Mark("fail"), ui.Red.Render("config unreadable"))
		field("error", r.Error)
		field("", ui.Dim.Render("left unchanged; fix the file and run tackroom sync again"))
		return
	}
	state := ui.Mark("ok") + " " + ui.Green.Render("synced")
	if !r.Synced {
		state = ui.Mark("fail") + " " + ui.Yellow.Render("drifted")
	}
	fmt.Fprintf(b, "%s  %s\n", ui.Bold.Render(r.Name), state)

	if r.RootPath != "" {
		if r.RootState == stateSynced {
			field("root doc", fmt.Sprintf("%s %s %s", ui.Mark("ok"), ui.Dim.Render("->"), r.RootExpected))
		} else {
			detail := fmt.Sprintf("expected %s", r.RootExpected)
			if r.RootActual != "" {
				detail += fmt.Sprintf(", actual %s", r.RootActual)
			}
			field("root doc", fmt.Sprintf("%s %s (%s)", ui.Mark("fail"), r.RootState, detail))
		}
	}
	field("skill root", ui.Dim.Render(r.SkillRoot))
	if r.AgentRoot != "" {
		field("agent root", ui.Dim.Render(r.AgentRoot))
	}
	if h := harnessFor(r.Name); h != nil && h.IntegrationNote != "" {
		field("integration", ui.Dim.Render(h.IntegrationNote))
	}

	if len(r.Managed) == 0 {
		field("skills (0)", ui.Dim.Render("-"))
	}
	list("skills", r.Managed)
	list("agents", r.ManagedAgent)
	list("mcp", r.ManagedMCP)
	list("hooks", r.ManagedHook)
	list("packages", r.ManagedPackage)
	list("plugins", r.ManagedPlugin)
	list("plugins off", r.DisabledPlugin)
	list("external", r.External)

	for _, verb := range []struct {
		label string
		style lipgloss.Style
		pick  func(surfaceActions) []string
	}{
		{"added", ui.Green, func(s surfaceActions) []string { return s.add }},
		{"updated", ui.Yellow, func(s surfaceActions) []string { return s.update }},
		{"removed", ui.Red, func(s surfaceActions) []string { return s.remove }},
	} {
		var parts []string
		for _, s := range actionsBySurface(r) {
			if items := verb.pick(s); len(items) > 0 {
				parts = append(parts, s.surface+": "+strings.Join(items, ", "))
			}
		}
		if len(parts) > 0 {
			field(verb.style.Render(verb.label), strings.Join(parts, " · "))
		}
	}

	for _, bucket := range driftBuckets(r) {
		// A package removal is this run's action, already listed above.
		if len(bucket.items) == 0 || bucket.label == "packages removed" {
			continue
		}
		field(ui.Red.Render(fmt.Sprintf("%s (%d)", bucket.label, len(bucket.items))), strings.Join(bucket.items, ", "))
	}
}

// writeField writes one "label  value" row. The value wraps to width and its
// continuation lines align under the first, so long lists stay a column.
func writeField(b *strings.Builder, indent, labelWidth int, label, value string, width int) {
	valueWidth := width - indent - labelWidth - 2
	if valueWidth < 30 {
		valueWidth = 30
	}
	pad := strings.Repeat(" ", indent)
	// ansi.Wrap always breaks after "-", which would split names such as
	// "remote-access"; wrap with a non-breaking stand-in so only spaces break.
	const nbHyphen = "\u2011"
	wrapped := lipgloss.Wrap(strings.ReplaceAll(value, "-", nbHyphen), valueWidth, " ")
	for i, line := range strings.Split(strings.ReplaceAll(wrapped, nbHyphen, "-"), "\n") {
		cell := ""
		if i == 0 {
			cell = label
		}
		if gap := labelWidth - lipgloss.Width(cell); gap > 0 {
			cell += strings.Repeat(" ", gap)
		}
		fmt.Fprintf(b, "%s%s  %s\n", pad, cell, strings.TrimRight(line, " "))
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
	sort.Strings(report.ManagedPlugin)
	sort.Strings(report.MissingPlugin)
	sort.Strings(report.DisabledPlugin)
	sort.Strings(report.StalePlugin)
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
			current[i].AddsPlugin = append([]string{}, original.AddsPlugin...)
			current[i].RemovesPlugin = append([]string{}, original.RemovesPlugin...)
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

// printStatusReport renders a human-scannable `tackroom status`: overall health
// first, then a compact per-harness block that leads with sync state and shows
// only actionable drift. The identical multi-harness managed lists collapse to
// counts; --verbose (verbose=true) restores the full lists and native roots.
func printStatusReport(repoRoot string, repoReport repoLinkReport, reports []agentReport, home string, cfg config, verbose bool) {
	var b strings.Builder
	defer func() { _ = ui.Fprint(os.Stdout, b.String()) }()
	fmt.Fprintln(&b, ui.Bold.Render("tackroom status"))
	fmt.Fprintln(&b)

	if repoReport.State == stateSynced {
		fmt.Fprintf(&b, "repo   %s ~/.agents %s %s\n", ui.Mark("ok"), ui.Dim.Render("->"), repoReport.ExpectedTarget)
	} else {
		detail := fmt.Sprintf("expected %s", repoReport.ExpectedTarget)
		if repoReport.ActualTarget != "" {
			detail += fmt.Sprintf(", actual %s", repoReport.ActualTarget)
		}
		fmt.Fprintf(&b, "repo   %s ~/.agents %s (%s)\n", ui.Mark("fail"), repoReport.State, detail)
	}

	if len(cfg.ExternalSkills) > 0 {
		cacheRoot := externalCacheDir(home)
		fmt.Fprintln(&b, ui.Dim.Render("sources"))
		for _, src := range cfg.ExternalSkills {
			name := repoName(src.URL)
			state := ui.Mark("fail") + " not cloned"
			if hasDir(filepath.Join(cacheRoot, name, ".git")) {
				state = fmt.Sprintf("%s %s", ui.Mark("ok"), externalSkillCommit(filepath.Join(cacheRoot, name)))
			}
			fmt.Fprintf(&b, "  %s  %s  %s\n", state, name, ui.Dim.Render(src.URL))
		}
	}

	var drifted, failed []string
	for _, report := range reports {
		fmt.Fprintln(&b)
		writeHarnessStatus(&b, report, repoRoot, home, cfg, verbose)
		switch {
		case report.Error != "":
			failed = append(failed, report.Name)
		case report.Detected && !report.Synced:
			drifted = append(drifted, report.Name)
		}
	}

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, ui.Dim.Render("checks"))
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
		fmt.Fprintf(&b, "  %s %-*s  %s\n", ui.Mark(checkMarkKind(chk.status)), width, chk.name, ui.Dim.Render(chk.detail))
	}

	fmt.Fprintln(&b)
	if len(failed) > 0 {
		fmt.Fprintf(&b, "%s %s (config could not be read; fix it, then run %s)\n", ui.Red.Render("failed:"), strings.Join(failed, ", "), ui.Bold.Render("tackroom sync"))
	}
	if len(drifted) == 0 && len(failed) == 0 && repoReport.State == stateSynced {
		fmt.Fprintln(&b, ui.Green.Render("Everything is synced."))
		return
	}
	if len(drifted) == 0 && repoReport.State == stateSynced {
		return
	}
	if repoReport.State != stateSynced {
		fmt.Fprintf(&b, "%s ~/.agents repo link is not synced.\n", ui.Yellow.Render("drift:"))
	}
	if len(drifted) > 0 {
		fmt.Fprintf(&b, "%s %s\n", ui.Yellow.Render("drifted:"), strings.Join(drifted, ", "))
	}
	fmt.Fprintf(&b, "run %s to reconcile.\n", ui.Bold.Render("tackroom sync"))
}

// checkMarkKind maps a checkResult status to a ui.Mark kind.
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

func writeHarnessStatus(b *strings.Builder, report agentReport, repoRoot string, home string, cfg config, verbose bool) {
	if report.Error != "" {
		fmt.Fprintf(b, "%s   %s %s\n", ui.Bold.Render(report.Name), ui.Mark("fail"), ui.Red.Render("config unreadable"))
		fmt.Fprintf(b, "  error       %s\n", report.Error)
		return
	}
	if !report.Detected {
		fmt.Fprintf(b, "%s   %s\n", ui.Bold.Render(report.Name), ui.Dim.Render("not detected (binary not on PATH)"))
		return
	}
	if report.Synced {
		fmt.Fprintf(b, "%s   %s %s\n", ui.Bold.Render(report.Name), ui.Mark("ok"), ui.Green.Render("synced"))
	} else {
		fmt.Fprintf(b, "%s   %s %s\n", ui.Bold.Render(report.Name), ui.Mark("fail"), ui.Yellow.Render("drifted"))
	}

	if verbose {
		fmt.Fprintf(b, "  skill root  %s\n", ui.Dim.Render(report.SkillRoot))
		if report.AgentRoot != "" {
			fmt.Fprintf(b, "  agent root  %s\n", ui.Dim.Render(report.AgentRoot))
		}
	}
	if h := harnessFor(report.Name); h != nil && h.IntegrationNote != "" {
		fmt.Fprintf(b, "  integration %s\n", ui.Dim.Render(h.IntegrationNote))
	}
	if report.RootPath != "" {
		if report.RootState == stateSynced {
			fmt.Fprintf(b, "  root doc    %s synced %s %s\n", ui.Mark("ok"), ui.Dim.Render("->"), report.RootExpected)
		} else {
			detail := fmt.Sprintf("expected %s", report.RootExpected)
			if report.RootActual != "" {
				detail += fmt.Sprintf(", actual %s", report.RootActual)
			}
			fmt.Fprintf(b, "  root doc    %s %s (%s)\n", ui.Mark("fail"), report.RootState, detail)
		}
	}

	fmt.Fprintf(b, "  managed     %s\n", surfaceCounts(report))
	contextSkills := contextSkillsForReport(cfg, repoRoot, home, report)
	listingBytes := skillListingBytes(contextSkills)
	fmt.Fprintf(b, "  context     %s\n", ui.Dim.Render(fmt.Sprintf("%d skills, %d bytes, %s", len(contextSkills), listingBytes, formatTokenEstimate(estimateTokens(listingBytes)))))

	if verbose {
		writeVerboseSurfaceLists(b, report)
	}

	for _, bucket := range driftBuckets(report) {
		if len(bucket.items) == 0 {
			continue
		}
		fmt.Fprintf(b, "  %s %s: %s\n", ui.Mark("fail"), bucket.label, displayList(bucket.items))
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
	if n := len(r.ManagedPlugin); n > 0 {
		parts = append(parts, fmt.Sprintf("%d plugins", n))
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
		{"plugins missing", r.MissingPlugin},
		{"plugins stale", r.StalePlugin},
		{"conflicts", r.Conflicts},
	}
}

// writeVerboseSurfaceLists restores the full managed and external skill lists
// that the concise view collapses to counts.
func writeVerboseSurfaceLists(b *strings.Builder, report agentReport) {
	fmt.Fprintf(b, "  skills (%d):  %s\n", len(report.Managed), displayList(report.Managed))
	if len(report.ManagedAgent) > 0 {
		fmt.Fprintf(b, "  agents (%d):  %s\n", len(report.ManagedAgent), displayList(report.ManagedAgent))
	}
	if len(report.ManagedMCP) > 0 {
		fmt.Fprintf(b, "  mcp (%d):     %s\n", len(report.ManagedMCP), displayList(report.ManagedMCP))
	}
	if len(report.ManagedHook) > 0 {
		fmt.Fprintf(b, "  hooks (%d):   %s\n", len(report.ManagedHook), displayList(report.ManagedHook))
	}
	if len(report.ManagedPackage) > 0 {
		fmt.Fprintf(b, "  packages (%d): %s\n", len(report.ManagedPackage), displayList(report.ManagedPackage))
	}
	if len(report.ManagedPlugin) > 0 {
		fmt.Fprintf(b, "  plugins (%d):  %s\n", len(report.ManagedPlugin), displayList(report.ManagedPlugin))
	}
	if len(report.DisabledPlugin) > 0 {
		fmt.Fprintf(b, "  plugins off (%d): %s\n", len(report.DisabledPlugin), displayList(report.DisabledPlugin))
	}
	if len(report.External) > 0 {
		fmt.Fprintf(b, "  external (%d): %s\n", len(report.External), displayList(report.External))
	}
}
