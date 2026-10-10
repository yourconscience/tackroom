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
}

type rootInstructionsJSON struct {
	Path  string `json:"path"`
	State string `json:"state"`
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
			MCP:       surface(r.ManagedMCP, r.MissingMCP, r.DriftedMCP),
			Roles:     surface(r.ManagedAgent, r.MissingAgent, r.DriftedAgent),
			Hooks:     surface(r.ManagedHook, r.MissingHook, r.DriftedHook),
			Conflicts: nonNil(r.Conflicts),
		}
		if r.RootPath != "" {
			agent.RootInstructions = &rootInstructionsJSON{Path: r.RootPath, State: r.RootState}
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
