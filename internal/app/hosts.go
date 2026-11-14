package app

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/yourconscience/tackroom/internal/ui"
)

// `tackroom hosts` reports tackroom itself on every machine in crons.hosts,
// the hosts `cron collect` scans: its version, whether it is set up and
// synced, and where its config root's commit stands against this machine's.
// The view's Overview and the Herdr panel draw the same result.

//go:embed hosts_probe.sh
var hostsProbeScript string

var gitCommitPattern = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

func runHostsCommand(args []string) error {
	fs := flag.NewFlagSet("hosts", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "Path to tackroom YAML config")
	asJSON := fs.Bool("json", false, "Print the result as JSON")
	hosts, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	result, err := collectHosts(*configPath, hosts)
	if err != nil {
		return err
	}
	if *asJSON {
		return encodeJSON(os.Stdout, result)
	}
	printHostsTable(os.Stdout, result)
	return nil
}

// collectHosts probes hosts, or crons.hosts from the config, else this
// machine, in parallel. "local" runs this binary with the config given here;
// another host runs the tackroom it has with its own default config.
func collectHosts(configPath string, hosts []string) (hostsJSON, error) {
	manifest, err := loadCronsManifest(configPath)
	if err != nil {
		return hostsJSON{}, err
	}
	if len(hosts) == 0 {
		hosts = manifest.Hosts
	}
	if len(hosts) == 0 {
		hosts = []string{"local"}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return hostsJSON{}, err
	}
	path, err := resolveConfigPath(configPath, home)
	if err != nil {
		return hostsJSON{}, err
	}
	root := filepath.Dir(path)
	localConfig := ""
	if strings.TrimSpace(configPath) != "" {
		localConfig = path
	}
	// This binary, not one earlier on PATH; not resolved through symlinks.
	self, _ := os.Executable()

	out := hostsJSON{Version: versionString(), Commit: gitHead(root), Hosts: make([]hostJSON, len(hosts))}
	var wg sync.WaitGroup
	for i, host := range hosts {
		wg.Add(1)
		go func(i int, host string) {
			defer wg.Done()
			if host == "local" {
				out.Hosts[i] = probeHost(host, self, localConfig)
			} else {
				out.Hosts[i] = probeHost(host, "", "")
			}
		}(i, host)
	}
	wg.Wait()
	out.CheckedAt = time.Now().Format(time.RFC3339)

	// A local fix names --config only when it is not the file tackroom finds anyway.
	fixConfig := localConfig
	if defaultPath, err := resolveConfigPath("", home); err == nil && defaultPath == localConfig {
		fixConfig = ""
	}
	// A machine that has not committed its tackroom.yaml cannot be the source
	// another machine sets up from.
	localUncommitted := false
	for _, h := range out.Hosts {
		if h.Host == "local" && h.ConfigUncommitted {
			localUncommitted = true
		}
	}
	for i := range out.Hosts {
		relateHost(&out.Hosts[i], root, out.Commit)
		annotateHost(&out.Hosts[i], out.Version, fixConfig, localUncommitted)
	}
	return out, nil
}

// probeHost runs hosts_probe.sh on host and reads its sections.
func probeHost(host, bin, config string) hostJSON {
	h := hostJSON{Host: host, OutOfSync: []hostAgentJSON{}}
	sections, err := runHostProbe(host, hostsProbeScript, bin, config)
	if err != nil {
		h.Error = strings.TrimPrefix(err.Error(), "probe failed: ")
		return h
	}
	h.Reachable = true
	got := map[string]probeSection{}
	for _, s := range sections {
		got[s.Kind] = s
	}
	found := got["bin"]
	h.bin, h.onPath = strings.TrimSpace(found.Body), found.Key == "path"
	if h.bin == "" {
		h.Error = "tackroom not found on PATH or in ~/.local/bin, ~/go/bin, /opt/homebrew/bin or /usr/local/bin"
		return h
	}
	if v, ok := got["version"]; ok && v.RC == 0 {
		h.Version = strings.TrimPrefix(firstLine(strings.TrimSpace(v.Body)), "tackroom ")
	}
	st := got["status"]
	if status, ok := statusFromProbe(st.Body); ok {
		h.ConfigRoot, h.Synced, h.repoLink = status.ConfigRoot, status.Synced, status.RepoLink
		h.OutOfSync = hostOutOfSync(status)
	} else {
		h.Error = lastLine(st.Body)
		if h.Error == "" {
			h.Error = fmt.Sprintf("tackroom status exited %d with no output", st.RC)
		}
	}
	if h.ConfigRoot == "" {
		h.ConfigRoot = strings.TrimSpace(got["root"].Body)
	}
	if c, ok := got["commit"]; ok && c.RC == 0 && gitCommitPattern.MatchString(strings.TrimSpace(c.Body)) {
		h.Commit = strings.TrimSpace(c.Body)
		if t, ok := got["committed"]; ok && t.RC == 0 {
			if sec, err := strconv.ParseInt(strings.TrimSpace(t.Body), 10, 64); err == nil {
				h.CommittedAt = unixString(sec)
			}
		}
		if fields := strings.Fields(got["dirty"].Body); len(fields) > 0 {
			h.Dirty, _ = strconv.Atoi(fields[len(fields)-1])
		}
		if t, ok := got["tracked"]; ok && t.RC != 0 && h.Error == "" {
			h.ConfigUncommitted = true
			h.hasLock = got["lock"].RC == 0
		}
	}
	return h
}

// statusFromProbe reads the JSON `status --json` printed. Warnings may come
// before it and the exit error after it; the JSON starts on a line of its own.
func statusFromProbe(body string) (statusJSON, bool) {
	start := -1
	if strings.HasPrefix(body, "{\n") {
		start = 0
	} else if i := strings.Index(body, "\n{\n"); i >= 0 {
		start = i + 1
	}
	var status statusJSON
	if start < 0 || json.NewDecoder(strings.NewReader(body[start:])).Decode(&status) != nil {
		return statusJSON{}, false
	}
	return status, true
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// hostOutOfSync lists the detected harnesses status reports out of sync and
// what is pending in each.
func hostOutOfSync(status statusJSON) []hostAgentJSON {
	out := []hostAgentJSON{}
	for _, a := range status.Agents {
		if !a.Detected || (a.Synced && a.Error == "") {
			continue
		}
		var pending []string
		if a.Error != "" {
			pending = append(pending, a.Error)
		}
		for _, s := range []struct {
			name    string
			surface surfaceJSON
		}{{"skills", a.Skills}, {"roles", a.Roles}, {"MCP servers", a.MCP}, {"hooks", a.Hooks}} {
			for _, kind := range []struct {
				word  string
				items []string
			}{{"missing", s.surface.Missing}, {"drifted", s.surface.Drifted}, {"stale", s.surface.Stale}} {
				if len(kind.items) > 0 {
					pending = append(pending, kind.word+" "+s.name+": "+strings.Join(kind.items, ", "))
				}
			}
		}
		if r := a.RootInstructions; r != nil && r.State != stateSynced {
			pending = append(pending, "root instructions "+r.State)
		}
		for _, c := range a.Conflicts {
			pending = append(pending, "conflict: "+c)
		}
		if len(pending) == 0 {
			pending = []string{"needs sync"}
		}
		out = append(out, hostAgentJSON{Name: a.Name, Pending: pending})
	}
	return out
}

func gitHead(root string) string {
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// relateHost places the host's config commit against local, this machine's,
// with this machine's git in root.
func relateHost(h *hostJSON, root, local string) {
	switch {
	case h.Commit == "" || local == "":
		return
	case h.Commit == local:
		h.Relation = "same"
		return
	case !gitHasCommit(root, h.Commit):
		h.Relation = "unknown"
		return
	}
	out, err := exec.Command("git", "-C", root, "rev-list", "--left-right", "--count", local+"..."+h.Commit).Output()
	fields := strings.Fields(string(out))
	if err != nil || len(fields) != 2 {
		h.Relation = "unknown"
		return
	}
	h.Behind, _ = strconv.Atoi(fields[0])
	h.Ahead, _ = strconv.Atoi(fields[1])
	switch {
	case h.Behind > 0 && h.Ahead > 0:
		h.Relation = "diverged"
	case h.Behind > 0:
		h.Relation = "behind"
	case h.Ahead > 0:
		h.Relation = "ahead"
	default:
		h.Relation = "same"
	}
}

func printHostsTable(w io.Writer, result hostsJSON) {
	out := ui.Writer(w)
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(ui.Dim).
		Headers("", "host", "version", "commit", "summary").
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return s.Inherit(ui.Bold)
			}
			return s
		})
	var fixes []string
	for _, h := range result.Hosts {
		version, commit := h.Version, "-"
		if version == "" {
			version = "-"
		}
		if h.Commit != "" {
			commit = h.Commit[:7]
			switch h.Relation {
			case "behind":
				commit += fmt.Sprintf(" behind %d", h.Behind)
			case "ahead":
				commit += fmt.Sprintf(" ahead %d", h.Ahead)
			case "diverged", "unknown":
				commit += " " + h.Relation
			}
		}
		t.Row(ui.Mark(h.Health), h.Host, version, commit, truncate(h.Summary, 90))
		if h.Fix != "" {
			fixes = append(fixes, h.Host+": "+h.Fix)
		}
	}
	fmt.Fprintln(out, t.String())
	if len(fixes) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Fix:")
		for _, fix := range fixes {
			fmt.Fprintln(out, "  "+fix)
		}
	}
}

var viewHosts struct {
	sync.Mutex
	result hostsJSON
	at     time.Time
}

// handleHosts serves `tackroom hosts --json` for the Overview's Machines
// block, cached like the Crons tab; ?refresh=1 probes again.
func (s *configWebServer) handleHosts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !s.authorizeAPI(w, r, false) {
		return
	}
	viewHosts.Lock()
	defer viewHosts.Unlock()
	if viewHosts.at.IsZero() || time.Since(viewHosts.at) > cronsCacheTTL || r.URL.Query().Get("refresh") == "1" {
		result, err := collectHosts(s.doc.sharedPath, nil)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "hosts_failed", err.Error())
			return
		}
		viewHosts.result, viewHosts.at = result, time.Now()
	}
	writeJSON(w, http.StatusOK, viewHosts.result)
}
