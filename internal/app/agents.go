package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/yourconscience/tackroom/internal/agentrole"
	"gopkg.in/yaml.v3"
)

type renderedAgentRole struct {
	Name    string
	Source  string
	Target  string
	Content string
}

func expectedAgentRoles(repoRoot string, agent agentConfig) (map[string]renderedAgentRole, error) {
	if agent.AgentRoot == "" {
		return map[string]renderedAgentRole{}, nil
	}

	roles, err := agentrole.Load(repoRoot)
	if err != nil {
		return nil, err
	}

	rendered := make(map[string]renderedAgentRole)
	for _, role := range roles {
		target, content, ok, err := renderAgentRole(role, agent)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		rendered[role.Name] = renderedAgentRole{
			Name:    role.Name,
			Source:  role.Source,
			Target:  target,
			Content: content,
		}
	}
	return rendered, nil
}

func renderAgentRole(role agentrole.Role, agent agentConfig) (string, string, bool, error) {
	h := harnessFor(agent.Name)
	if h == nil || h.roles == nil {
		return "", "", false, nil
	}
	if role.Model == "" && agent.RoleModel != "" {
		role.Model = agent.RoleModel
	}
	target := filepath.Join(agent.AgentRoot, role.Name+h.roles.Extension())
	content, err := h.roles.Render(role)
	if err != nil {
		return "", "", false, err
	}
	return target, content, true, nil
}

func inspectAgentRoles(report *agentReport, repoRoot string, agent agentConfig) error {
	expected, err := expectedAgentRoles(repoRoot, agent)
	if err != nil {
		return err
	}
	if len(expected) == 0 {
		return nil
	}

	for _, name := range sortedAgentRoleNames(expected) {
		rendered := expected[name]
		data, err := os.ReadFile(rendered.Target)
		if errors.Is(err, fs.ErrNotExist) {
			report.MissingAgent = append(report.MissingAgent, name)
			report.AddsAgent = append(report.AddsAgent, name)
			continue
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", rendered.Target, err)
		}

		if string(data) == rendered.Content {
			report.ManagedAgent = append(report.ManagedAgent, name)
			continue
		}
		if isManagedAgentFile(rendered.Target, data, repoRoot) {
			report.DriftedAgent = append(report.DriftedAgent, name)
			report.UpdatesAgent = append(report.UpdatesAgent, name)
			continue
		}
		// A role is replaced only when the file tackroom writes keeps everything
		// the native one has; a link is never replaced, so it stays live.
		if why := roleNotReplaceable(rendered.Target, data, rendered.Content); why != "" {
			msg := fmt.Sprintf("role %s %s, so it was left in place (rename it to keep it, or move it out of %s to use the shared role %s)", rendered.Target, why, filepath.Dir(rendered.Target), name)
			report.Conflicts = append(report.Conflicts, msg)
			report.RoleConflicts = append(report.RoleConflicts, msg)
			continue
		}
		msg := fmt.Sprintf("agent %s exists but is not tackroom-managed", rendered.Target)
		report.Conflicts = append(report.Conflicts, msg)
		report.RoleConflicts = append(report.RoleConflicts, msg)
		report.Replaceable = append(report.Replaceable, replaceableConflict{Path: rendered.Target, Role: name, Message: msg})
	}
	return nil
}

// roleNotReplaceable says why the native role file at path must not be
// replaced by rendered, or "" when rendered is the same role: same
// instructions and the same fields with the same values.
func roleNotReplaceable(path string, native []byte, rendered string) string {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		target, _ := os.Readlink(path)
		return "is a link to " + target
	}
	return roleDiff(native, []byte(rendered), filepath.Ext(path) == ".toml")
}

// roleDiff says how rendered differs from a native role file, or "" when it
// is the same role. Fields the native file has that rendered drops, fields
// rendered would add, and fields or instructions it would change all count;
// only an added name (always the file's own name) does not.
func roleDiff(native []byte, rendered []byte, isTOML bool) string {
	if !isTOML && !strings.HasPrefix(strings.ReplaceAll(string(native), "\r\n", "\n"), "---\n") {
		// A shared role always has frontmatter; this one has none to keep.
		return "has no frontmatter (name and description)"
	}
	have, body, err := roleFileFields(native, isTOML)
	if err != nil {
		return "cannot be read as a role: " + err.Error()
	}
	want, wantBody, err := roleFileFields(rendered, isTOML)
	if err != nil {
		return "cannot be compared with the shared role: " + err.Error()
	}
	var missing, changed, added []string
	for _, key := range sortedKeys(have) {
		value, ok := want[key]
		switch {
		case !ok:
			missing = append(missing, key)
		case value != have[key]:
			changed = append(changed, key)
		}
	}
	for _, key := range sortedKeys(want) {
		if _, ok := have[key]; !ok && key != "name" {
			added = append(added, key)
		}
	}
	if body != wantBody {
		changed = append(changed, "instructions")
	}
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "has "+joinAnd(missing)+" that tackroom cannot write")
	}
	if len(added) > 0 {
		parts = append(parts, "would gain "+joinAnd(added))
	}
	if len(changed) > 0 {
		parts = append(parts, "would get different "+joinAnd(changed))
	}
	return strings.Join(parts, "; ")
}

// roleFileFields reads a role file: YAML frontmatter Markdown, or Codex TOML
// whose developer_instructions is the body. Values are flattened to text so
// a list and a comma-separated string compare equal, and tackroom's own
// generated-by comment is dropped from the body.
func roleFileFields(data []byte, isTOML bool) (map[string]string, string, error) {
	raw := map[string]any{}
	body := ""
	if isTOML {
		if _, err := toml.Decode(string(data), &raw); err != nil {
			return nil, "", err
		}
		body, _ = raw["developer_instructions"].(string)
		delete(raw, "developer_instructions")
	} else {
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		body = text
		if strings.HasPrefix(text, "---\n") {
			rest := text[len("---\n"):]
			end := strings.Index(rest, "\n---")
			if end < 0 {
				return nil, "", errors.New("unterminated frontmatter")
			}
			if err := yaml.Unmarshal([]byte(rest[:end]), &raw); err != nil {
				return nil, "", err
			}
			body = rest[end+len("\n---"):]
		}
	}
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "<!--") && (strings.Contains(trimmed, agentrole.GeneratedMarker) || strings.Contains(trimmed, agentrole.LegacyGeneratedMarker)) {
			continue
		}
		lines = append(lines, line)
	}
	fields := make(map[string]string, len(raw))
	for key, value := range raw {
		fields[key] = roleFieldText(value)
		if key == "name" {
			fields[key] = strings.ToLower(fields[key])
		}
	}
	return fields, strings.TrimSpace(strings.Join(lines, "\n")), nil
}

func roleFieldText(value any) string {
	switch v := value.(type) {
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = roleFieldText(item)
		}
		return strings.Join(parts, ",")
	case string:
		parts := strings.Split(v, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprint(v)
	}
}

func isManagedAgentFile(path string, data []byte, repoRoot string) bool {
	if strings.Contains(string(data), agentrole.GeneratedMarker) || strings.Contains(string(data), agentrole.LegacyGeneratedMarker) {
		return true
	}

	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return false
	}
	rawTarget, err := os.Readlink(path)
	if err != nil {
		return false
	}
	targetAbs := absoluteTarget(path, rawTarget)
	repoAgentsRoot := filepath.Join(repoRoot, "agents")
	return targetAbs == repoAgentsRoot || strings.HasPrefix(targetAbs, repoAgentsRoot+string(os.PathSeparator))
}

func applyAgentRoleSync(reports []agentReport, selected []agentConfig, repoRoot string) error {
	agentIndex := make(map[string]agentConfig, len(selected))
	for _, agent := range selected {
		agentIndex[agent.Name] = agent
	}
	for _, report := range reports {
		if !report.Detected {
			continue
		}
		agent, ok := agentIndex[report.Name]
		if !ok || agent.AgentRoot == "" {
			continue
		}
		if len(blockingConflicts(report)) > 0 {
			return fmt.Errorf("%s has conflicts", report.Name)
		}
		expected, err := expectedAgentRoles(repoRoot, agent)
		if err != nil {
			return err
		}
		if len(report.AddsAgent)+len(report.UpdatesAgent) == 0 {
			continue
		}
		if err := os.MkdirAll(agent.AgentRoot, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", agent.AgentRoot, err)
		}
		for _, name := range append(append([]string{}, report.AddsAgent...), report.UpdatesAgent...) {
			rendered, ok := expected[name]
			if !ok {
				continue
			}
			if err := os.Remove(rendered.Target); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("remove %s before rewrite: %w", rendered.Target, err)
			}
			if err := os.WriteFile(rendered.Target, []byte(rendered.Content), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", rendered.Target, err)
			}
		}
	}
	return nil
}

func sortedAgentRoleNames(roles map[string]renderedAgentRole) []string {
	names := make([]string, 0, len(roles))
	for name := range roles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
