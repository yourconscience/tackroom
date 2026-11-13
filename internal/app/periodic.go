package app

import (
	"fmt"
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

// periodicSyncProblem is the one-line problem, ending in the command that
// fixes it, or "" when the job is healthy or its state is unknown.
func periodicSyncProblem() string {
	job, ok := readPeriodicSync()
	if !ok {
		return ""
	}
	return periodicSyncMessage(job)
}

func periodicSyncMessage(job *cronJob) string {
	switch {
	case job == nil:
		return "periodic sync is off, so edited root instruction files reach Codex, Pi and OMP only on a manual `tackroom sync`. Fix: tackroom cron"
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

func checkPeriodicSync() checkResult {
	job, ok := readPeriodicSync()
	switch {
	case !ok:
		return checkResult{"periodic sync", checkStatusPass, "scheduler state not readable; not checked"}
	case periodicSyncMessage(job) != "":
		return checkResult{"periodic sync", checkStatusWarn, periodicSyncMessage(job)}
	}
	return checkResult{"periodic sync", checkStatusPass, periodicSyncID + " installed and healthy"}
}
