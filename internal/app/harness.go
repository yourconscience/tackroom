package app

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"

	"github.com/yourconscience/tackroom/internal/agentrole"
)

// skillsKind describes how an agent discovers skills from tackroom.
type skillsKind int

const (
	// skillsSymlink means tackroom creates symlinks in the agent's skill root.
	skillsSymlink skillsKind = iota
	// skillsConfigDriven means the agent reads skills from a config-driven
	// shared path (e.g. Amp's amp.skills.path or Hermes' skills.external_dirs).
	// Inspect and setup are handled by custom functions on the harness.
	skillsConfigDriven
)

// rootInstructionsCapability describes a root instructions symlink the agent needs.
type rootInstructionsCapability struct {
	Path     func(home string) string     // symlink path, e.g. ~/.factory/AGENTS.md
	Expected func(repoRoot string) string // target path, e.g. <config-root>/AGENTS.md
}

// doctorCheck is a named health check contributed by a harness.
type doctorCheck struct {
	Name string
	Run  func(repoRoot, home string, cfg config) checkResult
}

// inspectSkillsFunc is the signature for config-driven skill inspection.
// Only used when Skills == skillsConfigDriven.
type inspectSkillsFunc func(agent agentConfig, expected map[string]string, agentsSkillRoot string, cfg config, home string) (agentReport, error)

// setupFunc is the signature for agent-specific config patching during setup.
type setupFunc func(home string, repoRoot string, cfg config) (bool, error)

// harness is the central descriptor for a coding agent integration.
// All feature dispatch reads capability fields instead of switching on agent names.
type harness struct {
	// Detect validates an executable after the generic PATH lookup.
	// nil means the executable's presence is sufficient.
	Detect func(executable string) bool

	// Skills integration mode.
	Skills skillsKind
	// InspectSkills is called instead of the generic symlink inspector
	// when Skills == skillsConfigDriven.
	InspectSkills inspectSkillsFunc
	// SkillsNativeRoot, when non-nil and returning true, marks that this
	// harness reads tackroom skills directly from the config root, so no
	// per-harness skill mirror is created. Only consulted for skillsSymlink
	// harnesses.
	SkillsNativeRoot func(repoRoot string, home string) bool
	// Setup patches the agent's config during `tackroom setup`.
	// nil means no patching needed.
	Setup setupFunc

	// MCP holds the MCP config target. nil = no MCP support.
	MCP *mcpTarget
	// roles holds the agent role rendering capability. nil = no role support.
	roles agentrole.Renderer
	// Hooks holds the hook config target. nil = no hook support.
	Hooks *hookTarget

	// RootInstructions describes a root instructions symlink. nil = none.
	RootInstructions *rootInstructionsCapability

	// IntegrationNote is printed in status/sync reports for this agent.
	// Empty string = no note.
	IntegrationNote string

	// doctorChecks are agent-specific health checks appended to `tackroom doctor`.
	doctorChecks []doctorCheck

	// TrailerExample is the Co-authored-by trailer this agent's bot produces.
	// Used by commit-msg hook tests to validate coverage.
	TrailerExample string
}

// harnesses maps agent name to its harness descriptor.
// Built at init time with current behavior encoded declaratively.
var harnesses map[string]*harness
var harnessesOnce sync.Once

func initHarnesses() {
	harnesses = map[string]*harness{
		"amp": {
			Skills: skillsConfigDriven,
			InspectSkills: func(agent agentConfig, expected map[string]string, agentsSkillRoot string, cfg config, home string) (agentReport, error) {
				return inspectAmpAgent(agent, expected, agentsSkillRoot, cfg, home)
			},
			Setup: func(home string, repoRoot string, _ config) (bool, error) {
				return patchAmpConfig(home, repoRoot)
			},
			MCP: mcpTargetPtr(mcpTarget{
				agentName:  "amp",
				configPath: ampSettingsPath,
				inspect:    inspectJSONMCPServer,
				patch:      patchJSONMCPServer,
				read:       readJSONMCPServer,
				rootKey:    "amp.mcpServers",
			}),
			RootInstructions: &rootInstructionsCapability{
				Path:     func(home string) string { return filepath.Join(home, ".config", "amp", "AGENTS.md") },
				Expected: func(repoRoot string) string { return filepath.Join(repoRoot, "AGENTS.md") },
			},
			IntegrationNote: "config-driven via Amp settings -> amp.skills.path",
			TrailerExample:  "Co-authored-by: amp[bot] <amp[bot]@users.noreply.github.com>",
		},

		agentClaudeCode: {
			Skills: skillsSymlink,
			MCP: mcpTargetPtr(mcpTarget{
				agentName:  agentClaudeCode,
				configPath: func(home string) string { return filepath.Join(home, ".claude.json") },
				inspect:    inspectJSONMCPServer,
				patch:      patchJSONMCPServer,
				read:       readClaudeMCPServer,
				rootKey:    "mcpServers",
				defaults:   map[string]interface{}{"type": "stdio"},
			}),
			roles: roleRenderer(agentClaudeCode),
			Hooks: &hookTarget{
				agentName: agentClaudeCode,
				inspect:   inspectClaudeHook,
				patch:     patchClaudeHook,
			},
			RootInstructions: &rootInstructionsCapability{
				Path:     func(home string) string { return filepath.Join(home, ".claude", "CLAUDE.md") },
				Expected: func(repoRoot string) string { return filepath.Join(repoRoot, "AGENTS.md") },
			},
			doctorChecks: []doctorCheck{
				{Name: "claude plugins", Run: func(repoRoot, _ string, cfg config) checkResult {
					return checkClaudePlugins(repoRoot, cfg)
				}},
			},
			TrailerExample: "Co-authored-by: claude[bot] <claude[bot]@users.noreply.github.com>",
		},

		"codex": {
			Skills: skillsSymlink,
			MCP: mcpTargetPtr(mcpTarget{
				agentName:  "codex",
				configPath: func(home string) string { return filepath.Join(home, ".codex", "config.toml") },
				inspect:    inspectCodexMCPServer,
				patch:      patchCodexMCPServer,
				read:       readCodexMCPServer,
			}),
			roles: roleRenderer("codex"),
			Hooks: &hookTarget{
				agentName: "codex",
				inspect:   inspectCodexHook,
				patch:     patchCodexHook,
			},
			RootInstructions: &rootInstructionsCapability{
				Path:     func(home string) string { return filepath.Join(home, ".codex", "AGENTS.md") },
				Expected: func(repoRoot string) string { return filepath.Join(repoRoot, "AGENTS.md") },
			},
			TrailerExample: "Co-Authored-By: codex[bot] <codex[bot]@users.noreply.github.com>",
		},

		"droid": {
			Skills: skillsSymlink,
			MCP: mcpTargetPtr(mcpTarget{
				agentName:  "droid",
				configPath: func(home string) string { return filepath.Join(home, ".factory", "mcp.json") },
				inspect:    inspectJSONMCPServer,
				patch:      patchJSONMCPServer,
				read:       readJSONMCPServer,
				rootKey:    "mcpServers",
				defaults:   map[string]interface{}{"type": "stdio", "disabled": false},
			}),
			roles: roleRenderer("droid"),
			Hooks: &hookTarget{
				agentName: "droid",
				inspect:   inspectDroidHook,
				patch:     patchDroidHook,
			},
			RootInstructions: &rootInstructionsCapability{
				Path:     func(home string) string { return filepath.Join(home, ".factory", "AGENTS.md") },
				Expected: func(repoRoot string) string { return filepath.Join(repoRoot, "AGENTS.md") },
			},
			TrailerExample: "Co-authored-by: factory-droid[bot] <factory-droid[bot]@users.noreply.github.com>",
		},

		"hermes": {
			Skills: skillsConfigDriven,
			InspectSkills: func(agent agentConfig, expected map[string]string, agentsSkillRoot string, cfg config, home string) (agentReport, error) {
				return inspectHermesAgent(agent, expected, agentsSkillRoot, cfg, home)
			},
			Setup: func(home string, repoRoot string, cfg config) (bool, error) {
				return patchHermesConfig(home, repoRoot, cfg)
			},
			MCP: mcpTargetPtr(mcpTarget{
				agentName:  "hermes",
				configPath: func(home string) string { return filepath.Join(home, ".hermes", "config.yaml") },
				inspect:    inspectYAMLMCPServer,
				patch:      patchYAMLMCPServer,
				read:       readYAMLMCPServer,
				rootKey:    "mcp_servers",
			}),
			Hooks: &hookTarget{
				agentName: "hermes",
				inspect:   inspectHermesHook,
				patch:     patchHermesHook,
			},
			IntegrationNote: "config-driven via ~/.hermes/config.yaml -> skills.external_dirs",
			doctorChecks: []doctorCheck{
				{Name: "hermes direct mirrors", Run: checkHermesDirectMirrors},
				{Name: "hermes hooks", Run: func(_, home string, cfg config) checkResult {
					return checkHermesHooks(home, cfg)
				}},
			},
			TrailerExample: "Co-Authored-By: hermes[bot] <hermes[bot]@users.noreply.github.com>",
		},

		agentOpenCode: {
			Skills:           skillsSymlink,
			SkillsNativeRoot: readsAgentsSkillsRoot,
			MCP: mcpTargetPtr(mcpTarget{
				agentName:  agentOpenCode,
				configPath: openCodeConfigPath,
				inspect:    inspectOpenCodeMCPServer,
				patch:      patchOpenCodeMCPServer,
				read:       readOpenCodeMCPServer,
				rootKey:    "mcp",
			}),
			roles:           roleRenderer(agentOpenCode),
			IntegrationNote: "skills read natively from ~/.agents/skills (mirrored into the skill root only when the config root differs)",
			doctorChecks: []doctorCheck{
				{Name: "opencode duplicate skills", Run: checkOpenCodeDuplicateSkills},
			},
		},

		agentPi: {
			Detect: detectVanillaPi,
			Skills: skillsSymlink,
			MCP: mcpTargetPtr(mcpTarget{
				agentName:  agentPi,
				configPath: func(home string) string { return filepath.Join(home, ".pi", "agent", "mcp.json") },
				inspect:    inspectJSONMCPServer,
				patch:      patchJSONMCPServer,
				read:       readJSONMCPServer,
				rootKey:    "mcpServers",
			}),
			roles: roleRenderer(agentPi),
			RootInstructions: &rootInstructionsCapability{
				Path:     func(home string) string { return filepath.Join(home, ".pi", "agent", "AGENTS.md") },
				Expected: func(repoRoot string) string { return filepath.Join(repoRoot, "AGENTS.md") },
			},
			IntegrationNote: "MCP and roles require pi-mcp-adapter and pi-subagents; Agent Plugins project through managed skills and MCP",
			TrailerExample:  "Co-authored-by: pi[bot] <pi[bot]@users.noreply.github.com>",
		},

		agentOMP: {
			Skills: skillsSymlink,
			MCP: mcpTargetPtr(mcpTarget{
				agentName:  agentOMP,
				configPath: func(home string) string { return filepath.Join(home, ".omp", "agent", "mcp.json") },
				inspect:    inspectJSONMCPServer,
				patch:      patchJSONMCPServer,
				read:       readJSONMCPServer,
				rootKey:    "mcpServers",
			}),
			roles: roleRenderer(agentOMP),
		},

		agentQwenCode: {
			Skills: skillsConfigDriven,
			InspectSkills: func(agent agentConfig, expected map[string]string, agentsSkillRoot string, cfg config, home string) (agentReport, error) {
				return inspectQwenAgent(agent, expected, agentsSkillRoot, cfg, home)
			},
			Setup: patchQwenConfig,
			MCP: mcpTargetPtr(mcpTarget{
				agentName:  agentQwenCode,
				configPath: qwenSettingsPath,
				inspect:    inspectJSONMCPServer,
				patch:      patchJSONMCPServer,
				read:       readJSONMCPServer,
				rootKey:    "mcpServers",
			}),
			roles: roleRenderer(agentQwenCode),
			Hooks: &hookTarget{
				agentName: agentQwenCode,
				inspect:   inspectQwenHook,
				patch:     patchQwenHook,
			},
			RootInstructions: &rootInstructionsCapability{
				Path:     func(home string) string { return filepath.Join(home, ".qwen", "QWEN.md") },
				Expected: func(repoRoot string) string { return filepath.Join(repoRoot, "AGENTS.md") },
			},
			IntegrationNote: "config-driven via ~/.qwen/settings.json -> skills.directories",
		},

		agentCursor: {
			Skills:           skillsSymlink,
			SkillsNativeRoot: readsAgentsSkillsRoot,
			MCP: mcpTargetPtr(mcpTarget{
				agentName:  agentCursor,
				configPath: func(home string) string { return filepath.Join(home, ".cursor", "mcp.json") },
				inspect:    inspectJSONMCPServer,
				patch:      patchJSONMCPServer,
				read:       readJSONMCPServer,
				rootKey:    "mcpServers",
			}),
			roles: roleRenderer(agentCursor),
			Hooks: &hookTarget{
				agentName: agentCursor,
				inspect:   inspectCursorHook,
				patch:     patchCursorHook,
			},
			IntegrationNote: "skills read natively from ~/.agents/skills; if Cursor's third-party configs are on, Claude Code hooks also run there",
		},

		agentCopilot: {
			Skills:           skillsSymlink,
			SkillsNativeRoot: readsAgentsSkillsRoot,
			MCP: mcpTargetPtr(mcpTarget{
				agentName:  agentCopilot,
				configPath: copilotMCPConfigPath,
				inspect:    inspectJSONMCPServer,
				patch:      patchCopilotMCPServer,
				read:       readJSONMCPServer,
				rootKey:    "mcpServers",
				defaults:   map[string]interface{}{"type": "local"},
			}),
			roles: roleRenderer(agentCopilot),
			Hooks: &hookTarget{
				agentName: agentCopilot,
				inspect:   inspectCopilotHook,
				patch:     patchCopilotHook,
			},
			RootInstructions: &rootInstructionsCapability{
				Path:     func(home string) string { return filepath.Join(copilotHome(home), "copilot-instructions.md") },
				Expected: func(repoRoot string) string { return filepath.Join(repoRoot, "AGENTS.md") },
			},
			IntegrationNote: "skills read natively from ~/.agents/skills",
		},

		agentGrok: {
			Skills:           skillsSymlink,
			SkillsNativeRoot: readsAgentsSkillsRoot,
			MCP: mcpTargetPtr(mcpTarget{
				agentName:  agentGrok,
				configPath: func(home string) string { return filepath.Join(grokHome(home), "config.toml") },
				inspect:    inspectCodexMCPServer,
				patch:      patchCodexMCPServer,
				read:       readCodexMCPServer,
			}),
			roles: roleRenderer(agentGrok),
			Hooks: &hookTarget{
				agentName: agentGrok,
				inspect:   inspectGrokHook,
				patch:     patchGrokHook,
			},
			RootInstructions: &rootInstructionsCapability{
				Path:     func(home string) string { return filepath.Join(grokHome(home), "AGENTS.md") },
				Expected: func(repoRoot string) string { return filepath.Join(repoRoot, "AGENTS.md") },
			},
			IntegrationNote: "skills read natively from ~/.agents/skills; Grok also reads Claude Code's skills, roles, hooks and MCP and dedups them by name",
		},

		agentOpenClaw: {
			Skills:           skillsSymlink,
			SkillsNativeRoot: openClawReadsAgentsSkills,
			MCP: mcpTargetPtr(mcpTarget{
				agentName:  agentOpenClaw,
				configPath: openClawConfigPath,
				inspect:    inspectJSONMCPServer,
				patch:      patchJSONMCPServer,
				read:       readJSONMCPServer,
				parentKey:  "mcp",
				rootKey:    "servers",
			}),
			IntegrationNote: "skills read natively from ~/.agents/skills; MCP goes to openclaw.json mcp.servers (run `openclaw mcp reload` for a running Gateway)",
		},

		agentDSH: {
			Skills:           skillsSymlink,
			SkillsNativeRoot: dshReadsAgentsSkills,
			MCP: mcpTargetPtr(mcpTarget{
				agentName:  agentDSH,
				configPath: dshPatchPath,
				inspect:    inspectDSHMCPServer,
				patch:      patchDSHMCPServer,
				read:       readDSHMCPServer,
			}),
			RootInstructions: &rootInstructionsCapability{
				Path:     func(home string) string { return filepath.Join(dshHome(home), "AGENTS.md") },
				Expected: func(repoRoot string) string { return filepath.Join(repoRoot, "AGENTS.md") },
			},
			IntegrationNote: "skills read natively from ~/.agents/skills; MCP goes to tackroom-mcp-* rows in ~/.dsh/cordis.patch.yml (every profile)",
		},
	}
}

func roleRenderer(name string) agentrole.Renderer {
	renderer, ok := agentrole.Lookup(name)
	if !ok {
		panic("missing role renderer for " + name)
	}
	return renderer
}

func getHarnesses() map[string]*harness {
	harnessesOnce.Do(initHarnesses)
	return harnesses
}

func mcpTargetPtr(t mcpTarget) *mcpTarget {
	return &t
}

// harnessFor returns the harness for the given agent name.
// Returns nil if no harness is registered (unknown agent).
func harnessFor(name string) *harness {
	return getHarnesses()[normalizeAgentName(name)]
}

// mcpTargetForHarness returns the MCP target from the harness registry.
// This replaces the old mcpTargets map lookup.
func mcpTargetForHarness(agentName string) (mcpTarget, error) {
	h := harnessFor(agentName)
	if h == nil || h.MCP == nil {
		return mcpTarget{}, fmt.Errorf("agent %q has no MCP support", agentName)
	}
	return *h.MCP, nil
}

// hookTargetForHarness returns the hook target from the harness registry.
func hookTargetForHarness(agentName string) (hookTarget, bool) {
	h := harnessFor(agentName)
	if h == nil || h.Hooks == nil {
		return hookTarget{}, false
	}
	return *h.Hooks, true
}

// hasMCPSupport returns true if the agent has MCP support registered.
func hasMCPSupport(agentName string) bool {
	h := harnessFor(agentName)
	return h != nil && h.MCP != nil
}

// allHarnessNames returns all registered agent names.
func allHarnessNames() []string {
	names := make([]string, 0, len(getHarnesses()))
	for name := range getHarnesses() {
		names = append(names, name)
	}
	return names
}

// sortedHarnessNames returns all registered agent names in alphabetical order.
func sortedHarnessNames() []string {
	names := allHarnessNames()
	sort.Strings(names)
	return names
}

func detectVanillaPi(executable string) bool {
	// executable is the absolute path returned by exec.LookPath in isDetected.
	version, _ := exec.Command(executable, "--version").CombinedOutput() // nosemgrep: go.lang.security.audit.dangerous-exec-command
	return !bytes.HasPrefix(bytes.TrimSpace(version), []byte("omp/"))
}
