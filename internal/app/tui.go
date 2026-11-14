package app

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/yourconscience/tackroom/internal/ui"
)

// `tackroom tui` is one read-only terminal view of what status, doctor,
// cron collect and the lock already report. It reuses their data functions
// and writes nothing. See docs/tui.md.

var tuiTabNames = []string{"Agents", "Lock", "Crons", "Doctor"}

// tuiMinColumn is the narrowest a shrinking column gets before the line is cut.
const tuiMinColumn = 8

func runTUICommand(args []string) error {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var opts runOptions
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to tackroom YAML config")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("tui does not accept positional arguments")
	}
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return errors.New("tackroom tui needs a terminal; use tackroom status")
	}
	return runTUI(opts)
}

func runTUI(opts runOptions) error {
	var programOpts []tea.ProgramOption
	// ui.Writer already decides what color os.Stdout can show, NO_COLOR included.
	if cw, ok := ui.Writer(os.Stdout).(*colorprofile.Writer); ok {
		programOpts = append(programOpts, tea.WithColorProfile(cw.Profile))
	}
	// Config warnings the loaders print to stderr would land in the middle of
	// the screen, so the view runs with stderr pointed at /dev/null.
	if devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
		stderr := os.Stderr
		os.Stderr = devNull
		defer func() {
			os.Stderr = stderr
			devNull.Close()
		}()
	}
	if _, err := tea.NewProgram(newTUIModel(opts), programOpts...).Run(); err != nil {
		return fmt.Errorf("terminal view: %w", err)
	}
	return nil
}

// tuiTable is one tab's data, already rendered to styled cells.
type tuiTable struct {
	headers []string
	rows    [][]string
	flex    []int  // columns that give up width first, in order, when the line is too wide
	summary string // pinned under the rows
	empty   string // shown instead of the table when there are no rows
}

type tuiPane struct {
	loading bool
	err     error
	table   tuiTable
	cursor  int
	top     int
}

type tuiLoadedMsg struct {
	gen   int
	tab   int
	table tuiTable
	err   error
}

type tuiModel struct {
	opts   runOptions
	tab    int
	panes  [4]tuiPane
	width  int
	height int
	help   bool
	gen    int
}

func newTUIModel(opts runOptions) tuiModel {
	m := tuiModel{opts: opts, gen: 1}
	for i := range m.panes {
		m.panes[i].loading = true
	}
	return m
}

func (m tuiModel) Init() tea.Cmd { return m.loadAll() }

// loadAll starts one command per tab, so a slow tab never holds up the others.
func (m tuiModel) loadAll() tea.Cmd {
	loaders := [...]func(runOptions) (tuiTable, error){tuiAgentsTable, tuiLockTable, tuiCronsTable, tuiDoctorTable}
	cmds := make([]tea.Cmd, len(loaders))
	for i, load := range loaders {
		gen, tab, opts := m.gen, i, m.opts
		cmds[i] = func() tea.Msg {
			table, err := load(opts)
			return tuiLoadedMsg{gen: gen, tab: tab, table: table, err: err}
		}
	}
	return tea.Batch(cmds...)
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		for i := range m.panes {
			m.fit(i)
		}
	case tuiLoadedMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.panes[msg.tab] = tuiPane{table: msg.table, err: msg.err}
		m.fit(msg.tab)
	case tea.KeyPressMsg:
		switch key := msg.String(); key {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.tab = (m.tab + 1) % len(tuiTabNames)
		case "shift+tab":
			m.tab = (m.tab + len(tuiTabNames) - 1) % len(tuiTabNames)
		case "1", "2", "3", "4":
			m.tab = int(key[0] - '1')
		case "j", "down":
			m.panes[m.tab].cursor++
			m.fit(m.tab)
		case "k", "up":
			m.panes[m.tab].cursor--
			m.fit(m.tab)
		case "r":
			m.gen++
			for i := range m.panes {
				m.panes[i] = tuiPane{loading: true}
			}
			return m, m.loadAll()
		case "?":
			m.help = !m.help
		}
	}
	return m, nil
}

// bodyHeight is what is left under the tab bar and rail and above the footer.
func (m tuiModel) bodyHeight() int { return max(m.height-3, 0) }

// visibleRows is how many data rows fit in a pane's body.
func (m tuiModel) visibleRows(p tuiPane) int {
	n := m.bodyHeight() - 1 // column headers
	if p.table.summary != "" {
		n--
	}
	return max(n, 1)
}

// fit keeps a pane's cursor inside its rows and its window on the cursor.
func (m *tuiModel) fit(tab int) {
	p := &m.panes[tab]
	p.cursor = max(min(p.cursor, len(p.table.rows)-1), 0)
	visible := m.visibleRows(*p)
	if p.cursor < p.top {
		p.top = p.cursor
	}
	if p.cursor >= p.top+visible {
		p.top = p.cursor - visible + 1
	}
	p.top = max(min(p.top, len(p.table.rows)-visible), 0)
}

func (m tuiModel) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m tuiModel) render() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	body := m.body()
	if len(body) > m.bodyHeight() {
		body = body[:m.bodyHeight()]
	}
	lines := append([]string{m.tabBar(), m.rail()}, body...)
	for len(lines) < m.height-1 {
		lines = append(lines, "")
	}
	lines = append(lines, m.footer())
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, m.width, "…")
	}
	return strings.Join(lines, "\n")
}

// tabBar draws the tab names as plates; the rail below carries them.
func (m tuiModel) tabBar() string {
	var b strings.Builder
	for i, name := range tuiTabNames {
		plate := fmt.Sprintf(" %d %s ", i+1, name)
		if i == m.tab {
			b.WriteString(ui.Bold.Render(plate))
		} else {
			b.WriteString(ui.Dim.Render(plate))
		}
	}
	return b.String()
}

// rail is the one horizontal rule under the tab bar. It is heavier under the
// current tab.
func (m tuiModel) rail() string {
	var b strings.Builder
	used := 0
	for i, name := range tuiTabNames {
		n := len(fmt.Sprintf(" %d %s ", i+1, name))
		if i == m.tab {
			b.WriteString(ui.Bold.Render(strings.Repeat("━", n)))
		} else {
			b.WriteString(ui.Dim.Render(strings.Repeat("─", n)))
		}
		used += n
	}
	if m.width > used {
		b.WriteString(ui.Dim.Render(strings.Repeat("─", m.width-used)))
	}
	return b.String()
}

func (m tuiModel) body() []string {
	p := m.panes[m.tab]
	switch {
	case p.loading:
		return []string{"  " + ui.Dim.Render("loading…")}
	case p.err != nil:
		// An error is a message, not a row: wrap it so its last words, which
		// usually say what to run, stay visible.
		var lines []string
		for i, text := range strings.Split(ansi.Wrap(oneLine(p.err.Error()), max(m.width-4, 1), ""), "\n") {
			prefix := "    "
			if i == 0 {
				prefix = "  " + ui.Mark("fail") + " "
			}
			lines = append(lines, prefix+ui.Red.Render(text))
		}
		return append(lines, "  "+ui.Dim.Render("press r to reload"))
	case len(p.table.rows) == 0:
		return []string{"  " + ui.Dim.Render(p.table.empty)}
	}
	t := p.table
	widths := t.widths(m.width - 2)
	lines := []string{"  " + ui.Dim.Render(tuiRow(t.headers, widths))}
	end := min(p.top+m.visibleRows(p), len(t.rows))
	for i := p.top; i < end; i++ {
		gutter := "  "
		if i == p.cursor {
			gutter = ui.Bold.Render("›") + " "
		}
		lines = append(lines, gutter+tuiRow(t.rows[i], widths))
	}
	if t.summary != "" {
		lines = append(lines, "  "+ui.Bold.Render(t.summary))
	}
	return lines
}

func (m tuiModel) footer() string {
	left := ui.Dim.Render("? keys")
	if m.help {
		left = ui.Dim.Render("tab 1-4 switch  j/k move  r reload  ? hide keys  q quit")
	}
	p := m.panes[m.tab]
	if p.loading || p.err != nil || len(p.table.rows) == 0 {
		return left
	}
	right := ui.Dim.Render(fmt.Sprintf("%d/%d", p.cursor+1, len(p.table.rows)))
	gap := m.width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 2 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// widths gives each column its natural width, then takes the excess over avail
// from the flex columns in order, never below tuiMinColumn.
func (t tuiTable) widths(avail int) []int {
	w := make([]int, len(t.headers))
	for i, h := range t.headers {
		w[i] = ansi.StringWidth(h)
	}
	total := 2 * (len(w) - 1)
	for _, row := range t.rows {
		for i, cell := range row {
			w[i] = max(w[i], ansi.StringWidth(cell))
		}
	}
	for _, n := range w {
		total += n
	}
	excess := total - avail
	for _, i := range t.flex {
		if excess <= 0 {
			break
		}
		cut := min(excess, max(w[i]-min(w[i], tuiMinColumn), 0))
		w[i] -= cut
		excess -= cut
	}
	return w
}

// tuiRow lays cells out in columns of the given widths, truncating with an
// ellipsis, with no trailing padding.
func tuiRow(cells []string, widths []int) string {
	var b strings.Builder
	for i, cell := range cells {
		if i > 0 {
			b.WriteString("  ")
		}
		cell = ansi.Truncate(cell, widths[i], "…")
		b.WriteString(cell)
		if i < len(cells)-1 {
			b.WriteString(strings.Repeat(" ", widths[i]-ansi.StringWidth(cell)))
		}
	}
	return b.String()
}

// oneLine flattens whitespace so a message never breaks a row.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// tuiState is a status mark and its word in one color, as status prints them.
func tuiState(kind, word string) string {
	style := ui.Red
	switch kind {
	case "ok":
		style = ui.Green
	case "warn":
		style = ui.Yellow
	}
	return ui.Mark(kind) + " " + style.Render(word)
}

func tuiAgentsTable(opts runOptions) (tuiTable, error) {
	g, err := loadStatusGates(opts, false)
	if err != nil {
		return tuiTable{}, err
	}
	_, reports, err := inspectStatus(g)
	if err != nil {
		return tuiTable{}, err
	}
	t := tuiTable{
		headers: []string{"agent", "key", "state", "managed"},
		flex:    []int{3, 0},
	}
	for _, r := range reports {
		var state, managed string
		switch {
		case r.Error != "":
			state, managed = tuiState("fail", "config unreadable"), ui.Dim.Render(oneLine(r.Error))
		case !r.Detected:
			state = ui.Dim.Render("not detected")
		case r.Synced:
			state, managed = tuiState("ok", "synced"), surfaceCounts(r)
		default:
			state, managed = tuiState("warn", "drifted"), surfaceCounts(r)
		}
		t.rows = append(t.rows, []string{ui.Bold.Render(harnessDisplayName(r.Name)), ui.Dim.Render(r.Name), state, managed})
	}
	return t, nil
}

func tuiLockTable(opts runOptions) (tuiTable, error) {
	g, err := loadStatusGates(opts, false)
	if err != nil {
		return tuiTable{}, err
	}
	t := tuiTable{
		headers: []string{"name", "source", "commit", "state", "audit"},
		flex:    []int{1, 3, 0},
		empty:   "no external skills: add one with tackroom skill sources add",
	}
	now := timeNow()
	for _, row := range lockRows(g) {
		source, commit, audit := ui.Dim.Render(strings.TrimPrefix(row.Source, "https://")), ui.Dim.Render("-"), ui.Dim.Render("-")
		if row.Source == "" {
			source = ui.Dim.Render(oneLine(row.Reason)) // a held package
		}
		if row.Commit != "" {
			commit = row.Commit
			if !row.Pinned {
				commit = ui.Dim.Render(commit)
			}
		}
		if row.Audited {
			audit = tuiAuditCell(row.Findings, row.Critical)
		}
		state := tuiState("ok", "pinned")
		if row.State == "held" || row.State == "blocked" {
			state = tuiHeldState(heldItem{Kind: row.State, Until: row.Until}, now)
		} else if row.State != "pinned" {
			state = tuiState("warn", row.State)
		}
		t.rows = append(t.rows, []string{ui.Bold.Render(row.Name), source, commit, state, audit})
	}
	return t, nil
}

// tuiHeldState words a held or blocked item. A hold's age is how much of the
// freshness window has passed: the window minus the time left until it ends.
func tuiHeldState(h heldItem, now time.Time) string {
	if h.Kind == "blocked" {
		return tuiState("fail", "blocked")
	}
	until, err := time.Parse(time.RFC3339, h.Until)
	if err != nil {
		return tuiState("warn", "held, age unknown")
	}
	age := min(max(defaultPackageAgeMinimum-until.Sub(now), 0), defaultPackageAgeMinimum)
	return tuiState("warn", fmt.Sprintf("held %dh of %dh", int(age.Hours()), int(defaultPackageAgeMinimum.Hours())))
}

// tuiAuditCell words the findings `doctor audit` lists for an item that the
// lock does not accept.
func tuiAuditCell(open, critical int) string {
	switch {
	case critical > 0:
		return tuiState("fail", fmt.Sprintf("%d critical", critical))
	case open == 1:
		return tuiState("warn", "1 finding")
	case open > 1:
		return tuiState("warn", fmt.Sprintf("%d findings", open))
	}
	return tuiState("ok", "pass")
}

func tuiCronsTable(opts runOptions) (tuiTable, error) {
	result, err := collectCrons(opts.ConfigPath, nil)
	if err != nil {
		return tuiTable{}, err
	}
	t := tuiTable{
		headers: []string{"host", "source", "id", "schedule", "state", "last run", "result"},
		flex:    []int{3, 1, 2},
		empty:   "no scheduled jobs found: declare crons.jobs, then run tackroom cron apply",
	}
	for _, j := range result.Jobs {
		var state string
		switch j.Health {
		case "ok":
			state = tuiState("ok", j.State)
		case "warn":
			state = tuiState("warn", j.State)
		case "fail":
			state = tuiState("fail", j.State)
		case "paused":
			state = ui.Dim.Render(j.State)
		default:
			state = j.State
		}
		res, lastRun := cronResultAndLastRun(j)
		t.rows = append(t.rows, []string{j.Host, j.Source, j.ID, oneLine(j.Schedule), state, lastRun, res})
	}
	return t, nil
}

func tuiDoctorTable(opts runOptions) (tuiTable, error) {
	repoRoot, home, cfg, selected, err := loadContext(opts)
	if err != nil {
		return tuiTable{}, err
	}
	results := runDoctorChecks(opts.SkipPackageAge, repoRoot, home, cfg, selected)
	t := tuiTable{
		headers: []string{"check", "detail"},
		flex:    []int{1, 0},
	}
	for _, r := range results {
		t.rows = append(t.rows, []string{ui.Mark(checkMarkKind(r.status)) + " " + r.name, ui.Dim.Render(oneLine(r.detail))})
	}
	t.summary = checkSummary(countChecks(results))
	return t, nil
}
