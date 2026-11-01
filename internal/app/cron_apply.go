package app

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// `tackroom cron apply` installs the jobs declared with a schedule and a
// command in crons.jobs as native units of the host's own scheduler, and
// removes the ones that are no longer declared. tackroom runs no daemon.
// Plain `tackroom cron` manages its two built-in jobs the same way.
// See docs/crons.md section 4.

const (
	ownedLaunchdPrefix = "dev.tackroom."
	ownedSystemdPrefix = "tackroom-"
	ownedCrontabMarker = "# tackroom:"

	cronBackendLaunchd = "launchd"
	cronBackendSystemd = "systemd"
	cronBackendCrontab = "crontab"
)

var (
	ownedCronIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	cronBackends       = []string{cronBackendLaunchd, cronBackendSystemd, cronBackendCrontab}
	// builtinCronIDs are the jobs plain `tackroom cron` owns; apply never
	// prunes them and the manifest cannot declare them.
	builtinCronIDs = map[string]bool{"tackroom-pull": true, "tackroom-deps": true}
	// legacyCronLine is the self-update line `tackroom cron` wrote to crontab
	// before it used owned units: any tackroom binary running pull, sync deps
	// or deps update, with an optional profile prefix and --config.
	legacyCronLine = regexp.MustCompile(`^(?:@\w+|\S+(?:\s+\S+){4})\s+(?:\. "\$HOME/\.profile" 2>/dev/null;\s*)?(?:"[^"]*/tackroom"|\S*/tackroom|tackroom)\s+(pull|sync deps|deps update)(?:\s+--config\s+(?:"[^"]*"|\S+))?\s*$`)
)

type ownedCronJob struct {
	ID       string
	Schedule string
	Command  []string // absolute executable, expanded paths
}

type ownedCronUnit struct {
	ID, Backend, Content string
}

type cronPlanStep struct {
	Op      string // install, replace, remove, keep
	Backend string
	Job     ownedCronJob
	Content string
}

func runCronApply(args []string) error {
	fs := flag.NewFlagSet("cron apply", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "Path to tackroom YAML config")
	dryRun := fs.Bool("dry-run", false, "Print the plan and the units it would write, change nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("cron apply does not accept positional arguments")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	// Lock before reading the manifest, so a run waiting for the lock plans
	// from the config as it is then. A dry run changes nothing and takes none.
	if !*dryRun {
		unlock, err := lockCronApply(home)
		if err != nil {
			return err
		}
		defer unlock()
	}
	// A missing config is an error here, never an empty manifest: an empty
	// manifest would prune every owned unit.
	path, err := resolveConfigPath(*configPath, home)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("cron apply needs a config: %w", err)
	}
	manifest, err := loadCronsManifest(*configPath)
	if err != nil {
		return err
	}
	desired, err := ownedCronJobs(manifest, home)
	if err != nil {
		return err
	}
	backend, err := selectCronBackend(manifest.Scheduler)
	if err != nil {
		return err
	}
	installed, err := readOwnedCronUnits(home)
	if err != nil {
		return err
	}
	plan, err := planOwnedCrons(desired, backend, installed, home, func(id string) bool { return !builtinCronIDs[id] })
	if err != nil {
		return err
	}
	return runCronPlan(plan, backend, home, nil, *dryRun)
}

// ownedCronJobs returns the manifest jobs this machine should run.
func ownedCronJobs(manifest cronsManifest, home string) ([]ownedCronJob, error) {
	ids := make([]string, 0, len(manifest.Jobs))
	for id := range manifest.Jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []ownedCronJob
	for _, id := range ids {
		entry := manifest.Jobs[id]
		if entry.Schedule == "" && len(entry.Command) == 0 {
			continue // an inventory annotation, not an owned job
		}
		if entry.Schedule == "" || len(entry.Command) == 0 {
			return nil, fmt.Errorf("crons.jobs.%s: an owned job needs both schedule and command", id)
		}
		if !ownedCronIDPattern.MatchString(id) {
			return nil, fmt.Errorf("crons.jobs.%s: owned job ids must match %s", id, ownedCronIDPattern)
		}
		if builtinCronIDs[id] {
			return nil, fmt.Errorf("crons.jobs.%s: reserved for `tackroom cron`", id)
		}
		if entry.Host != "" && manifest.Self == "" {
			return nil, fmt.Errorf("crons.jobs.%s has host %q but crons.self is not set in tackroom.local.yaml; nothing was changed", id, entry.Host)
		}
		if entry.Host != "" && entry.Host != manifest.Self {
			continue
		}
		command, err := resolveCronCommand(entry.Command, home)
		if err != nil {
			return nil, fmt.Errorf("crons.jobs.%s: %w", id, err)
		}
		out = append(out, ownedCronJob{ID: id, Schedule: entry.Schedule, Command: command})
	}
	return out, nil
}

// resolveCronCommand expands ~ and $HOME, since schedulers run argv without a
// shell, and makes the executable absolute.
func resolveCronCommand(command []string, home string) ([]string, error) {
	out := make([]string, len(command))
	for i, arg := range command {
		arg = expandPath(arg, home)
		arg = strings.ReplaceAll(arg, "${HOME}", home)
		out[i] = strings.ReplaceAll(arg, "$HOME", home)
	}
	if !filepath.IsAbs(out[0]) {
		path, err := exec.LookPath(out[0])
		if err != nil {
			return nil, fmt.Errorf("command %q not found on PATH", out[0])
		}
		if out[0], err = filepath.Abs(path); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// selectCronBackend resolves crons.scheduler. auto never picks crontab.
func selectCronBackend(scheduler string) (string, error) {
	switch scheduler {
	case cronBackendLaunchd, cronBackendSystemd, cronBackendCrontab:
		return scheduler, nil
	case "", "auto":
	default:
		return "", fmt.Errorf("crons.scheduler must be auto, launchd, systemd or crontab, got %q", scheduler)
	}
	switch runtime.GOOS {
	case "darwin":
		return cronBackendLaunchd, nil
	case "linux":
		if exec.Command("systemctl", "--user", "show-environment").Run() == nil {
			return cronBackendSystemd, nil
		}
		return "", errors.New("crons.scheduler is auto, but `systemctl --user` does not answer; set crons.scheduler: crontab to use crontab")
	}
	return "", fmt.Errorf("crons.scheduler is auto, and %s has no default scheduler; set crons.scheduler", runtime.GOOS)
}

func lockCronApply(home string) (func(), error) {
	dir := filepath.Join(home, ".local", "state", "tackroom", "cron")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "apply.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock %s: %w", f.Name(), err)
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

func cronLogPath(home, id string) string {
	return filepath.Join(home, ".local", "state", "tackroom", "cron", id+".log")
}

func launchdAgentsDir(home string) string { return filepath.Join(home, "Library", "LaunchAgents") }
func systemdUserDir(home string) string   { return filepath.Join(home, ".config", "systemd", "user") }

// readOwnedCronUnits finds every owned unit on this machine, on all backends,
// by its prefix or marker. Nothing else is read as owned.
func readOwnedCronUnits(home string) ([]ownedCronUnit, error) {
	var units []ownedCronUnit
	plists, _ := filepath.Glob(filepath.Join(launchdAgentsDir(home), ownedLaunchdPrefix+"*.plist"))
	for _, path := range plists {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), ownedLaunchdPrefix), ".plist")
		units = append(units, ownedCronUnit{ID: id, Backend: cronBackendLaunchd, Content: string(data)})
	}
	timers, _ := filepath.Glob(filepath.Join(systemdUserDir(home), ownedSystemdPrefix+"*.timer"))
	for _, path := range timers {
		timer, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		service, _ := os.ReadFile(strings.TrimSuffix(path, ".timer") + ".service")
		id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), ownedSystemdPrefix), ".timer")
		units = append(units, ownedCronUnit{ID: id, Backend: cronBackendSystemd, Content: string(timer) + string(service)})
	}
	lines, err := readCrontabLines()
	if err != nil {
		return nil, err
	}
	for _, line := range lines {
		if id, ok := crontabOwnedID(line); ok {
			units = append(units, ownedCronUnit{ID: id, Backend: cronBackendCrontab, Content: line})
		}
	}
	return units, nil
}

func crontabOwnedID(line string) (string, bool) {
	i := strings.LastIndex(line, ownedCrontabMarker)
	if i < 0 {
		return "", false
	}
	id := strings.TrimSpace(line[i+len(ownedCrontabMarker):])
	return id, ownedCronIDPattern.MatchString(id)
}

// readCrontabLines returns the user's crontab; no crontab is an empty one.
func readCrontabLines() ([]string, error) {
	if _, err := exec.LookPath("crontab"); err != nil {
		return nil, nil
	}
	out, err := exec.Command("crontab", "-l").CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "no crontab for") {
			return nil, nil
		}
		return nil, fmt.Errorf("crontab -l: %s", strings.TrimSpace(string(out)))
	}
	text := strings.TrimSuffix(string(out), "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

// planOwnedCrons compares the desired jobs with what is installed. inScope
// limits which installed ids this run may remove.
func planOwnedCrons(desired []ownedCronJob, backend string, installed []ownedCronUnit, home string, inScope func(string) bool) ([]cronPlanStep, error) {
	want := map[string]bool{}
	var plan []cronPlanStep
	for _, job := range desired {
		want[job.ID] = true
		content, err := renderOwnedCron(job, backend, home)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", job.ID, err)
		}
		op, matches := "install", 0
		for _, unit := range installed {
			if unit.ID == job.ID && unit.Backend == backend {
				matches++
				op = "replace"
				if unit.Content == content && matches == 1 {
					op = "keep"
				}
			}
		}
		if matches > 1 {
			op = "replace" // duplicate crontab lines collapse into one
		}
		plan = append(plan, cronPlanStep{Op: op, Backend: backend, Job: job, Content: content})
	}
	for _, unit := range installed {
		if !inScope(unit.ID) || (want[unit.ID] && unit.Backend == backend) {
			continue
		}
		plan = append(plan, cronPlanStep{Op: "remove", Backend: unit.Backend, Job: ownedCronJob{ID: unit.ID}})
	}
	return plan, nil
}

func renderOwnedCron(job ownedCronJob, backend, home string) (string, error) {
	switch backend {
	case cronBackendLaunchd:
		return renderLaunchdPlist(job, home)
	case cronBackendSystemd:
		calendar, err := cronToOnCalendar(job.Schedule)
		if err != nil {
			return "", err
		}
		return renderSystemdTimer(job, calendar) + renderSystemdService(job), nil
	case cronBackendCrontab:
		if _, ok := parseCronSpec(job.Schedule); !ok {
			return "", fmt.Errorf("schedule %q is not a 5-field cron expression", job.Schedule)
		}
		// cron turns an unescaped % into a newline.
		quote := func(arg string) string {
			return strings.ReplaceAll("'"+strings.ReplaceAll(arg, "'", `'\''`)+"'", "%", `\%`)
		}
		quoted := make([]string, len(job.Command))
		for i, arg := range job.Command {
			quoted[i] = quote(arg)
		}
		return fmt.Sprintf("%s %s >> %s 2>&1 %s%s", job.Schedule, strings.Join(quoted, " "), quote(cronLogPath(home, job.ID)), ownedCrontabMarker, job.ID), nil
	}
	return "", fmt.Errorf("unknown backend %q", backend)
}

// cronFieldValues returns a field's sorted values, or nil when the field
// allows every value.
func cronFieldValues(spec cronSpec, field int) []int {
	sizes := [5]int{60, 24, 31, 12, 7}
	if len(spec.fields[field]) == sizes[field] {
		return nil
	}
	values := make([]int, 0, len(spec.fields[field]))
	for v := range spec.fields[field] {
		values = append(values, v)
	}
	sort.Ints(values)
	return values
}

func parseOwnedSchedule(schedule string) (cronSpec, error) {
	spec, ok := parseCronSpec(schedule)
	if !ok {
		return cronSpec{}, fmt.Errorf("schedule %q is not a 5-field numeric cron expression", schedule)
	}
	if cronFieldValues(spec, 2) != nil && cronFieldValues(spec, 4) != nil {
		return cronSpec{}, fmt.Errorf("schedule %q restricts both day of month and day of week; cron runs it when either matches, which launchd and systemd cannot express", schedule)
	}
	return spec, nil
}

// cronToCalendarIntervals expands a cron expression into launchd
// StartCalendarInterval dictionaries.
func cronToCalendarIntervals(schedule string) ([]map[string]int, error) {
	spec, err := parseOwnedSchedule(schedule)
	if err != nil {
		return nil, err
	}
	keys := [5]string{"Minute", "Hour", "Day", "Month", "Weekday"}
	dicts := []map[string]int{{}}
	for field, key := range keys {
		values := cronFieldValues(spec, field)
		if values == nil {
			continue
		}
		var next []map[string]int
		for _, dict := range dicts {
			for _, v := range values {
				d := map[string]int{key: v}
				for k, x := range dict {
					d[k] = x
				}
				next = append(next, d)
			}
		}
		dicts = next
		if len(dicts) > 64 {
			return nil, fmt.Errorf("schedule %q expands to more than 64 launchd calendar intervals", schedule)
		}
	}
	return dicts, nil
}

// cronToOnCalendar translates a cron expression into a systemd OnCalendar=.
func cronToOnCalendar(schedule string) (string, error) {
	spec, err := parseOwnedSchedule(schedule)
	if err != nil {
		return "", err
	}
	list := func(field int, names []string) string {
		values := cronFieldValues(spec, field)
		if values == nil {
			return "*"
		}
		parts := make([]string, len(values))
		for i, v := range values {
			if names != nil {
				parts[i] = names[v]
			} else {
				parts[i] = fmt.Sprintf("%02d", v)
			}
		}
		return strings.Join(parts, ",")
	}
	calendar := fmt.Sprintf("*-%s-%s %s:%s:00", list(3, nil), list(2, nil), list(1, nil), list(0, nil))
	if dow := list(4, []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}); dow != "*" {
		calendar = dow + " " + calendar
	}
	return calendar, nil
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

func renderLaunchdPlist(job ownedCronJob, home string) (string, error) {
	dicts, err := cronToCalendarIntervals(job.Schedule)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`)
	fmt.Fprintf(&b, "\t<key>Label</key>\n\t<string>%s</string>\n", xmlEscape(ownedLaunchdPrefix+job.ID))
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, arg := range job.Command {
		fmt.Fprintf(&b, "\t\t<string>%s</string>\n", xmlEscape(arg))
	}
	b.WriteString("\t</array>\n\t<key>StartCalendarInterval</key>\n\t<array>\n")
	for _, dict := range dicts {
		b.WriteString("\t\t<dict>\n")
		for _, key := range []string{"Minute", "Hour", "Day", "Month", "Weekday"} {
			if v, ok := dict[key]; ok {
				fmt.Fprintf(&b, "\t\t\t<key>%s</key>\n\t\t\t<integer>%d</integer>\n", key, v)
			}
		}
		b.WriteString("\t\t</dict>\n")
	}
	log := xmlEscape(cronLogPath(home, job.ID))
	fmt.Fprintf(&b, "\t</array>\n\t<key>StandardOutPath</key>\n\t<string>%s</string>\n\t<key>StandardErrorPath</key>\n\t<string>%s</string>\n</dict>\n</plist>\n", log, log)
	return b.String(), nil
}

func systemdQuote(arg string) string {
	arg = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(arg)
	return `"` + arg + `"`
}

func renderSystemdTimer(job ownedCronJob, calendar string) string {
	return fmt.Sprintf("[Unit]\nDescription=tackroom cron %s\n\n[Timer]\nOnCalendar=%s\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n", job.ID, calendar)
}

func renderSystemdService(job ownedCronJob) string {
	quoted := make([]string, len(job.Command))
	for i, arg := range job.Command {
		quoted[i] = systemdQuote(arg)
	}
	return fmt.Sprintf("[Unit]\nDescription=tackroom cron %s\n\n[Service]\nType=oneshot\nExecStart=%s\n", job.ID, strings.Join(quoted, " "))
}

// runCronPlan prints the plan and, unless dryRun, executes it. legacy are
// crontab lines to drop in the same crontab write.
func runCronPlan(plan []cronPlanStep, backend, home string, legacy []string, dryRun bool) error {
	changes := 0
	for _, step := range plan {
		mark := map[string]string{"install": "+", "replace": "~", "remove": "-", "keep": "="}[step.Op]
		line := fmt.Sprintf("%s %-7s %-8s %s", mark, step.Op, step.Backend, step.Job.ID)
		if step.Op != "remove" {
			line += fmt.Sprintf("  %s  %s", step.Job.Schedule, shellJoin(step.Job.Command))
		}
		fmt.Println(line)
		if step.Op != "keep" {
			changes++
		}
		if dryRun && (step.Op == "install" || step.Op == "replace") {
			for _, l := range strings.Split(strings.TrimSuffix(step.Content, "\n"), "\n") {
				fmt.Println("    " + l)
			}
		}
	}
	for _, line := range legacy {
		fmt.Printf("- legacy  crontab  %s\n", line)
		changes++
	}
	if changes == 0 {
		fmt.Println("owned cron jobs are up to date")
		return nil
	}
	if dryRun {
		return nil
	}
	if backend == cronBackendSystemd {
		if out, err := exec.Command("loginctl", "show-user", os.Getenv("USER"), "-p", "Linger", "--value").Output(); err == nil && strings.TrimSpace(string(out)) != "yes" {
			fmt.Println("warning: lingering is off, so systemd user timers stop at logout; enable with: loginctl enable-linger $USER")
		}
	}
	if err := os.MkdirAll(filepath.Dir(cronLogPath(home, "x")), 0o755); err != nil {
		return err
	}

	// Install first, then remove. A unit is removed only when its job's new
	// unit installed, so a job moving between backends is never left without one.
	var errs []string
	failed := map[string]bool{}
	fail := func(id string, err error) {
		errs = append(errs, fmt.Sprintf("%s: %v", id, err))
		failed[id] = true
	}
	var crontabAdd, crontabIDs []string
	crontabDrop := map[string]bool{}
	// Old self-update lines go in the same crontab write as their crontab
	// replacement; on another backend they go only after every install worked.
	if backend == cronBackendCrontab {
		for _, line := range legacy {
			crontabDrop[line] = true
		}
	}
	var systemdSteps []cronPlanStep
	for _, step := range plan {
		if step.Op != "install" && step.Op != "replace" {
			continue
		}
		switch step.Backend {
		case cronBackendLaunchd:
			if err := installLaunchdUnit(step, home); err != nil {
				fail(step.Job.ID, err)
			}
		case cronBackendSystemd:
			if err := writeSystemdUnits(step, home); err != nil {
				fail(step.Job.ID, err)
			} else {
				systemdSteps = append(systemdSteps, step)
			}
		case cronBackendCrontab:
			crontabAdd = append(crontabAdd, step.Content)
			crontabIDs = append(crontabIDs, step.Job.ID)
			crontabDrop[ownedCrontabMarker+step.Job.ID] = true
		}
	}
	if len(systemdSteps) > 0 {
		reload := systemctlUser("daemon-reload")
		for _, step := range systemdSteps {
			if reload != nil {
				fail(step.Job.ID, reload)
				continue
			}
			timer := ownedSystemdPrefix + step.Job.ID + ".timer"
			err := systemctlUser("enable", "--now", timer)
			if err == nil && step.Op == "replace" {
				err = systemctlUser("restart", timer)
			}
			if err != nil {
				fail(step.Job.ID, err)
			}
		}
	}
	if len(crontabAdd) > 0 || (backend == cronBackendCrontab && len(legacy) > 0) {
		if err := rewriteCrontab(crontabAdd, crontabDrop); err != nil {
			errs = append(errs, err.Error())
			for _, id := range crontabIDs {
				failed[id] = true
			}
		}
	}

	removeDrop := map[string]bool{}
	systemdRemoved := false
	for _, step := range plan {
		if step.Op != "remove" {
			continue
		}
		if failed[step.Job.ID] {
			errs = append(errs, fmt.Sprintf("%s: kept the %s unit because its replacement did not install", step.Job.ID, step.Backend))
			continue
		}
		switch step.Backend {
		case cronBackendLaunchd:
			if err := removeLaunchdUnit(step.Job.ID, home); err != nil {
				errs = append(errs, fmt.Sprintf("remove %s: %v", step.Job.ID, err))
			}
		case cronBackendSystemd:
			if err := removeSystemdUnits(step.Job.ID, home); err != nil {
				errs = append(errs, fmt.Sprintf("remove %s: %v", step.Job.ID, err))
			}
			systemdRemoved = true
		case cronBackendCrontab:
			removeDrop[ownedCrontabMarker+step.Job.ID] = true
		}
	}
	if backend != cronBackendCrontab && len(legacy) > 0 {
		if len(failed) > 0 {
			errs = append(errs, "kept the old crontab self-update line because its replacement did not install")
		} else {
			for _, line := range legacy {
				removeDrop[line] = true
			}
		}
	}
	if len(removeDrop) > 0 {
		if err := rewriteCrontab(nil, removeDrop); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if systemdRemoved {
		if err := systemctlUser("daemon-reload"); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("cron apply did not finish; check `tackroom cron collect`:\n  %s", strings.Join(errs, "\n  "))
	}
	fmt.Printf("applied %d change(s)\n", changes)
	return nil
}

func writeFileReplacing(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func launchdDomain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

func launchdLoaded(label string) bool {
	return exec.Command("launchctl", "print", launchdDomain()+"/"+label).Run() == nil
}

func launchctl(args ...string) error {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %s", strings.Join(args, " "), commandFailure(out, err))
	}
	return nil
}

func installLaunchdUnit(step cronPlanStep, home string) error {
	label := ownedLaunchdPrefix + step.Job.ID
	path := filepath.Join(launchdAgentsDir(home), label+".plist")
	if err := writeFileReplacing(path, step.Content); err != nil {
		return err
	}
	if launchdLoaded(label) {
		if err := launchctl("bootout", launchdDomain()+"/"+label); err != nil {
			return err
		}
	}
	return launchctl("bootstrap", launchdDomain(), path)
}

func removeLaunchdUnit(id, home string) error {
	label := ownedLaunchdPrefix + id
	if launchdLoaded(label) {
		if err := launchctl("bootout", launchdDomain()+"/"+label); err != nil {
			return err
		}
	}
	return os.Remove(filepath.Join(launchdAgentsDir(home), label+".plist"))
}

func systemctlUser(args ...string) error {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl --user %s: %s", strings.Join(args, " "), commandFailure(out, err))
	}
	return nil
}

// commandFailure is a failed command's output, or its error when it printed nothing.
func commandFailure(out []byte, err error) string {
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return msg
	}
	return err.Error()
}

func writeSystemdUnits(step cronPlanStep, home string) error {
	calendar, err := cronToOnCalendar(step.Job.Schedule)
	if err != nil {
		return err
	}
	base := filepath.Join(systemdUserDir(home), ownedSystemdPrefix+step.Job.ID)
	if err := writeFileReplacing(base+".service", renderSystemdService(step.Job)); err != nil {
		return err
	}
	return writeFileReplacing(base+".timer", renderSystemdTimer(step.Job, calendar))
}

func removeSystemdUnits(id, home string) error {
	timer := ownedSystemdPrefix + id + ".timer"
	if err := systemctlUser("disable", "--now", timer); err != nil {
		return err
	}
	base := filepath.Join(systemdUserDir(home), ownedSystemdPrefix+id)
	for _, path := range []string{base + ".timer", base + ".service"} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// rewriteCrontab writes the crontab once: drop lines that carry a dropped
// marker or equal a dropped legacy line, then append add.
func rewriteCrontab(add []string, drop map[string]bool) error {
	lines, err := readCrontabLines()
	if err != nil {
		return err
	}
	var kept []string
	for _, line := range lines {
		if drop[line] {
			continue
		}
		if id, ok := crontabOwnedID(line); ok && drop[ownedCrontabMarker+id] {
			continue
		}
		kept = append(kept, line)
	}
	kept = append(kept, add...)
	text := strings.Join(kept, "\n")
	if text != "" {
		text += "\n"
	}
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab install: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// legacyCronLines returns the crontab lines the old `tackroom cron` wrote
// for mode ("pull" or "sync deps"; "deps update" is the old spelling).
func legacyCronLines(mode string) ([]string, error) {
	lines, err := readCrontabLines()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range lines {
		m := legacyCronLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		if m[1] == mode || (mode == "sync deps" && m[1] == "deps update") {
			out = append(out, line)
		}
	}
	return out, nil
}
