package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/yourconscience/tackroom/internal/agentrole"
)

// reviewTTYAvailable reports whether the review TUI can run: setup must be
// wired to the real stdin/stdout and both must be terminals.
func reviewTTYAvailable(streams setupIO) bool {
	if streams.in != os.Stdin || streams.out != os.Stdout {
		return false
	}
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

// runReviewImport shows the unified review screen, applies the decisions and
// returns them. Returns errReviewAborted when the user quits without applying.
var errReviewAborted = errors.New("review aborted")

func runReviewImport(root string, cfg *config, detection *detectionResult, skills []nativeSkillCandidate, roles []nativeRoleCandidate, mcps []nativeMCPCandidate, streams setupIO) ([]reviewDecision, error) {
	if len(detection.Items) == 0 && len(detection.Resolved) == 0 {
		return nil, nil
	}
	program := tea.NewProgram(newReviewModel(detection))
	final, err := program.Run()
	if err != nil {
		return nil, fmt.Errorf("review screen: %w", err)
	}
	model, ok := final.(reviewModel)
	if !ok {
		return nil, errors.New("review screen returned unexpected model")
	}
	if model.aborted {
		return nil, errReviewAborted
	}
	decisions := model.decisions(detection)
	shared, err := applyReviewDecisions(root, cfg, decisions, skills, roles, mcps)
	if err != nil {
		return nil, err
	}
	skipped := 0
	kept := 0
	for _, d := range decisions {
		switch d.Action {
		case actionKeep:
			kept++
		case actionSkip:
			skipped++
		}
	}
	fmt.Fprintf(streams.out, "review: %d shared, %d kept harness-specific, %d skipped\n\n", shared, kept, skipped)
	return decisions, nil
}

// applyReviewDecisions imports every share decision using the matching scan
// candidate, which carries the payload (role conversion, MCP server config).
// Keep and skip decisions leave native content untouched.
func applyReviewDecisions(root string, cfg *config, decisions []reviewDecision, skills []nativeSkillCandidate, roles []nativeRoleCandidate, mcps []nativeMCPCandidate) (int, error) {
	shared := 0
	for _, d := range decisions {
		if d.Action != actionShare {
			continue
		}
		switch d.Surface {
		case "skill":
			if err := copyDirMissing(filepath.Join(root, "skills", d.Name), d.Source.Path); err != nil {
				return shared, err
			}
			shared++
		case "role":
			role, ok := findRoleCandidate(roles, d.Name, d.Source.Harness)
			if !ok {
				continue
			}
			if err := importRoleCandidate(root, role); err != nil {
				return shared, err
			}
			shared++
		case "mcp":
			mcp, ok := findMCPCandidate(mcps, d.Name, d.Source.Harness)
			if !ok {
				continue
			}
			server := mcp.Server
			server.Agents = []string{mcp.Origin}
			if err := upsertCanonicalMCPServer(cfg, server); err != nil {
				return shared, err
			}
			shared++
		}
	}
	return shared, nil
}

// renderSetupPlan is `setup --dry-run`: what the default run would import,
// whose copy it keeps, and which roles stay each agent's own. root is the
// config root as the run will find it (for --from, a throwaway clone);
// shown is how to name it.
func renderSetupPlan(detection *detectionResult, decisions []reviewDecision, kept map[string]string, agents []agentConfig, root string, shown string, home string) string {
	var b strings.Builder
	for _, w := range detection.Warnings {
		b.WriteString("warning: " + w + "\n")
	}
	nouns := map[string]string{"skill": "skill", "role": "role", "mcp": "MCP server"}
	var imports, keeps []string
	for _, item := range append(append([]detectedItem(nil), detection.Items...), detection.Resolved...) {
		label := nouns[item.Surface] + " " + item.Name
		existing := ""
		switch item.Surface {
		case "skill":
			existing = filepath.Join(root, "skills", item.Name)
		case "role":
			existing = filepath.Join(root, "agents", item.Name+agentrole.MarkdownExtension)
		}
		if _, err := os.Stat(existing); existing != "" && err == nil {
			// The config root already has it: that copy stays, and each
			// native copy is backed up and replaced, or (a role tackroom
			// cannot write the same) left in place and listed.
			var notes []string
			for _, src := range item.Sources {
				notes = append(notes, existingCopyNote(item.Surface, src, existing, agents))
			}
			imports = append(imports, label+": the copy already in "+shown+" ("+strings.Join(notes, "; ")+")")
			continue
		}
		if text, ok := kept[item.Name]; ok && item.Surface == "role" {
			keeps = append(keeps, label+" "+strings.ReplaceAll(text, home+string(filepath.Separator), "~"+string(filepath.Separator)))
			continue
		}
		var winner string
		for _, d := range decisions {
			if d.Surface == item.Surface && d.Name == item.Name && d.Action == actionShare {
				winner = d.Source.Harness
			}
		}
		line := label + ": " + winner + "'s copy"
		var notes []string
		for _, src := range item.Sources {
			switch {
			case src.Harness == winner:
			case item.Surface == "mcp":
				notes = append(notes, src.Harness+" keeps its own")
			case item.Surface == "role" || item.Identical:
				notes = append(notes, "same in "+src.Harness)
			default:
				notes = append(notes, src.Harness+"'s differs")
			}
		}
		if len(notes) > 0 {
			line += " (" + strings.Join(notes, "; ") + ")"
		}
		imports = append(imports, line)
	}
	if len(imports) == 0 && len(keeps) == 0 {
		b.WriteString("nothing to import\n")
		return b.String()
	}
	if len(imports) > 0 {
		fmt.Fprintf(&b, "would share from %s, keeping these copies:\n", shown)
		for _, line := range imports {
			b.WriteString("  " + line + "\n")
		}
	}
	if len(keeps) > 0 {
		b.WriteString("would not share; each agent keeps its own:\n")
		for _, line := range keeps {
			b.WriteString("  " + line + "\n")
		}
	}
	fmt.Fprintf(&b, "every native file or folder it replaces is backed up first under %s\n", displayPath(conflictBackupRoot(home), home))
	return b.String()
}

// existingCopyNote says what happens to one agent's copy of an item the config
// root already has.
func existingCopyNote(surface string, src detectedSource, existing string, agents []agentConfig) string {
	if surface == "skill" {
		if same, err := treesEqual(src.Path, existing); err == nil && same {
			return src.Harness + "'s is the same"
		}
		return src.Harness + "'s differs"
	}
	role, err := agentrole.LoadFile(existing)
	native, readErr := os.ReadFile(src.Path)
	if err != nil || readErr != nil {
		return src.Harness + "'s could not be compared"
	}
	agent := agentConfig{Name: src.Harness, AgentRoot: filepath.Dir(src.Path)}
	for _, a := range agents {
		if normalizeAgentName(a.Name) == src.Harness {
			agent.RoleModel = a.RoleModel
		}
	}
	_, content, ok, err := renderAgentRole(role, agent)
	if err != nil || !ok {
		return src.Harness + "'s could not be compared"
	}
	if why := roleNotReplaceable(src.Path, native, content); why != "" {
		return src.Harness + "'s would be left in place and listed: it " + why
	}
	return src.Harness + "'s is the same role"
}

// renderDetectionSummary is the plain-text detection view used by
// setup --review --dry-run.
func renderDetectionSummary(detection *detectionResult) string {
	var b strings.Builder
	for _, w := range detection.Warnings {
		b.WriteString("warning: " + w + "\n")
	}
	if len(detection.Items) == 0 && len(detection.Resolved) == 0 {
		b.WriteString("no import candidates detected\n")
		return b.String()
	}
	if len(detection.Items) > 0 {
		b.WriteString("needs review:\n")
		for _, item := range detection.Items {
			b.WriteString("  " + formatDetectedItem(item) + "\n")
		}
	}
	if len(detection.Resolved) > 0 {
		b.WriteString("identical everywhere (would be shared automatically):\n")
		for _, item := range detection.Resolved {
			b.WriteString("  " + formatDetectedItem(item) + "\n")
		}
	}
	return b.String()
}

func formatDetectedItem(item detectedItem) string {
	harnesses := make([]string, 0, len(item.Sources))
	for _, src := range item.Sources {
		harnesses = append(harnesses, src.Harness)
	}
	line := fmt.Sprintf("%-7s %s  [%s]", item.Surface, item.Name, strings.Join(harnesses, " "))
	if len(item.Sources) > 1 && !item.Identical {
		line += " (differ)"
	}
	return line
}

// shareAllDecisions marks every detected item for sharing, picking the first
// source when content differs. Used by setup's default, non-interactive import.
func shareAllDecisions(detection *detectionResult) []reviewDecision {
	out := make([]reviewDecision, 0, len(detection.Items)+len(detection.Resolved))
	for _, item := range append(append([]detectedItem(nil), detection.Items...), detection.Resolved...) {
		out = append(out, reviewDecision{
			Surface: item.Surface,
			Name:    item.Name,
			Action:  actionShare,
			Source:  item.Sources[0],
		})
	}
	return out
}

// setupImportDecisions is shareAllDecisions for setup's default import, except
// that a role is shared only when every agent's copy would survive being
// replaced by the shared one (see roleNotReplaceable). Otherwise each agent
// keeps its own copy, and the reason is returned per role name.
func setupImportDecisions(detection *detectionResult, roles []nativeRoleCandidate, agents []agentConfig, home string) ([]reviewDecision, map[string]string) {
	decisions := shareAllDecisions(detection)
	kept := map[string]string{}
	items := append(append([]detectedItem(nil), detection.Items...), detection.Resolved...)
	for i, d := range decisions {
		if d.Surface != "role" {
			continue
		}
		for _, item := range items {
			if item.Surface != "role" || item.Name != d.Name {
				continue
			}
			if why := roleKeptReason(item, roles, agents, home); why != "" {
				decisions[i].Action = actionKeep
				kept[d.Name] = keptRoleText(item, why)
			}
		}
	}
	return decisions, kept
}

// keptRoleText is the summary text for a role that is not shared, for one
// agent or several.
func keptRoleText(item detectedItem, why string) string {
	if len(item.Sources) == 1 {
		h := item.Sources[0].Harness
		return fmt.Sprintf("(%s): %s", h, strings.TrimPrefix(why, h+"'s "))
	}
	var harnesses []string
	for _, src := range item.Sources {
		harnesses = append(harnesses, src.Harness)
	}
	return fmt.Sprintf("(%s): %s", joinAnd(harnesses), why)
}

// roleKeptReason says why a detected role must stay each agent's own, or "".
func roleKeptReason(item detectedItem, roles []nativeRoleCandidate, agents []agentConfig, home string) string {
	for _, src := range item.Sources {
		if info, err := os.Lstat(src.Path); err == nil && info.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(src.Path)
			return fmt.Sprintf("%s's is a link to %s", src.Harness, displayPath(target, home))
		}
	}
	first := item.Sources[0].Harness
	chosen, ok := findRoleCandidate(roles, item.Name, first)
	if !ok {
		return fmt.Sprintf("%s's could not be read", first)
	}
	data, err := chosen.Convert()
	if err != nil {
		return fmt.Sprintf("%s's could not be converted (%v)", first, err)
	}
	role, err := agentrole.ParseMarkdown(chosen.Path, data)
	if err == nil {
		_, err = agentrole.RenderCanonical(role)
	}
	if err != nil {
		return fmt.Sprintf("%s's is not a complete role (%v)", first, err)
	}
	for _, src := range item.Sources {
		agent := agentConfig{Name: src.Harness, AgentRoot: filepath.Dir(src.Path)}
		for _, a := range agents {
			if normalizeAgentName(a.Name) == src.Harness {
				agent.RoleModel = a.RoleModel
			}
		}
		_, content, ok, err := renderAgentRole(role, agent)
		native, readErr := os.ReadFile(src.Path)
		if err != nil || !ok || readErr != nil {
			return fmt.Sprintf("%s's could not be compared with the shared role", src.Harness)
		}
		diff := roleDiff(native, []byte(content), filepath.Ext(src.Path) == ".toml")
		switch {
		case diff == "":
		case src.Harness != first:
			return fmt.Sprintf("%s's differs from %s's", src.Harness, first)
		default:
			return fmt.Sprintf("%s's %s", src.Harness, diff)
		}
	}
	return ""
}

func findRoleCandidate(roles []nativeRoleCandidate, name string, harness string) (nativeRoleCandidate, bool) {
	for _, role := range roles {
		if role.TargetName == name && role.Origin == harness {
			return role, true
		}
	}
	return nativeRoleCandidate{}, false
}

func findMCPCandidate(mcps []nativeMCPCandidate, name string, harness string) (nativeMCPCandidate, bool) {
	for _, mcp := range mcps {
		if mcp.Name == name && mcp.Origin == harness {
			return mcp, true
		}
	}
	return nativeMCPCandidate{}, false
}

func importRoleCandidate(root string, role nativeRoleCandidate) error {
	data, err := role.Convert()
	if err != nil {
		return err
	}
	path := filepath.Join(root, "agents", role.TargetName+agentrole.MarkdownExtension)
	if _, err := os.Lstat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
