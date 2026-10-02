package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
	"github.com/yourconscience/tackroom/internal/agentrole"
)

// reviewTTYAvailable reports whether the review TUI can run: setup must be
// wired to the real stdin/stdout and both must be terminals.
func reviewTTYAvailable(streams setupIO) bool {
	if streams.in != os.Stdin || streams.out != os.Stdout {
		return false
	}
	return isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
}

// runReviewImport shows the unified review screen and applies the decisions.
// Returns errReviewAborted when the user quits without applying.
var errReviewAborted = errors.New("review aborted")

func runReviewImport(root string, cfg *config, detection *detectionResult, skills []nativeSkillCandidate, roles []nativeRoleCandidate, mcps []nativeMCPCandidate, streams setupIO) error {
	if len(detection.Items) == 0 && len(detection.Resolved) == 0 {
		return nil
	}
	program := tea.NewProgram(newReviewModel(detection))
	final, err := program.Run()
	if err != nil {
		return fmt.Errorf("review screen: %w", err)
	}
	model, ok := final.(reviewModel)
	if !ok {
		return errors.New("review screen returned unexpected model")
	}
	if model.aborted {
		return errReviewAborted
	}
	decisions := model.decisions(detection)
	shared, err := applyReviewDecisions(root, cfg, decisions, skills, roles, mcps)
	if err != nil {
		return err
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
	return nil
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

// renderDetectionSummary is the plain-text detection view used by --dry-run.
func renderDetectionSummary(detection *detectionResult) string {
	var b strings.Builder
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
// source when content differs. Used by --yes non-interactive imports.
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
