package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"gopkg.in/yaml.v3"
)

// `tackroom cron collect` observes every scheduled job on every configured
// host. Plain `tackroom cron` still installs tackroom's own self-update entry.
// The view's Crons tab shows the same result. See docs/crons.md.

//go:embed crons_probe.sh
var cronsProbeScript string

type cronJob struct {
	Host       string   `json:"host"`
	Source     string   `json:"source"`
	ID         string   `json:"id"`
	Name       string   `json:"name,omitempty"`
	Schedule   string   `json:"schedule,omitempty"`
	TZ         string   `json:"tz,omitempty"`
	Command    string   `json:"command,omitempty"`
	Log        string   `json:"log,omitempty"`
	Runs       *int     `json:"runs,omitempty"`
	LastExit   *int     `json:"last_exit,omitempty"`
	LastStatus string   `json:"last_status,omitempty"`
	LastRun    string   `json:"last_run,omitempty"`
	State      string   `json:"state"`
	CreatedBy  string   `json:"created_by,omitempty"`
	Connectors []string `json:"connectors,omitempty"`
	Effect     string   `json:"effect,omitempty"`
	Problems   []string `json:"problems,omitempty"`
	// Inferred names the fields derived by tackroom rather than read from the
	// scheduler or declared in the manifest.
	Inferred []string `json:"inferred,omitempty"`
}

type cronAccess struct {
	Host   string `json:"host"`
	Source string `json:"source"`
	State  string `json:"state"` // found, empty, unreadable, not_scanned
	Jobs   int    `json:"jobs"`
	Detail string `json:"detail,omitempty"`
}

type cronsScanResult struct {
	ScannedAt string       `json:"scanned_at"`
	Jobs      []cronJob    `json:"jobs"`
	Access    []cronAccess `json:"access"`
}

type cronsManifest struct {
	Hosts []string                   `yaml:"hosts"`
	Jobs  map[string]cronManifestJob `yaml:"jobs"`
}

type cronManifestJob struct {
	Host       string   `yaml:"host"`
	Source     string   `yaml:"source"`
	CreatedBy  string   `yaml:"created_by"`
	Connectors []string `yaml:"connectors"`
	Effect     string   `yaml:"effect"`
}

var cronSources = []string{"launchd", "crontab", "systemd", "hermes", "codex", "claude-desktop", "claude-cloud", "openbot"}

func runCronCollect(args []string) error {
	fs := flag.NewFlagSet("cron collect", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "Path to tackroom YAML config")
	hostsFlag := fs.String("hosts", "", "Comma-separated hosts: local and SSH aliases (default: crons.hosts, else local)")
	table := fs.Bool("table", false, "Print a table instead of JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("cron collect does not accept positional arguments")
	}
	result, err := collectCrons(*configPath, splitCSV(*hostsFlag))
	if err != nil {
		return err
	}
	if *table {
		printCronsTable(result)
		return nil
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

// collectCrons scans hosts, or crons.hosts from the config, else this machine.
// Shared by `tackroom cron collect` and the view's Crons tab.
func collectCrons(configPath string, hosts []string) (cronsScanResult, error) {
	manifest, err := loadCronsManifest(configPath)
	if err != nil {
		return cronsScanResult{}, err
	}
	if len(hosts) == 0 {
		hosts = manifest.Hosts
	}
	if len(hosts) == 0 {
		hosts = []string{"local"}
	}
	return scanCrons(hosts, manifest), nil
}

func splitCSV(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// loadCronsManifest reads only the crons: section; a missing config is fine.
func loadCronsManifest(override string) (cronsManifest, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return cronsManifest{}, err
	}
	path, err := resolveConfigPath(override, home)
	if err != nil {
		return cronsManifest{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && strings.TrimSpace(override) == "" {
		return cronsManifest{}, nil
	}
	if err != nil {
		return cronsManifest{}, err
	}
	var doc struct {
		Crons cronsManifest `yaml:"crons"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return cronsManifest{}, fmt.Errorf("parse %s: %w", path, err)
	}
	// Hosts differ per machine, so tackroom.local.yaml may set its own
	// crons.hosts; its jobs entries override the shared ones by id.
	localPath := filepath.Join(filepath.Dir(path), "tackroom.local.yaml")
	if data, err := os.ReadFile(localPath); err == nil {
		var local struct {
			Crons cronsManifest `yaml:"crons"`
		}
		if err := yaml.Unmarshal(data, &local); err != nil {
			return cronsManifest{}, fmt.Errorf("parse %s: %w", localPath, err)
		}
		if len(local.Crons.Hosts) > 0 {
			doc.Crons.Hosts = local.Crons.Hosts
		}
		for id, job := range local.Crons.Jobs {
			if doc.Crons.Jobs == nil {
				doc.Crons.Jobs = map[string]cronManifestJob{}
			}
			doc.Crons.Jobs[id] = job
		}
	}
	return doc.Crons, nil
}

func scanCrons(hosts []string, manifest cronsManifest) cronsScanResult {
	results := make([]hostScan, len(hosts))
	var wg sync.WaitGroup
	for i, host := range hosts {
		wg.Add(1)
		go func(i int, host string) {
			defer wg.Done()
			results[i] = scanCronHost(host)
		}(i, host)
	}
	wg.Wait()

	out := cronsScanResult{ScannedAt: time.Now().Format(time.RFC3339), Jobs: []cronJob{}}
	for _, r := range results {
		out.Jobs = append(out.Jobs, r.jobs...)
		out.Access = append(out.Access, r.access...)
	}
	out.Jobs = mergeCronsManifest(out.Jobs, manifest, out.Access)
	return out
}

// mergeCronsManifest adds declared fields to matching records. An entry
// matches by id, narrowed by host and source when set. A declared job that is
// not found is reported missing only when every place it could live was read.
func mergeCronsManifest(jobs []cronJob, manifest cronsManifest, access []cronAccess) []cronJob {
	ids := make([]string, 0, len(manifest.Jobs))
	for id := range manifest.Jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		entry := manifest.Jobs[id]
		matched := false
		for i := range jobs {
			job := &jobs[i]
			if job.ID != id || (entry.Host != "" && entry.Host != job.Host) || (entry.Source != "" && entry.Source != job.Source) {
				continue
			}
			matched = true
			if entry.CreatedBy != "" {
				job.CreatedBy = entry.CreatedBy
				job.Inferred = removeString(job.Inferred, "created_by")
			}
			if len(entry.Connectors) > 0 {
				job.Connectors = entry.Connectors
				job.Inferred = removeString(job.Inferred, "connectors")
			}
			job.Effect = entry.Effect
		}
		if matched || !manifestEntryRead(entry, access) {
			continue
		}
		jobs = append(jobs, cronJob{
			Host: entry.Host, Source: entry.Source, ID: id, State: "missing", CreatedBy: entry.CreatedBy,
			Connectors: entry.Connectors, Effect: entry.Effect,
			Problems: []string{"declared in crons.jobs but not found on any scanned host"},
		})
	}
	return jobs
}

// manifestEntryRead reports whether every host and source a declared job
// could live on was read. With no source set, documented gaps (not_scanned)
// do not count against it, but an unreadable source does.
func manifestEntryRead(entry cronManifestJob, access []cronAccess) bool {
	seen := false
	for _, a := range access {
		if entry.Host != "" && a.Host != entry.Host {
			continue
		}
		if entry.Source != "" && a.Source != entry.Source {
			continue
		}
		seen = true
		if entry.Source != "" && a.State != "found" && a.State != "empty" {
			return false
		}
		if a.State == "unreadable" || (a.State == "not_scanned" && strings.HasPrefix(a.Detail, "probe failed")) {
			return false
		}
	}
	return seen
}

func removeString(list []string, value string) []string {
	out := list[:0]
	for _, item := range list {
		if item != value {
			out = append(out, item)
		}
	}
	return out
}

type probeSection struct {
	Kind, Key, Body string
	RC              int
}

type hostScan struct {
	host   string
	jobs   []cronJob
	access []cronAccess
}

func scanCronHost(host string) hostScan {
	scan := hostScan{host: host}
	sections, err := runCronProbe(host)
	if err != nil {
		for _, source := range cronSources {
			scan.access = append(scan.access, cronAccess{Host: host, Source: source, State: "not_scanned", Detail: err.Error()})
		}
		return scan
	}
	p := newProbeData(host, sections)
	for _, source := range cronSources {
		jobs, access := p.collect(source)
		access.Host, access.Source, access.Jobs = host, source, len(jobs)
		if access.State == "" {
			access.State = "empty"
			if len(jobs) > 0 {
				access.State = "found"
			}
		}
		scan.jobs = append(scan.jobs, jobs...)
		scan.access = append(scan.access, access)
	}
	return scan
}

func runCronProbe(host string) ([]probeSection, error) {
	tokenBytes := make([]byte, 12)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	token := "@@" + hex.EncodeToString(tokenBytes)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var cmd *exec.Cmd
	if host == "local" {
		cmd = exec.CommandContext(ctx, "sh", "-s", "--", token)
	} else {
		cmd = exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", host, "sh -s -- "+token)
	}
	cmd.Stdin = strings.NewReader(cronsProbeScript)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("probe failed: %s", firstLine(msg))
	}
	return parseProbeOutput(string(out), token), nil
}

func parseProbeOutput(out, token string) []probeSection {
	var sections []probeSection
	var body strings.Builder
	current := -1
	flush := func() {
		if current >= 0 {
			sections[current].Body = strings.TrimSuffix(body.String(), "\n")
		}
		body.Reset()
	}
	for _, line := range strings.SplitAfter(out, "\n") {
		trimmed := strings.TrimSuffix(line, "\n")
		if !strings.HasPrefix(trimmed, token+" ") {
			body.WriteString(line)
			continue
		}
		header := strings.TrimPrefix(trimmed, token+" ")
		kind, key, _ := strings.Cut(header, "\t")
		if kind == "rc" {
			flush()
			if current >= 0 {
				sections[current].RC, _ = strconv.Atoi(key)
			}
			current = -1
			continue
		}
		flush()
		sections = append(sections, probeSection{Kind: kind, Key: key, RC: -1})
		current = len(sections) - 1
	}
	flush()
	return sections
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

type probeData struct {
	host     string
	os       string
	tz       string
	now      int64
	sections []probeSection
	mtimes   map[string]int64 // path -> mtime; missing paths absent
	statted  map[string]bool
}

func newProbeData(host string, sections []probeSection) *probeData {
	p := &probeData{host: host, sections: sections, mtimes: map[string]int64{}, statted: map[string]bool{}}
	for _, s := range sections {
		switch s.Kind {
		case "os":
			p.os = s.Body
		case "tz":
			if _, zone, ok := strings.Cut(strings.TrimSpace(s.Body), "zoneinfo/"); ok && s.RC == 0 {
				p.tz = zone
			}
		case "now":
			p.now, _ = strconv.ParseInt(strings.TrimSpace(s.Body), 10, 64)
		case "mtime":
			p.statted[s.Key] = true
			if s.RC == 0 {
				if v, err := strconv.ParseInt(strings.TrimSpace(s.Body), 10, 64); err == nil {
					p.mtimes[s.Key] = v
				}
			}
		}
	}
	return p
}

func (p *probeData) find(kind string) []probeSection {
	var out []probeSection
	for _, s := range p.sections {
		if s.Kind == kind {
			out = append(out, s)
		}
	}
	return out
}

func (p *probeData) one(kind, key string) (probeSection, bool) {
	for _, s := range p.sections {
		if s.Kind == kind && s.Key == key {
			return s, true
		}
	}
	return probeSection{}, false
}

func (p *probeData) collect(source string) ([]cronJob, cronAccess) {
	switch source {
	case "launchd":
		if p.os != "Darwin" {
			return nil, cronAccess{State: "not_scanned", Detail: "macOS only"}
		}
		return p.launchd()
	case "crontab":
		return p.crontab()
	case "systemd":
		if p.os != "Linux" {
			return nil, cronAccess{State: "not_scanned", Detail: "Linux only"}
		}
		return p.systemd()
	case "hermes":
		return p.hermes()
	case "codex":
		return p.codex()
	case "claude-desktop":
		if p.os != "Darwin" {
			return nil, cronAccess{State: "not_scanned", Detail: "desktop app state is read on macOS only"}
		}
		return p.claudeDesktop()
	case "claude-cloud":
		return nil, cronAccess{State: "not_scanned", Detail: "cloud routines have no on-disk state; list them from a Claude session"}
	case "openbot":
		return nil, cronAccess{State: "not_scanned", Detail: "routines are not readable from disk"}
	}
	return nil, cronAccess{State: "not_scanned"}
}

func (p *probeData) newJob(source, id string) cronJob {
	job := cronJob{Host: p.host, Source: source, ID: id, Name: id, TZ: p.tz, State: "scheduled"}
	if p.tz != "" {
		job.Inferred = append(job.Inferred, "tz")
	}
	return job
}

func ageString(sec int64) string {
	switch {
	case sec < 120:
		return fmt.Sprintf("%ds", sec)
	case sec < 7200:
		return fmt.Sprintf("%dm", sec/60)
	case sec < 172800:
		return fmt.Sprintf("%dh", sec/3600)
	}
	return fmt.Sprintf("%dd", sec/86400)
}

func unixString(sec int64) string {
	if sec <= 0 {
		return ""
	}
	return time.Unix(sec, 0).Format(time.RFC3339)
}

// pathTokenPattern mirrors paths() in crons_probe.sh.
var pathTokenPattern = regexp.MustCompile(`(?:^|[\s>="'(:])(~?/[A-Za-z0-9._/+@%-]+)`)
var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var redirectPattern = regexp.MustCompile(`>>?\s*(~?/[A-Za-z0-9._/+@%-]+)`)

// missingPaths reports path tokens in text that the probe stat'ed and did
// not find, excluding redirect targets (a log a job creates itself).
func (p *probeData) missingPaths(text string) []string {
	skip := map[string]bool{}
	for _, m := range redirectPattern.FindAllStringSubmatch(text, -1) {
		skip[m[1]] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range pathTokenPattern.FindAllStringSubmatch(text, -1) {
		path := m[1]
		if skip[path] || seen[path] || !p.statted[path] {
			continue
		}
		seen[path] = true
		if _, ok := p.mtimes[path]; !ok && !p.hasLongerPath(path) {
			out = append(out, "path does not exist: "+path)
		}
	}
	return out
}

// hasLongerPath reports whether path is the leading word of an existing path
// with spaces, as in an unquoted "Application Support" directory.
func (p *probeData) hasLongerPath(path string) bool {
	for other := range p.mtimes {
		if strings.HasPrefix(other, path+" ") {
			return true
		}
	}
	return false
}

func (p *probeData) latestMtime(paths ...string) int64 {
	var latest int64
	for _, path := range paths {
		if v := p.mtimes[path]; v > latest {
			latest = v
		}
	}
	return latest
}

// launchd

var launchdKnownKeys = strings.Fields(`Label Disabled UserName GroupName inetdCompatibility LimitLoadToHosts
LimitLoadFromHosts LimitLoadToSessionType LimitLoadToHardware LimitLoadFromHardware Program ProgramArguments
EnableGlobbing EnableTransactions EnablePressuredExit OnDemand ServiceIPC KeepAlive RunAtLoad RootDirectory
WorkingDirectory EnvironmentVariables Umask TimeOut ExitTimeOut ThrottleInterval InitGroups WatchPaths
QueueDirectories StartOnMount StartInterval StartCalendarInterval StandardInPath StandardOutPath
StandardErrorPath Debug WaitForDebugger SoftResourceLimits HardResourceLimits Nice ProcessType
AbandonProcessGroup LowPriorityIO LowPriorityBackgroundIO MaterializeDatalessFiles LaunchOnlyOnce MachServices
Sockets LaunchEvents SessionCreate LegacyTimers AssociatedBundleIdentifiers BundleProgram POSIXSpawnType
KeepAliveMode MinimalBootProfile ServiceDescription`)

var launchdCalendarKeys = []string{"Minute", "Hour", "Day", "Weekday", "Month"}

func caseMatch(key string, known []string) (string, bool) {
	for _, k := range known {
		if k == key {
			return k, true
		}
	}
	for _, k := range known {
		if strings.EqualFold(k, key) {
			return k, false
		}
	}
	return "", false
}

func keyProblems(dict map[string]any, known []string, where string) []string {
	keys := make([]string, 0, len(dict))
	for k := range dict {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, key := range keys {
		want, exact := caseMatch(key, known)
		switch {
		case exact:
		case want != "":
			out = append(out, fmt.Sprintf("%skey %q is ignored by launchd; did you mean %q?", where, key, want))
		default:
			out = append(out, fmt.Sprintf("%sunknown key %q", where, key))
		}
	}
	return out
}

func (p *probeData) launchd() ([]cronJob, cronAccess) {
	disabled := map[string]bool{}
	if s, ok := p.one("launchd-disabled", ""); ok {
		for _, line := range strings.Split(s.Body, "\n") {
			label, state, ok := strings.Cut(strings.TrimSpace(line), "=>")
			state = strings.TrimSpace(state)
			if ok && (state == "disabled" || state == "true") {
				disabled[strings.Trim(strings.TrimSpace(label), `"`)] = true
			}
		}
	}
	var jobs []cronJob
	var unreadable []string
	for _, s := range p.find("plist") {
		var plist map[string]any
		if s.RC != 0 || json.Unmarshal([]byte(s.Body), &plist) != nil {
			unreadable = append(unreadable, filepath.Base(s.Key)+": "+firstLine(s.Body))
			continue
		}
		scheduled := false
		for key := range plist {
			if strings.EqualFold(key, "StartCalendarInterval") || strings.EqualFold(key, "StartInterval") {
				scheduled = true
			}
		}
		label, _ := plist["Label"].(string)
		if label == "" {
			label = strings.TrimSuffix(filepath.Base(s.Key), ".plist")
		}
		job := p.newJob("launchd", label)
		job.Problems = keyProblems(plist, launchdKnownKeys, "")

		var schedules []string
		if v, ok := plist["StartInterval"]; ok {
			schedules = append(schedules, fmt.Sprintf("every %vs", v))
		}
		var entries []any
		switch v := plist["StartCalendarInterval"].(type) {
		case map[string]any:
			entries = []any{v}
		case []any:
			entries = v
		}
		for _, e := range entries {
			dict, _ := e.(map[string]any)
			fields := map[string]string{}
			for k, v := range dict {
				if want, exact := caseMatch(k, launchdCalendarKeys); exact {
					fields[want] = fmt.Sprint(v)
				}
			}
			job.Problems = append(job.Problems, keyProblems(dict, launchdCalendarKeys, "StartCalendarInterval: ")...)
			if len(fields) == 0 {
				job.Problems = append(job.Problems, "StartCalendarInterval entry has no recognized key; launchd fires it every minute")
			}
			cronField := func(k string) string {
				if v, ok := fields[k]; ok {
					return v
				}
				return "*"
			}
			schedules = append(schedules, strings.Join([]string{cronField("Minute"), cronField("Hour"), cronField("Day"), cronField("Month"), cronField("Weekday")}, " "))
		}
		job.Schedule = strings.Join(schedules, "; ")

		var args []string
		if list, ok := plist["ProgramArguments"].([]any); ok {
			for _, a := range list {
				args = append(args, fmt.Sprint(a))
			}
		}
		if prog, ok := plist["Program"].(string); ok && len(args) == 0 {
			args = []string{prog}
		}
		job.Command = shellJoin(args)
		checked := append([]string{}, args...)
		if wd, ok := plist["WorkingDirectory"].(string); ok {
			checked = append(checked, wd)
		}
		for _, arg := range checked {
			if _, found := p.mtimes[arg]; strings.HasPrefix(arg, "/") && p.statted[arg] {
				if !found {
					job.Problems = append(job.Problems, "path does not exist: "+arg)
				}
				continue
			}
			job.Problems = append(job.Problems, p.missingPaths(arg)...)
		}

		outPath, _ := plist["StandardOutPath"].(string)
		errPath, _ := plist["StandardErrorPath"].(string)
		job.Log = outPath
		if job.Log == "" {
			job.Log = errPath
		}
		if t := p.latestMtime(outPath, errPath); t > 0 {
			job.LastRun = unixString(t)
			job.Inferred = append(job.Inferred, "last_run")
		}

		running := ""
		if lc, ok := p.one("launchctl", s.Key); !ok || lc.RC != 0 {
			job.State = "not_loaded"
		} else {
			for _, line := range strings.Split(lc.Body, "\n") {
				k, v, ok := strings.Cut(strings.TrimSpace(line), " = ")
				if !ok {
					continue
				}
				switch k {
				case "state":
					if running == "" {
						running = v
					}
				case "runs":
					if n, err := strconv.Atoi(v); err == nil {
						job.Runs = &n
					}
				case "last exit code": // "2", or "78: EX_CONFIG"
					code, _, _ := strings.Cut(v, ":")
					if n, err := strconv.Atoi(code); err == nil {
						job.LastExit = &n
					}
				}
			}
		}
		if disabled[label] || plist["Disabled"] == true {
			job.State = "disabled"
		}
		if !scheduled {
			// Services (KeepAlive, RunAtLoad, WatchPaths) are not schedules;
			// they are reported only when their last exit was non-zero.
			if job.LastExit == nil || *job.LastExit == 0 || job.State == "disabled" {
				continue
			}
			job.Schedule = launchdTrigger(plist)
			job.State, job.LastStatus = "service", running
			job.Inferred = append(job.Inferred, "state")
			if plist["KeepAlive"] != nil && plist["KeepAlive"] != false && job.Runs != nil && *job.Runs >= 10 {
				job.Problems = append(job.Problems, fmt.Sprintf("crash loop: KeepAlive restarted it %d times, last exit %d", *job.Runs, *job.LastExit))
			} else {
				job.Problems = append(job.Problems, fmt.Sprintf("last exit %d", *job.LastExit))
			}
		}
		jobs = append(jobs, job)
	}
	access := cronAccess{}
	if len(unreadable) > 0 {
		access.Detail = "unreadable plists: " + strings.Join(unreadable, "; ")
		if len(jobs) == 0 {
			access.State = "unreadable"
		}
	}
	return jobs, access
}

func launchdTrigger(plist map[string]any) string {
	var out []string
	for _, key := range []string{"KeepAlive", "RunAtLoad", "WatchPaths", "QueueDirectories", "StartOnMount", "MachServices", "Sockets"} {
		if v, ok := plist[key]; ok && v != false {
			out = append(out, key)
		}
	}
	if len(out) == 0 {
		return "on demand"
	}
	return strings.Join(out, ", ")
}

func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\n'\"$&|;<>*?()") {
			quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		} else {
			quoted[i] = a
		}
	}
	return strings.Join(quoted, " ")
}

// crontab

func (p *probeData) crontab() ([]cronJob, cronAccess) {
	s, ok := p.one("crontab", "")
	if !ok {
		return nil, cronAccess{Detail: "crontab not installed"}
	}
	if s.RC != 0 {
		if strings.Contains(s.Body, "no crontab for") {
			return nil, cronAccess{Detail: "no crontab"}
		}
		return nil, cronAccess{State: "unreadable", Detail: firstLine(s.Body)}
	}
	var jobs []cronJob
	tz := ""
	for _, raw := range strings.Split(s.Body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if name, value, ok := strings.Cut(line, "="); ok && envNamePattern.MatchString(strings.TrimSpace(name)) {
			if n := strings.TrimSpace(name); n == "CRON_TZ" || n == "TZ" {
				tz = strings.Trim(strings.TrimSpace(value), `"'`)
			}
			continue
		}
		var schedule, command string
		fields := strings.Fields(line)
		if strings.HasPrefix(line, "@") {
			if fields[0] == "@reboot" || len(fields) < 2 {
				continue // a boot hook, not a schedule
			}
			schedule, command = fields[0], strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		} else {
			if len(fields) < 6 {
				continue
			}
			schedule = strings.Join(fields[:5], " ")
			rest := line
			for i := 0; i < 5; i++ {
				rest = strings.TrimSpace(rest)
				rest = rest[strings.IndexAny(rest, " \t"):]
			}
			command = strings.TrimSpace(rest)
		}
		sum := sha256.Sum256([]byte(schedule + " " + command))
		job := p.newJob("crontab", "crontab-"+hex.EncodeToString(sum[:4]))
		job.Name = cronCommandName(command)
		job.Schedule, job.Command = schedule, command
		if tz != "" {
			job.TZ = tz
			job.Inferred = removeString(job.Inferred, "tz")
		}
		var logs []string
		for _, m := range redirectPattern.FindAllStringSubmatch(command, -1) {
			logs = append(logs, m[1])
		}
		if len(logs) > 0 {
			job.Log = logs[0]
			job.Inferred = append(job.Inferred, "log")
			if t := p.latestMtime(logs...); t > 0 {
				job.LastRun = unixString(t)
				job.Inferred = append(job.Inferred, "last_run")
			}
		}
		job.Problems = p.missingPaths(command)
		jobs = append(jobs, job)
	}
	return jobs, cronAccess{}
}

// cronCommandName names a crontab job after the program the line ends up
// running: the last command, without redirects, with the program's base name.
func cronCommandName(command string) string {
	segment := command
	for _, sep := range []string{";", "&&", "||"} {
		if i := strings.LastIndex(segment, sep); i >= 0 {
			segment = segment[i+len(sep):]
		}
	}
	var words []string
	for _, word := range strings.Fields(segment) {
		if strings.ContainsAny(word, "<>") {
			break
		}
		words = append(words, strings.Trim(word, `"'`))
	}
	if len(words) == 0 {
		return truncate(command, 60)
	}
	words[0] = filepath.Base(words[0])
	return truncate(strings.Join(words, " "), 60)
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

// systemd

func parseShow(body string) map[string][]string {
	out := map[string][]string{}
	for _, line := range strings.Split(body, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			out[k] = append(out[k], v)
		}
	}
	return out
}

// braceField pulls "name=value" out of systemd's "{ a=1 ; b=2 }" property form.
func braceField(value, name string) string {
	for _, part := range strings.Split(strings.Trim(value, "{} "), " ; ") {
		if k, v, ok := strings.Cut(strings.TrimSpace(part), "="); ok && k == name {
			return v
		}
	}
	return ""
}

func (p *probeData) systemd() ([]cronJob, cronAccess) {
	s, ok := p.one("timers", "")
	if !ok {
		return nil, cronAccess{Detail: "systemctl not installed"}
	}
	if s.RC != 0 {
		return nil, cronAccess{State: "unreadable", Detail: firstLine(s.Body)}
	}
	var listed []struct {
		Unit string `json:"unit"`
		Last int64  `json:"last"`
	}
	if err := json.Unmarshal([]byte(s.Body), &listed); err != nil {
		return nil, cronAccess{State: "unreadable", Detail: "list-timers: " + err.Error()}
	}
	lastRun := map[string]int64{}
	for _, t := range listed {
		lastRun[t.Unit] = t.Last
	}
	var jobs []cronJob
	for _, t := range p.find("timer") {
		props := parseShow(t.Body)
		job := p.newJob("systemd", t.Key)
		var schedules []string
		for _, v := range props["TimersCalendar"] {
			if c := braceField(v, "OnCalendar"); c != "" {
				schedules = append(schedules, "OnCalendar="+c)
			}
		}
		for _, v := range props["TimersMonotonic"] {
			for _, part := range strings.Split(strings.Trim(v, "{} "), " ; ") {
				if k, val, ok := strings.Cut(strings.TrimSpace(part), "="); ok && strings.HasPrefix(k, "On") {
					schedules = append(schedules, strings.TrimSuffix(k, "USec")+"Sec="+val)
				}
			}
		}
		job.Schedule = strings.Join(schedules, "; ")
		if first(props["ActiveState"]) != "active" {
			job.State = "not_loaded"
		}
		if frag := first(props["FragmentPath"]); strings.HasPrefix(frag, "/usr/") || strings.HasPrefix(frag, "/lib/") {
			job.CreatedBy = "system"
			job.Inferred = append(job.Inferred, "created_by")
		}
		if last := lastRun[t.Key]; last > 0 {
			job.LastRun = unixString(last / 1_000_000)
		}
		if svc, ok := p.one("service", t.Key); ok {
			sp := parseShow(svc.Body)
			job.Command = braceField(first(sp["ExecStart"]), "argv[]")
			job.Log = first(sp["StandardOutput"])
			job.LastStatus = first(sp["Result"])
			if first(sp["ExecMainStartTimestamp"]) != "" {
				if n, err := strconv.Atoi(first(sp["ExecMainStatus"])); err == nil {
					job.LastExit = &n
				}
			}
			job.Problems = p.missingPaths(job.Command)
		}
		jobs = append(jobs, job)
	}
	return jobs, cronAccess{}
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// SQLite sources: the probe streams the database files back and they are
// opened from a local temporary copy, so nothing is written on the host.

func (p *probeData) querySQLite(kind, dbName, query string) ([]map[string]any, error) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return nil, errors.New("sqlite3 is not installed on this machine")
	}
	dir, err := os.MkdirTemp("", "tackroom-crons-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	for _, s := range p.find(kind) {
		if s.RC != 0 {
			return nil, fmt.Errorf("read %s: %s", s.Key, firstLine(s.Body))
		}
		data, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(s.Body), ""))
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", s.Key, err)
		}
		if err := os.WriteFile(filepath.Join(dir, s.Key), data, 0o600); err != nil {
			return nil, err
		}
	}
	out, err := exec.Command("sqlite3", "-json", filepath.Join(dir, dbName), query).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s: %s", dbName, firstLine(strings.TrimSpace(string(out))))
	}
	var rows []map[string]any
	if strings.TrimSpace(string(out)) == "" {
		return rows, nil
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("%s: %w", dbName, err)
	}
	return rows, nil
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func asInt(v any) (int, bool) {
	f, ok := v.(float64)
	return int(f), ok
}

// epochString accepts seconds or milliseconds.
func epochString(v any) string {
	f, ok := v.(float64)
	if !ok || f <= 0 {
		return ""
	}
	sec := int64(f)
	if sec > 1e11 {
		sec /= 1000
	}
	return unixString(sec)
}

// hermes

func (p *probeData) hermes() ([]cronJob, cronAccess) {
	s, ok := p.one("hermes-jobs", "")
	if !ok {
		return nil, cronAccess{Detail: "no Hermes cron jobs file"}
	}
	var doc struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if s.RC != 0 || json.Unmarshal([]byte(s.Body), &doc) != nil {
		return nil, cronAccess{State: "unreadable", Detail: "jobs.json: " + firstLine(s.Body)}
	}

	var details []string
	alive := true
	if hb, ok := p.one("hermes-heartbeat", ""); ok && hb.RC == 0 {
		beat, _ := strconv.ParseFloat(first(strings.Fields(hb.Body)), 64)
		age := p.now - int64(beat)
		alive = beat > 0 && age <= 600
		details = append(details, "scheduler heartbeat "+ageString(age)+" ago")
	} else {
		details = append(details, "no scheduler heartbeat; liveness unknown")
	}

	runs := map[string]int{}
	if len(p.find("hermes-db")) > 0 {
		rows, err := p.querySQLite("hermes-db", "executions.db", "select job_id, count(*) as runs from executions group by job_id")
		if err != nil {
			details = append(details, "executions.db unreadable: "+err.Error())
		}
		for _, row := range rows {
			n, _ := asInt(row["runs"])
			runs[asString(row["job_id"])] = n
		}
	}

	var jobs []cronJob
	for _, j := range doc.Jobs {
		job := p.newJob("hermes", asString(j["id"]))
		if name := asString(j["name"]); name != "" {
			job.Name = name
		}
		if sched, ok := j["schedule"].(map[string]any); ok {
			job.Schedule = asString(sched["expr"])
			if job.Schedule == "" {
				job.Schedule = asString(sched["display"])
			}
		}
		if next, err := time.Parse(time.RFC3339, asString(j["next_run_at"])); err == nil {
			job.TZ = next.Format("-07:00")
		}
		switch {
		case asString(j["script"]) != "":
			job.Command = asString(j["script"])
		case asString(j["skill"]) != "":
			job.Command = "skill " + asString(j["skill"])
		default:
			job.Command = "prompt: " + truncate(asString(j["prompt"]), 60)
		}
		job.LastRun = asString(j["last_run_at"])
		job.LastStatus = asString(j["last_status"])
		if n, ok := runs[job.ID]; ok {
			job.Runs = &n
		} else if repeat, ok := j["repeat"].(map[string]any); ok {
			if n, ok := asInt(repeat["completed"]); ok {
				job.Runs = &n
			}
		}
		if e := asString(j["last_error"]); e != "" {
			job.Problems = append(job.Problems, "last error: "+truncate(e, 160))
		}
		deliver := asString(j["deliver"])
		if origin, ok := j["origin"].(map[string]any); ok && deliver == "origin" {
			deliver = asString(origin["platform"])
		}
		if platform, _, _ := strings.Cut(deliver, ":"); platform != "" && platform != "local" {
			job.Connectors = []string{platform}
			job.Inferred = append(job.Inferred, "connectors")
		}
		enabled, _ := j["enabled"].(bool)
		switch {
		case !enabled:
			job.State = "disabled"
		case j["paused_at"] != nil || asString(j["state"]) == "paused":
			job.State = "paused"
		case !alive:
			job.State = "dead"
			job.Problems = append(job.Problems, "enabled, but the Hermes scheduler is not running on this host")
		}
		job.Inferred = append(job.Inferred, "state")
		missedRuns(&job, time.Unix(p.now, 0))
		jobs = append(jobs, job)
	}
	return jobs, cronAccess{Detail: strings.Join(details, "; ")}
}

// codex

func (p *probeData) codex() ([]cronJob, cronAccess) {
	if len(p.find("codex-db")) == 0 {
		return nil, cronAccess{Detail: "no Codex database"}
	}
	rows, err := p.querySQLite("codex-db", "codex-dev.db", `select a.id, a.name, a.status, a.rrule, a.prompt, a.last_run_at,
		(select count(*) from automation_runs r where r.automation_id = a.id) as runs,
		(select r.status from automation_runs r where r.automation_id = a.id order by r.created_at desc limit 1) as last_status
		from automations a`)
	if err != nil {
		return nil, cronAccess{State: "unreadable", Detail: err.Error()}
	}
	var jobs []cronJob
	for _, row := range rows {
		job := p.newJob("codex", asString(row["id"]))
		job.Name = asString(row["name"])
		job.Schedule = asString(row["rrule"])
		job.Command = "prompt: " + truncate(asString(row["prompt"]), 60)
		job.LastRun = epochString(row["last_run_at"])
		job.LastStatus = asString(row["last_status"])
		if n, ok := asInt(row["runs"]); ok {
			job.Runs = &n
		}
		switch status := strings.ToLower(asString(row["status"])); status {
		case "active":
		default:
			job.State = status
		}
		jobs = append(jobs, job)
	}
	return jobs, cronAccess{}
}

// claude desktop

func (p *probeData) claudeDesktop() ([]cronJob, cronAccess) {
	runs := map[string]int{}
	for _, s := range p.find("claude-run") {
		var session struct {
			ScheduledTaskID string `json:"scheduledTaskId"`
		}
		if json.Unmarshal([]byte(s.Body), &session) == nil && session.ScheduledTaskID != "" {
			runs[session.ScheduledTaskID]++
		}
	}
	var jobs []cronJob
	var unreadable []string
	seen := map[string]bool{}
	for _, s := range p.find("claude-tasks") {
		var doc struct {
			ScheduledTasks []struct {
				ID             string `json:"id"`
				CronExpression string `json:"cronExpression"`
				Enabled        bool   `json:"enabled"`
				FilePath       string `json:"filePath"`
				LastRunAt      string `json:"lastRunAt"`
			} `json:"scheduledTasks"`
		}
		if s.RC != 0 || json.Unmarshal([]byte(s.Body), &doc) != nil {
			unreadable = append(unreadable, firstLine(s.Body))
			continue
		}
		for _, t := range doc.ScheduledTasks {
			seen[t.ID] = true
			job := p.newJob("claude-desktop", t.ID)
			job.Schedule, job.Command, job.LastRun = t.CronExpression, t.FilePath, t.LastRunAt
			n := runs[t.ID]
			job.Runs = &n
			if !t.Enabled {
				job.State = "disabled"
			}
			if skill, ok := p.one("claude-skill", t.FilePath); ok {
				if desc := frontmatterValue(skill.Body, "description"); desc != "" {
					job.Name = t.ID + ": " + desc
				}
			} else if t.FilePath != "" {
				job.Problems = append(job.Problems, "task file not found: "+t.FilePath)
			}
			missedRuns(&job, time.Unix(p.now, 0))
			jobs = append(jobs, job)
		}
	}
	for _, s := range p.find("claude-skill") {
		id := filepath.Base(filepath.Dir(s.Key))
		if seen[id] {
			continue
		}
		job := p.newJob("claude-desktop", id)
		job.Command, job.State = s.Key, "not_loaded"
		job.Problems = []string{"task file has no schedule in the desktop app"}
		jobs = append(jobs, job)
	}
	access := cronAccess{}
	if len(unreadable) > 0 {
		access.Detail = "unreadable scheduled-tasks.json: " + strings.Join(unreadable, "; ")
		if len(jobs) == 0 {
			access.State = "unreadable"
		}
	}
	return jobs, access
}

func frontmatterValue(body, key string) string {
	for _, line := range strings.Split(body, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == key {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

func printCronsTable(result cronsScanResult) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "HOST\tSOURCE\tID\tSCHEDULE\tSTATE\tLAST RUN\tRESULT\tRUNS\tPROBLEMS")
	for _, j := range result.Jobs {
		res := j.LastStatus
		if j.LastExit != nil {
			res = "exit " + strconv.Itoa(*j.LastExit)
		}
		runs := ""
		if j.Runs != nil {
			runs = strconv.Itoa(*j.Runs)
		}
		lastRun := j.LastRun
		if t, err := time.Parse(time.RFC3339, lastRun); err == nil {
			lastRun = t.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\n", j.Host, j.Source, truncate(j.ID, 40), truncate(j.Schedule, 28), j.State, lastRun, res, runs, len(j.Problems))
	}
	w.Flush()
	fmt.Println()
	w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "HOST\tSOURCE\tACCESS\tJOBS\tDETAIL")
	for _, a := range result.Access {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", a.Host, a.Source, a.State, a.Jobs, truncate(a.Detail, 90))
	}
	w.Flush()
	var problems []string
	for _, j := range result.Jobs {
		for _, prob := range j.Problems {
			problems = append(problems, fmt.Sprintf("%s %s %s: %s", j.Host, j.Source, j.ID, prob))
		}
	}
	if len(problems) > 0 {
		fmt.Println()
		fmt.Println("Problems:")
		for _, prob := range problems {
			fmt.Println("  " + prob)
		}
	}
}

// Missed-run check for schedulers that record their own last run. Log mtimes
// are not used: a job that runs but writes nothing would look stale.

var cronMacros = map[string]string{
	"@yearly": "0 0 1 1 *", "@annually": "0 0 1 1 *", "@monthly": "0 0 1 * *",
	"@weekly": "0 0 * * 0", "@daily": "0 0 * * *", "@midnight": "0 0 * * *", "@hourly": "0 * * * *",
}

type cronSpec struct {
	fields         [5]map[int]bool
	domAny, dowAny bool
}

// parseCronSpec reads a five-field numeric cron expression; names and
// extensions return false so the caller skips the check.
func parseCronSpec(expr string) (cronSpec, bool) {
	if m, ok := cronMacros[strings.TrimSpace(expr)]; ok {
		expr = m
	}
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return cronSpec{}, false
	}
	bounds := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	var spec cronSpec
	for i, part := range parts {
		set := map[int]bool{}
		for _, item := range strings.Split(part, ",") {
			rangePart, stepPart, hasStep := strings.Cut(item, "/")
			step := 1
			if hasStep {
				n, err := strconv.Atoi(stepPart)
				if err != nil || n <= 0 {
					return cronSpec{}, false
				}
				step = n
			}
			lo, hi := bounds[i][0], bounds[i][1]
			if rangePart != "*" {
				a, b, isRange := strings.Cut(rangePart, "-")
				var err error
				if lo, err = strconv.Atoi(a); err != nil {
					return cronSpec{}, false
				}
				hi = lo
				if isRange {
					if hi, err = strconv.Atoi(b); err != nil {
						return cronSpec{}, false
					}
				} else if hasStep {
					hi = bounds[i][1]
				}
			}
			if lo < bounds[i][0] || hi > bounds[i][1] || lo > hi {
				return cronSpec{}, false
			}
			for v := lo; v <= hi; v += step {
				if i == 4 {
					set[v%7] = true // Sunday is both 0 and 7
					continue
				}
				set[v] = true
			}
		}
		spec.fields[i] = set
	}
	spec.domAny, spec.dowAny = parts[2] == "*", parts[4] == "*"
	return spec, true
}

func (c cronSpec) matches(t time.Time) bool {
	if !c.fields[0][t.Minute()] || !c.fields[1][t.Hour()] || !c.fields[3][int(t.Month())] {
		return false
	}
	dom, dow := c.fields[2][t.Day()], c.fields[4][int(t.Weekday())]
	if c.domAny || c.dowAny {
		return dom && dow
	}
	return dom || dow // cron ORs day-of-month and day-of-week when both are set
}

// previousFire returns the latest matching minute strictly before t, looking
// back at most 400 days.
func (c cronSpec) previousFire(t time.Time) (time.Time, bool) {
	t = t.Truncate(time.Minute).Add(-time.Minute)
	for i := 0; i < 400*24*60; i++ {
		if c.matches(t) {
			return t, true
		}
		t = t.Add(-time.Minute)
	}
	return time.Time{}, false
}

func jobLocation(tz string) (*time.Location, bool) {
	if loc, err := time.LoadLocation(tz); err == nil && tz != "" {
		return loc, true
	}
	if t, err := time.Parse("-07:00", tz); err == nil {
		_, offset := t.Zone()
		return time.FixedZone(tz, offset), true
	}
	return nil, false
}

// missedRuns flags a scheduled job whose recorded last run is older than the
// two most recent times it should have fired.
func missedRuns(job *cronJob, now time.Time) {
	if job.State != "scheduled" || job.LastRun == "" {
		return
	}
	spec, ok := parseCronSpec(job.Schedule)
	loc, locOK := jobLocation(job.TZ)
	last, err := time.Parse(time.RFC3339, job.LastRun)
	if !ok || !locOK || err != nil {
		return
	}
	prev, ok := spec.previousFire(now.In(loc))
	if !ok {
		return
	}
	if prev2, ok := spec.previousFire(prev); ok && last.Before(prev2) {
		job.Problems = append(job.Problems, fmt.Sprintf("enabled, but has not run since %s; it should have fired at %s and %s", last.Format("2006-01-02"), prev2.Format("2006-01-02 15:04"), prev.Format("2006-01-02 15:04")))
	}
}
