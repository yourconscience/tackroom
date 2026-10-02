package app

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type nativeHookEntry struct {
	Agent         string
	Event         string
	Command       string
	ConfigPath    string
	Managed       bool
	MissingTarget string
}

type hookCommandOptions struct {
	runOptions
	Query string
}

func runHookCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("hook requires subcommand: list or remove")
	}
	switch args[0] {
	case "list":
		opts, err := parseHookCommandFlags("hook list", args[1:], false)
		if err != nil {
			return err
		}
		return runHookList(opts)
	case "remove":
		opts, err := parseHookCommandFlags("hook remove", args[1:], true)
		if err != nil {
			return err
		}
		return runHookRemove(opts)
	default:
		return fmt.Errorf("unknown hook subcommand %q", args[0])
	}
}

func parseHookCommandFlags(name string, args []string, requireQuery bool) (hookCommandOptions, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var opts hookCommandOptions
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to tackroom YAML config")
	fs.StringVar(&opts.Agents, "agents", "", "Comma-separated agent names to inspect")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "Preview removals without changing native config")
	if err := fs.Parse(args); err != nil {
		return hookCommandOptions{}, err
	}
	if fs.NArg() > 1 {
		return hookCommandOptions{}, fmt.Errorf("%s accepts at most one query", name)
	}
	if fs.NArg() == 1 {
		opts.Query = strings.TrimSpace(fs.Arg(0))
	}
	if requireQuery && opts.Query == "" {
		return hookCommandOptions{}, fmt.Errorf("usage: tackroom hook remove [--dry-run] [--agents ...] <query>")
	}
	return opts, nil
}

func runHookList(opts hookCommandOptions) error {
	_, home, cfg, selected, err := loadContext(opts.runOptions)
	if err != nil {
		return err
	}
	entries, unsupported, err := collectNativeHooks(home, cfg, selected)
	if err != nil {
		return err
	}
	entries = filterNativeHooks(entries, opts.Query)
	printNativeHooks(entries, unsupported)
	return nil
}

func runHookRemove(opts hookCommandOptions) error {
	_, home, cfg, selected, err := loadContext(opts.runOptions)
	if err != nil {
		return err
	}
	entries, unsupported, err := collectNativeHooks(home, cfg, selected)
	if err != nil {
		return err
	}
	matches := filterNativeHooks(entries, opts.Query)
	printNativeHooks(matches, unsupported)
	if len(matches) == 0 {
		fmt.Printf("no native hooks match %q\n", opts.Query)
		return nil
	}
	if opts.DryRun {
		fmt.Printf("dry-run: would remove %d hook registration(s)\n", len(matches))
		return nil
	}
	changed, err := removeNativeHookEntries(matches)
	if err != nil {
		return err
	}
	fmt.Printf("removed %d hook registration(s) from %d native config file(s)\n", len(matches), changed)
	return nil
}

func collectNativeHooks(home string, cfg config, selected []agentConfig) ([]nativeHookEntry, []string, error) {
	var entries []nativeHookEntry
	var unsupported []string
	seenPaths := make(map[string]bool)
	for _, agent := range selected {
		name := normalizeAgentName(agent.Name)
		paths, simple, supported := nativeHookConfigPaths(name, home)
		if !supported {
			unsupported = append(unsupported, name)
			continue
		}
		for _, path := range paths {
			key := name + "\x00" + path
			if seenPaths[key] {
				continue
			}
			seenPaths[key] = true
			var found []nativeHookEntry
			var err error
			if simple {
				found, err = readSimpleNativeHooks(name, path)
			} else {
				found, err = readGroupedNativeHooks(name, path)
			}
			if err != nil {
				return nil, nil, err
			}
			for i := range found {
				found[i].Managed = nativeHookIsManaged(found[i], cfg)
				found[i].MissingTarget = missingHookTarget(found[i].Command, home)
			}
			entries = append(entries, found...)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Agent != entries[j].Agent {
			return entries[i].Agent < entries[j].Agent
		}
		if entries[i].Event != entries[j].Event {
			return entries[i].Event < entries[j].Event
		}
		return entries[i].Command < entries[j].Command
	})
	sort.Strings(unsupported)
	return entries, unsupported, nil
}

func nativeHookConfigPaths(agent string, home string) ([]string, bool, bool) {
	switch agent {
	case agentClaudeCode:
		return []string{claudeHooksConfigPath(home)}, false, true
	case agentCodex:
		return []string{codexHooksConfigPath(home)}, false, true
	case agentDroid:
		return []string{droidHooksConfigPath(home), droidLegacyHooksConfigPath(home)}, false, true
	case agentHermes:
		return []string{filepath.Join(home, ".hermes", "config.yaml")}, true, true
	case agentQwenCode:
		return []string{qwenSettingsPath(home)}, false, true
	default:
		return nil, false, false
	}
}

func readGroupedNativeHooks(agent string, path string) ([]nativeHookEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var raw map[string]interface{}
	if err := parseJSONConfig(path, data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	root, _ := raw["hooks"].(map[string]interface{})
	var entries []nativeHookEntry
	for event, groupsRaw := range root {
		groups, _ := groupsRaw.([]interface{})
		for _, groupRaw := range groups {
			group, _ := groupRaw.(map[string]interface{})
			items, _ := group["hooks"].([]interface{})
			for _, itemRaw := range items {
				item, _ := itemRaw.(map[string]interface{})
				command, _ := item["command"].(string)
				if strings.TrimSpace(command) != "" {
					entries = append(entries, nativeHookEntry{Agent: agent, Event: event, Command: command, ConfigPath: path})
				}
			}
		}
	}
	return entries, nil
}

func readSimpleNativeHooks(agent string, path string) ([]nativeHookEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	root, _ := raw["hooks"].(map[string]interface{})
	var entries []nativeHookEntry
	for event, itemsRaw := range root {
		items, _ := itemsRaw.([]interface{})
		for _, itemRaw := range items {
			item, _ := itemRaw.(map[string]interface{})
			command, _ := item["command"].(string)
			if strings.TrimSpace(command) != "" {
				entries = append(entries, nativeHookEntry{Agent: agent, Event: event, Command: command, ConfigPath: path})
			}
		}
	}
	return entries, nil
}

func nativeHookIsManaged(entry nativeHookEntry, cfg config) bool {
	for _, hook := range cfg.Hooks {
		if !hook.Enabled || (len(hook.Agents) > 0 && !stringInSlice(entry.Agent, hook.Agents)) {
			continue
		}
		event := hook.Event
		if entry.Agent == agentHermes {
			var ok bool
			event, ok = hermesHookEvent(event)
			if !ok {
				continue
			}
		}
		if event == entry.Event && hookCommandMatches(entry.Command, hook.Command) {
			return true
		}
	}
	return false
}

func filterNativeHooks(entries []nativeHookEntry, query string) []nativeHookEntry {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return entries
	}
	var filtered []nativeHookEntry
	for _, entry := range entries {
		haystack := strings.ToLower(entry.Agent + "\n" + entry.Event + "\n" + entry.Command + "\n" + entry.ConfigPath)
		if strings.Contains(haystack, query) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func printNativeHooks(entries []nativeHookEntry, unsupported []string) {
	for _, entry := range entries {
		ownership := "unmanaged"
		if entry.Managed {
			ownership = "managed"
		}
		stale := ""
		if entry.MissingTarget != "" {
			stale = " stale=" + entry.MissingTarget
		}
		fmt.Printf("%s\t%s\t%s%s\t%s\n", entry.Agent, entry.Event, ownership, stale, compactHookCommand(entry.Command))
	}
	if len(unsupported) > 0 {
		fmt.Printf("unsupported hook surfaces: %s\n", strings.Join(unsupported, ", "))
	}
	fmt.Printf("%d native hook registration(s)\n", len(entries))
}

func compactHookCommand(command string) string {
	command = strings.Join(strings.Fields(command), " ")
	const limit = 180
	if len(command) <= limit {
		return command
	}
	return command[:limit-3] + "..."
}

func removeNativeHookEntries(entries []nativeHookEntry) (int, error) {
	byPath := make(map[string][]nativeHookEntry)
	agentByPath := make(map[string]string)
	for _, entry := range entries {
		byPath[entry.ConfigPath] = append(byPath[entry.ConfigPath], entry)
		agentByPath[entry.ConfigPath] = entry.Agent
	}
	changed := 0
	for path, pathEntries := range byPath {
		var didChange bool
		var err error
		if agentByPath[path] == agentHermes {
			didChange, err = removeSimpleNativeHookEntries(path, pathEntries)
		} else {
			didChange, err = removeGroupedNativeHookEntries(path, pathEntries)
		}
		if err != nil {
			return changed, err
		}
		if didChange {
			changed++
		}
	}
	return changed, nil
}

func removeGroupedNativeHookEntries(path string, entries []nativeHookEntry) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	var raw map[string]interface{}
	if err := parseJSONConfig(path, data, &raw); err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	if !removeSelectedGroupedHooks(raw, entries) {
		return false, nil
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

func removeSelectedGroupedHooks(raw map[string]interface{}, entries []nativeHookEntry) bool {
	root, ok := raw["hooks"].(map[string]interface{})
	if !ok {
		return false
	}
	byEvent := nativeHookCommandsByEvent(entries)
	changed := false
	for event, commands := range byEvent {
		groups, ok := root[event].([]interface{})
		if !ok {
			continue
		}
		keptGroups := groups[:0]
		for _, groupRaw := range groups {
			group, ok := groupRaw.(map[string]interface{})
			if !ok {
				keptGroups = append(keptGroups, groupRaw)
				continue
			}
			items, ok := group["hooks"].([]interface{})
			if !ok {
				keptGroups = append(keptGroups, groupRaw)
				continue
			}
			filtered, removed := removeHookCommands(items, commands)
			if removed {
				changed = true
				group["hooks"] = filtered
			}
			if len(filtered) > 0 || !removed {
				keptGroups = append(keptGroups, groupRaw)
			}
		}
		if len(keptGroups) == 0 {
			delete(root, event)
		} else {
			root[event] = keptGroups
		}
	}
	if len(root) == 0 {
		delete(raw, "hooks")
	}
	return changed
}

func removeSimpleNativeHookEntries(path string, entries []nativeHookEntry) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	root, ok := raw["hooks"].(map[string]interface{})
	if !ok {
		return false, nil
	}
	changed := false
	for event, commands := range nativeHookCommandsByEvent(entries) {
		items, ok := root[event].([]interface{})
		if !ok {
			continue
		}
		filtered, removed := removeHookCommands(items, commands)
		if !removed {
			continue
		}
		changed = true
		if len(filtered) == 0 {
			delete(root, event)
		} else {
			root[event] = filtered
		}
	}
	if !changed {
		return false, nil
	}
	if len(root) == 0 {
		delete(raw, "hooks")
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		return false, fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

func nativeHookCommandsByEvent(entries []nativeHookEntry) map[string][]string {
	byEvent := make(map[string][]string)
	for _, entry := range entries {
		byEvent[entry.Event] = append(byEvent[entry.Event], entry.Command)
	}
	return byEvent
}

var hookScriptPathPattern = regexp.MustCompile(`(?:~|\$\{HOME-\}|\$HOME|/)[^'"[:space:];]+\.(?:sh|py|cmd|ts)`)

func missingHookTarget(command string, home string) string {
	paths := hookScriptPathPattern.FindAllString(command, -1)
	if len(paths) == 0 {
		return ""
	}
	seen := make(map[string]bool)
	for _, path := range paths {
		path = strings.ReplaceAll(path, "${HOME-}", home)
		path = strings.ReplaceAll(path, "$HOME", home)
		path = expandPath(path, home)
		if seen[path] {
			continue
		}
		seen[path] = true
		if _, err := os.Stat(path); err != nil && os.IsNotExist(err) {
			return path
		}
	}
	return ""
}
