package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
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
// open it next to a shell. See docs/terminals.md.

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
	// Status collection prints config and plugin warnings on stderr, which
	// would land in the middle of the screen. While it is up stdout and stderr
	// go to a pipe that keeps the tail; the tail is printed only when the
	// screen fails, so a crash, which bubbletea reports on stderr, stays
	// readable. A sync runs as a child with output of its own (dashSync).
	stop, err := dashCaptureOutput()
	if err != nil {
		return err
	}
	final, runErr := tea.NewProgram(newDashModel(*configPath), programOpts...).Run()
	tail := stop()
	if runErr != nil {
		_, _ = os.Stderr.Write(tail)
		return fmt.Errorf("tackroom dash: %w", runErr)
	}
	// A q that waited for an action or a sync left before its outcome could
	// be read on the screen, so the outcome is printed after it.
	if m, ok := final.(dashModel); ok && m.quitHeld && m.result != "" {
		fmt.Fprintln(os.Stderr, m.result)
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
	s := dashStatus{repo: repo, reports: reports, gated: len(gatedLines(g.gated)), lock: statusLockCheck(g.repoRoot, g.cfg, g.home), sources: len(g.sources)}
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

// dashSyncMsg is the outcome line of a sync: what it changed, or why it failed.
type dashSyncMsg struct {
	text   string
	failed bool
}

type dashTickMsg struct{ crons bool }

// dashPending is a question in the message line: the key that asked it, and
// the verb y starts on job (run, pause or resume), or sync.
type dashPending struct {
	question string
	key      string
	verb     string
	job      cronJob
}

type dashModel struct {
	configPath    string
	width, height int

	// A refresh error is kept apart from the last action's result: it clears
	// when that refresh next succeeds and never takes the result's place.
	status     *dashStatus
	statusGen  int
	statusBusy bool
	statusErr  string
	statusAt   time.Time        // the last status collect that succeeded
	crons      *cronsScanResult // jobs in dash order
	cronsGen   int
	cronsBusy  bool
	cronsErr   string
	cronsAt    time.Time

	cursor, top int
	selected    string // the selected job's key, kept across refreshes

	pending  *dashPending
	busy     string // the action that runs
	syncing  bool
	quitHeld bool   // q was pressed while busy
	result   string // the outcome of the last action, sync or refusal
	failed   bool   // result is an error
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
	m.result, m.failed = text, failed
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
			m.statusErr = firstLine(msg.err.Error())
			return m, nil
		}
		m.status, m.statusErr, m.statusAt = &msg.status, "", time.Now()
		m.fit()
	case dashCronsMsg:
		if msg.gen != m.cronsGen {
			return m, nil
		}
		m.cronsBusy = false
		if msg.err != nil {
			m.cronsErr = firstLine(msg.err.Error())
			return m, nil
		}
		m.cronsErr, m.cronsAt = "", time.Now()
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
		if m.quitHeld {
			return m, tea.Quit
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
		if msg.fresh == nil || m.cronsBusy {
			return m, m.refreshCrons()
		}
	case dashSyncMsg:
		m.busy, m.syncing = "", false
		m.say(msg.text, msg.failed)
		if m.quitHeld {
			return m, tea.Quit
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
		return m.start(*p)
	}
	jobs := m.jobs()
	switch key {
	case "q":
		// q waits for an action or a sync that runs, and quits when it ends.
		if m.busy != "" {
			m.quitHeld = true
			return m, nil
		}
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
		m.result, m.failed = "", false
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
		verb, why := dashVerb(job, key)
		if why != "" {
			m.say(why, false)
			return m, nil
		}
		where := job.ID + " on " + job.Host
		question := verb + " " + where + "? y/n"
		if verb == "run" {
			question = "run " + where + " now? y/n"
		}
		m.pending = &dashPending{question: question, key: key, verb: verb, job: job}
	case "s":
		if m.busy != "" {
			return m, nil
		}
		m.pending = &dashPending{question: "sync now? y/n", key: key, verb: "sync"}
	}
	return m, nil
}

// dashVerb is what Enter or p does to job, decided as the web view's buttons
// are (runButtons in web/app.js): only a launchd job with a unit has actions,
// a paused one (disabled or not loaded) only Resume, any other one Run now and
// Pause. why says why the key does nothing.
func dashVerb(job cronJob, key string) (verb, why string) {
	if job.Source != "launchd" || job.Unit == "" {
		return "", "actions work on launchd jobs only"
	}
	paused := job.State == "disabled" || job.State == "not_loaded"
	switch {
	case key == "enter" && paused:
		return "", job.ID + " is paused; p resumes it"
	case key == "enter":
		return "run", ""
	case paused:
		return "resume", ""
	case job.State == "service":
		// The scan leaves a disabled launchd service out (crons.go), so a
		// paused one would vanish from the list with nothing to resume it.
		return "", "pausing a service hides it from the scan, so it could not be resumed here"
	}
	return "pause", ""
}

// start runs what the question asked once it is answered y. A job action acts
// only on a job the last scan lists, by its record there, as the web view does
// (stale_crons in crons_actions.go).
func (m dashModel) start(p dashPending) (tea.Model, tea.Cmd) {
	configPath := m.configPath
	if p.verb == "sync" {
		m.busy, m.syncing, m.result = "syncing…", true, ""
		return m, func() tea.Msg { return dashSync(configPath) }
	}
	i := slices.IndexFunc(m.jobs(), func(j cronJob) bool { return sameCronJob(j, p.job) })
	if i < 0 {
		m.say("this job is not in the last scan; press r", false)
		return m, nil
	}
	job := m.jobs()[i]
	if verb, why := dashVerb(job, p.key); why != "" || verb != p.verb {
		if why == "" {
			why = job.ID + " changed since the question; ask again"
		}
		m.say(why, false)
		return m, nil
	}
	m.busy = map[string]string{"run": "running ", "pause": "pausing ", "resume": "resuming "}[p.verb] + job.ID + " on " + job.Host + "…"
	m.result = ""
	return m, func() tea.Msg {
		out, err := doCronAction(job, p.verb, "", configPath)
		return dashActionMsg{job: job, verb: p.verb, out: out, err: err, fresh: refreshedCronJob(configPath, job)}
	}
}

// dashSync runs `tackroom sync` as a child of this binary in a session of its
// own, with no controlling terminal and stdin from /dev/null, so nothing it
// starts (git, ssh, or an ssh command of the user's own) can ask anything on
// the terminal the dash draws on: a question that needs the terminal fails
// instead. Its output goes to unlinked temporary files, not pipes, so a child
// whose dash quit runs to the end on its own.
func dashSync(configPath string) dashSyncMsg {
	self, err := os.Executable()
	if err != nil {
		return dashSyncMsg{text: "sync: " + err.Error(), failed: true}
	}
	args := []string{"sync"}
	if configPath != "" {
		args = append(args, "--config", configPath)
	}
	var out [2]*os.File // stdout, stderr
	for i := range out {
		f, err := os.CreateTemp("", "tackroom-dash-sync-")
		if err != nil {
			return dashSyncMsg{text: "sync: " + err.Error(), failed: true}
		}
		defer f.Close()
		_ = os.Remove(f.Name())
		out[i] = f
	}
	cmd := exec.Command(self, args...)
	cmd.Stdout, cmd.Stderr = out[0], out[1]
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	runErr := cmd.Run()
	read := func(f *os.File) string {
		_, _ = f.Seek(0, io.SeekStart)
		data, _ := io.ReadAll(f)
		return ansi.Strip(string(data))
	}
	if runErr != nil {
		reason := dashSyncFailure(read(out[1]))
		if reason == "" {
			reason = runErr.Error()
		}
		return dashSyncMsg{text: "sync: " + reason, failed: true}
	}
	if changes := dashChangesLine(read(out[0])); changes != "" {
		return dashSyncMsg{text: "synced, changes: " + changes}
	}
	return dashSyncMsg{text: "synced"}
}

// dashChangesLine is the "changes" field of a `tackroom sync` report (report.go,
// renderReport) with its wrapped lines joined: the label at the start of a line,
// its value after it, and continuation lines indented under the value.
func dashChangesLine(report string) string {
	var parts []string
	for _, line := range strings.Split(report, "\n") {
		switch {
		case parts == nil && strings.HasPrefix(line, "changes "):
			parts = append(parts, strings.TrimSpace(strings.TrimPrefix(line, "changes")))
		case parts != nil && strings.HasPrefix(line, " ") && strings.TrimSpace(line) != "":
			parts = append(parts, strings.TrimSpace(line))
		case parts != nil:
			return strings.Join(parts, " ")
		}
	}
	return strings.Join(parts, " ")
}

// dashExitStatus matches an error that says only that a command exited.
var dashExitStatus = regexp.MustCompile(`exit status \d+$`)

// dashSyncFailure is why a sync failed: the line that names conflicts left in
// place, else the last line it printed, which is its error, and when that says
// only that a command such as git exited, the first line before it from ssh or
// git that says why.
func dashSyncFailure(stderr string) string {
	var lines []string
	for _, line := range strings.Split(stderr, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	last := lines[len(lines)-1]
	// A sync that left conflicts ends with advice; the line before it says
	// what was left.
	for _, line := range lines {
		if strings.Contains(line, "conflict(s) in place") {
			return line
		}
	}
	if !dashExitStatus.MatchString(last) {
		return last
	}
	for _, line := range lines[:len(lines)-1] {
		if strings.HasPrefix(line, "ssh:") || strings.HasPrefix(line, "fatal:") || strings.HasPrefix(line, "error:") ||
			strings.Contains(line, "verification failed") || strings.Contains(line, "Permission denied") {
			return last + " (" + line + ")"
		}
	}
	return last
}

func (m dashModel) jobs() []cronJob {
	if m.crons == nil {
		return nil
	}
	return m.crons.Jobs
}

func dashJobKey(j cronJob) string { return j.Host + "\x00" + j.Source + "\x00" + j.ID }

// setJobs takes a scan in the web view's order: failing jobs first, then the
// ones whose last run failed, by the shared Health (crons_words.go), then the
// rest in scan order. The selection stays on its job.
func (m *dashModel) setJobs(result cronsScanResult) {
	rank := map[string]int{"fail": 0, "warn": 1}
	order := func(j cronJob) int {
		if r, ok := rank[j.Health]; ok {
			return r
		}
		return 2
	}
	result.Jobs = slices.Clone(result.Jobs)
	slices.SortStableFunc(result.Jobs, func(a, b cronJob) int { return order(a) - order(b) })
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
// line fewer to say how many more there are, and always the selected one.
func dashVisibleRows(jobs, rows int) int {
	switch {
	case jobs <= rows:
		return jobs
	case rows <= 1:
		return 1
	}
	return rows - 1
}

// dashLayout is what the pane shows, from the top.
type dashLayout struct {
	header, title bool
	rows          int // lines for the job list, its "more" line included
	detail        []string
	harness       []string
	footer        []string // the message line and the keys
}

// layout gives the job list at least three rows, with the header, the crons
// line, the message line and the keys. What is left goes to the harness block
// as one line, then to the detail lines, then to the rest of the jobs; the
// full harness block takes the place of its one line only when that leaves
// room. So as the pane gets shorter the harness block becomes one line first,
// then the list scrolls, then the detail lines go.
//
// A pane too short for that keeps the selected row and puts the message line,
// when it has a question, a result or an error, in the place of the keys; then
// the crons line goes, then the header.
func (m dashModel) layout() dashLayout {
	n := len(m.jobs())
	minRows := max(min(n, 3), 1)
	if n > 3 {
		minRows = 4 // three jobs and the "more" line
	}
	message, keys := m.messageLine(), ui.Dim.Render(m.keysLine())
	l := dashLayout{header: true, title: true, footer: []string{message, keys}}
	left := m.height - 4 - minRows
	if left < 0 {
		l.footer = []string{keys}
		if message != "" {
			l.footer = []string{message}
		}
		l.rows = m.height - 3
		if l.rows < 1 {
			l.title, l.rows = false, l.rows+1
		}
		if l.rows < 1 {
			l.header, l.rows = false, l.rows+1
		}
		if l.rows < 1 && m.pending == nil {
			l.footer, l.rows = nil, 1
		}
		return l
	}
	l.rows = minRows
	if left == 0 {
		return l
	}
	l.harness = []string{m.harnessSummary()}
	left--
	detail := m.detailLines()
	l.detail = detail[:min(len(detail), left)]
	left -= len(l.detail)
	more := min(max(n-minRows, 0), left) // up to every job, without the "more" line
	l.rows += more
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
	var lines []string
	if l.header {
		lines = append(lines, m.headerLine())
	}
	if l.title {
		lines = append(lines, m.cronsTitle())
	}
	lines = append(lines, m.listLines(l.rows)...)
	lines = append(lines, l.detail...)
	lines = append(lines, l.harness...)
	for len(lines)+len(l.footer) < m.height {
		lines = append(lines, "")
	}
	lines = append(lines[:min(len(lines), m.height-len(l.footer))], l.footer...)
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

// asOf is " (as of 15:04)" for data whose last refresh failed.
func asOf(at time.Time) string {
	return ui.Dim.Render(" (as of " + at.Format("15:04") + ")")
}

func (m dashModel) headerLine() string {
	left := ui.Bold.Render("tackroom") + "  "
	switch s := m.status; {
	case m.statusErr != "":
		// The state is not known, so the last one is not shown as current.
		left += ui.Mark("warn") + " " + ui.Yellow.Render("status unknown") + ui.Dim.Render(": "+m.statusErr)
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
	default:
		left += ui.Dim.Render("collecting…")
	}
	// A narrow pane gets the short form, so the counts stay whole.
	updated := m.statusAt
	if m.cronsAt.After(updated) {
		updated = m.cronsAt
	}
	switch {
	case m.statusBusy || m.cronsBusy:
		return m.rightAlign(left, ui.Dim.Render("refreshing…"), ui.Dim.Render("…"))
	case !updated.IsZero():
		at := updated.Format("15:04")
		return m.rightAlign(left, ui.Dim.Render("updated "+at), ui.Dim.Render(at))
	}
	return left
}

func (m dashModel) cronsTitle() string {
	line := ui.Bold.Render("crons") + "  "
	if m.crons == nil {
		switch {
		case m.cronsErr != "":
			return line + ui.Mark("warn") + " " + ui.Yellow.Render("unknown") + ui.Dim.Render(": "+m.cronsErr)
		case m.cronsBusy:
			return line + ui.Dim.Render("collecting…")
		}
		return line + ui.Dim.Render("not collected")
	}
	// Counted by the shared Health, in the web view's words.
	failing, failed := 0, 0
	for _, j := range m.crons.Jobs {
		switch j.Health {
		case "fail":
			failing++
		case "warn":
			failed++
		}
	}
	// A host whose probe failed has no jobs listed, and a source that could
	// not be read lists none of its own; say so, or the jobs would just be
	// missing.
	var unreached []string
	unreadable := 0
	for _, a := range m.crons.Access {
		switch {
		case strings.HasPrefix(a.Detail, "probe failed"):
			if !slices.Contains(unreached, a.Host) {
				unreached = append(unreached, a.Host)
			}
		case a.State == "unreadable":
			unreadable++
		}
	}
	// A narrow pane gets "1 failed" for "1 last run failed".
	title := func(failedWords string) string {
		parts := []string{fmt.Sprint(len(m.crons.Jobs))}
		if m.cronsErr != "" {
			parts = append(parts, ui.Yellow.Render("stale, from "+m.cronsAt.Format("15:04")))
		}
		if failing > 0 {
			parts = append(parts, ui.Red.Render(fmt.Sprintf("%d failing", failing)))
		}
		if failed > 0 {
			parts = append(parts, ui.Yellow.Render(fmt.Sprintf(failedWords, failed)))
		}
		if len(unreached) > 0 {
			parts = append(parts, ui.Red.Render(strings.Join(unreached, ", ")+" not reached"))
		}
		if unreadable > 0 {
			word := "sources"
			if unreadable == 1 {
				word = "source"
			}
			parts = append(parts, ui.Red.Render(fmt.Sprintf("%d %s unreadable", unreadable, word)))
		}
		return line + strings.Join(parts, " · ")
	}
	if full := title("%d last run failed"); lipgloss.Width(full) <= m.width {
		return full
	}
	return title("%d failed")
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

// dashResult is the result cell: exit N or the last status, or problem for a
// job with problems and no failed exit.
func dashResult(j cronJob) string {
	if (j.LastExit == nil || *j.LastExit == 0) && len(j.Problems) > 0 {
		return "problem"
	}
	result, _ := cronResultAndLastRun(j)
	return result
}

// listLines is the job list in rows lines at most, scrolled to the cursor.
func (m dashModel) listLines(rows int) []string {
	jobs := m.jobs()
	switch {
	case rows <= 0:
		return nil
	case m.crons == nil && m.cronsBusy:
		return []string{"  " + ui.Dim.Render("collecting…")}
	case m.crons == nil:
		return []string{"  " + ui.Dim.Render("no jobs: the collect failed")}
	case len(jobs) == 0:
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
	// The result is colored by the shared Health, as the web view colors it.
	color := map[string]lipgloss.Style{"fail": ui.Red, "warn": ui.Yellow, "paused": ui.Dim}
	var lines []string
	for i := top; i < top+visible; i++ {
		c, j := all[i], jobs[i]
		result := c.result
		selected := i == m.cursor
		if style, ok := color[j.Health]; ok && !selected && result != "" {
			result = style.Render(result)
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
	if visible < len(jobs) && len(lines) < rows {
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
	switch {
	case s == nil && m.statusErr != "":
		return []string{label + ui.Dim.Render("unknown")}
	case s == nil:
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
	if m.statusErr != "" {
		lines = append(lines, ui.Dim.Render("as of "+m.statusAt.Format("15:04")+"; the last status refresh failed"))
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
	switch {
	case s == nil && m.statusErr != "":
		return ui.Bold.Render("harnesses") + "  " + ui.Dim.Render("unknown")
	case s == nil:
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
	stale := ""
	if m.statusErr != "" {
		stale = asOf(m.statusAt)
	}
	return fmt.Sprintf("%s%s: %s", plural(synced+len(drifted)+len(failed), "harness"), stale, strings.Join(parts, ", "))
}

// messageLine is a pending question, the action that runs, the last action's
// result, or else a refresh error.
func (m dashModel) messageLine() string {
	switch {
	case m.pending != nil:
		// A narrow pane drops " on <host>", then shortens the job's id, then
		// the middle, so the question keeps the job's name and its "y/n".
		question, job := m.pending.question, m.pending.job
		if lipgloss.Width(question) > m.width && job.ID != "" {
			question = strings.Replace(question, " on "+job.Host, "", 1)
		}
		if over := lipgloss.Width(question) - m.width; over > 0 && job.ID != "" {
			question = strings.Replace(question, job.ID, shortenMiddle(job.ID, max(lipgloss.Width(job.ID)-over, 1)), 1)
		}
		return ui.Bold.Render(shortenMiddle(question, max(m.width, 1)))
	case m.busy != "" && m.quitHeld:
		return ui.Bold.Render(m.busy + " quitting when it ends; ctrl+c quits now")
	case m.busy != "":
		return ui.Dim.Render(m.busy)
	case m.result != "" && m.failed:
		return ui.Red.Render(m.result)
	case m.result != "":
		return m.result
	case m.statusErr != "":
		return ui.Red.Render("status: " + m.statusErr)
	case m.cronsErr != "":
		return ui.Red.Render("crons: " + m.cronsErr)
	}
	return ""
}

func (m dashModel) keysLine() string {
	if full := "↑↓ select · enter run now · p pause/resume · r refresh · s sync · q quit"; ansi.StringWidth(full) <= m.width {
		return full
	}
	return "↑↓ · enter run · p pause · r refresh · s sync · q"
}
