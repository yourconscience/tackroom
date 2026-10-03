package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// checkAgentManagerCoexistence looks for other tools that write the same
// files tackroom manages. It warns only on real overlap; a manager that keeps
// to its own store is reported as a note so the doctor output stays honest.
func checkAgentManagerCoexistence(repoRoot string, home string, cfg config) checkResult {
	var warnings, notes []string

	// Sentry's dotagents keeps global state in ~/.agents, the default tackroom
	// root, and links ~/.agents/skills into the same agent folders.
	var sentryFiles []string
	for _, name := range []string{"agents.toml", "agents.lock"} {
		if hasFile(filepath.Join(repoRoot, name)) {
			sentryFiles = append(sentryFiles, name)
		}
	}
	if len(sentryFiles) > 0 {
		warnings = append(warnings, fmt.Sprintf("Sentry dotagents state in %s (%s): both tools write %s and agent skill links; keep dotagents to --project scope or use one tool", repoRoot, strings.Join(sentryFiles, ", "), filepath.Join(repoRoot, "skills")))
	}

	ccSwitchDir := filepath.Join(home, ".cc-switch")
	if hasDir(ccSwitchDir) {
		overlap := false
		if ccSwitchStoresInAgentsFolder(ccSwitchDir) {
			overlap = true
			warnings = append(warnings, "cc-switch stores skills in ~/.agents/skills, so skills it installs land in the tackroom repo; switch its skill storage back to CC Switch")
		}
		if links := foreignSkillLinks(cfg, home, ccSwitchDir); len(links) > 0 {
			overlap = true
			warnings = append(warnings, "skill links managed by cc-switch ("+strings.Join(links, "; ")+"); manage each skill in one tool")
		}
		if !overlap {
			notes = append(notes, "cc-switch keeps its own skill store")
		}
	}

	if hasDir(filepath.Join(home, ".harnesskit")) {
		notes = append(notes, "HarnessKit found: reading is fine, its deploy and enable actions bypass tackroom")
	}

	switch {
	case len(warnings) > 0:
		return checkResult{"other agent managers", checkStatusWarn, strings.Join(append(warnings, notes...), "; ")}
	case len(notes) > 0:
		return checkResult{"other agent managers", checkStatusPass, strings.Join(notes, "; ")}
	default:
		return checkResult{"other agent managers", checkStatusPass, "no other agent managers found"}
	}
}

// ccSwitchStoresInAgentsFolder reports whether cc-switch is set to keep its
// skills in ~/.agents/skills ("unified" storage) instead of its own folder.
func ccSwitchStoresInAgentsFolder(ccSwitchDir string) bool {
	data, err := os.ReadFile(filepath.Join(ccSwitchDir, "settings.json"))
	if err != nil {
		return false
	}
	var settings struct {
		SkillStorageLocation string `json:"skillStorageLocation"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return false
	}
	return settings.SkillStorageLocation == "unified"
}

// foreignSkillLinks lists, per enabled agent, skill links that resolve into
// another tool's store, formatted as "agent: a, b".
func foreignSkillLinks(cfg config, home string, store string) []string {
	var out []string
	for _, agent := range cfg.Agents {
		if !agent.Enabled || agent.SkillRoot == "" {
			continue
		}
		root := expandPath(agent.SkillRoot, home)
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		var names []string
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink == 0 {
				continue
			}
			target, err := os.Readlink(filepath.Join(root, entry.Name()))
			if err != nil {
				continue
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(root, target)
			}
			if rel, err := filepath.Rel(store, filepath.Clean(target)); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				names = append(names, entry.Name())
			}
		}
		if len(names) > 0 {
			sort.Strings(names)
			out = append(out, agent.Name+": "+strings.Join(names, ", "))
		}
	}
	return out
}
