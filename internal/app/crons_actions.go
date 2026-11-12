package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
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
	viewCrons.Lock()
	viewCrons.at = time.Time{} // the next GET collects again
	viewCrons.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"output": out})
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

// setOwnedCronSchedule rewrites crons.jobs.<id>.schedule in the file that
// declares the job (a tackroom.local.yaml entry replaces the shared one by id,
// so it wins even without a schedule of its own), then runs `cron apply`,
// which replaces the unit. The edit holds the cron apply lock, so two edits
// never interleave, and keeps the file's permissions.
func setOwnedCronSchedule(sharedPath, id, schedule string) (string, error) {
	schedule = strings.TrimSpace(schedule)
	if _, err := parseOwnedSchedule(schedule); err != nil {
		return "", err
	}
	if _, err := cronToCalendarIntervals(schedule); err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	shared, err := resolveConfigPath(sharedPath, home)
	if err != nil {
		return "", err
	}
	path, err := writeOwnedCronSchedule(shared, id, schedule, home)
	if err != nil {
		return "", err
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "cron", "apply", "--config", shared).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("saved the schedule in %s, but cron apply failed: %s", displayPath(path, home), commandFailure(out, err))
	}
	return strings.TrimSpace(string(out)), nil
}

// writeOwnedCronSchedule edits the declaring file under the cron apply lock
// (released before `cron apply` takes it) and returns that file's path.
func writeOwnedCronSchedule(shared, id, schedule, home string) (string, error) {
	unlock, err := lockCronApply(home)
	if err != nil {
		return "", err
	}
	defer unlock()
	path := filepath.Join(filepath.Dir(shared), "tackroom.local.yaml")
	root, job, err := cronJobNode(path, id)
	if err != nil {
		return "", err
	}
	if job == nil {
		path = shared
		if root, job, err = cronJobNode(path, id); err != nil {
			return "", err
		}
	}
	if job == nil {
		return "", fmt.Errorf("crons.jobs.%s is not declared in %s or %s", id, filepath.Base(shared), "tackroom.local.yaml")
	}
	node := dshMappingValue(job, "schedule")
	if node == nil {
		node = &yaml.Node{Kind: yaml.ScalarNode}
		job.Content = append(job.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "schedule"}, node)
	} else if node.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("crons.jobs.%s.schedule in %s is not a string", id, path)
	}
	node.Value, node.Tag, node.Style = schedule, "!!str", yaml.DoubleQuotedStyle
	data, err := marshalConfigYAML(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if err := atomicConfigWrite(path, data, info.Mode().Perm()); err != nil {
		return "", err
	}
	return path, nil
}

// cronJobNode parses path and returns its document and the mapping at
// crons.jobs.<id>, or a nil job when the file or the entry is absent.
func cronJobNode(path, id string) (*yaml.Node, *yaml.Node, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(root.Content) == 0 {
		return &root, nil, nil
	}
	node := root.Content[0]
	for _, key := range []string{"crons", "jobs", id} {
		if node = dshMappingValue(node, key); node == nil {
			return &root, nil, nil
		}
	}
	if node.Kind != yaml.MappingNode {
		return &root, nil, fmt.Errorf("crons.jobs.%s in %s is not a mapping", id, path)
	}
	return &root, node, nil
}
