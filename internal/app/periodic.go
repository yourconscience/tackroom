package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Periodic sync: OMP, Codex and Pi read a generated copy of the root
// instruction files that only `tackroom sync` refreshes, so setup installs the
// built-in `tackroom-pull` job and status and doctor warn when it is not
// running.

const periodicSyncID = "tackroom-pull"

// Replaced in tests, so no test installs into or reads the real scheduler.
var (
	installPeriodicSync = runCron
	readPeriodicSync    = scanPeriodicSync
)

func setupPlannedActions(opts runOptions) []string {
	if opts.NoCron {
		return nil
	}
	return []string{"install the periodic sync job " + periodicSyncID + " (tackroom pull every " + cronIntervalDefault + "); skip with --no-cron"}
}

// periodicSyncEnabled reports whether the config opts into periodic sync. The
// key defaults to true; only an explicit false disables it.
func periodicSyncEnabled(cfg config) bool {
	return cfg.PeriodicSync == nil || *cfg.PeriodicSync
}

// scanPeriodicSync reads the built-in job on this machine only. job is nil
// when it is not installed; ok is false when the scheduler state could not be
// read, which must never be reported as "not installed".
func scanPeriodicSync() (job *cronJob, ok bool) {
	sections, err := runCronProbe("local", periodicSyncID)
	if err != nil {
		return nil, false
	}
	return periodicSyncFromProbe(sections)
}

func periodicSyncFromProbe(sections []probeSection) (job *cronJob, ok bool) {
	p := newProbeData("local", sections)
	var sources []string
	switch p.os {
	case "Darwin":
		sources = []string{"launchd", "crontab"}
	case "Linux":
		sources = []string{"systemd", "crontab"}
	default:
		return nil, false
	}
	unreadable := false
	for _, source := range sources {
		jobs, access := p.collect(source)
		unreadable = unreadable || access.State == "unreadable"
		for _, j := range jobs {
			if id, owned := ownedCronID(j); owned && id == periodicSyncID {
				if source == "crontab" {
					// A crontab line carries PATH entries the probe also
					// stats; they are not the job's own paths.
					j.Problems = nil
				}
				jobs := []cronJob{j}
				annotateCronJobs(jobs, time.Now())
				j = jobs[0]
				return &j, true
			}
		}
	}
	return nil, !unreadable
}

func periodicSyncMessage(job *cronJob, enabled bool) string {
	switch {
	case job == nil && !enabled:
		// The user opted out; a missing job is expected, not a problem.
		return ""
	case job == nil:
		return "periodic sync is off, so edited root instruction files reach Codex, Pi and OMP only on a manual `tackroom sync`. Fix: tackroom cron (or set periodic_sync: false in tackroom.local.yaml)"
	case job.Health == "paused" && job.Source == "systemd":
		return "periodic sync is paused or disabled. Fix: systemctl --user enable --now " + ownedSystemdPrefix + periodicSyncID + ".timer"
	case job.Health == "paused":
		return "periodic sync is paused or disabled. Fix: tackroom cron resume " + periodicSyncID
	case job.Health == "fail":
		detail := job.State
		if len(job.Problems) > 0 {
			detail = job.Problems[0]
		}
		return "periodic sync job is broken (" + detail + "). Fix: tackroom cron"
	case job.Health == "warn":
		last := job.LastStatus
		if job.LastExit != nil {
			last = fmt.Sprintf("exit %d", *job.LastExit)
		}
		return "periodic sync last run failed (" + last + "). Fix: run tackroom pull to see the error"
	}
	return ""
}

func checkPeriodicSync(repoRoot, home string, cfg config) checkResult {
	job, ok := readPeriodicSync()
	var problems []string
	if ok {
		if msg := periodicSyncMessage(job, periodicSyncEnabled(cfg)); msg != "" {
			problems = append(problems, msg)
		}
	}
	if msg := pullFailureProblem(repoRoot, home); msg != "" {
		problems = append(problems, msg)
	}
	switch {
	case !ok && len(problems) == 0:
		return checkResult{"periodic sync", checkStatusPass, "scheduler state not readable; not checked"}
	case len(problems) > 0:
		return checkResult{"periodic sync", checkStatusWarn, strings.Join(problems, "; ")}
	case !periodicSyncEnabled(cfg):
		return checkResult{"periodic sync", checkStatusPass, "disabled by periodic_sync: false"}
	}
	return checkResult{"periodic sync", checkStatusPass, periodicSyncID + " installed and healthy"}
}

// periodicSyncProblems collects every one-line problem status and doctor should
// surface: the job state (unless the user opted out of a missing job) and the
// last pull failure.
func periodicSyncProblems(repoRoot, home string, cfg config) []string {
	var problems []string
	if job, ok := readPeriodicSync(); ok {
		if msg := periodicSyncMessage(job, periodicSyncEnabled(cfg)); msg != "" {
			problems = append(problems, msg)
		}
	}
	if msg := pullFailureProblem(repoRoot, home); msg != "" {
		problems = append(problems, msg)
	}
	return problems
}

// pullState is tackroom pull's last git step, stored machine-locally so status
// and doctor can surface a machine whose config root has stopped receiving
// updates from its remote.
type pullState struct {
	// LastAttempt is the RFC3339 UTC time of the last git pull attempt.
	LastAttempt string `json:"last_attempt"`
	// Failed is true when the last attempt failed.
	Failed bool `json:"failed"`
	// Error is the first line of git's message on the last failure.
	Error string `json:"error,omitempty"`
	// LastSuccess is the RFC3339 UTC time of the last successful pull; empty
	// when the pull has never succeeded.
	LastSuccess string `json:"last_success,omitempty"`
}

func pullStatePath(home string) string {
	return filepath.Join(tackroomStateDir(home), "pull.json")
}

func readPullState(home string) (pullState, error) {
	data, err := os.ReadFile(pullStatePath(home))
	if errors.Is(err, fs.ErrNotExist) {
		return pullState{}, nil
	}
	if err != nil {
		return pullState{}, err
	}
	var state pullState
	if err := json.Unmarshal(data, &state); err != nil {
		return pullState{}, err
	}
	return state, nil
}

func writePullState(home string, state pullState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicConfigWrite(pullStatePath(home), data, 0o644)
}

// gitErrorLine returns the first meaningful error line of git's output (the
// line that starts with "fatal:" or "error:"), falling back to the first line
// when git printed no such line.
func gitErrorLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "fatal:") || strings.HasPrefix(line, "error:") {
			return line
		}
	}
	return firstLine(strings.TrimSpace(out))
}

// shouldPull reports whether `tackroom pull` should run the git step. It
// returns false (quietly, not an error) when the config root is not a git
// repository, has no remote, or its current branch has no upstream. These are
// detected with git plumbing commands, never by parsing pull's error text.
func shouldPull(repoRoot string) bool {
	if out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "--is-inside-work-tree").Output(); err != nil || strings.TrimSpace(string(out)) != "true" {
		return false
	}
	if out, err := exec.Command("git", "-C", repoRoot, "remote").Output(); err != nil || strings.TrimSpace(string(out)) == "" {
		return false
	}
	if err := exec.Command("git", "-C", repoRoot, "rev-parse", "--abbrev-ref", "@{u}").Run(); err != nil {
		return false
	}
	return true
}

// pullFailureProblem is the one-line warning for a failed git pull, or "" when
// the last pull succeeded or none was recorded.
func pullFailureProblem(repoRoot, home string) string {
	state, err := readPullState(home)
	if err != nil || !state.Failed {
		return ""
	}
	since := state.LastSuccess
	if since == "" {
		since = "never succeeded"
	}
	return fmt.Sprintf("last pull failed (%s); failing since %s. Fix: git -C %s status", state.Error, since, repoRoot)
}
