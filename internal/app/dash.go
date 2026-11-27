package app

import (
	"errors"
	"flag"
	"fmt"
	"image/color"
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
	"github.com/yourconscience/tackroom/internal/agentrole"
	"github.com/yourconscience/tackroom/internal/ui"
)

// `tackroom dash` is a live screen for a terminal pane: whether everything is
// in sync and which scheduled jobs fail, with run now, pause and resume for a
// launchd job and a sync. The workspace commands (tmux, zellij, tern) open it
// next to a shell, and `tackroom rex` as a panel. See docs/terminals.md.

const (
	dashStatusEvery = 30 * time.Second
	dashCronsEvery  = 5 * time.Minute
	// dashKeepOutput is how much of what the collectors print is kept, to be
	// shown when the screen fails.
	dashKeepOutput = 16 << 10
)

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
	// What the config manages, for the counts strip.
	skills, roles, mcp, hooks int
	blocked                   int // sources the audit or a gate blocked
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
	s := dashStatus{repo: repo, reports: reports, gated: len(gatedLines(g.gated)), lock: statusLockCheck(g.repoRoot, g.cfg, g.home), sources: len(g.sources),
		mcp: len(g.cfg.MCPServers), hooks: len(g.cfg.Hooks)}
	if expected, err := expectedSkills(g.repoRoot, g.home, g.cfg); err == nil {
		s.skills = len(expected)
	}
	if roles, err := agentrole.Load(g.repoRoot); err == nil {
		s.roles = len(roles)
	}
	for _, item := range g.gated {
		if item.Kind == "blocked" {
			s.blocked++
		}
	}
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

type dashHostsMsg struct {
	gen    int
	result hostsJSON
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
	hosts      *hostsJSON // tackroom on each machine in crons.hosts
	hostsGen   int
	hostsBusy  bool
	hostsErr   string
	hostsAt    time.Time

	cursor, top int
	selected    string // the selected job's key, kept across refreshes

	pending  *dashPending
	busy     string // the action that runs
	syncing  bool
	quitHeld bool   // q was pressed while busy
	result   string // the outcome of the last action, sync or refusal
	failed   bool   // result is an error

	// Tints of the terminal's own background, once it says what that is: the
	// selected row's, and the key caps'. Without them the selected row is bold
	// with a bar, and a key is bold.
	tint, keyBg color.Color
}

func newDashModel(configPath string) dashModel {
	return dashModel{configPath: configPath, statusGen: 1, statusBusy: true, cronsGen: 1, cronsBusy: true, hostsGen: 1, hostsBusy: true}
}

func (m dashModel) Init() tea.Cmd {
	return tea.Batch(dashCollectStatus(m.configPath, m.statusGen), dashCollectCrons(m.configPath, m.cronsGen), dashCollectHosts(m.configPath, m.hostsGen),
		dashTick(false), dashTick(true), tea.RequestBackgroundColor)
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

func dashCollectHosts(configPath string, gen int) tea.Cmd {
	return func() tea.Msg {
		result, err := collectHosts(configPath, nil)
		return dashHostsMsg{gen: gen, result: result, err: err}
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

// refreshHosts probes the machines again; it runs with the crons, every 5
// minutes and on r.
func (m *dashModel) refreshHosts() tea.Cmd {
	m.hostsGen++
	m.hostsBusy = true
	return dashCollectHosts(m.configPath, m.hostsGen)
}

func (m *dashModel) say(text string, failed bool) {
	m.result, m.failed = text, failed
}

func (m dashModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.fit()
	case tea.BackgroundColorMsg:
		if msg.Color != nil {
			m.tint, m.keyBg = lipgloss.Lighten(msg.Color, 0.06), lipgloss.Lighten(msg.Color, 0.14)
			if !msg.IsDark() {
				m.tint, m.keyBg = lipgloss.Darken(msg.Color, 0.05), lipgloss.Darken(msg.Color, 0.1)
			}
		}
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
	case dashHostsMsg:
		if msg.gen != m.hostsGen {
			return m, nil
		}
		m.hostsBusy = false
		if msg.err != nil {
			m.hostsErr = firstLine(msg.err.Error())
			return m, nil
		}
		m.hosts, m.hostsErr, m.hostsAt = &msg.result, "", time.Now()
		m.fit()
	case dashTickMsg:
		next := dashTick(msg.crons)
		switch {
		case msg.crons:
			cmds := []tea.Cmd{next}
			if !m.cronsBusy {
				cmds = append(cmds, m.refreshCrons())
			}
			if !m.hostsBusy {
				cmds = append(cmds, m.refreshHosts())
			}
			return m, tea.Batch(cmds...)
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
		if !m.hostsBusy {
			cmds = append(cmds, m.refreshHosts())
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

// fit keeps the cursor on a job and the list window on the cursor, with the
// title of the cursor's group in view when the window has room for it.
func (m *dashModel) fit() {
	jobs := m.jobs()
	m.cursor = max(min(m.cursor, len(jobs)-1), 0)
	if len(jobs) > 0 {
		m.selected = dashJobKey(jobs[m.cursor])
	}
	if m.width <= 0 || m.height <= 0 {
		return
	}
	rows := m.listRows()
	visible := dashVisibleRows(len(rows), m.layout().rows)
	at := dashRowOf(rows, m.cursor)
	want := at
	if at > 0 && rows[at-1].job < 0 && visible > 1 {
		want = at - 1
	}
	if want < m.top {
		m.top = want
	}
	if at >= m.top+visible {
		m.top = at - visible + 1
	}
	m.top = max(min(m.top, len(rows)-visible), 0)
}

// dashVisibleRows is how many list rows fit in rows lines: all of them, or one
// line fewer to say how many more there are, and always the selected one.
func dashVisibleRows(total, rows int) int {
	switch {
	case total <= rows:
		return total
	case rows <= 1:
		return 1
	}
	return rows - 1
}

// dashRow is a line of the job list: the title of a group, or a job.
type dashRow struct {
	group string // fail, warn, or empty for every other job
	job   int    // the job's index in jobs(), or -1 for a group's title
}

// dashGroup is the group a job is listed in, by the shared Health: failing,
// last run failed, or everything else.
func dashGroup(j cronJob) string {
	if j.Health == "fail" || j.Health == "warn" {
		return j.Health
	}
	return ""
}

// listRows is the job list: each group's title, then its jobs. setJobs keeps
// a group's jobs together.
func (m dashModel) listRows() []dashRow {
	jobs := m.jobs()
	var rows []dashRow
	for i, j := range jobs {
		if i == 0 || dashGroup(j) != dashGroup(jobs[i-1]) {
			rows = append(rows, dashRow{group: dashGroup(j), job: -1})
		}
		rows = append(rows, dashRow{group: dashGroup(j), job: i})
	}
	return rows
}

// groupGaps is how many blank lines the list's groups take apart: one above
// every group's title but the first.
func (m dashModel) groupGaps() int {
	gaps := -1
	for _, r := range m.listRows() {
		if r.job < 0 {
			gaps++
		}
	}
	return max(gaps, 0)
}

func dashRowOf(rows []dashRow, job int) int {
	for i, r := range rows {
		if r.job == job {
			return i
		}
	}
	return 0
}

// dashLevel is how much the pane shows around the job list. The levels go
// from the full screen to the job list alone; layout takes the first that
// leaves the list minRows lines.
type dashLevel struct {
	spaced         bool // a blank line between the blocks
	title, answer  bool
	sub            bool // the line under the answer
	counts         int  // 2 the boxed strip, 1 one line, 0 none
	harness        int  // 2 the full block, 1 one line, 0 none
	detail         int  // 2 the card, 1 two plain lines, 0 none
	minRows        int
	footerOneLine  bool // the message line takes the place of the keys
	footerOptional bool // a pane of one line keeps the selected job instead
}

var dashLevels = []dashLevel{
	{spaced: true, title: true, answer: true, sub: true, counts: 2, harness: 2, detail: 2, minRows: 8},
	{title: true, answer: true, sub: true, counts: 2, harness: 2, detail: 2, minRows: 8},
	{title: true, answer: true, sub: true, counts: 1, harness: 2, detail: 2, minRows: 6},
	{title: true, answer: true, sub: true, counts: 1, harness: 1, detail: 2, minRows: 6},
	{title: true, answer: true, counts: 1, harness: 1, detail: 1, minRows: 5},
	{title: true, answer: true, harness: 1, minRows: 4},
	{answer: true, minRows: 3, footerOneLine: true},
	{minRows: 1, footerOneLine: true, footerOptional: true},
}

// dashLayout is what the pane shows, from the top.
type dashLayout struct {
	top    []string // the title bar, the answer, the counts and the harnesses
	rows   int      // lines for the job list
	detail []string // the selected job
	footer []string // the message line and the keys
}

// layout gives the job list what is left once the richest level that leaves it
// its minimum is drawn. As the pane gets shorter the spacing goes first, then
// the counts become one line, then the harnesses, then the detail card becomes
// two lines; then the counts, the harnesses, the detail and the title bar go,
// and last the answer.
func (m dashModel) layout() dashLayout {
	w, _ := m.inner()
	total := max(len(m.listRows()), 1)
	message, keys := m.messageLine(w), m.keysLine(w)
	for i, lv := range dashLevels {
		footer := []string{keys}
		if message != "" {
			footer = []string{message, keys}
		}
		if lv.footerOneLine && message != "" {
			footer = []string{message}
		} else if lv.footerOneLine {
			footer = []string{keys}
		}
		top := m.topLines(w, lv)
		// The detail takes the most it can take whichever job is selected, so
		// the list does not move as the cursor does; render puts it at the
		// bottom.
		var detail []string
		reserve := 0
		switch lv.detail {
		case 2:
			detail, reserve = m.detailCard(w), dashCardLines+2
			// A pane too narrow for the card gets the two plain lines.
			if len(detail) == 0 {
				detail, reserve = m.detailPlain(w), 2
			}
		case 1:
			detail, reserve = m.detailPlain(w), 2
		}
		if len(detail) == 0 {
			reserve = 0
		} else if lv.spaced {
			reserve++
		}
		fixed := len(top) + reserve + len(footer)
		if fixed+min(lv.minRows, total) <= m.height {
			// A spaced level gives each group after the first a blank line
			// above its title when the whole list fits with them.
			most := total
			if lv.spaced {
				most += m.groupGaps()
			}
			return dashLayout{top: top, rows: min(m.height-fixed, most), detail: detail, footer: footer}
		}
		if i == len(dashLevels)-1 {
			l := dashLayout{rows: m.height - len(footer), footer: footer}
			if l.rows < 1 && m.pending == nil {
				l.footer, l.rows = nil, 1
			}
			return l
		}
	}
	return dashLayout{}
}

// inner is the width of the content and its left margin: 2 columns of margin
// from a 50-column pane, 1 from 30, and at most 100 columns of content, so a
// wide pane keeps a job's name near its result.
func (m dashModel) inner() (w, margin int) {
	switch {
	case m.width >= 50:
		margin = 2
	case m.width >= 30:
		margin = 1
	}
	return max(min(m.width-2*margin, 100), 1), margin
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
	w, margin := m.inner()
	lines := slices.Clone(l.top)
	lines = append(lines, m.listLines(w, l.rows)...)
	for len(lines)+len(l.detail)+len(l.footer) < m.height {
		lines = append(lines, "")
	}
	lines = append(lines, l.detail...)
	lines = append(lines[:min(len(lines), m.height-len(l.footer))], l.footer...)
	// No line may wrap: a wrapped line would push the screen down.
	pad := strings.Repeat(" ", margin)
	for i, line := range lines {
		if line != "" {
			line = pad + line
		}
		lines[i] = ansi.Truncate(line, m.width, "…")
	}
	return strings.Join(lines, "\n")
}

// topLines are the blocks above the job list that lv shows, with a blank line
// after each when lv is spaced.
func (m dashModel) topLines(w int, lv dashLevel) []string {
	var out []string
	add := func(lines ...string) {
		if len(lines) == 0 {
			return
		}
		out = append(out, lines...)
		if lv.spaced {
			out = append(out, "")
		}
	}
	if lv.title {
		add(m.titleBar(w))
	}
	if lv.answer {
		add(m.answerLines(w, lv.sub)...)
	}
	switch {
	case lv.counts == 2 && w >= dashCountsBoxMin:
		add(m.countsBox(w)...)
	case lv.counts > 0:
		add(m.countsLine(w))
	}
	switch lv.harness {
	case 2:
		add(m.harnessBlock(w)...)
		add(m.machineBlock(w)...)
	case 1:
		add(m.harnessLine(w))
		if line := m.machineLine(w); line != "" {
			add(line)
		}
	}
	return out
}

// spread puts right at the end of a line of width w after left, or leaves it
// out when both do not fit with two spaces between them.
func spread(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if right == "" || gap < 2 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// padRight pads s with spaces to width w.
func padRight(s string, w int) string {
	return s + strings.Repeat(" ", max(w-lipgloss.Width(s), 0))
}

// titleBar is the name and when the data was last refreshed.
func (m dashModel) titleBar(w int) string {
	left := ui.Bold.Render("tackroom")
	updated := m.statusAt
	for _, at := range []time.Time{m.cronsAt, m.hostsAt} {
		if at.After(updated) {
			updated = at
		}
	}
	switch {
	case m.statusBusy || m.cronsBusy || m.hostsBusy:
		return spread(left, ui.Dim.Render("refreshing…"), w)
	case !updated.IsZero():
		return spread(left, ui.Dim.Render("updated "+updated.Format("15:04")), w)
	}
	return left
}

// machines is the machines in crons.hosts as `tackroom hosts` reports them,
// when there is more than this one, as the Herdr panel shows them.
func (m dashModel) machines() []hostJSON {
	if m.hosts == nil || len(m.hosts.Hosts) < 2 {
		return nil
	}
	return m.hosts.Hosts
}

// downHosts are the hosts the crons collect could not reach: every source on
// them unscanned, one with a failed probe. They list no jobs, so they are said,
// or their jobs would just be missing.
func (m dashModel) downHosts() []string {
	if m.crons == nil {
		return nil
	}
	var down []string
	for _, a := range m.crons.Access {
		if slices.Contains(down, a.Host) {
			continue
		}
		all, probe := true, false
		for _, b := range m.crons.Access {
			if b.Host != a.Host {
				continue
			}
			all = all && b.State == "not_scanned"
			probe = probe || strings.HasPrefix(b.Detail, "probe failed")
		}
		if all && probe {
			down = append(down, a.Host)
		}
	}
	return down
}

// unreadableSources is how many cron sources the collect could not read.
func (m dashModel) unreadableSources() int {
	n := 0
	if m.crons != nil {
		for _, a := range m.crons.Access {
			if a.State == "unreadable" {
				n++
			}
		}
	}
	return n
}

// answer is the headline and the sentences under it, counted as the Herdr
// panel's Overview counts (overviewModel in herdr/tackroom/mods/tackroom), so
// the two agree. Failing: a failing cron, a harness with a conflict or an
// unreadable config, a failing machine, a host the crons collect could not
// reach that is not a machine already counted. Needs attention: a cron whose
// last run failed, a sync waiting, each machine that needs attention, a source
// blocked by the audit or a gate, and status, crons or machines that could not
// be read at all. level is fail, warn, ok, or off while the
// first collects run.
func (m dashModel) answer() (level, title string, subs []string) {
	fail, warn := 0, 0
	failKinds := map[string]bool{}
	var pending []string
	if s := m.status; s != nil {
		live, broken := 0, 0
		var drifted []string
		for _, r := range s.reports {
			switch {
			case r.Error != "" || len(r.Conflicts) > 0:
				broken++
			case !r.Detected:
			case !r.Synced:
				live++
				drifted = append(drifted, r.Name)
			default:
				live++
			}
		}
		fail += broken
		if broken > 0 {
			failKinds["harness"] = true
		}
		if len(drifted) > 0 || s.repo.State != stateSynced {
			warn++
		}
		if s.repo.State != stateSynced {
			subs = append(subs, "The ~/.agents link is "+s.repo.State+".")
		}
		switch {
		case len(drifted) == 1:
			subs = append(subs, drifted[0]+" needs a sync.")
		case len(drifted) == 2:
			subs = append(subs, drifted[0]+" and "+drifted[1]+" need a sync.")
		case len(drifted) > 2:
			subs = append(subs, fmt.Sprintf("%d harnesses need a sync.", len(drifted)))
		case broken == 0 && s.repo.State == stateSynced && live == 1:
			subs = append(subs, "The harness is in sync.")
		case broken == 0 && s.repo.State == stateSynced && live > 1:
			subs = append(subs, fmt.Sprintf("All %d harnesses are in sync.", live))
		}
		warn += s.blocked
		if s.blocked > 0 {
			subs = append(subs, plural(s.blocked, "source")+" blocked.")
		}
		if m.statusErr != "" {
			subs = append(subs, "Harnesses as of "+m.statusAt.Format("15:04")+".")
		}
	} else if m.statusErr != "" {
		warn++
		subs = append(subs, "Harness status could not be read.")
	} else {
		pending = append(pending, "harnesses")
	}
	machines := m.machines()
	counted := map[string]bool{}
	sick := 0
	for _, h := range machines {
		switch h.Health {
		case "ok":
		case "fail":
			fail++
			failKinds["machine"] = true
			counted[h.Host] = true
		default:
			sick++
			counted[h.Host] = true
		}
	}
	warn += sick
	switch {
	case m.hosts != nil && m.hostsErr != "":
		subs = append(subs, "Machines as of "+m.hostsAt.Format("15:04")+".")
	case m.hosts == nil && m.hostsErr != "":
		warn++
		subs = append(subs, "Machines could not be checked.")
	case m.hosts == nil:
		pending = append(pending, "machines")
	}
	if c := m.crons; c != nil {
		for _, j := range c.Jobs {
			switch j.Health {
			case "fail":
				fail++
				failKinds["cron"] = true
			case "warn":
				warn++
			}
		}
		var down []string
		for _, h := range m.downHosts() {
			if !counted[h] {
				down = append(down, h)
			}
		}
		fail += len(down)
		if len(down) > 0 {
			failKinds["machine"] = true
			subs = append(subs, strings.Join(down, " and ")+" could not be reached.")
		}
		if n := m.unreadableSources(); n > 0 {
			subs = append(subs, plural(n, "cron source")+" could not be read.")
		}
		if m.cronsErr != "" {
			subs = append(subs, "Crons as of "+m.cronsAt.Format("15:04")+".")
		}
	} else if m.cronsErr != "" {
		warn++
		subs = append(subs, "Crons could not be collected.")
	} else {
		pending = append(pending, "crons")
	}
	// What the failures are, when they are all one kind.
	if len(failKinds) == 1 {
		kinds := map[string][2]string{"cron": {"a cron", "crons"}, "harness": {"a harness", "harnesses"}, "machine": {"a machine", "machines"}}
		for kind := range failKinds {
			one, many := kinds[kind][0], kinds[kind][1]
			switch fail {
			case 1:
				subs = append(subs, "The failure is "+one+".")
			case 2:
				subs = append(subs, "Both failures are "+many+".")
			default:
				subs = append(subs, "The failures are "+many+".")
			}
		}
	}
	if len(pending) > 0 {
		subs = append(subs, "Still checking "+strings.Join(pending, " and ")+".")
	}
	var parts []string
	if fail > 0 {
		parts = append(parts, fmt.Sprintf("%d failing", fail))
	}
	if warn == 1 {
		parts = append(parts, "1 needs attention")
	} else if warn > 1 {
		parts = append(parts, fmt.Sprintf("%d need attention", warn))
	}
	switch {
	case fail > 0:
		level = "fail"
	case warn > 0:
		level = "warn"
	case len(pending) > 0:
		return "off", "Checking…", subs
	default:
		return "ok", "All clear", subs
	}
	return level, strings.Join(parts, ", "), subs
}

// machineBlock is the machines when there is more than this one: a title with
// how they stand, then a line each with its health and summary, or for one
// that is fine its tackroom version and config commit.
func (m dashModel) machineBlock(w int) []string {
	machines := m.machines()
	if len(machines) == 0 {
		return nil
	}
	out := []string{spread(ui.Bold.Render("Machines"), m.machineTally(), w)}
	for _, h := range machines {
		name := ui.Bold.Render(h.Host)
		switch h.Health {
		case "ok":
			what := "tackroom " + h.Version
			if len(h.Commit) >= 7 {
				what += " · config " + h.Commit[:7]
			}
			out = append(out, ui.Mark("ok")+" "+name+"  "+ui.Dim.Render(what))
		case "fail":
			out = append(out, ui.Mark("fail")+" "+name+"  "+ui.Red.Render(h.Summary))
		default:
			out = append(out, ui.Mark("warn")+" "+name+"  "+ui.Yellow.Render(h.Summary))
		}
	}
	for i := range out {
		out[i] = ansi.Truncate(out[i], w, "…")
	}
	return out
}

// machineTally is how the machines stand in a few words, for the title of
// their block and for their line.
func (m dashModel) machineTally() string {
	var parts []string
	failing, sick := 0, 0
	for _, h := range m.machines() {
		switch h.Health {
		case "ok":
		case "fail":
			failing++
		default:
			sick++
		}
	}
	if failing > 0 {
		parts = append(parts, ui.Red.Render(fmt.Sprintf("%d failing", failing)))
	}
	if sick > 0 {
		parts = append(parts, ui.Yellow.Render(fmt.Sprintf("%d need attention", sick)))
	}
	if len(parts) == 0 {
		parts = append(parts, ui.Dim.Render(fmt.Sprintf("%d in order", len(m.machines()))))
	}
	return strings.Join(parts, ui.Dim.Render(" · "))
}

// machineLine is the machines in one line, for a short pane.
func (m dashModel) machineLine(w int) string {
	if len(m.machines()) == 0 {
		return ""
	}
	return ansi.Truncate(ui.Bold.Render("Machines")+"  "+m.machineTally(), w, "…")
}

// dashDot is a level's dot: red for failing, yellow for needs attention, green
// for clear, and hollow while unknown.
func dashDot(level string) string {
	switch level {
	case "fail":
		return ui.Red.Render("●")
	case "warn":
		return ui.Yellow.Render("●")
	case "ok":
		return ui.Green.Render("●")
	}
	return ui.Dim.Render("○")
}

// answerLines is the headline with its dot, and with sub the sentences under
// it in two lines at most.
func (m dashModel) answerLines(w int, sub bool) []string {
	level, title, subs := m.answer()
	lines := []string{dashDot(level) + " " + ui.Bold.Render(title)}
	if !sub || len(subs) == 0 || w < 12 {
		return lines
	}
	wrapped := packItems(strings.Fields(strings.Join(subs, " ")), "", w-2)
	if len(wrapped) > 2 {
		wrapped = wrapped[:2]
		wrapped[1] = ansi.Truncate(wrapped[1], w-3, "") + "…"
	}
	for _, line := range wrapped {
		lines = append(lines, "  "+ui.Dim.Render(line))
	}
	return lines
}

// dashCountsBoxMin is the narrowest content the boxed counts fit in: five
// cells of at least seven columns and their six borders.
const dashCountsBoxMin = 41

// counts are the numbers of the counts strip with their labels: what the config
// manages, and the jobs, red when one fails and yellow when a last run failed.
func (m dashModel) counts() (values, labels []string) {
	labels = []string{"Skills", "Roles", "MCP", "Hooks", "Crons"}
	unknown := ui.Dim.Render("–")
	values = []string{unknown, unknown, unknown, unknown, unknown}
	if s := m.status; s != nil {
		for i, n := range []int{s.skills, s.roles, s.mcp, s.hooks} {
			values[i] = ui.Bold.Render(fmt.Sprint(n))
		}
	}
	if c := m.crons; c != nil {
		style := ui.Bold
		for _, j := range c.Jobs {
			if j.Health == "fail" {
				style = ui.Red.Bold(true)
				break
			}
			if j.Health == "warn" {
				style = ui.Yellow.Bold(true)
			}
		}
		values[4] = style.Render(fmt.Sprint(len(c.Jobs)))
	}
	return values, labels
}

// countsBox is the counts strip as a rounded box of five cells across w.
func (m dashModel) countsBox(w int) []string {
	values, labels := m.counts()
	n := len(values)
	cells := make([]int, n)
	for i := range cells {
		cells[i] = (w - n - 1) / n
		if i < (w-n-1)%n {
			cells[i]++
		}
	}
	center := func(s string, cw int) string {
		gap := max(cw-lipgloss.Width(s), 0)
		return strings.Repeat(" ", gap/2) + s + strings.Repeat(" ", gap-gap/2)
	}
	rule := func(left, mid, right string) string {
		segs := make([]string, n)
		for i, cw := range cells {
			segs[i] = strings.Repeat("─", cw)
		}
		return ui.Dim.Render(left + strings.Join(segs, mid) + right)
	}
	row := func(texts []string) string {
		bar := ui.Dim.Render("│")
		segs := make([]string, n)
		for i, cw := range cells {
			segs[i] = center(texts[i], cw)
		}
		return bar + strings.Join(segs, bar) + bar
	}
	dimLabels := make([]string, n)
	for i, l := range labels {
		dimLabels[i] = ui.Dim.Render(l)
	}
	return []string{rule("╭", "┬", "╮"), row(values), row(dimLabels), rule("╰", "┴", "╯")}
}

// countsLine is the counts strip in one line, for a narrow or short pane.
func (m dashModel) countsLine(w int) string {
	values, labels := m.counts()
	parts := make([]string, len(values))
	for i := range values {
		label := strings.ToLower(labels[i])
		if labels[i] == "MCP" {
			label = "MCP"
		}
		parts[i] = values[i] + " " + ui.Dim.Render(label)
	}
	return ansi.Truncate(strings.Join(parts, ui.Dim.Render(" · ")), w, "…")
}

// harnessTally is how the harnesses stand: the ones in sync and not detected
// by name, and a line each for what is wrong.
func (m dashModel) harnessTally() (synced, absent, wrong []string, drifted, broken int) {
	s := m.status
	if s.repo.State != stateSynced {
		wrong = append(wrong, ui.Mark("warn")+" "+ui.Bold.Render("~/.agents link")+"  "+ui.Yellow.Render(s.repo.State))
	}
	for _, r := range s.reports {
		switch {
		case r.Error != "":
			broken++
			wrong = append(wrong, ui.Mark("fail")+" "+ui.Bold.Render(r.Name)+"  "+ui.Red.Render("config unreadable: "+firstLine(r.Error)))
		case len(r.Conflicts) > 0:
			broken++
			wrong = append(wrong, ui.Mark("fail")+" "+ui.Bold.Render(r.Name)+"  "+ui.Red.Render(plural(len(r.Conflicts), "conflict")))
		case !r.Detected:
			absent = append(absent, r.Name)
		case !r.Synced:
			drifted++
			var what []string
			for _, b := range driftBuckets(r) {
				if len(b.items) > 0 {
					what = append(what, fmt.Sprintf("%s %d", b.label, len(b.items)))
				}
			}
			if r.RootState != "" && r.RootState != stateSynced {
				what = append(what, "root doc "+r.RootState)
			}
			wrong = append(wrong, ui.Mark("warn")+" "+ui.Bold.Render(r.Name)+"  "+ui.Yellow.Render(strings.Join(what, ", ")))
		default:
			synced = append(synced, r.Name)
		}
	}
	return synced, absent, wrong, drifted, broken
}

// harnessBlock is the harnesses: a title with how many are in sync, a line for
// each one that is wrong, the rest as a grid, the ones not detected last and
// dim, then the items in cooldown or blocked and a lock check that did not pass.
func (m dashModel) harnessBlock(w int) []string {
	title := ui.Bold.Render("Harnesses")
	if m.status == nil {
		if m.statusErr != "" {
			return []string{spread(title, ui.Dim.Render("unknown"), w)}
		}
		return []string{spread(title, ui.Dim.Render("collecting…"), w)}
	}
	s := m.status
	synced, absent, wrong, drifted, broken := m.harnessTally()
	var right string
	switch {
	case broken > 0:
		right = ui.Red.Render(fmt.Sprintf("%d failing", broken))
	case drifted > 0:
		right = ui.Yellow.Render(fmt.Sprintf("%d need a sync", drifted))
	default:
		right = ui.Dim.Render(fmt.Sprintf("%d in sync", len(synced)))
	}
	lines := []string{spread(title, right, w)}
	lines = append(lines, wrong...)
	// The grid: a column per chip, as wide as the widest, two spaces apart.
	chips := make([]string, 0, len(synced)+len(absent))
	width := 0
	for _, name := range synced {
		chips = append(chips, ui.Green.Render("✓")+" "+name)
		width = max(width, 2+lipgloss.Width(name))
	}
	for _, name := range absent {
		chips = append(chips, ui.Dim.Render("– "+name))
		width = max(width, 2+lipgloss.Width(name))
	}
	if cols := max((w+3)/(width+3), 1); len(chips) > 0 {
		for i := 0; i < len(chips); i += cols {
			row := chips[i:min(i+cols, len(chips))]
			for j := range row[:len(row)-1] {
				row[j] = padRight(row[j], width+3)
			}
			lines = append(lines, strings.Join(row, ""))
		}
	}
	if s.gated > 0 {
		lines = append(lines, ui.Yellow.Render(fmt.Sprintf("%d in cooldown or blocked, skipped by sync", s.gated)))
	}
	if s.lock.status != checkStatusPass {
		lines = append(lines, ui.Mark(checkMarkKind(s.lock.status))+" lock  "+s.lock.detail)
	}
	return lines
}

// harnessLine is the harnesses in one line, for a short pane.
func (m dashModel) harnessLine(w int) string {
	title := ui.Bold.Render("Harnesses") + "  "
	if m.status == nil {
		if m.statusErr != "" {
			return title + ui.Dim.Render("unknown")
		}
		return title + ui.Dim.Render("collecting…")
	}
	synced, absent, _, drifted, broken := m.harnessTally()
	parts := []string{fmt.Sprintf("%d in sync", len(synced))}
	if drifted > 0 {
		parts = append(parts, ui.Yellow.Render(fmt.Sprintf("%d need a sync", drifted)))
	}
	if broken > 0 {
		parts = append(parts, ui.Red.Render(fmt.Sprintf("%d failing", broken)))
	}
	if m.status.repo.State != stateSynced {
		parts = append(parts, ui.Yellow.Render("~/.agents link "+m.status.repo.State))
	}
	if len(absent) > 0 {
		parts = append(parts, ui.Dim.Render(fmt.Sprintf("%d not installed", len(absent))))
	}
	return ansi.Truncate(title+strings.Join(parts, ui.Dim.Render(" · ")), w, "…")
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

// dashJobName is the name a job is listed by: a launchd label without its
// reverse-DNS prefix, and any other job's id, since its name may carry a
// description.
func dashJobName(j cronJob) string {
	if j.Source == "launchd" && j.ShortName != "" {
		return j.ShortName
	}
	return j.ID
}

// dashGroupTitles are the titles of the list's groups.
var dashGroupTitles = map[string]string{"fail": "Failing", "warn": "Last run failed", "": "Everything else"}

// listLines is the job list in rows lines at most, scrolled to the cursor: a
// group's title, then a row per job with a dot by its health, its name, host,
// schedule when the pane is wide, last run and result.
func (m dashModel) listLines(w, rows int) []string {
	jobs := m.jobs()
	switch {
	case rows <= 0:
		return nil
	case m.crons == nil && m.cronsBusy:
		return []string{ui.Bold.Render("Crons") + "  " + ui.Dim.Render("collecting…")}
	case m.crons == nil:
		return []string{ui.Bold.Render("Crons") + "  " + ui.Dim.Render("not collected: the collect failed")}
	case len(jobs) == 0:
		return []string{ui.Bold.Render("Crons") + "  " + ui.Dim.Render("no scheduled jobs found")}
	}
	all := m.listRows()
	groups := map[string]int{}
	for _, j := range jobs {
		groups[dashGroup(j)]++
	}
	now := time.Now()
	type cells struct{ name, host, schedule, age, result string }
	cs := make([]cells, len(jobs))
	var nameW, hostW, schedW, ageW, resultW int
	for i, j := range jobs {
		c := cells{name: dashJobName(j), host: truncate(j.Host, 12), schedule: truncate(j.ScheduleText, 18), age: dashAge(j.LastRun, now), result: dashResult(j)}
		cs[i] = c
		nameW, hostW = max(nameW, ansi.StringWidth(c.name)), max(hostW, ansi.StringWidth(c.host))
		schedW, ageW, resultW = max(schedW, ansi.StringWidth(c.schedule)), max(ageW, ansi.StringWidth(c.age)), max(resultW, ansi.StringWidth(c.result))
	}
	// The name shrinks to nameMin before a column goes: the schedule first,
	// then the last run, then the host.
	showSched, showAge, showHost := schedW > 0 && w >= 84, ageW > 0, hostW > 0
	fixed := func() int {
		n := 4 + 2 + resultW // the bar, the dot, their spaces
		for _, c := range []struct {
			on bool
			w  int
		}{{showSched, schedW}, {showAge, ageW}, {showHost, hostW}} {
			if c.on {
				n += c.w + 2
			}
		}
		return n
	}
	nameMin := min(nameW, 14)
	for _, drop := range []*bool{&showSched, &showAge, &showHost} {
		if fixed()+nameMin > w {
			*drop = false
		}
	}
	nameW = max(min(nameW, w-fixed()), 1)

	visible := dashVisibleRows(len(all), rows)
	top := max(min(m.top, len(all)-visible), 0)
	if at := dashRowOf(all, m.cursor); at < top || at >= top+visible {
		top = max(min(at, len(all)-visible), 0)
	}
	color := map[string]lipgloss.Style{"fail": ui.Red, "warn": ui.Yellow}
	gaps := visible == len(all) && rows >= len(all)+m.groupGaps()
	var lines []string
	for i, r := range all[top : top+visible] {
		if r.job < 0 {
			if gaps && i > 0 {
				lines = append(lines, "")
			}
			title := dashGroupTitles[r.group]
			if len(groups) == 1 && r.group == "" {
				title = "Crons"
			}
			style := ui.Bold
			if c, ok := color[r.group]; ok {
				style = c.Bold(true)
			}
			lines = append(lines, style.Render(title)+ui.Dim.Render(fmt.Sprintf(" · %d", groups[r.group])))
			continue
		}
		c, j, selected := cs[r.job], jobs[r.job], r.job == m.cursor
		// The selected row is tinted across the content's width, each part
		// with the tint, so the styles inside it do not end it.
		tint := func(s lipgloss.Style) lipgloss.Style {
			if selected && m.tint != nil {
				return s.Background(m.tint)
			}
			return s
		}
		plain := tint(lipgloss.NewStyle())
		gap := plain.Render("  ")
		bar, name := plain.Render(" "), tint(lipgloss.NewStyle())
		if selected {
			bar, name = tint(ui.Bold).Render("▌"), tint(ui.Bold)
		}
		dot := tint(ui.Dim).Render("●")
		switch j.Health {
		case "fail", "warn":
			dot = tint(color[j.Health]).Render("●")
		case "paused", "idle":
			dot = tint(ui.Dim).Render("○")
		}
		result := tint(ui.Dim)
		if s, ok := color[j.Health]; ok {
			result = tint(s)
		}
		line := bar + plain.Render(" ") + dot + plain.Render(" ") + name.Render(padRight(shortenMiddle(c.name, nameW), nameW))
		if showSched {
			line += gap + tint(ui.Dim).Render(padRight(c.schedule, schedW))
		}
		if showHost {
			line += gap + tint(ui.Dim).Render(padRight(c.host, hostW))
		}
		if showAge {
			line += gap + tint(ui.Dim).Render(strings.Repeat(" ", ageW-ansi.StringWidth(c.age))+c.age)
		}
		line += gap + result.Render(padRight(c.result, resultW))
		if selected {
			line += plain.Render(strings.Repeat(" ", max(w-lipgloss.Width(line), 0)))
		}
		lines = append(lines, line)
	}
	if visible < len(all) && len(lines) < rows {
		var more []string
		if top > 0 {
			more = append(more, fmt.Sprintf("↑ %d more", top))
		}
		if below := len(all) - top - visible; below > 0 {
			more = append(more, fmt.Sprintf("↓ %d more", below))
		}
		lines = append(lines, "    "+ui.Dim.Render(strings.Join(more, " · ")))
	}
	return lines
}

// selectedJob is the job under the cursor, if any.
func (m dashModel) selectedJob() (cronJob, bool) {
	jobs := m.jobs()
	if len(jobs) == 0 {
		return cronJob{}, false
	}
	return jobs[max(min(m.cursor, len(jobs)-1), 0)], true
}

// detailFacts are the selected job's schedule, source, state and host, its
// last run and result, and its problems.
func (m dashModel) detailFacts(j cronJob) (meta, last string, problems []string) {
	schedule := j.ScheduleText
	if schedule == "" {
		schedule = j.Schedule
	}
	if schedule == "" {
		schedule = "no schedule"
	}
	var parts []string
	for _, p := range []string{schedule, j.Source, j.State, j.Host} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	meta = strings.Join(parts, " · ")
	if age := dashAge(j.LastRun, time.Now()); age != "" {
		last = "Last run " + age
		if result := dashResult(j); result != "" {
			last += " · " + result
		}
	}
	return meta, last, j.Problems
}

// dashCardLines is the most lines the detail card's body takes.
const dashCardLines = 6

// detailCard is the selected job in a rounded box with its id in the top
// border: schedule, source, state and host, its last run, and its problems,
// wrapped, in dashCardLines lines at most.
func (m dashModel) detailCard(w int) []string {
	j, ok := m.selectedJob()
	if !ok || w < 24 {
		return nil
	}
	meta, last, problems := m.detailFacts(j)
	cw := w - 4
	body := []string{ui.Dim.Render(ansi.Truncate(meta, cw, "…"))}
	if last != "" {
		style := lipgloss.NewStyle()
		switch j.Health {
		case "fail":
			style = ui.Red
		case "warn":
			style = ui.Yellow
		}
		body = append(body, style.Render(ansi.Truncate(last, cw, "…")))
	}
	for _, p := range problems {
		body = append(body, strings.Split(ansi.Wrap(p, cw, " /"), "\n")...)
	}
	if len(body) > dashCardLines {
		body = body[:dashCardLines]
		body[dashCardLines-1] = ansi.Truncate(body[dashCardLines-1], cw-1, "") + "…"
	}
	title := ui.Bold.Render(shortenMiddle(j.ID, max(w-6, 1)))
	lines := []string{ui.Dim.Render("╭─ ") + title + ui.Dim.Render(" "+strings.Repeat("─", max(w-5-lipgloss.Width(title), 0))+"╮")}
	for _, line := range body {
		lines = append(lines, ui.Dim.Render("│ ")+padRight(line, cw)+ui.Dim.Render(" │"))
	}
	return append(lines, ui.Dim.Render("╰"+strings.Repeat("─", w-2)+"╯"))
}

// detailPlain is the selected job in two plain lines, for a short pane.
func (m dashModel) detailPlain(w int) []string {
	j, ok := m.selectedJob()
	if !ok {
		return nil
	}
	meta, last, problems := m.detailFacts(j)
	second := last
	if len(problems) > 0 {
		second = strings.Join(problems, " · ")
	}
	lines := []string{"  " + ui.Dim.Render(ansi.Truncate(meta, max(w-2, 1), "…"))}
	if second != "" {
		lines = append(lines, "  "+ansi.Truncate(second, max(w-2, 1), "…"))
	}
	return lines
}

// messageLine is a pending question, the action that runs, the last action's
// result, or else a refresh error.
func (m dashModel) messageLine(w int) string {
	switch {
	case m.pending != nil:
		// A narrow pane drops " on <host>", then shortens the job's id, then
		// the middle, so the question keeps the job's name and its "y/n".
		question, job := m.pending.question, m.pending.job
		if lipgloss.Width(question) > w && job.ID != "" {
			question = strings.Replace(question, " on "+job.Host, "", 1)
		}
		if over := lipgloss.Width(question) - w; over > 0 && job.ID != "" {
			question = strings.Replace(question, job.ID, shortenMiddle(job.ID, max(lipgloss.Width(job.ID)-over, 1)), 1)
		}
		return ui.Bold.Render(shortenMiddle(question, max(w, 1)))
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
	case m.hostsErr != "":
		return ui.Red.Render("machines: " + m.hostsErr)
	}
	return ""
}

// keysLine is the keys, each as a key cap with what it does, as many words as
// fit: every label, then the short ones, then the keys alone.
func (m dashModel) keysLine(w int) string {
	keyCap := func(k string) string {
		if m.keyBg != nil {
			return lipgloss.NewStyle().Background(m.keyBg).Padding(0, 1).Render(k)
		}
		return ui.Bold.Render(k)
	}
	join := func(keys [][2]string) string {
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = keyCap(k[0])
			if k[1] != "" {
				parts[i] += " " + ui.Dim.Render(k[1])
			}
		}
		return strings.Join(parts, "  ")
	}
	for _, keys := range [][][2]string{
		{{"↑↓", "select"}, {"enter", "run now"}, {"p", "pause"}, {"s", "sync"}, {"r", "refresh"}, {"q", "quit"}},
		{{"↑↓", ""}, {"enter", "run"}, {"p", "pause"}, {"s", "sync"}, {"r", "refresh"}, {"q", "quit"}},
	} {
		if line := join(keys); lipgloss.Width(line) <= w {
			return line
		}
	}
	return ansi.Truncate(ui.Dim.Render("↑↓ enter p s r q"), w, "")
}
