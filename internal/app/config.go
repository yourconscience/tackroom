package app

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func loadContext(opts runOptions) (string, string, config, []agentConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", config{}, nil, fmt.Errorf("resolve home: %w", err)
	}

	configPath, err := resolveConfigPath(opts.ConfigPath, home)
	if err != nil {
		return "", "", config{}, nil, err
	}
	repoRoot := filepath.Dir(configPath)
	if err := refuseWorktreeRoot(repoRoot); err != nil {
		return "", "", config{}, nil, err
	}

	var cfg config
	if opts.ConfigOverride != nil {
		cfg = *opts.ConfigOverride
	} else {
		cfg, err = loadConfig(repoRoot, home, configPath)
		if err != nil {
			return "", "", config{}, nil, err
		}
	}

	selected, err := selectAgents(cfg, opts.Agents)
	if err != nil {
		return "", "", config{}, nil, err
	}

	return repoRoot, home, cfg, selected, nil
}
func loadConfig(repoRoot string, home string, overridePath string) (config, error) {
	configPath := overridePath
	if strings.TrimSpace(configPath) == "" {
		configPath = defaultConfigPath(repoRoot)
	}
	doc, err := newConfigDocument(configPath, home)
	if err != nil {
		return config{}, err
	}
	return doc.effective, nil
}

func mergeConfig(base *config, overlay config) {
	base.Agents = mergeByKey(base.Agents, overlay.Agents, func(a agentConfig) string { return normalizeAgentName(a.Name) })
	base.ExternalSkills = mergeByKey(base.ExternalSkills, overlay.ExternalSkills, func(s externalSkillSource) string { return repoName(s.URL) })
	base.MCPServers = mergeByKey(base.MCPServers, overlay.MCPServers, func(s mcpServerConfig) string { return strings.TrimSpace(s.Name) })
	base.Hooks = mergeByKey(base.Hooks, overlay.Hooks, func(h hookConfig) string { return strings.TrimSpace(h.Name) })
	base.PublishTargets = mergeByKey(base.PublishTargets, overlay.PublishTargets, func(t publishTarget) string { return strings.TrimSpace(t.Name) })
	if overlay.ContextNoteTokens != nil {
		base.ContextNoteTokens = overlay.ContextNoteTokens
	}
	if overlay.UI != nil {
		copyUI := *overlay.UI
		copyUI.Links = append([]uiLink(nil), overlay.UI.Links...)
		base.UI = &copyUI
	}
}

func mergeByKey[T any](base []T, overlay []T, key func(T) string) []T {
	if len(overlay) == 0 {
		return base
	}
	index := make(map[string]int, len(base))
	for i, item := range base {
		index[key(item)] = i
	}
	for _, item := range overlay {
		if i, ok := index[key(item)]; ok {
			base[i] = item
		} else {
			base = append(base, item)
		}
	}
	return base
}

func defaultConfigPath(repoRoot string) string {
	return filepath.Join(repoRoot, "tackroom.yaml")
}

func validateConfig(cfg *config, home string, expand bool) error {
	if cfg.Version == 0 {
		cfg.Version = 1
	}

	seen := make(map[string]struct{})
	for i := range cfg.Agents {
		cfg.Agents[i].Name = normalizeAgentName(cfg.Agents[i].Name)
		if expand {
			cfg.Agents[i].SkillRoot = expandPath(cfg.Agents[i].SkillRoot, home)
			cfg.Agents[i].AgentRoot = expandPath(cfg.Agents[i].AgentRoot, home)
		}
		if cfg.Agents[i].Name == "" {
			return errors.New("config agent name cannot be empty")
		}
		if cfg.Agents[i].SkillRoot == "" {
			return fmt.Errorf("config agent %s is missing skill_root", cfg.Agents[i].Name)
		}
		if _, ok := seen[cfg.Agents[i].Name]; ok {
			return fmt.Errorf("config agent %s is duplicated", cfg.Agents[i].Name)
		}
		seen[cfg.Agents[i].Name] = struct{}{}

		if cfg.Agents[i].Packages != nil {
			packages := *cfg.Agents[i].Packages
			seenPackages := make(map[string]struct{}, len(packages))
			for j, pkg := range packages {
				pkg = strings.TrimSpace(pkg)
				if pkg == "" {
					return fmt.Errorf("config agent %s has an empty package", cfg.Agents[i].Name)
				}
				if _, ok := seenPackages[pkg]; ok {
					return fmt.Errorf("config agent %s has duplicate package %q", cfg.Agents[i].Name, pkg)
				}
				seenPackages[pkg] = struct{}{}
				packages[j] = pkg
			}
			cfg.Agents[i].Packages = &packages
			if cfg.Agents[i].Name != agentPi {
				return fmt.Errorf("config agent %s does not support packages", cfg.Agents[i].Name)
			}
		}
	}

	seenExt := make(map[string]struct{})
	for i := range cfg.ExternalSkills {
		src := &cfg.ExternalSkills[i]
		src.URL = strings.TrimSpace(src.URL)
		if src.URL == "" {
			return errors.New("config external_skills entry has empty url")
		}
		src.Branch = strings.TrimSpace(src.Branch)
		if src.Branch == "" {
			src.Branch = "main"
		}
		src.SkillDir = strings.TrimSpace(src.SkillDir)
		if src.SkillDir != "" && len(src.SkillDirs) > 0 {
			return fmt.Errorf("config external_skills repo %q cannot set both skill_dir and skill_dirs", repoName(src.URL))
		}
		if src.SkillDir == "" && len(src.SkillDirs) == 0 {
			src.SkillDir = "skills"
		}
		seenDirs := make(map[string]struct{})
		if src.SkillDir != "" {
			clean, err := cleanExternalSkillDir(src.SkillDir)
			if err != nil {
				return fmt.Errorf("config external_skills repo %q: %w", repoName(src.URL), err)
			}
			src.SkillDir = clean
		}
		for j := range src.SkillDirs {
			clean, err := cleanExternalSkillDir(src.SkillDirs[j])
			if err != nil {
				return fmt.Errorf("config external_skills repo %q: %w", repoName(src.URL), err)
			}
			if _, exists := seenDirs[clean]; exists {
				return fmt.Errorf("config external_skills repo %q has duplicate skill_dirs path %q", repoName(src.URL), clean)
			}
			seenDirs[clean] = struct{}{}
			src.SkillDirs[j] = clean
		}
		name := repoName(src.URL)
		if name == "" || name == "." || name == ".." {
			return fmt.Errorf("config external_skills: cannot derive safe repo name from %q", src.URL)
		}
		if _, ok := seenExt[name]; ok {
			return fmt.Errorf("config external_skills repo %q is duplicated", name)
		}
		seenExt[name] = struct{}{}

		if src.MCP && len(src.MCPAgents) > 0 {
			kept := src.MCPAgents[:0]
			for _, agentName := range src.MCPAgents {
				agentName = normalizeAgentName(agentName)
				if agentName == "" {
					continue
				}
				if len(seen) > 0 {
					if _, ok := seen[agentName]; !ok {
						return fmt.Errorf("config external_skills repo %q mcp_agents targets unknown agent %q", name, agentName)
					}
				}
				if !hasMCPSupport(agentName) {
					fmt.Fprintf(os.Stderr, "warning: config external_skills repo %q mcp_agents targets agent %q without MCP support; target ignored\n", name, agentName)
					continue
				}
				kept = append(kept, agentName)
			}
			src.MCPAgents = kept
		}
	}

	seenMCP := make(map[string]struct{})
	for i := range cfg.MCPServers {
		cfg.MCPServers[i].Name = strings.TrimSpace(cfg.MCPServers[i].Name)
		if cfg.MCPServers[i].Name == "" {
			return errors.New("config MCP server name cannot be empty")
		}
		if strings.TrimSpace(cfg.MCPServers[i].Command) == "" {
			return fmt.Errorf("config MCP server %s is missing command", cfg.MCPServers[i].Name)
		}
		if _, ok := seenMCP[cfg.MCPServers[i].Name]; ok {
			return fmt.Errorf("config MCP server %s is duplicated", cfg.MCPServers[i].Name)
		}
		seenMCP[cfg.MCPServers[i].Name] = struct{}{}
		kept := cfg.MCPServers[i].Agents[:0]
		for j := range cfg.MCPServers[i].Agents {
			agentName := normalizeAgentName(cfg.MCPServers[i].Agents[j])
			if len(seen) > 0 {
				if _, ok := seen[agentName]; !ok {
					return fmt.Errorf("config MCP server %s targets unknown agent %q", cfg.MCPServers[i].Name, agentName)
				}
			}
			// Legacy configs predate the Pi/OMP split and target "pi" with MCP.
			// Dropping the target with a warning keeps setup/status/doctor usable
			// on old configs instead of hard-failing on first contact.
			if !hasMCPSupport(agentName) {
				hint := ""
				if agentName == agentPi {
					hint = ` (if this config predates the Pi/OMP split, rename "pi" to "omp" in tackroom.yaml)`
				}
				fmt.Fprintf(os.Stderr, "warning: config MCP server %s targets agent %q without MCP support; target ignored%s\n", cfg.MCPServers[i].Name, agentName, hint)
				continue
			}
			kept = append(kept, agentName)
		}
		cfg.MCPServers[i].Agents = kept
	}

	seenHooks := make(map[string]struct{})
	for i := range cfg.Hooks {
		cfg.Hooks[i].Name = strings.TrimSpace(cfg.Hooks[i].Name)
		cfg.Hooks[i].Event = strings.TrimSpace(cfg.Hooks[i].Event)
		cfg.Hooks[i].Command = strings.TrimSpace(cfg.Hooks[i].Command)
		if cfg.Hooks[i].Name == "" {
			return errors.New("config hook name cannot be empty")
		}
		if cfg.Hooks[i].Event == "" {
			return fmt.Errorf("config hook %s is missing event", cfg.Hooks[i].Name)
		}
		if cfg.Hooks[i].Command == "" {
			return fmt.Errorf("config hook %s is missing command", cfg.Hooks[i].Name)
		}
		if cfg.Hooks[i].Timeout < 0 {
			return fmt.Errorf("config hook %s has negative timeout", cfg.Hooks[i].Name)
		}
		if _, ok := seenHooks[cfg.Hooks[i].Name]; ok {
			return fmt.Errorf("config hook %s is duplicated", cfg.Hooks[i].Name)
		}
		seenHooks[cfg.Hooks[i].Name] = struct{}{}
		for j := range cfg.Hooks[i].Agents {
			cfg.Hooks[i].Agents[j] = normalizeAgentName(cfg.Hooks[i].Agents[j])
			agentName := cfg.Hooks[i].Agents[j]
			if len(seen) > 0 {
				if _, ok := seen[agentName]; !ok {
					return fmt.Errorf("config hook %s targets unknown agent %q", cfg.Hooks[i].Name, agentName)
				}
			}
		}
	}

	if cfg.UI != nil {
		seenLinks := make(map[string]struct{}, len(cfg.UI.Links))
		for i := range cfg.UI.Links {
			link := &cfg.UI.Links[i]
			link.Name = strings.TrimSpace(link.Name)
			link.URL = strings.TrimSpace(link.URL)
			if link.Name == "" {
				return errors.New("config ui link name cannot be empty")
			}
			if _, ok := seenLinks[link.Name]; ok {
				return fmt.Errorf("config ui link %s is duplicated", link.Name)
			}
			seenLinks[link.Name] = struct{}{}
			if strings.HasPrefix(link.URL, "/") && !strings.HasPrefix(link.URL, "//") {
				continue
			}
			parsed, err := url.Parse(link.URL)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
				return fmt.Errorf("config ui link %s must be an absolute https URL or origin-relative path", link.Name)
			}
		}
	}

	seenPub := make(map[string]struct{})
	for i := range cfg.PublishTargets {
		t := &cfg.PublishTargets[i]
		t.Name = strings.TrimSpace(t.Name)
		t.Kind = strings.TrimSpace(t.Kind)
		t.VersionStrategy = strings.TrimSpace(t.VersionStrategy)
		t.APIKeyEnv = strings.TrimSpace(t.APIKeyEnv)
		if t.Name == "" {
			return errors.New("config publish target name cannot be empty")
		}
		if _, ok := seenPub[t.Name]; ok {
			return fmt.Errorf("config publish target %s is duplicated", t.Name)
		}
		seenPub[t.Name] = struct{}{}
		if t.Kind == "" {
			t.Kind = publishKindOpenAISkills
		}
		if t.Kind != publishKindOpenAISkills {
			return fmt.Errorf("config publish target %s has unsupported kind %q (only %q)", t.Name, t.Kind, publishKindOpenAISkills)
		}
		if t.VersionStrategy == "" {
			t.VersionStrategy = publishStrategyNewVersion
		}
		if t.VersionStrategy != publishStrategyNewVersion && t.VersionStrategy != publishStrategySetDefault {
			return fmt.Errorf("config publish target %s has unsupported version_strategy %q (%q or %q)", t.Name, t.VersionStrategy, publishStrategyNewVersion, publishStrategySetDefault)
		}
		if t.APIKeyEnv == "" {
			t.APIKeyEnv = publishDefaultAPIKeyEnv
		}
		seenSkills := make(map[string]struct{}, len(t.Skills))
		for j := range t.Skills {
			t.Skills[j] = strings.TrimSpace(t.Skills[j])
			if t.Skills[j] == "" {
				continue
			}
			if _, ok := seenSkills[t.Skills[j]]; ok {
				return fmt.Errorf("config publish target %s lists skill %q more than once", t.Name, t.Skills[j])
			}
			seenSkills[t.Skills[j]] = struct{}{}
		}
	}

	return nil
}

func selectAgents(cfg config, override string) ([]agentConfig, error) {
	index := make(map[string]agentConfig, len(cfg.Agents))
	for _, agent := range cfg.Agents {
		index[agent.Name] = agent
	}

	if strings.TrimSpace(override) == "" {
		var selected []agentConfig
		for _, agent := range cfg.Agents {
			if agent.Enabled {
				selected = append(selected, agent)
			}
		}
		if len(selected) == 0 {
			return nil, errors.New("config has no enabled agents; use --agents to override")
		}
		return selected, nil
	}

	var selected []agentConfig
	seen := make(map[string]struct{})
	for _, part := range strings.Split(override, ",") {
		name := normalizeAgentName(part)
		if name == "" {
			continue
		}
		agent, ok := index[name]
		if !ok {
			return nil, fmt.Errorf("unknown agent %q in --agents", name)
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		selected = append(selected, agent)
	}
	if len(selected) == 0 {
		return nil, errors.New("--agents did not resolve to any configured agents")
	}
	return selected, nil
}

func findRoots() (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("resolve home: %w", err)
	}
	configPath, err := resolveConfigPath("", home)
	if err != nil {
		return "", "", err
	}
	root := filepath.Dir(configPath)
	return root, filepath.Join(root, "skills", "tackroom"), nil
}

func resolveConfigPath(overridePath string, home string) (string, error) {
	path := strings.TrimSpace(overridePath)
	if path != "" {
		return absoluteExpandedPath(path, home)
	}
	if env := strings.TrimSpace(os.Getenv("TACKROOM_HOME")); env != "" {
		root, err := absoluteExpandedPath(env, home)
		if err != nil {
			return "", err
		}
		return filepath.Join(root, "tackroom.yaml"), nil
	}
	root := filepath.Join(home, ".agents")
	return filepath.Join(root, "tackroom.yaml"), nil
}

func absoluteExpandedPath(path string, home string) (string, error) {
	expanded := expandPath(path, home)
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	return abs, nil
}

// refuseWorktreeRoot rejects repo roots that are (or live under) a linked
// git worktree. sync materializes absolute links into the repo root; when
// the worktree is removed those links dangle and every managed skill
// breaks (ENOENT). The canonical checkout is the only valid root.
//
// Two detections: a linked worktree has a .git FILE pointing at its git
// dir (the canonical checkout has a .git directory or none), and the
// project convention places worktrees under .worktrees/, which catches
// roots whose .git check cannot run (missing dir, odd layouts).
func refuseWorktreeRoot(repoRoot string) error {
	resolved := repoRoot
	if real, err := filepath.EvalSymlinks(repoRoot); err == nil {
		resolved = real
	}
	refuse := func() error {
		return fmt.Errorf("refusing to run with repo root %s: it is (or lives inside) a linked git worktree; materialized links would dangle when the worktree is removed — run from the canonical checkout", repoRoot)
	}
	if fi, err := os.Stat(filepath.Join(resolved, ".git")); err == nil && fi.Mode().IsRegular() {
		return refuse()
	}
	for _, seg := range strings.Split(filepath.ToSlash(resolved), "/") {
		if seg == ".worktrees" {
			return refuse()
		}
	}
	return nil
}

func expandPath(path string, home string) string {
	path = strings.TrimSpace(path)
	switch {
	case path == "~":
		return home
	case strings.HasPrefix(path, "~/"):
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	default:
		return path
	}
}

func normalizeAgentName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
