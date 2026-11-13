package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// The Crons tab's actions (docs/crons.md, "View actions"): run, pause and
// resume a launchd job on any scanned host, and change the schedule of an
// owned job on this machine through its crons.jobs entry and `cron apply`.

type cronActionRequest struct {
	Host     string `json:"host"`
	Source   string `json:"source"`
	ID       string `json:"id"`
	Action   string `json:"action"` // run, pause, resume, schedule
	Schedule string `json:"schedule,omitempty"`
}

// launchdActionScript runs in the user's gui domain, locally or over SSH.
// $1 is the action, $2 the label, $3 the plist path. Pause disables the job,
// so it stays off across logins, and unloads it; resume reverses both.
const launchdActionScript = `d="gui/$(id -u)"
case "$1" in
run) launchctl kickstart "$d/$2" ;;
pause) launchctl disable "$d/$2" && { launchctl bootout "$d/$2" 2>/dev/null || ! launchctl print "$d/$2" >/dev/null 2>&1 || { echo "disabled, but $2 is still loaded" >&2; exit 1; }; } ;;
resume) launchctl enable "$d/$2" && { launchctl print "$d/$2" >/dev/null 2>&1 || launchctl bootstrap "$d" "$3"; } ;;
*) echo "unknown action: $1" >&2; exit 2 ;;
esac
`

// cronActionMu serializes actions, so two schedule edits never interleave
// their manifest writes.
var cronActionMu sync.Mutex

func (s *configWebServer) handleCronAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !s.authorizeAPI(w, r, true) {
		return
	}
	var req cronActionRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	// Act only on a job the last scan reported, addressed by what it reported,
	// never by a label or path taken from the request.
	viewCrons.Lock()
	var job cronJob
	found := false
	for _, candidate := range viewCrons.result.Jobs {
		if candidate.Host == req.Host && candidate.Source == req.Source && candidate.ID == req.ID {
			job, found = candidate, true
			break
		}
	}
	viewCrons.Unlock()
	if !found {
		writeAPIError(w, http.StatusConflict, "stale_crons", "this job is not in the last scan; collect again")
		return
	}
	out, err := doCronAction(job, req.Action, req.Schedule, s.doc.sharedPath)
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "cron_action_failed", err.Error())
		return
	}
	fresh := refreshedCronJob(s.doc.sharedPath, job)
	if fresh != nil {
		patchCronScan(func(candidate cronJob) bool { return sameCronJob(candidate, job) }, fresh)
	} else {
		invalidateCronScan()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"output": out, "job": fresh})
}

func sameCronJob(a, b cronJob) bool { return a.Host == b.Host && a.Source == b.Source && a.ID == b.ID }

// refreshedCronJob scans the job's host again and returns the job's record as
// it now is, so a client can redraw that one row without a full collect. nil
// when the scan fails or no longer lists the job.
func refreshedCronJob(configPath string, job cronJob) *cronJob {
	scan, err := collectCrons(configPath, []string{job.Host})
	if err != nil {
		return nil
	}
	for i := range scan.Jobs {
		if sameCronJob(scan.Jobs[i], job) {
			return &scan.Jobs[i]
		}
	}
	return nil
}

// runCronActionCommand is `tackroom cron run|pause|resume ID` and
// `tackroom cron schedule ID EXPR`: the view's actions for scripts and for
// clients such as the Herdr mod. It collects the job's host first, so it acts
// on what the scan reports, like the view.
func runCronActionCommand(action string, args []string) error {
	fs := flag.NewFlagSet("cron "+action, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "Path to tackroom YAML config")
	host := fs.String("host", "local", "Host the job runs on: local or an SSH alias")
	source := fs.String("source", "launchd", "Scheduler the job lives in")
	if err := fs.Parse(args); err != nil {
		return err
	}
	want := 1
	if action == "schedule" {
		want = 2
	}
	if fs.NArg() != want {
		if action == "schedule" {
			return errors.New(`usage: tackroom cron schedule [--host HOST] ID "MIN HOUR DOM MON DOW"`)
		}
		return fmt.Errorf("usage: tackroom cron %s [--host HOST] ID", action)
	}
	result, err := collectCrons(*configPath, []string{*host})
	if err != nil {
		return err
	}
	id := fs.Arg(0)
	for _, job := range result.Jobs {
		if job.Host == *host && job.Source == *source && job.ID == id {
			out, err := doCronAction(job, action, fs.Arg(1), *configPath)
			if out != "" {
				fmt.Println(out)
			}
			return err
		}
	}
	return fmt.Errorf("no %s job %q on %s", *source, id, *host)
}

// doCronAction runs one action on a collected job. configPath is the shared
// config; a schedule change edits the file that declares the job.
func doCronAction(job cronJob, action, schedule, configPath string) (string, error) {
	if job.Source != "launchd" || job.Unit == "" {
		return "", errors.New("actions work on launchd jobs only")
	}
	cronActionMu.Lock()
	defer cronActionMu.Unlock()
	switch action {
	case "run", "pause", "resume":
		return runLaunchdAction(job, action)
	case "schedule":
		switch {
		case !job.Owned || job.Host != "local":
			return "", errors.New("only jobs that tackroom owns on this machine take a new schedule here")
		case job.State == "disabled":
			return "", errors.New("resume the job before changing its schedule")
		}
		return setOwnedCronSchedule(configPath, job.ID, schedule)
	}
	return "", fmt.Errorf("unknown action %q", action)
}

func runLaunchdAction(job cronJob, action string) (string, error) {
	args := []string{action, job.Unit, job.Plist}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if job.Host == "local" {
		cmd = exec.CommandContext(ctx, "sh", append([]string{"-s", "--"}, args...)...)
	} else {
		quoted := make([]string, len(args))
		for i, arg := range args {
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
		}
		cmd = exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", job.Host, "sh -s -- "+strings.Join(quoted, " "))
	}
	cmd.Stdin = strings.NewReader(launchdActionScript)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s on %s: %s", action, job.Unit, job.Host, commandFailure(out, err))
	}
	return strings.TrimSpace(string(out)), nil
}

// setOwnedCronSchedule changes the schedule of an owned job on this machine:
// an edit of crons.jobs.<id>.schedule in the file that declares the job (a
// tackroom.local.yaml entry replaces the shared one by id, so it wins), then
// `cron apply`, which replaces the unit. See crons_jobs.go.
func setOwnedCronSchedule(sharedPath, id, schedule string) (string, error) {
	res, err := changeCronJob(sharedPath, cronJobChange{Action: "edit", ID: id, Schedule: &schedule, LocalOnly: true})
	return strings.Join(res.Plan, "\n"), err
}
