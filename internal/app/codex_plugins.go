package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Codex installs a plugin by copying it into its own cache, so tackroom drives
// Codex's CLI instead of writing that cache. A spec-format source with
// `mcp: true` that targets Codex is listed in a tackroom-owned marketplace,
// <config-root>/external/.agents/plugins/marketplace.json, registered with
// `codex plugin marketplace add` and installed with `codex plugin add`. Codex
// then loads that source's skills and MCP servers from the plugin alone, so
// tackroom skips the same source's skill links and [mcp_servers] rows for it.
// Tested with codex-cli 0.159.2.

const codexMarketplaceName = "tackroom"

// codexPluginSource is one external source projected into Codex as a plugin.
type codexPluginSource struct {
	Name   string // manifest name: the plugin's identity in Codex
	Source string // external source name: its directory under the marketplace root
}

func codexMarketplaceFilePath(home string) string {
	return filepath.Join(externalCacheDir(home), ".agents", "plugins", "marketplace.json")
}

func codexInstalledPluginDir(home string, name string) string {
	return filepath.Join(home, ".codex", "plugins", "cache", codexMarketplaceName, name)
}

// codexPluginSources lists the sources Codex gets as native plugins, and the
// manifest names two sources both declare (neither is projected). A source
// qualifies when Codex is detected, the cache holds a valid spec
// plugin.json, mcp is on, Codex is an MCP target and not excluded, and its
// skills are the plugin's own skills/ directory: a plugin carries every skill in
// skills/, so a source that narrows or relocates its skills keeps its links.
func codexPluginSources(cfg config, home string) ([]codexPluginSource, []string) {
	if _, ok := codexAgent(cfg); !ok {
		return nil, nil
	}
	bySource := map[string][]string{}
	for _, src := range cfg.ExternalSkills {
		if !src.MCP || stringInSlice(agentCodex, src.ExcludeAgents) {
			continue
		}
		if len(src.MCPAgents) > 0 && !stringInSlice(agentCodex, src.MCPAgents) {
			continue
		}
		if (src.SkillDir != "" && src.SkillDir != "skills") || len(src.SkillDirs) > 0 || len(src.Skills) > 0 {
			continue
		}
		cachePath := filepath.Join(externalCacheDir(home), sourceName(src))
		if !hasDir(filepath.Join(cachePath, ".git")) {
			continue
		}
		manifest, _, ok, err := parsePluginManifest(cachePath)
		if err != nil || !ok {
			continue
		}
		bySource[manifest.Name] = append(bySource[manifest.Name], sourceName(src))
	}
	var plugins []codexPluginSource
	var duplicates []string
	for name, sources := range bySource {
		if len(sources) > 1 {
			sort.Strings(sources)
			duplicates = append(duplicates, fmt.Sprintf("%s (%s)", name, strings.Join(sources, ", ")))
			continue
		}
		plugins = append(plugins, codexPluginSource{Name: name, Source: sources[0]})
	}
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Name < plugins[j].Name })
	sort.Strings(duplicates)
	return plugins, duplicates
}

// codexAgent returns the configured Codex agent when it is detected. A disabled
// agent still counts: `--agents codex` syncs it, and its plugins must not look
// stale then.
func codexAgent(cfg config) (agentConfig, bool) {
	for _, agent := range cfg.Agents {
		if normalizeAgentName(agent.Name) == agentCodex && isDetected(agent) {
			return agent, true
		}
	}
	return agentConfig{}, false
}

// codexPluginPlan is the one description of what sync changes for Codex
// plugins; status reports it and sync applies it.
type codexPluginPlan struct {
	// Marketplace is the generated marketplace.json, nil when no plugin is
	// wanted; MarketplaceDrift is whether the file on disk differs from it.
	Marketplace      []byte
	MarketplaceDrift bool
	Register         bool
	Unregister       bool
	Add              []string // plugin names to install or refresh
	Remove           []string // installed plugin ids no longer wanted
	Managed          []string
	Disabled         []string
	Conflicts        []string
}

// planCodexPlugins reads config.toml, the generated marketplace and Codex's
// plugin cache; it never runs codex.
func planCodexPlugins(cfg config, home string) (codexPluginPlan, error) {
	plugins, _ := codexPluginSources(cfg, home)
	plan := codexPluginPlan{}
	if len(plugins) > 0 {
		entries := make([]map[string]interface{}, 0, len(plugins))
		for _, p := range plugins {
			entries = append(entries, map[string]interface{}{
				"name":     p.Name,
				"source":   "./" + p.Source,
				"policy":   map[string]string{"installation": "AVAILABLE", "authentication": "ON_INSTALL"},
				"category": "Other",
			})
		}
		data, err := json.MarshalIndent(map[string]interface{}{"name": codexMarketplaceName, "plugins": entries}, "", "  ")
		if err != nil {
			return plan, err
		}
		plan.Marketplace = append(data, '\n')
	}
	onDisk, err := os.ReadFile(codexMarketplaceFilePath(home))
	if err != nil && !os.IsNotExist(err) {
		return plan, fmt.Errorf("read %s: %w", codexMarketplaceFilePath(home), err)
	}
	plan.MarketplaceDrift = !bytes.Equal(onDisk, plan.Marketplace)

	configPath := codexConfigPath(home)
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return plan, fmt.Errorf("read %s: %w", configPath, err)
	}
	// Nothing wanted and nothing of ours on disk: leave config.toml unparsed.
	if len(plugins) == 0 && !plan.MarketplaceDrift && !bytes.Contains(data, []byte(codexMarketplaceName)) {
		return plan, nil
	}
	var doc map[string]interface{}
	if _, err := toml.Decode(string(data), &doc); err != nil {
		return plan, fmt.Errorf("parse %s: %w", configPath, err)
	}
	marketplaces, _ := doc["marketplaces"].(map[string]interface{})
	installed, _ := doc["plugins"].(map[string]interface{})

	root := externalCacheDir(home)
	registered := false
	if entry, ok := marketplaces[codexMarketplaceName].(map[string]interface{}); ok {
		source, _ := entry["source"].(string)
		if source != root && !sameResolvedPath(source, root) {
			// Another tool's marketplace: leave it, and write nothing for plugins.
			if len(plugins) > 0 {
				plan.Conflicts = append(plan.Conflicts, fmt.Sprintf("marketplace %q is already registered in %s from %s", codexMarketplaceName, configPath, source))
			}
			plan.Marketplace, plan.MarketplaceDrift = nil, false
			return plan, nil
		}
		registered = true
	}
	plan.Register = len(plugins) > 0 && !registered
	plan.Unregister = len(plugins) == 0 && registered

	wanted := map[string]bool{}
	for _, p := range plugins {
		id := p.Name + "@" + codexMarketplaceName
		wanted[id] = true
		entry, inConfig := installed[id].(map[string]interface{})
		if enabled, ok := entry["enabled"].(bool); inConfig && ok && !enabled {
			plan.Disabled = append(plan.Disabled, id+" (disabled in config.toml)")
			continue
		}
		current, err := codexPluginCurrent(home, p)
		if err != nil {
			return plan, err
		}
		if inConfig && current {
			plan.Managed = append(plan.Managed, id)
		} else {
			plan.Add = append(plan.Add, p.Name)
		}
	}
	for id := range installed {
		if strings.HasSuffix(id, "@"+codexMarketplaceName) && !wanted[id] {
			plan.Remove = append(plan.Remove, id)
		}
	}
	sort.Strings(plan.Remove)
	sort.Strings(plan.Managed)
	sort.Strings(plan.Disabled)
	return plan, nil
}

// codexPluginCurrent reports whether Codex's installed copy of the plugin
// matches tackroom's cached source. Comparing trees needs no state file; .git is
// ignored because the cache's git metadata changes with every fetch.
func codexPluginCurrent(home string, p codexPluginSource) (bool, error) {
	versions, err := os.ReadDir(codexInstalledPluginDir(home, p.Name))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	cachePath := filepath.Join(externalCacheDir(home), p.Source)
	for _, version := range versions {
		if !version.IsDir() {
			continue
		}
		equal, err := externalTreesEqual(cachePath, filepath.Join(codexInstalledPluginDir(home, p.Name), version.Name()), true)
		if err != nil {
			return false, err
		}
		if equal {
			return true, nil
		}
	}
	return false, nil
}

func augmentCodexPluginReport(report *agentReport, agent agentConfig, cfg config, home string) error {
	if normalizeAgentName(agent.Name) != agentCodex {
		return nil
	}
	plan, err := planCodexPlugins(cfg, home)
	if err != nil {
		return err
	}
	for _, conflict := range plan.Conflicts {
		report.Conflicts = append(report.Conflicts, "plugins: "+conflict)
	}
	if plan.Register || (plan.Marketplace != nil && plan.MarketplaceDrift) {
		report.MissingPlugin = append(report.MissingPlugin, "marketplace "+codexMarketplaceName)
	}
	for _, name := range plan.Add {
		report.MissingPlugin = append(report.MissingPlugin, name+"@"+codexMarketplaceName)
	}
	if plan.Unregister || (plan.Marketplace == nil && plan.MarketplaceDrift) {
		report.StalePlugin = append(report.StalePlugin, "marketplace "+codexMarketplaceName)
	}
	report.StalePlugin = append(report.StalePlugin, plan.Remove...)
	report.ManagedPlugin = append(report.ManagedPlugin, plan.Managed...)
	report.DisabledPlugin = append(report.DisabledPlugin, plan.Disabled...)
	report.AddsPlugin = append([]string{}, report.MissingPlugin...)
	report.RemovesPlugin = append([]string{}, report.StalePlugin...)
	return nil
}

// codexPluginRun runs `codex plugin <args>` with CODEX_HOME pinned to the
// config home tackroom manages; a var so tests can stand in for codex.
var codexPluginRun = func(codex string, home string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, codex, append([]string{"plugin"}, args...)...)
	cmd.Env = append(os.Environ(), "CODEX_HOME="+filepath.Join(home, ".codex"))
	return cmd.CombinedOutput()
}

// syncCodexPlugins applies the current plan. Removals are skipped when the
// caller declined them (setup's confirmation, the view's destructive list).
func syncCodexPlugins(cfg config, home string, allowRemovals bool) error {
	plan, err := planCodexPlugins(cfg, home)
	if err != nil {
		return err
	}
	if len(plan.Conflicts) > 0 {
		return fmt.Errorf("codex plugins: %s", strings.Join(plan.Conflicts, "; "))
	}
	writeFile := plan.Marketplace != nil && plan.MarketplaceDrift
	dropFile := allowRemovals && plan.Marketplace == nil && plan.MarketplaceDrift
	removePlugins := allowRemovals && (len(plan.Remove) > 0 || plan.Unregister)

	if writeFile {
		if err := atomicConfigWrite(codexMarketplaceFilePath(home), plan.Marketplace, 0o644); err != nil {
			return fmt.Errorf("codex plugins: write %s: %w", codexMarketplaceFilePath(home), err)
		}
	}
	if plan.Register || len(plan.Add) > 0 || removePlugins {
		agent, _ := codexAgent(cfg)
		detect := agent.Detect
		if detect == "" {
			detect = agentCodex
		}
		codex, err := exec.LookPath(detect)
		if err != nil {
			return fmt.Errorf("codex plugins: %s not found on PATH", detect)
		}
		backedUp := false
		run := func(args ...string) error {
			if !backedUp {
				// Codex refuses a CODEX_HOME that does not exist yet.
				if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
					return fmt.Errorf("codex plugins: %w", err)
				}
				if err := backupNativeConfig(codexConfigPath(home), newConflictBackupDir(home), home); err != nil {
					return fmt.Errorf("codex plugins: back up %s: %w", codexConfigPath(home), err)
				}
				backedUp = true
			}
			out, err := codexPluginRun(codex, home, args...)
			if err != nil {
				reason := strings.TrimSpace(string(out))
				if reason == "" {
					reason = err.Error()
				} else if lines := strings.Split(reason, "\n"); len(lines) > 1 {
					reason = strings.TrimSpace(lines[len(lines)-1])
				}
				return fmt.Errorf("codex plugins: codex plugin %s: %s", strings.Join(args, " "), reason)
			}
			return nil
		}
		if plan.Register {
			if err := run("marketplace", "add", externalCacheDir(home)); err != nil {
				return err
			}
		}
		for _, name := range plan.Add {
			if err := run("add", name+"@"+codexMarketplaceName); err != nil {
				return err
			}
		}
		if removePlugins {
			for _, id := range plan.Remove {
				if err := run("remove", id); err != nil {
					return err
				}
			}
			if plan.Unregister {
				if err := run("marketplace", "remove", codexMarketplaceName); err != nil {
					return err
				}
			}
		}
	}
	if dropFile {
		path := codexMarketplaceFilePath(home)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("codex plugins: %w", err)
		}
		// Best effort: the directories tackroom made for it, when empty.
		_ = os.Remove(filepath.Dir(path))
		_ = os.Remove(filepath.Dir(filepath.Dir(path)))
	}
	return nil
}

func applyCodexPluginSync(reports []agentReport, cfg config, home string) error {
	for _, report := range reports {
		if !report.Detected || normalizeAgentName(report.Name) != agentCodex {
			continue
		}
		if len(report.AddsPlugin)+len(report.RemovesPlugin) == 0 {
			continue
		}
		if err := syncCodexPlugins(cfg, home, len(report.RemovesPlugin) > 0); err != nil {
			return err
		}
	}
	return nil
}
