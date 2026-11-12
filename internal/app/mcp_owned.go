package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// mcpOwnership is the machine-local record of the MCP servers sync wrote into
// each agent's native config. A recorded server that is no longer wanted for
// that agent (removed from tackroom.yaml, disabled, or retargeted) is stale
// and sync removes it. Servers tackroom never wrote are never touched.
type mcpOwnership map[string][]string

func mcpOwnershipPath(home string) string {
	return filepath.Join(tackroomStateDir(home), "mcp-servers.json")
}

// readMCPOwnership returns the record; a missing file is an empty record. An
// unreadable one is an error: treating it as empty would forget servers that
// still need cleanup.
func readMCPOwnership(home string) (mcpOwnership, error) {
	path := mcpOwnershipPath(home)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return mcpOwnership{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	owned := mcpOwnership{}
	if err := json.Unmarshal(data, &owned); err != nil {
		return nil, fmt.Errorf("parse %s: %w (fix it, or delete it to stop tracking MCP servers tackroom wrote)", path, err)
	}
	return owned, nil
}

// writeMCPOwnership replaces the record atomically.
func writeMCPOwnership(home string, owned mcpOwnership) error {
	path := mcpOwnershipPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(owned, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mcp-servers-*.json")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// staleMCPServers lists servers sync wrote for this agent earlier that are no
// longer wanted there and are still present in its native config.
func staleMCPServers(agentName string, desired []mcpServerConfig, owned mcpOwnership, home string) ([]string, error) {
	wanted := make(map[string]bool, len(desired))
	for _, server := range desired {
		wanted[server.Name] = true
	}
	var stale []string
	for _, name := range owned[normalizeAgentName(agentName)] {
		if wanted[name] {
			continue
		}
		// A bare entry inspects as missing only when the name is absent.
		state, err := inspectMCPServer(agentName, mcpServerConfig{Name: name}, home)
		if err != nil {
			return nil, err
		}
		if state != stateMissing {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	return stale, nil
}

// claimMatchingMCPServer records name as tackroom-owned for every targeted
// agent whose native entry still matches the canonical definition. Agents
// whose config cannot be read are skipped.
func claimMatchingMCPServer(cfg config, name string, home string) error {
	owned, err := readMCPOwnership(home)
	if err != nil {
		return err
	}
	changed := false
	for _, agent := range cfg.Agents {
		if !agent.Enabled || !hasMCPSupport(agent.Name) {
			continue
		}
		key := normalizeAgentName(agent.Name)
		for _, server := range desiredMCPServersForAgent(cfg, agent.Name) {
			if server.Name != name || stringInSlice(name, owned[key]) {
				continue
			}
			if state, err := inspectMCPServer(agent.Name, server, home); err == nil && state == stateSynced {
				owned[key] = append(owned[key], name)
				sort.Strings(owned[key])
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	return writeMCPOwnership(home, owned)
}

func removeMCPServer(agentName string, name string, home string) error {
	target, err := mcpTargetForHarness(agentName)
	if err != nil {
		return err
	}
	return target.remove(target, name, home)
}

// backupNativeConfig copies a native config file into backupDir before
// tackroom removes entries from it. An identical copy already there is
// reused; a differing one gets a numbered sibling.
func backupNativeConfig(path string, backupDir string, home string) error {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	dest := backupPathFor(path, backupDir, home)
	for n := 1; ; n++ {
		if _, err := os.Lstat(dest); err != nil {
			break
		}
		if same, err := filesEqual(path, dest); err == nil && same {
			return nil // this exact content is already backed up
		}
		dest = fmt.Sprintf("%s.%d", backupPathFor(path, backupDir, home), n)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	return copyBackupFile(path, dest, info.Mode().Perm())
}

func removeJSONMCPServer(target mcpTarget, name string, home string) error {
	configPath := target.configPath(home)
	data, err := os.ReadFile(configPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}
	var raw map[string]interface{}
	if err := parseJSONConfig(configPath, data, &raw); err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}
	servers, ok := asMap(jsonMCPParent(raw, target)[target.rootKey])
	if !ok {
		return nil
	}
	if _, ok := servers[name]; !ok {
		return nil
	}
	delete(servers, name)
	// asMap may have converted the map; store it back so the delete sticks.
	jsonMCPParent(raw, target)[target.rootKey] = servers
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", configPath, err)
	}
	if err := os.WriteFile(configPath, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", configPath, err)
	}
	return nil
}

func removeCodexMCPServer(target mcpTarget, name string, home string) error {
	configPath := target.configPath(home)
	data, err := os.ReadFile(configPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}
	// Never edit a file that does not parse (apart from repairable duplicates).
	if _, _, err := decodeCodexMCPEntry(configPath, data, name); err != nil && !errors.Is(err, errCodexEntryRepairable) {
		return err
	}
	content := string(data)
	updated := removeTOMLSectionsIncludingDescendants(content, fmt.Sprintf("[mcp_servers.%s]", name))
	if updated == content {
		return nil
	}
	var check map[string]interface{}
	if _, err := toml.Decode(updated, &check); err != nil {
		return fmt.Errorf("remove %s from %s: result does not parse: %w", name, configPath, err)
	}
	if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", configPath, err)
	}
	return nil
}

func removeDSHMCPServer(_ mcpTarget, name string, home string) error {
	path := dshPatchPath(home)
	doc, err := loadDSHPatch(path)
	if err != nil {
		return err
	}
	list, index := findDSHRow(doc.Content[0], name)
	if list == nil {
		return nil
	}
	list.Content = append(list.Content[:index], list.Content[index+1:]...)
	return writeDSHPatch(path, doc)
}

// deleteMappingKey removes key from a YAML mapping node and reports whether
// it was there.
func deleteMappingKey(mapping *yaml.Node, key string) bool {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return true
		}
	}
	return false
}
