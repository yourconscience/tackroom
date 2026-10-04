package app

import (
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yourconscience/tackroom/internal/agentrole"
)

// Cell states the view UI renders. They describe what is on disk for one
// agent, never what the user should do.
const (
	cellOK          = "ok"
	cellDrift       = "drift"
	cellMissing     = "missing"
	cellConflict    = "conflict"
	cellStale       = "stale"
	cellOff         = "off"
	cellDisabled    = "disabled"
	cellUnsupported = "unsupported"
	cellAbsent      = "absent"
	cellError       = "error"
	cellUnknown     = "unknown"
)

type viewInventory struct {
	Revision  string          `json:"revision"`
	Home      string          `json:"home"`
	RepoState string          `json:"repo_state"`
	Agents    []viewAgent     `json:"agents"`
	Skills    []viewSkill     `json:"skills"`
	Roles     []viewRole      `json:"roles"`
	MCP       []viewWired     `json:"mcp"`
	Hooks     []viewWired     `json:"hooks"`
	Unmanaged []viewUnmanaged `json:"unmanaged"`
}

type viewAgent struct {
	Name          string         `json:"name"`
	Enabled       bool           `json:"enabled"`
	Inspected     bool           `json:"inspected"`
	Detected      bool           `json:"detected"`
	Synced        bool           `json:"synced"`
	Error         string         `json:"error,omitempty"`
	SkillRoot     string         `json:"skill_root,omitempty"`
	RootState     string         `json:"root_state,omitempty"`
	Pending       int            `json:"pending"`
	Tokens        int            `json:"tokens"`
	Counts        map[string]int `json:"counts"`
	SupportsMCP   bool           `json:"supports_mcp"`
	SupportsHooks bool           `json:"supports_hooks"`
	SupportsRoles bool           `json:"supports_roles"`
}

type viewSkill struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Origin      string            `json:"origin"`
	Path        string            `json:"path"`
	Tokens      int               `json:"tokens"`
	States      map[string]string `json:"states"`
}

type viewRole struct {
	Name   string            `json:"name"`
	States map[string]string `json:"states"`
}

type viewWired struct {
	Name     string            `json:"name"`
	Enabled  bool              `json:"enabled"`
	Event    string            `json:"event,omitempty"`
	Command  string            `json:"command"`
	Explicit bool              `json:"explicit"`
	Targets  []string          `json:"targets"`
	States   map[string]string `json:"states"`
}

type viewUnmanaged struct {
	Kind   string   `json:"kind"`
	Name   string   `json:"name"`
	Detail string   `json:"detail"`
	Agents []string `json:"agents"`
	Hint   string   `json:"hint"`
}

// skillMeta is what the inventory needs from a canonical skill on disk.
type skillMeta struct {
	Path        string
	Description string
	Origin      string
}

// assembleInventory turns inspect reports into the per-agent matrices the view
// UI renders. It is pure so state mapping can be tested with synthetic reports;
// describe resolves an unmanaged skill entry's detail line.
func assembleInventory(cfg config, reports []agentReport, skills map[string]skillMeta, roleNames []string, describe func(agent agentReport, name string) string) viewInventory {
	byName := make(map[string]agentReport, len(reports))
	for _, report := range reports {
		byName[report.Name] = report
	}
	inv := viewInventory{Skills: []viewSkill{}, Roles: []viewRole{}, MCP: []viewWired{}, Hooks: []viewWired{}, Unmanaged: []viewUnmanaged{}}
	var columns []string
	for _, agent := range cfg.Agents {
		_, hooks := hookTargetForHarness(agent.Name)
		view := viewAgent{Name: agent.Name, Enabled: agent.Enabled, SkillRoot: agent.SkillRoot, Counts: map[string]int{}, SupportsMCP: hasMCPSupport(agent.Name), SupportsHooks: hooks, SupportsRoles: supportsRoles(agent.Name, agent.AgentRoot)}
		if report, ok := byName[agent.Name]; ok {
			view.Inspected = true
			view.Detected = report.Detected
			view.Synced = report.Synced
			view.Error = report.Error
			view.RootState = report.RootState
			view.Pending = pendingChanges(report)
			view.Counts = map[string]int{
				"managed":   len(report.Managed),
				"drifted":   len(report.Drifted),
				"missing":   len(skillNamesOnly(report, report.Missing)),
				"conflicts": len(report.Conflicts),
				"unmanaged": len(report.External),
				"stale":     len(report.StaleManaged),
			}
			tokens := 0
			for name := range report.ExpectedSkills {
				tokens += estimateTokens(len(name) + len(skills[name].Description))
			}
			view.Tokens = tokens
			columns = append(columns, agent.Name)
		}
		inv.Agents = append(inv.Agents, view)
	}

	skillNames := make([]string, 0, len(skills))
	for name := range skills {
		skillNames = append(skillNames, name)
	}
	sort.Strings(skillNames)
	for _, name := range skillNames {
		meta := skills[name]
		row := viewSkill{Name: name, Description: meta.Description, Origin: meta.Origin, Path: meta.Path, Tokens: estimateTokens(len(name) + len(meta.Description)), States: map[string]string{}}
		for _, agent := range columns {
			row.States[agent] = skillCell(byName[agent], name)
		}
		inv.Skills = append(inv.Skills, row)
	}

	roleSet := map[string]string{}
	for _, name := range roleNames {
		roleSet[name] = name
	}
	for _, report := range reports {
		for _, list := range [][]string{report.ManagedAgent, report.DriftedAgent, report.MissingAgent} {
			for _, name := range list {
				roleSet[name] = name
			}
		}
	}
	for _, name := range sortedKeys(roleSet) {
		row := viewRole{Name: name, States: map[string]string{}}
		for _, agent := range columns {
			row.States[agent] = roleCell(byName[agent], name)
		}
		inv.Roles = append(inv.Roles, row)
	}

	for _, server := range cfg.MCPServers {
		row := viewWired{Name: server.Name, Enabled: server.Enabled, Command: server.Command, Explicit: len(server.Agents) > 0, Targets: append([]string{}, server.Agents...), States: map[string]string{}}
		for _, agent := range columns {
			report := byName[agent]
			targeted := len(server.Agents) == 0 || stringInSlice(normalizeAgentName(agent), server.Agents)
			row.States[agent] = wiredCell(report, server.Name, server.Enabled, hasMCPSupport(agent), targeted, report.ManagedMCP, report.DriftedMCP, report.MissingMCP, nil)
		}
		inv.MCP = append(inv.MCP, row)
	}
	for _, hook := range cfg.Hooks {
		row := viewWired{Name: hook.Name, Enabled: hook.Enabled, Event: hook.Event, Command: hook.Command, Explicit: len(hook.Agents) > 0, Targets: append([]string{}, hook.Agents...), States: map[string]string{}}
		for _, agent := range columns {
			report := byName[agent]
			_, supported := hookTargetForHarness(agent)
			targeted := len(hook.Agents) == 0 || stringInSlice(normalizeAgentName(agent), hook.Agents)
			row.States[agent] = wiredCell(report, hook.Name, hook.Enabled, supported, targeted, report.ManagedHook, report.DriftedHook, report.MissingHook, report.UnsupportedHook)
		}
		inv.Hooks = append(inv.Hooks, row)
	}

	inv.Unmanaged = unmanagedItems(reports, describe)
	return inv
}

func pendingChanges(report agentReport) int {
	total := 0
	for _, list := range [][]string{
		report.Adds, report.AddsAgent, report.AddsMCP, report.AddsHook,
		report.Updates, report.UpdatesAgent, report.UpdatesMCP, report.UpdatesHook, report.UpdatesPackage,
		report.Removes, report.RemovesAgent, report.RemovesPackage,
	} {
		total += len(list)
	}
	return total
}

// skillNamesOnly drops the integration-level messages config-driven harnesses
// record in Missing, keeping only expected skill names.
func skillNamesOnly(report agentReport, items []string) []string {
	var out []string
	for _, item := range items {
		if _, ok := report.ExpectedSkills[item]; ok {
			out = append(out, item)
		}
	}
	return out
}

func agentCellBase(report agentReport) (string, bool) {
	switch {
	case report.Error != "":
		return cellError, true
	case !report.Detected:
		return cellAbsent, true
	}
	return "", false
}

func skillCell(report agentReport, name string) string {
	if state, done := agentCellBase(report); done {
		return state
	}
	switch {
	case containsString(report.Drifted, name):
		return cellDrift
	case containsString(report.Missing, name):
		return cellMissing
	case containsString(report.Managed, name):
		return cellOK
	case mentionsPath(report.Conflicts, filepath.Join(report.SkillRoot, name), ""):
		return cellConflict
	}
	return cellUnknown
}

func roleCell(report agentReport, name string) string {
	if state, done := agentCellBase(report); done {
		return state
	}
	switch {
	case !supportsRoles(report.Name, report.AgentRoot):
		return cellUnsupported
	case containsString(report.DriftedAgent, name):
		return cellDrift
	case containsString(report.MissingAgent, name):
		return cellMissing
	case containsString(report.ManagedAgent, name):
		return cellOK
	case mentionsPath(report.Conflicts, filepath.Join(report.AgentRoot, name), "."):
		return cellConflict
	}
	return cellUnknown
}

func supportsRoles(agentName, agentRoot string) bool {
	h := harnessFor(agentName)
	return agentRoot != "" && h != nil && h.roles != nil
}

// mentionsPath reports whether a conflict sentence names path itself, not a
// longer sibling: "/skills/foo" must not match "/skills/foo-bar". A role
// target may continue with its file extension, passed as suffix.
func mentionsPath(conflicts []string, path, suffix string) bool {
	for _, conflict := range conflicts {
		for rest := conflict; ; {
			i := strings.Index(rest, path)
			if i < 0 {
				break
			}
			after := rest[i+len(path):]
			if after == "" || !isPathChar(after[0]) || (suffix != "" && strings.HasPrefix(after, suffix)) {
				return true
			}
			rest = after
		}
	}
	return false
}

func isPathChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.'
}

func wiredCell(report agentReport, name string, enabled, supported, targeted bool, managed, drifted, missing, unsupported []string) string {
	if state, done := agentCellBase(report); done {
		return state
	}
	switch {
	case !supported || containsString(unsupported, name):
		return cellUnsupported
	case !targeted:
		return cellOff
	case !enabled:
		return cellDisabled
	case containsString(drifted, name):
		return cellDrift
	case containsString(missing, name):
		return cellMissing
	case containsString(managed, name):
		return cellOK
	}
	return cellUnknown
}

// unmanagedItems groups foreign skill entries, stale managed links and
// conflicts across agents so one item that shows up everywhere is one row.
func unmanagedItems(reports []agentReport, describe func(agentReport, string) string) []viewUnmanaged {
	type key struct{ kind, name string }
	index := map[key]int{}
	var items []viewUnmanaged
	add := func(kind, name, detail, hint, agent string) {
		k := key{kind, name}
		if i, ok := index[k]; ok {
			if !containsString(items[i].Agents, agent) {
				items[i].Agents = append(items[i].Agents, agent)
			}
			return
		}
		index[k] = len(items)
		items = append(items, viewUnmanaged{Kind: kind, Name: name, Detail: detail, Hint: hint, Agents: []string{agent}})
	}
	for _, report := range reports {
		if report.Error != "" || !report.Detected {
			continue
		}
		for _, name := range report.External {
			detail := ""
			if describe != nil {
				detail = describe(report, name)
			}
			hint := "tackroom skill promote " + filepath.Join(report.SkillRoot, name)
			if strings.HasPrefix(name, ".") {
				// Hidden folders such as Codex's .system belong to the harness.
				detail, hint = "hidden folder the agent keeps for itself", ""
			}
			add("skill", name, detail, hint, report.Name)
		}
		for _, name := range report.StaleManaged {
			add("stale", name, "links into the tackroom store but is no longer a canonical skill", "tackroom sync", report.Name)
		}
		for _, conflict := range report.Conflicts {
			add("conflict", conflict, "", "move the native copy aside, then tackroom sync", report.Name)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].Name < items[j].Name
	})
	if items == nil {
		return []viewUnmanaged{}
	}
	return items
}

// buildViewInventory inspects every enabled agent against the effective config
// and assembles the inventory. Unlike the sync plan it tolerates a config with
// no enabled agents, so the UI can still show the agent toggles.
func buildViewInventory(doc *configDocument) (viewInventory, error) {
	snapshot, err := doc.syncSnapshot()
	if err != nil {
		return viewInventory{}, err
	}
	cfg := snapshot.cfg
	expected, err := expectedSkills(snapshot.repoRoot, snapshot.home, cfg)
	if err != nil {
		return viewInventory{}, err
	}
	var selected []agentConfig
	for _, agent := range cfg.Agents {
		if agent.Enabled {
			selected = append(selected, agent)
		}
	}
	var reports []agentReport
	if len(selected) > 0 {
		reports, err = inspectAgents(selected, expected, snapshot.repoRoot, snapshot.home, cfg)
		if err != nil {
			return viewInventory{}, err
		}
	}
	origins, err := skillOrigins(cfg, snapshot.repoRoot, snapshot.home, expected)
	if err != nil {
		return viewInventory{}, err
	}
	skills := make(map[string]skillMeta, len(expected))
	for name, dir := range expected {
		meta := skillMeta{Path: dir, Origin: origins[name]}
		if meta.Origin == "" {
			meta.Origin = "local"
		}
		if fm, fmErr := parseSkillFrontmatter(filepath.Join(dir, "SKILL.md")); fmErr == nil {
			meta.Description = fm.Description
		}
		skills[name] = meta
	}
	var roleNames []string
	if roles, roleErr := agentrole.Load(snapshot.repoRoot); roleErr == nil {
		for _, role := range roles {
			roleNames = append(roleNames, role.Name)
		}
	}
	home := snapshot.home
	inv := assembleInventory(cfg, reports, skills, roleNames, func(report agentReport, name string) string {
		return describeExternalSkillEntry(filepath.Join(report.SkillRoot, name), home)
	})
	inv.Revision = snapshot.revision
	inv.Home = snapshot.home
	if repo, repoErr := inspectRepoLink(snapshot.repoRoot, snapshot.home); repoErr == nil {
		inv.RepoState = repo.State
	}
	return inv, nil
}

func (s *configWebServer) handleInventory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !s.authorizeAPI(w, r, false) {
		return
	}
	if err := s.doc.reload(); err != nil {
		writeCandidateError(w, err)
		return
	}
	inv, err := buildViewInventory(s.doc)
	if err != nil {
		writeCandidateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, inv)
}
