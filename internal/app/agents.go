package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yourconscience/tackroom/internal/agentrole"
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
		target, content, ok := renderAgentRole(role, agent)
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

func renderAgentRole(role agentrole.Role, agent agentConfig) (string, string, bool) {
	h := harnessFor(agent.Name)
	if h == nil || h.roles == nil {
		return "", "", false
	}
	if role.Model == "" && agent.RoleModel != "" {
		role.Model = agent.RoleModel
	}
	target := filepath.Join(agent.AgentRoot, role.Name+h.roles.Extension())
	return target, h.roles.Render(role), true
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
		report.Conflicts = append(report.Conflicts, fmt.Sprintf("agent %s exists but is not tackroom-managed", rendered.Target))
	}
	return nil
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
		if len(report.Conflicts) > 0 {
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
