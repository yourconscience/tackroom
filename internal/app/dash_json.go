package app

import (
	"os"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

// `tackroom dash --json` prints the answer the dash draws once, for scripts and
// a menu bar item, and needs no terminal. It fills the dash's own model with the
// dash's own collects, so its level, title and numbers are the dash's.

// dashJSON is the document of `tackroom dash --json`. Schema is 1.
type dashJSON struct {
	Schema    int               `json:"schema"`
	Level     string            `json:"level"`
	Title     string            `json:"title"`
	Failing   int               `json:"failing"`
	Attention int               `json:"attention"`
	Notes     []string          `json:"notes"`
	Counts    dashCountsJSON    `json:"counts"`
	Harnesses dashHarnessesJSON `json:"harnesses"`
	Jobs      []dashJobJSON     `json:"jobs"`
	Machines  []dashMachineJSON `json:"machines"`
	CheckedAt string            `json:"checked_at"`
}

// dashCountsJSON is the counts strip; nil, null in JSON, is a count that could
// not be read.
type dashCountsJSON struct {
	Skills *int `json:"skills"`
	Roles  *int `json:"roles"`
	MCP    *int `json:"mcp"`
	Hooks  *int `json:"hooks"`
	Crons  *int `json:"crons"`
}

// dashHarnessesJSON is how many harnesses are in sync and the names of those
// that need a sync and of those that are broken; one not installed is left out.
type dashHarnessesJSON struct {
	Synced    int      `json:"synced"`
	NeedsSync []string `json:"needs_sync"`
	Broken    []string `json:"broken"`
}

// dashJobJSON is a job the list shows under Failing or Last run failed.
type dashJobJSON struct {
	Host    string `json:"host"`
	Source  string `json:"source"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Health  string `json:"health"`
	LastRun string `json:"last_run"`
	Result  string `json:"result"`
}

// dashMachineJSON is a machine's line in the machine block.
type dashMachineJSON struct {
	Host    string `json:"host"`
	Health  string `json:"health"`
	Summary string `json:"summary"`
}

// runDashJSON collects the sync state, the crons and the machines at once, as
// the dash does, waits for all three and prints the document. A config that
// cannot be loaded is an error; any other part that could not be read counts as
// the dash counts it.
func runDashJSON(configPath string) error {
	var m tea.Model = newDashModel(configPath)
	first := m.(dashModel)
	collects := []tea.Cmd{dashCollectStatus(configPath, first.statusGen), dashCollectCrons(configPath, first.cronsGen), dashCollectHosts(configPath, first.hostsGen)}
	msgs := make([]tea.Msg, len(collects))
	var wg sync.WaitGroup
	for i, collect := range collects {
		wg.Add(1)
		go func() {
			defer wg.Done()
			msgs[i] = collect()
		}()
	}
	wg.Wait()
	if status := msgs[0].(dashStatusMsg); status.err != nil {
		if _, _, _, _, err := loadContext(runOptions{ConfigPath: configPath}); err != nil {
			return err
		}
	}
	for _, msg := range msgs {
		m, _ = m.Update(msg)
	}
	return encodeJSON(os.Stdout, m.(dashModel).document(time.Now()))
}

// document is the answer, the counts, the harnesses, the failing and the
// warning jobs in the dash's order, and the machines, for a model whose
// collects have all come back.
func (m dashModel) document(checkedAt time.Time) dashJSON {
	level, title, fail, warn, notes := m.answer()
	numbers := m.countNumbers()
	doc := dashJSON{
		Schema: 1, Level: level, Title: title, Failing: fail, Attention: warn,
		Notes:     append([]string{}, notes...),
		Counts:    dashCountsJSON{Skills: numbers[0], Roles: numbers[1], MCP: numbers[2], Hooks: numbers[3], Crons: numbers[4]},
		Harnesses: dashHarnessesJSON{NeedsSync: []string{}, Broken: []string{}},
		Jobs:      []dashJobJSON{},
		Machines:  []dashMachineJSON{},
		CheckedAt: checkedAt.UTC().Format(time.RFC3339),
	}
	if m.status != nil {
		synced, _, _, drifted, broken := m.harnessTally()
		doc.Harnesses.Synced = len(synced)
		doc.Harnesses.NeedsSync = append(doc.Harnesses.NeedsSync, drifted...)
		doc.Harnesses.Broken = append(doc.Harnesses.Broken, broken...)
	}
	for _, j := range m.jobs() {
		if dashGroup(j) != "" {
			doc.Jobs = append(doc.Jobs, dashJobJSON{Host: j.Host, Source: j.Source, ID: j.ID, Name: dashJobName(j), Health: j.Health, LastRun: j.LastRun, Result: dashResult(j)})
		}
	}
	for _, h := range m.machines() {
		doc.Machines = append(doc.Machines, dashMachineJSON{Host: h.Host, Health: h.Health, Summary: machineSummary(h)})
	}
	return doc
}
