package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/yourconscience/tackroom/internal/ui"
)

// printReport writes the full `tackroom sync` report to w: what the run changed,
// the repo link and sources, one table row per harness, then a block per
// detected harness listing every managed item and any problem. applied is false
// when sync stopped before changing anything, so the run's actions read as
// planned rather than done.
func printReport(w io.Writer, applied bool, repoReport repoLinkReport, reports []agentReport, home string, cfg config) {
	if w == io.Discard {
		return
	}
	_ = ui.Fprint(w, renderReport(applied, repoReport, reports, home, cfg, ui.Width(w)))
}

func renderReport(applied bool, repoReport repoLinkReport, reports []agentReport, home string, cfg config, width int) string {
	var b strings.Builder
	problems := reportProblems(repoReport, reports)
	state := ui.Mark("ok") + " " + ui.Green.Render("synced")
	if len(problems) > 0 {
		state = ui.Mark("fail") + " " + ui.Yellow.Render("needs attention")
	}
	fmt.Fprintf(&b, "%s  %s\n\n", ui.Bold.Render("tackroom sync"), state)

	changesLabel := "changes"
	if !applied {
		changesLabel = "planned"
	}
	head := []field{
		{label: changesLabel, value: changesLine(repoReport, reports)},
		{label: "repo", value: repoLine(repoReport)},
	}
	label := "sources"
	for _, line := range sourceLines(cfg, home) {
		head = append(head, field{label: label, value: line})
		label = ""
	}
	writeFields(&b, 0, labelWidthOf(head), head, width)

	if len(reports) > 0 {
		b.WriteString("\n" + harnessTable(reports, width) + "\n")
	}
	// One value column for every harness block, so blocks line up.
	blocks := make([][]field, len(reports))
	labelWidth := 0
	for i, report := range reports {
		if report.Detected && report.Error == "" {
			blocks[i] = harnessRows(report, applied)
			labelWidth = max(labelWidth, labelWidthOf(blocks[i]))
		}
	}
	for i, report := range reports {
		switch {
		case report.Error != "":
			b.WriteString("\n")
			fmt.Fprintf(&b, "%s  %s %s\n", ui.Bold.Render(report.Name), ui.Mark("fail"), ui.Red.Render("config unreadable"))
			writeFields(&b, 2, labelWidth, []field{
				{label: "error", value: report.Error},
				{value: ui.Dim.Render("left unchanged; fix the file and run tackroom sync again")},
			}, width)
		case report.Detected:
			fmt.Fprintf(&b, "\n%s  %s\n", ui.Bold.Render(report.Name), harnessState(report))
			writeFields(&b, 2, labelWidth, blocks[i], width)
		}
	}

	if len(problems) > 0 {
		b.WriteString("\n")
		writeFields(&b, 0, 0, []field{{label: ui.Yellow.Render("needs attention"), items: problems, sep: ","}}, width)
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
		case report.Detected && !report.Synced:
			problems = append(problems, report.Name)
		}
	}
	return problems
}

// repoLine, rootDocLine and sourceLines are shared by the sync and status reports.
func repoLine(repoReport repoLinkReport) string {
	if repoReport.State == stateSynced {
		line := fmt.Sprintf("%s ~/.agents %s %s", ui.Mark("ok"), ui.Dim.Render("->"), repoReport.ExpectedTarget)
		if repoReport.Linked {
			line += " " + ui.Dim.Render("(linked this run)")
		}
		return line
	}
	detail := fmt.Sprintf("expected %s", repoReport.ExpectedTarget)
	if repoReport.ActualTarget != "" {
		detail += fmt.Sprintf(", actual %s", repoReport.ActualTarget)
	}
	return fmt.Sprintf("%s ~/.agents %s (%s)", ui.Mark("fail"), repoReport.State, detail)
}

func rootDocLine(r agentReport) string {
	if r.RootState == stateSynced {
		line := fmt.Sprintf("%s synced %s %s", ui.Mark("ok"), ui.Dim.Render("->"), r.RootExpected)
		if r.RootLinked {
			line += " " + ui.Dim.Render("(linked this run)")
		}
		return line
	}
	detail := fmt.Sprintf("expected %s", r.RootExpected)
	if r.RootActual != "" {
		detail += fmt.Sprintf(", actual %s", r.RootActual)
	}
	return fmt.Sprintf("%s %s (%s)", ui.Mark("fail"), r.RootState, detail)
}

// sourceLines describes each external skill source, names aligned.
func sourceLines(cfg config, home string) []string {
	cacheRoot := externalCacheDir(home)
	nameWidth := 0
	for _, src := range cfg.ExternalSkills {
		nameWidth = max(nameWidth, len(repoName(src.URL)))
	}
	lines := make([]string, 0, len(cfg.ExternalSkills))
	for _, src := range cfg.ExternalSkills {
		name := repoName(src.URL)
		state := ui.Mark("fail") + " not cloned"
		if hasDir(filepath.Join(cacheRoot, name, ".git")) {
			state = ui.Mark("ok") + " " + externalSkillCommit(filepath.Join(cacheRoot, name))
		}
		lines = append(lines, fmt.Sprintf("%-*s  %s  %s", nameWidth, name, state, ui.Dim.Render(src.URL)))
	}
	return lines
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
// The key names the changed items, so only identical changes share a phrase.
func actionPhrase(r agentReport) (phrase, key string) {
	var parts, keys []string
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
			keys = append(keys, strings.Join([]string{s.surface, sortedJoin(s.add), sortedJoin(s.update), sortedJoin(s.remove)}, "\x00"))
		}
	}
	if r.RootLinked {
		parts = append(parts, "root doc linked")
		keys = append(keys, "root")
	}
	return strings.Join(parts, ", "), strings.Join(keys, "\x00\x00")
}

func sortedJoin(items []string) string {
	sorted := slices.Clone(items)
	slices.Sort(sorted)
	return strings.Join(sorted, "\x01")
}

// changesLine summarizes the run, merging harnesses that made exactly the same
// changes, so a skill added to five harnesses reads as one entry, not five.
func changesLine(repoReport repoLinkReport, reports []agentReport) string {
	var groups []string
	if repoReport.Linked {
		groups = append(groups, "~/.agents linked")
	}
	var order []string
	phrases := map[string]string{}
	names := map[string][]string{}
	for _, report := range reports {
		phrase, key := actionPhrase(report)
		if phrase == "" {
			continue
		}
		if _, seen := names[key]; !seen {
			order = append(order, key)
			phrases[key] = phrase
		}
		names[key] = append(names[key], report.Name)
	}
	for _, key := range order {
		groups = append(groups, phrases[key]+" "+ui.Dim.Render("("+strings.Join(names[key], ", ")+")"))
	}
	if len(groups) == 0 {
		return ui.Dim.Render("none")
	}
	return strings.Join(groups, " · ")
}

// harnessTable is the at-a-glance view: state and managed counts per harness.
// Where the table does not fit width, it falls back to one line per harness.
func harnessTable(reports []agentReport, width int) string {
	count := func(items []string) string {
		if len(items) == 0 {
			return ui.Dim.Render("-")
		}
		return fmt.Sprint(len(items))
	}
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(ui.Dim).
		Headers("harness", "state", "skills", "agents", "mcp", "hooks", "plugins", "packages").
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return s.Inherit(ui.Bold)
			}
			return s
		})
	var lines []field
	for _, r := range reports {
		state := harnessState(r)
		switch {
		case r.Error != "", !r.Detected:
			t.Row(r.Name, state, "", "", "", "", "", "")
			lines = append(lines, field{label: r.Name, value: state})
		default:
			t.Row(r.Name, state, count(r.Managed), count(r.ManagedAgent), count(r.ManagedMCP), count(r.ManagedHook), count(r.ManagedPlugin), count(r.ManagedPackage))
			lines = append(lines, field{label: r.Name, value: state + "  " + surfaceCounts(r)})
		}
	}
	if out := t.String(); lipgloss.Width(out) <= width {
		return out
	}
	var b strings.Builder
	writeFields(&b, 0, 0, lines, width)
	return strings.TrimSuffix(b.String(), "\n")
}

// harnessState is the short sync state shown for a harness.
func harnessState(r agentReport) string {
	switch {
	case r.Error != "":
		return ui.Mark("fail") + " " + ui.Red.Render("error")
	case !r.Detected:
		return ui.Dim.Render("not detected")
	case !r.Synced:
		return ui.Mark("fail") + " " + ui.Yellow.Render("drifted")
	default:
		return ui.Mark("ok") + " " + ui.Green.Render("synced")
	}
}

// harnessRows lists everything sync knows about one detected harness: roots,
// every managed item, this run's changes, and each problem bucket.
func harnessRows(r agentReport, applied bool) []field {
	var rows []field
	if r.RootPath != "" {
		rows = append(rows, field{label: "root doc", value: rootDocLine(r)})
	}
	rows = append(rows, field{label: "skill root", value: ui.Dim.Render(r.SkillRoot)})
	if r.AgentRoot != "" {
		rows = append(rows, field{label: "agent root", value: ui.Dim.Render(r.AgentRoot)})
	}
	if h := harnessFor(r.Name); h != nil && h.IntegrationNote != "" {
		rows = append(rows, field{label: "integration", value: ui.Dim.Render(h.IntegrationNote)})
	}

	rows = append(rows, managedRows(r)...)

	verbs := []struct {
		done, planned string
		style         lipgloss.Style
		pick          func(surfaceActions) []string
	}{
		{"added", "to add", ui.Green, func(s surfaceActions) []string { return s.add }},
		{"updated", "to update", ui.Yellow, func(s surfaceActions) []string { return s.update }},
		{"removed", "to remove", ui.Red, func(s surfaceActions) []string { return s.remove }},
	}
	for _, verb := range verbs {
		label := verb.done
		if !applied {
			label = verb.planned
		}
		label = verb.style.Render(label)
		for _, s := range actionsBySurface(r) {
			items := verb.pick(s)
			if len(items) == 0 {
				continue
			}
			prefixed := append([]string{s.surface + ": " + items[0]}, items[1:]...)
			rows = append(rows, field{label: label, items: prefixed, sep: ","})
			label = ""
		}
	}

	for _, bucket := range driftBuckets(r) {
		// Actions are listed above. Before an apply, drift is exactly what the
		// planned actions fix, so only what sync cannot fix is listed.
		if len(bucket.items) == 0 || bucket.kind == bucketAction || (!applied && bucket.kind == bucketDrift) {
			continue
		}
		style := ui.Red
		if bucket.kind == bucketNotice {
			style = ui.Yellow
		}
		rows = append(rows, field{label: style.Render(fmt.Sprintf("%s (%d)", bucket.label, len(bucket.items))), items: bucket.items, sep: ","})
	}
	return rows
}

// managedRows lists every managed item of a harness, one row per surface.
func managedRows(r agentReport) []field {
	var rows []field
	list := func(label string, items []string) {
		if len(items) > 0 {
			rows = append(rows, field{label: fmt.Sprintf("%s (%d)", label, len(items)), items: items, sep: ","})
		}
	}
	if len(r.Managed) == 0 {
		rows = append(rows, field{label: "skills (0)", value: ui.Dim.Render("-")})
	}
	list("skills", r.Managed)
	list("agents", r.ManagedAgent)
	list("mcp", r.ManagedMCP)
	list("hooks", r.ManagedHook)
	list("packages", r.ManagedPackage)
	list("plugins", r.ManagedPlugin)
	list("plugins off", r.DisabledPlugin)
	list("external", r.External)
	return rows
}

// field is one "label  value" row of a report. A value wraps only at spaces,
// so paths and URLs stay whole. A field with items is a list: it wraps only
// between items, each followed by sep except the last.
type field struct {
	label string
	value string
	items []string
	sep   string
}

func labelWidthOf(rows []field) int {
	width := 0
	for _, row := range rows {
		width = max(width, lipgloss.Width(row.label))
	}
	return width
}

// writeFields writes rows with their values in one column after labelWidth,
// so continuation lines align under the first. When width leaves too little
// room beside the labels, values move under their label.
func writeFields(b *strings.Builder, indent, labelWidth int, rows []field, width int) {
	labelWidth = max(labelWidth, labelWidthOf(rows))
	valueIndent := indent + labelWidth + 2
	stacked := width-valueIndent < 30
	if stacked {
		valueIndent = indent + 2
	}
	valueWidth := max(width-valueIndent, 10)
	pad := strings.Repeat(" ", indent)
	for _, row := range rows {
		var lines []string
		if row.items != nil {
			lines = packItems(row.items, row.sep, valueWidth)
		} else {
			lines = packItems(strings.Split(row.value, " "), "", valueWidth)
		}
		if stacked {
			if row.label != "" {
				fmt.Fprintf(b, "%s%s\n", pad, row.label)
			}
			for _, line := range lines {
				fmt.Fprintf(b, "%s%s\n", strings.Repeat(" ", valueIndent), strings.TrimRight(line, " "))
			}
			continue
		}
		for i, line := range lines {
			cell := ""
			if i == 0 {
				cell = row.label
			}
			cell += strings.Repeat(" ", labelWidth-lipgloss.Width(cell))
			fmt.Fprintf(b, "%s%s  %s\n", pad, cell, strings.TrimRight(line, " "))
		}
	}
}

// packItems lays items out across lines of at most width columns, breaking
// only between items. An item wider than width keeps a line of its own.
func packItems(items []string, sep string, width int) []string {
	var lines []string
	line := ""
	for i, item := range items {
		if i < len(items)-1 {
			item += sep
		}
		switch {
		case line == "":
			line = item
		case lipgloss.Width(line)+1+lipgloss.Width(item) <= width:
			line += " " + item
		default:
			lines = append(lines, line)
			line = item
		}
	}
	return append(lines, line)
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
			current[i].RootLinked = original.RootPath != "" && original.RootState != stateSynced && current[i].RootState == stateSynced
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
// Each section prints as soon as it is ready, so slow checks do not hold back
// the harness blocks.
func printStatusReport(w io.Writer, repoRoot string, repoReport repoLinkReport, reports []agentReport, home string, cfg config, verbose bool) {
	var b strings.Builder
	out, width := ui.Writer(w), ui.Width(w)
	flush := func() {
		_, _ = io.WriteString(out, b.String())
		b.Reset()
	}
	defer flush()

	fmt.Fprintln(&b, ui.Bold.Render("tackroom status"))
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "repo   %s\n", repoLine(repoReport))
	if lines := sourceLines(cfg, home); len(lines) > 0 {
		fmt.Fprintln(&b, ui.Dim.Render("sources"))
		for _, line := range lines {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	flush()

	var drifted, failed []string
	for _, report := range reports {
		fmt.Fprintln(&b)
		writeHarnessStatus(&b, report, repoRoot, home, cfg, verbose, width)
		flush()
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
	nameWidth := 0
	for _, chk := range checks {
		nameWidth = max(nameWidth, len(chk.name))
	}
	for _, chk := range checks {
		fmt.Fprintf(&b, "  %s %-*s  %s\n", ui.Mark(checkMarkKind(chk.status)), nameWidth, chk.name, ui.Dim.Render(chk.detail))
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

func writeHarnessStatus(b *strings.Builder, report agentReport, repoRoot string, home string, cfg config, verbose bool, width int) {
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
		fmt.Fprintf(b, "  root doc    %s\n", rootDocLine(report))
	}

	fmt.Fprintf(b, "  managed     %s\n", surfaceCounts(report))
	contextSkills := contextSkillsForReport(cfg, repoRoot, home, report)
	listingBytes := skillListingBytes(contextSkills)
	fmt.Fprintf(b, "  context     %s\n", ui.Dim.Render(fmt.Sprintf("%d skills, %d bytes, %s", len(contextSkills), listingBytes, formatTokenEstimate(estimateTokens(listingBytes)))))

	if verbose {
		writeFields(b, 2, 0, managedRows(report), width)
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
	kind  bucketKind
}

// bucketKind says how a sync run relates to a drift bucket.
type bucketKind int

const (
	bucketDrift    bucketKind = iota // sync fixes it
	bucketAction                     // a sync action list, not drift
	bucketNotice                     // informational; sync does not change it
	bucketConflict                   // blocks sync until resolved by hand
)

// driftBuckets lists the actionable, non-synced surfaces for a harness in a
// stable order so the concise status view can render only what needs a sync.
func driftBuckets(r agentReport) []driftBucket {
	return []driftBucket{
		{"skills drifted", r.Drifted, bucketDrift},
		{"skills missing", r.Missing, bucketDrift},
		{"skills stale", r.StaleManaged, bucketDrift},
		{"agents drifted", r.DriftedAgent, bucketDrift},
		{"agents missing", r.MissingAgent, bucketDrift},
		{"mcp drifted", r.DriftedMCP, bucketDrift},
		{"mcp missing", r.MissingMCP, bucketDrift},
		{"hooks drifted", r.DriftedHook, bucketDrift},
		{"hooks missing", r.MissingHook, bucketDrift},
		{"hooks unsupported", r.UnsupportedHook, bucketNotice},
		{"packages drifted", r.DriftedPackage, bucketDrift},
		{"packages removed", r.RemovesPackage, bucketAction},
		{"plugins missing", r.MissingPlugin, bucketDrift},
		{"plugins stale", r.StalePlugin, bucketDrift},
		{"conflicts", r.Conflicts, bucketConflict},
	}
}
