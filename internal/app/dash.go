package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/yourconscience/tackroom/internal/ui"
)

// `tackroom dash` is a live screen for a terminal pane: whether everything is
// in sync and which scheduled jobs fail, with run now, pause and resume for a
// launchd job and a sync. The workspace commands (rex, tmux, zellij, tern)
// open it above the shell. See docs/terminals.md.

const (
	dashStatusEvery = 30 * time.Second
	dashCronsEvery  = 5 * time.Minute
	// dashKeepOutput is how much of what the collectors print is kept, to be
	// shown when the screen fails.
	dashKeepOutput = 16 << 10
)

var dashSelectedStyle = lipgloss.NewStyle().Reverse(true)

func runDashCommand(args []string) error {
	fs := flag.NewFlagSet("dash", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "Path to tackroom YAML config")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: tackroom dash [--config PATH]")
	}
	if !term.IsTerminal(os.Stdout.Fd()) {
		return errors.New("tackroom dash needs a terminal; use tackroom status and tackroom cron collect --table")
	}

	screen := os.Stdout
	programOpts := []tea.ProgramOption{tea.WithOutput(screen)}
	// ui.Writer already decides what color the terminal can show, NO_COLOR included.
	if cw, ok := ui.Writer(screen).(*colorprofile.Writer); ok {
		programOpts = append(programOpts, tea.WithColorProfile(cw.Profile))
	}
	// The collectors and sync print config warnings and git output on stderr
	// and a README line on stdout, which would land in the middle of the
	// screen. While it is up both go to a pipe that keeps the tail; the tail is
	// printed only when the screen fails, so a crash stays readable.
	stop, err := dashCaptureOutput()
	if err != nil {
		return err
	}
	_, runErr := tea.NewProgram(newDashModel(*configPath), programOpts...).Run()
	tail := stop()
	if runErr != nil {
		_, _ = os.Stderr.Write(tail)
		return fmt.Errorf("tackroom dash: %w", runErr)
	}
	return nil
}

// dashCaptureOutput points os.Stdout and os.Stderr at a pipe and keeps the
// last dashKeepOutput bytes written to it. stop puts them back and returns
// what was kept.
func dashCaptureOutput() (stop func() []byte, err error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w
	var mu sync.Mutex
	var tail []byte
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			mu.Lock()
			tail = append(tail, buf[:n]...)
			if len(tail) > dashKeepOutput {
				tail = tail[len(tail)-dashKeepOutput:]
			}
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return func() []byte {
		os.Stdout, os.Stderr = stdout, stderr
		w.Close()
		// A command a quit left running may still hold the pipe; do not wait
		// for it.
		select {
		case <-done:
		case <-time.After(200 * time.Millisecond):
		}
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(tail)
	}, nil
}

// dashStatus is what `tackroom status` settles, as the dash draws it.
type dashStatus struct {
	repo    repoLinkReport
	reports []agentReport
	gated   int         // items in cooldown or blocked
	lock    checkResult // the external skill lock check
	sources int
	pinned  int
}

// collectDashStatus is statusReport's data: no registry lookup, no lock write,
// and no plugin data directories created.
func collectDashStatus(configPath string) (dashStatus, error) {
	g, err := loadStatusGates(runOptions{ConfigPath: configPath}, false)
	if err != nil {
		return dashStatus{}, err
	}
	repo, reports, err := inspectStatus(g)
	if err != nil {
		return dashStatus{}, err
	}
	lockCfg := g.cfg
	if g.cfg.configuredSources != nil {
		lockCfg.ExternalSkills = g.cfg.configuredSources
	}
	lock := checkExternalSkillLock(g.repoRoot, lockCfg, g.home)
	lock.detail, _, _ = strings.Cut(lock.detail, "; see tackroom status")
	s := dashStatus{repo: repo, reports: reports, gated: len(gatedLines(g.gated)), lock: lock, sources: len(g.sources)}
	cacheRoot := externalCacheDir(g.home)
	for _, src := range g.sources {
		if _, issue := sourcePinIssue(g.lock, src, cacheRoot); issue == "" {
			s.pinned++
		}
	}
	return s, nil
}

type dashStatusMsg struct {
	gen    int
	status dashStatus
	err    error
}

type dashCronsMsg struct {
	gen    int
	result cronsScanResult
	err    error
}

// dashActionMsg is what one run, pause or resume did, and the job as a new
// scan of its host reports it: nil when that scan failed or no longer lists it.
type dashActionMsg struct {
	job   cronJob
	verb  string
	out   string
	err   error
	fresh *cronJob
}

type dashSyncMsg struct {
	result setupSyncResult
	err    error
}

type dashTickMsg struct{ crons bool }

// dashPending is a question in the message line and what y starts.
type dashPending struct {
	question string
	busy     string
	run      tea.Cmd
	sync     bool
}

type dashModel struct {
	configPath    string
	width, height int

	status     *dashStatus
	statusGen  int
	statusBusy bool
	crons      *cronsScanResult // jobs in dash order
	cronsGen   int
	cronsBusy  bool
	updated    time.Time // the last refresh that brought data

	cursor, top int
	selected    string // the selected job's key, kept across refreshes

	pending *dashPending
	busy    string // the action that runs
	syncing bool
	message string
	failed  bool // message is an error
}

func newDashModel(configPath string) dashModel {
	return dashModel{configPath: configPath, statusGen: 1, statusBusy: true, cronsGen: 1, cronsBusy: true}
}

func (m dashModel) Init() tea.Cmd {
	return tea.Batch(dashCollectStatus(m.configPath, m.statusGen), dashCollectCrons(m.configPath, m.cronsGen), dashTick(false), dashTick(true))
}

func dashCollectStatus(configPath string, gen int) tea.Cmd {
	return func() tea.Msg {
		s, err := collectDashStatus(configPath)
		return dashStatusMsg{gen: gen, status: s, err: err}
	}
}

func dashCollectCrons(configPath string, gen int) tea.Cmd {
	return func() tea.Msg {
		result, err := collectCrons(configPath, nil)
		return dashCronsMsg{gen: gen, result: result, err: err}
	}
}

func dashTick(crons bool) tea.Cmd {
	every := dashStatusEvery
	if crons {
		every = dashCronsEvery
	}
	return tea.Tick(every, func(time.Time) tea.Msg { return dashTickMsg{crons: crons} })
}

// refreshStatus starts a status collect; a collect already running is
// superseded.
func (m *dashModel) refreshStatus() tea.Cmd {
	m.statusGen++
	m.statusBusy = true
	return dashCollectStatus(m.configPath, m.statusGen)
}

func (m *dashModel) refreshCrons() tea.Cmd {
	m.cronsGen++
	m.cronsBusy = true
	return dashCollectCrons(m.configPath, m.cronsGen)
}

func (m *dashModel) say(text string, failed bool) {
	m.message, m.failed = text, failed
}

func (m dashModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.fit()
	case dashStatusMsg:
		if msg.gen != m.statusGen {
			return m, nil
		}
		m.statusBusy = false
		if msg.err != nil {
			m.say("status: "+firstLine(msg.err.Error()), true)
			return m, nil
		}
		m.status, m.updated = &msg.status, time.Now()
		m.fit()
	case dashCronsMsg:
		if msg.gen != m.cronsGen {
			return m, nil
		}
		m.cronsBusy = false
		if msg.err != nil {
			m.say("crons: "+firstLine(msg.err.Error()), true)
			return m, nil
		}
		m.updated = time.Now()
		m.setJobs(msg.result)
	case dashTickMsg:
		next := dashTick(msg.crons)
		switch {
		case msg.crons && !m.cronsBusy:
			return m, tea.Batch(m.refreshCrons(), next)
		case !msg.crons && !m.statusBusy && !m.syncing:
			return m, tea.Batch(m.refreshStatus(), next)
		}
		return m, next
	case dashActionMsg:
		m.busy = ""
		where := msg.job.ID + " on " + msg.job.Host
		if msg.err != nil {
			m.say(firstLine(msg.err.Error()), true)
		} else {
			done := map[string]string{"run": "started ", "pause": "paused ", "resume": "resumed "}[msg.verb] + where
			if msg.out != "" {
				done += ": " + firstLine(msg.out)
			}
			m.say(done, false)
		}
		if msg.fresh != nil && m.crons != nil {
			result := *m.crons
			result.Jobs = slices.Clone(result.Jobs)
			for i := range result.Jobs {
				if sameCronJob(result.Jobs[i], msg.job) {
					result.Jobs[i] = *msg.fresh
				}
			}
			m.setJobs(result)
		}
		// Collect every host again when the job's host could not be read,
		// and when a collect that began before the action is still running,
		// so its older record of the job does not come back.
		if msg.job.Source == "launchd" && (msg.fresh == nil || m.cronsBusy) {
			return m, m.refreshCrons()
		}
	case dashSyncMsg:
		m.busy, m.syncing = "", false
		if msg.err != nil {
			m.say("sync: "+firstLine(msg.err.Error()), true)
		} else {
			m.say("synced, changes: "+changesLine(msg.result.repo, msg.result.reports), false)
		}
		return m, m.refreshStatus()
	case tea.KeyPressMsg:
		return m.key(msg.String())
	}
	return m, nil
}

func (m dashModel) key(key string) (tea.Model, tea.Cmd) {
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	if p := m.pending; p != nil {
		m.pending = nil
		if key != "y" {
			m.say("cancelled", false)
			return m, nil
		}
		m.busy, m.syncing, m.message = p.busy, p.sync, ""
		return m, p.run
	}
	jobs := m.jobs()
	switch key {
	case "q":
		return m, tea.Quit
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "home", "g":
		m.move(-len(jobs))
	case "end", "G":
		m.move(len(jobs))
	case "r":
		var cmds []tea.Cmd
		if !m.statusBusy && !m.syncing {
			cmds = append(cmds, m.refreshStatus())
		}
		if !m.cronsBusy {
			cmds = append(cmds, m.refreshCrons())
		}
		return m, tea.Batch(cmds...)
	case "enter", "p":
		if m.busy != "" || len(jobs) == 0 {
			return m, nil
		}
		job := jobs[m.cursor]
		verb := "run"
		if key == "p" {
			verb = "pause"
			if cronPausedStates[job.State] {
				verb = "resume"
			}
		}
		where := job.ID + " on " + job.Host
		question := verb + " " + where + "? y/n"
		if verb == "run" {
			question = "run " + where + " now? y/n"
		}
		configPath := m.configPath
		m.pending = &dashPending{
			question: question,
			busy:     map[string]string{"run": "running ", "pause": "pausing ", "resume": "resuming "}[verb] + where + "…",
			run: func() tea.Msg {
				out, err := doCronAction(job, verb, "", configPath)
				msg := dashActionMsg{job: job, verb: verb, out: out, err: err}
				if job.Source == "launchd" {
					msg.fresh = refreshedCronJob(configPath, job)
				}
				return msg
			},
		}
	case "s":
		if m.busy != "" {
			return m, nil
		}
		configPath := m.configPath
		m.pending = &dashPending{
			question: "sync now? y/n",
			busy:     "syncing…",
			sync:     true,
			run: func() tea.Msg {
				// As `tackroom sync` runs it; the report is not printed, and
				// result receives what the run changed.
				var result setupSyncResult
				err := runSync(runOptions{ConfigPath: configPath, Stdout: io.Discard, Stdin: strings.NewReader(""), setupResult: &result})
				return dashSyncMsg{result: result, err: err}
			},
		}
	}
	return m, nil
}

func (m dashModel) jobs() []cronJob {
	if m.crons == nil {
		return nil
	}
	return m.crons.Jobs
}

func dashJobKey(j cronJob) string { return j.Host + "\x00" + j.Source + "\x00" + j.ID }

func dashFailed(j cronJob) bool { return j.LastExit != nil && *j.LastExit != 0 }

// setJobs takes a scan: jobs whose last run failed first, then jobs with
// problems, then the rest in scan order. The selection stays on its job.
func (m *dashModel) setJobs(result cronsScanResult) {
	rank := func(j cronJob) int {
		switch {
		case dashFailed(j):
			return 0
		case len(j.Problems) > 0:
			return 1
		}
		return 2
	}
	result.Jobs = slices.Clone(result.Jobs)
	slices.SortStableFunc(result.Jobs, func(a, b cronJob) int { return rank(a) - rank(b) })
	m.crons = &result
	if i := slices.IndexFunc(result.Jobs, func(j cronJob) bool { return dashJobKey(j) == m.selected }); i >= 0 {
		m.cursor = i
	}
	m.fit()
}

func (m *dashModel) move(delta int) {
	m.cursor += delta
	m.fit()
}

// fit keeps the cursor on a job and the list window on the cursor.
func (m *dashModel) fit() {
	jobs := m.jobs()
	m.cursor = max(min(m.cursor, len(jobs)-1), 0)
	if len(jobs) > 0 {
		m.selected = dashJobKey(jobs[m.cursor])
	}
	if m.width <= 0 || m.height <= 0 {
		return
	}
	visible := dashVisibleRows(len(jobs), m.layout().rows)
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+visible {
		m.top = m.cursor - visible + 1
	}
	m.top = max(min(m.top, len(jobs)-visible), 0)
}

// dashVisibleRows is how many jobs fit in rows lines: all of them, or one
// line fewer to say how many more there are.
func dashVisibleRows(jobs, rows int) int {
	if jobs <= rows {
		return jobs
	}
	return max(rows-1, 1)
}

// dashLayout is what fits between the crons title and the footer.
type dashLayout struct {
	harness []string
	detail  []string
	rows    int // lines for the job list, its "more" line included
}

// layout gives the job list at least three rows, then the harness block one
// line, then the detail lines, then the list the rest of its jobs; the full
// harness block takes the place of its one line only when that leaves room.
// So as the pane gets shorter the harness block becomes one line first, then
// the list scrolls, then the detail lines go.
func (m dashModel) layout() dashLayout {
	n := len(m.jobs())
	minRows := max(min(n, 3), 1)
	if n > 3 {
		minRows = 4 // three jobs and the "more" line
	}
	left := m.height - 4 - minRows // header, crons title, message, keys
	var l dashLayout
	if left < 1 {
		l.rows = minRows
		return l
	}
	l.harness = []string{m.harnessSummary()}
	left--
	detail := m.detailLines()
	l.detail = detail[:min(len(detail), left)]
	left -= len(l.detail)
	more := min(max(n-minRows, 0), left) // up to every job, without the "more" line
	l.rows = minRows + more
	left -= more
	if full := m.harnessLines(); len(full)-1 <= left {
		l.harness = full
	}
	return l
}

func (m dashModel) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m dashModel) render() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	l := m.layout()
	lines := []string{m.headerLine(), m.cronsTitle()}
	lines = append(lines, m.listLines(l.rows)...)
	lines = append(lines, l.detail...)
	lines = append(lines, l.harness...)
	footer := []string{m.messageLine(), ui.Dim.Render(m.keysLine())}
	for len(lines)+len(footer) < m.height {
		lines = append(lines, "")
	}
	lines = append(lines, footer...)
	if len(lines) > m.height {
		// Shorter than the minimum: keep the top and the keys.
		lines = append(lines[:max(m.height-1, 0)], footer[1])[:m.height]
	}
	// No line may wrap: a wrapped line would push the screen down.
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, m.width, "…")
	}
	return strings.Join(lines, "\n")
}

// rightAlign puts the first of rights that leaves left whole at the end of a
// line of the pane's width; when none does, the last, with left cut to make
// room.
func (m dashModel) rightAlign(left string, rights ...string) string {
	right := ""
	for _, right = range rights {
		if lipgloss.Width(left)+2+lipgloss.Width(right) <= m.width {
			break
		}
	}
	if right == "" {
		return left
	}
	room := m.width - lipgloss.Width(right) - 2
	if room < 10 {
		return left
	}
	left = ansi.Truncate(left, room, "…")
	return left + strings.Repeat(" ", m.width-lipgloss.Width(left)-lipgloss.Width(right)) + right
}

func (m dashModel) headerLine() string {
	left := ui.Bold.Render("tackroom") + "  "
	switch s := m.status; {
	case s != nil:
		harnesses := 0
		for _, r := range s.reports {
			if r.Detected || r.Error != "" {
				harnesses++
			}
		}
		left += reportState(reportProblems(s.repo, s.reports)) + " · " + plural(harnesses, "harness")
		switch {
		case s.sources == 0:
		case s.pinned == s.sources:
			left += " · " + plural(s.sources, "source") + " pinned"
		default:
			left += fmt.Sprintf(" · %d of %s pinned", s.pinned, plural(s.sources, "source"))
		}
	case m.statusBusy:
		left += ui.Dim.Render("collecting…")
	default:
		left += ui.Dim.Render("no status")
	}
	// A narrow pane gets the short form, so the counts stay whole.
	switch {
	case m.statusBusy || m.cronsBusy:
		return m.rightAlign(left, ui.Dim.Render("refreshing…"), ui.Dim.Render("…"))
	case !m.updated.IsZero():
		at := m.updated.Format("15:04")
		return m.rightAlign(left, ui.Dim.Render("updated "+at), ui.Dim.Render(at))
	}
	return left
}

func (m dashModel) cronsTitle() string {
	line := ui.Bold.Render("crons") + "  "
	if m.crons == nil {
		if m.cronsBusy {
			return line + ui.Dim.Render("collecting…")
		}
		return line + ui.Dim.Render("not collected")
	}
	failed, problems := 0, 0
	for _, j := range m.crons.Jobs {
		switch {
		case dashFailed(j):
			failed++
		case len(j.Problems) > 0:
			problems++
		}
	}
	// A host whose probe failed has no jobs listed; say so, or its jobs
	// would just be missing.
	var unreached []string
	for _, a := range m.crons.Access {
		if strings.HasPrefix(a.Detail, "probe failed") && !slices.Contains(unreached, a.Host) {
			unreached = append(unreached, a.Host)
		}
	}
	// A narrow pane gets "1 problem" for "1 with problems".
	title := func(problemWords string) string {
		parts := []string{fmt.Sprint(len(m.crons.Jobs))}
		if failed > 0 {
			parts = append(parts, ui.Red.Render(fmt.Sprintf("%d failed", failed)))
		}
		if problems > 0 {
			parts = append(parts, ui.Yellow.Render(problemWords))
		}
		if len(unreached) > 0 {
			parts = append(parts, ui.Red.Render(strings.Join(unreached, ", ")+" not reached"))
		}
		return line + strings.Join(parts, " · ")
	}
	if full := title(fmt.Sprintf("%d with problems", problems)); lipgloss.Width(full) <= m.width {
		return full
	}
	return title(plural(problems, "problem"))
}

// dashAge is a last run as an age: 2m ago, 3h ago, 8d ago; empty when unknown.
func dashAge(lastRun string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, lastRun)
	if err != nil {
		return ""
	}
	switch d := now.Sub(t); {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// dashResult is the result cell: exit N, or problem for a job with problems
// and no failed exit.
func dashResult(j cronJob) string {
	if !dashFailed(j) && len(j.Problems) > 0 {
		return "problem"
	}
	result, _ := cronResultAndLastRun(j)
	return result
}

// listLines is the job list in rows lines at most, scrolled to the cursor.
func (m dashModel) listLines(rows int) []string {
	jobs := m.jobs()
	if m.crons == nil {
		return []string{"  " + ui.Dim.Render("collecting…")}
	}
	if len(jobs) == 0 {
		return []string{"  " + ui.Dim.Render("no scheduled jobs found")}
	}
	now := time.Now()
	type cells struct{ host, id, state, age, result string }
	all := make([]cells, len(jobs))
	var hostW, idW, stateW, ageW, resultW int
	for i, j := range jobs {
		c := cells{host: truncate(j.Host, 12), id: j.ID, state: j.State, age: dashAge(j.LastRun, now), result: dashResult(j)}
		all[i] = c
		hostW, idW, stateW = max(hostW, ansi.StringWidth(c.host)), max(idW, ansi.StringWidth(c.id)), max(stateW, ansi.StringWidth(c.state))
		ageW, resultW = max(ageW, ansi.StringWidth(c.age)), max(resultW, ansi.StringWidth(c.result))
	}
	// The id shrinks to idMin before a column goes: state first, then the last run.
	showState, showAge := stateW > 0, ageW > 0
	fixed := func() int {
		n := 2 + hostW + 2 + 2 + resultW
		if showState {
			n += stateW + 2
		}
		if showAge {
			n += ageW + 2
		}
		return n
	}
	idMin := min(idW, 20)
	if fixed()+idMin > m.width {
		showState = false
	}
	if fixed()+idMin > m.width {
		showAge = false
	}
	idW = max(min(idW, m.width-fixed()), 1)

	visible := dashVisibleRows(len(jobs), rows)
	top := max(min(m.top, len(jobs)-visible), 0)
	if m.cursor < top || m.cursor >= top+visible {
		top = max(min(m.cursor, len(jobs)-visible), 0)
	}
	pad := func(s string, w int) string { return s + strings.Repeat(" ", max(w-ansi.StringWidth(s), 0)) }
	var lines []string
	for i := top; i < top+visible; i++ {
		c, j := all[i], jobs[i]
		result := c.result
		selected := i == m.cursor
		if !selected {
			switch {
			case dashFailed(j):
				result = ui.Red.Render(result)
			case c.result == "problem":
				result = ui.Yellow.Render(result)
			}
		}
		line := pad(c.host, hostW) + "  " + pad(shortenMiddle(c.id, idW), idW) + "  "
		if showState {
			line += pad(c.state, stateW) + "  "
		}
		if showAge {
			line += pad(c.age, ageW) + "  "
		}
		line += result
		if selected {
			line = dashSelectedStyle.Render(pad("▸ "+line, m.width))
		} else {
			line = "  " + line
		}
		lines = append(lines, line)
	}
	if visible < len(jobs) {
		var more []string
		if top > 0 {
			more = append(more, fmt.Sprintf("↑ %d more", top))
		}
		if below := len(jobs) - top - visible; below > 0 {
			more = append(more, fmt.Sprintf("↓ %d more", below))
		}
		lines = append(lines, "  "+ui.Dim.Render(strings.Join(more, " · ")))
	}
	return lines
}

// detailLines are up to three lines on the selected job: its schedule,
// source and problems, wrapped between words.
func (m dashModel) detailLines() []string {
	jobs := m.jobs()
	if len(jobs) == 0 || m.width <= 0 {
		return nil
	}
	j := jobs[max(min(m.cursor, len(jobs)-1), 0)]
	schedule := j.Schedule
	if schedule == "" {
		schedule = "no schedule"
	}
	text := strings.Join(append([]string{schedule, j.Source}, j.Problems...), " · ")
	lines := packItems(strings.Fields(text), "", max(m.width-2, 10))
	if len(lines) > 3 {
		lines = lines[:3]
		lines[2] = ansi.Truncate(lines[2], max(m.width-3, 1), "") + "…"
	}
	for i := range lines {
		lines[i] = "  " + lines[i]
	}
	return lines
}

// harnessLines is the full harness block: what is wrong first, a line each,
// then every harness in order, the ones not detected last and dim, then the
// items in cooldown or blocked and a lock check that did not pass.
func (m dashModel) harnessLines() []string {
	label := ui.Bold.Render("harnesses") + "  "
	indent := strings.Repeat(" ", len("harnesses  "))
	s := m.status
	if s == nil {
		return []string{label + ui.Dim.Render("collecting…")}
	}
	var wrong, synced, absent []string
	if s.repo.State != stateSynced {
		wrong = append(wrong, ui.Bold.Render("~/.agents link")+" "+ui.Mark("fail")+" "+s.repo.State)
	}
	for _, r := range s.reports {
		name := ui.Bold.Render(r.Name)
		switch {
		case r.Error != "":
			wrong = append(wrong, name+" "+ui.Mark("fail")+" "+ui.Red.Render("config unreadable: "+firstLine(r.Error)))
		case !r.Detected:
			absent = append(absent, ui.Dim.Render(r.Name+" –"))
		case !r.Synced:
			var what []string
			for _, b := range driftBuckets(r) {
				if len(b.items) > 0 {
					what = append(what, fmt.Sprintf("%s %d", b.label, len(b.items)))
				}
			}
			if r.RootState != "" && r.RootState != stateSynced {
				what = append(what, "root doc "+r.RootState)
			}
			wrong = append(wrong, name+" "+ui.Mark("warn")+" "+ui.Yellow.Render(strings.Join(what, ", ")))
		default:
			synced = append(synced, r.Name+" "+ui.Mark("ok"))
		}
	}
	lines := wrong
	if strip := append(synced, absent...); len(strip) > 0 {
		for _, line := range packItems(strip, " ", max(m.width-len(indent), 10)) {
			lines = append(lines, strings.TrimRight(line, " "))
		}
	}
	if s.gated > 0 {
		lines = append(lines, ui.Yellow.Render(fmt.Sprintf("%d in cooldown or blocked, skipped by sync", s.gated)))
	}
	if s.lock.status != checkStatusPass {
		lines = append(lines, "lock "+ui.Mark(checkMarkKind(s.lock.status))+" "+s.lock.detail)
	}
	for i := range lines {
		if i == 0 {
			lines[i] = label + lines[i]
		} else {
			lines[i] = indent + lines[i]
		}
	}
	return lines
}

// harnessSummary is the harness block in one line, for a short pane.
func (m dashModel) harnessSummary() string {
	s := m.status
	if s == nil {
		return ui.Bold.Render("harnesses") + "  " + ui.Dim.Render("collecting…")
	}
	var synced int
	var drifted, failed []string
	for _, r := range s.reports {
		switch {
		case r.Error != "":
			failed = append(failed, r.Name)
		case !r.Detected:
		case r.Synced:
			synced++
		default:
			drifted = append(drifted, r.Name)
		}
	}
	parts := []string{fmt.Sprintf("%d synced", synced)}
	if len(drifted) > 0 {
		parts = append(parts, ui.Yellow.Render(fmt.Sprintf("%d drifted (%s)", len(drifted), strings.Join(drifted, ", "))))
	}
	if len(failed) > 0 {
		parts = append(parts, ui.Red.Render(fmt.Sprintf("%d failed (%s)", len(failed), strings.Join(failed, ", "))))
	}
	if s.repo.State != stateSynced {
		parts = append(parts, ui.Red.Render("~/.agents link "+s.repo.State))
	}
	return fmt.Sprintf("%s: %s", plural(synced+len(drifted)+len(failed), "harness"), strings.Join(parts, ", "))
}

func (m dashModel) messageLine() string {
	switch {
	case m.pending != nil:
		return ui.Bold.Render(m.pending.question)
	case m.busy != "":
		return ui.Dim.Render(m.busy)
	case m.failed:
		return ui.Red.Render(m.message)
	}
	return m.message
}

func (m dashModel) keysLine() string {
	if full := "↑↓ select · enter run now · p pause/resume · r refresh · s sync · q quit"; ansi.StringWidth(full) <= m.width {
		return full
	}
	return "↑↓ · enter run · p pause · r refresh · s sync · q"
}
