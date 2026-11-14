package app

import (
	"encoding/json"
	"io"
	"strings"
)

// Machine-readable output for agents and scripts. Field names are part of the
// CLI contract: add fields freely, never rename or remove them.

type surfaceJSON struct {
	Managed []string `json:"managed"`
	Missing []string `json:"missing"`
	Drifted []string `json:"drifted"`
	// Stale lists servers sync wrote earlier that are no longer wanted (MCP only).
	Stale []string `json:"stale,omitempty"`
}

type rootInstructionsJSON struct {
	Path           string   `json:"path"`
	State          string   `json:"state"`
	Sources        []string `json:"sources,omitempty"`
	MissingSources []string `json:"missing_sources,omitempty"`
}

type agentStatusJSON struct {
	Name             string                `json:"name"`
	Detected         bool                  `json:"detected"`
	Synced           bool                  `json:"synced"`
	Error            string                `json:"error,omitempty"`
	Skills           surfaceJSON           `json:"skills"`
	MCP              surfaceJSON           `json:"mcp"`
	Roles            surfaceJSON           `json:"roles"`
	Hooks            surfaceJSON           `json:"hooks"`
	RootInstructions *rootInstructionsJSON `json:"root_instructions,omitempty"`
	Conflicts        []string              `json:"conflicts"`
}

type statusJSON struct {
	ConfigRoot string            `json:"config_root"`
	Synced     bool              `json:"synced"`
	RepoLink   string            `json:"repo_link"`
	Agents     []agentStatusJSON `json:"agents"`
	Hint       string            `json:"hint,omitempty"`
}

func nonNil(items []string) []string {
	if items == nil {
		return []string{}
	}
	return items
}

func surface(managed, missing, drifted []string) surfaceJSON {
	return surfaceJSON{Managed: nonNil(managed), Missing: nonNil(missing), Drifted: nonNil(drifted)}
}

func buildStatusJSON(repoRoot string, repoReport repoLinkReport, reports []agentReport) statusJSON {
	out := statusJSON{ConfigRoot: repoRoot, RepoLink: repoReport.State, Synced: repoReport.State == stateSynced, Agents: []agentStatusJSON{}}
	for _, r := range reports {
		agent := agentStatusJSON{
			Name:      r.Name,
			Detected:  r.Detected,
			Synced:    r.Synced,
			Error:     r.Error,
			Skills:    surface(r.Managed, r.Missing, r.Drifted),
			MCP:       surfaceJSON{Managed: nonNil(r.ManagedMCP), Missing: nonNil(r.MissingMCP), Drifted: nonNil(r.DriftedMCP), Stale: r.StaleMCP},
			Roles:     surface(r.ManagedAgent, r.MissingAgent, r.DriftedAgent),
			Hooks:     surface(r.ManagedHook, r.MissingHook, r.DriftedHook),
			Conflicts: nonNil(r.Conflicts),
		}
		if r.RootPath != "" {
			agent.RootInstructions = &rootInstructionsJSON{Path: r.RootPath, State: r.RootState, Sources: r.RootSources, MissingSources: r.RootMissing}
		}
		if r.Detected && (!r.Synced || r.Error != "") {
			out.Synced = false
		}
		out.Agents = append(out.Agents, agent)
	}
	return out
}

// appendConflict records an "agent: message" conflict on that agent's entry.
// Agents skipped for conflicts are not re-inspected after sync, so their
// conflicts come from the pre-apply list.
func appendConflict(agents []agentStatusJSON, entry string) []agentStatusJSON {
	name, msg, ok := strings.Cut(entry, ": ")
	if !ok {
		return agents
	}
	for i := range agents {
		if agents[i].Name == name {
			for _, existing := range agents[i].Conflicts {
				if existing == msg {
					return agents
				}
			}
			agents[i].Conflicts = append(agents[i].Conflicts, msg)
			agents[i].Synced = false
			return agents
		}
	}
	return agents
}

func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// hostsJSON is `tackroom hosts --json` and the view's /api/hosts: tackroom
// itself on every machine in crons.hosts. Version and Commit are this machine's.
type hostsJSON struct {
	CheckedAt string     `json:"checked_at"`
	Version   string     `json:"version"`
	Commit    string     `json:"commit,omitempty"`
	Hosts     []hostJSON `json:"hosts"`
}

type hostJSON struct {
	Host      string `json:"host"`
	Reachable bool   `json:"reachable"`
	Version   string `json:"version,omitempty"`
	// ConfigRoot, Commit, CommittedAt and Dirty describe the host's config root:
	// its git HEAD, the time of that commit and the count of uncommitted changes.
	ConfigRoot  string `json:"config_root,omitempty"`
	Commit      string `json:"commit,omitempty"`
	CommittedAt string `json:"committed_at,omitempty"`
	Dirty       int    `json:"dirty"`
	// ConfigUncommitted: tackroom.yaml is in a git config root but was never
	// committed, so no other machine can pull it.
	ConfigUncommitted bool `json:"config_uncommitted,omitempty"`
	// Relation places Commit against this machine's config commit: same,
	// behind, ahead, diverged, or unknown when this machine's git does not
	// have it. Ahead and Behind count the commits.
	Relation     string          `json:"relation,omitempty"`
	Ahead        int             `json:"ahead,omitempty"`
	Behind       int             `json:"behind,omitempty"`
	RelationText string          `json:"relation_text,omitempty"`
	Synced       bool            `json:"synced"`
	OutOfSync    []hostAgentJSON `json:"out_of_sync"`
	Error        string          `json:"error,omitempty"`
	// Health (ok, warn or fail), Summary and Fix are what both views draw
	// (hosts_words.go). Fix is a command to run on this machine.
	Health  string `json:"health"`
	Summary string `json:"summary"`
	Fix     string `json:"fix,omitempty"`

	bin      string // the tackroom the probe ran
	onPath   bool   // found on the host's non-login PATH
	repoLink string // status repo_link
	hasLock  bool   // tackroom.lock exists beside an uncommitted tackroom.yaml
}

// hostAgentJSON is one harness a host's status reports out of sync, with what
// is pending there.
type hostAgentJSON struct {
	Name    string   `json:"name"`
	Pending []string `json:"pending"`
}

type doctorCheckJSON struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type doctorJSON struct {
	ConfigRoot string            `json:"config_root"`
	Passed     int               `json:"passed"`
	Warnings   int               `json:"warnings"`
	Failed     int               `json:"failed"`
	Checks     []doctorCheckJSON `json:"checks"`
}

func printDoctorJSON(w io.Writer, repoRoot string, results []checkResult) error {
	out := doctorJSON{ConfigRoot: repoRoot, Checks: []doctorCheckJSON{}}
	for _, r := range results {
		out.Checks = append(out.Checks, doctorCheckJSON{Name: r.name, Status: r.status, Detail: r.detail})
		switch r.status {
		case checkStatusPass:
			out.Passed++
		case checkStatusWarn:
			out.Warnings++
		case checkStatusFail:
			out.Failed++
		}
	}
	if err := encodeJSON(w, out); err != nil {
		return err
	}
	return doctorExitError(out.Failed, out.Warnings)
}
