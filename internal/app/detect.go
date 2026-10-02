package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// detectedSource records where an item was found.
type detectedSource struct {
	Harness string `json:"harness"`
	Path    string `json:"path"`
	Hash    string `json:"hash"`
}

// detectedItem is a unified representation of a skill, role, or MCP server
// found in one or more harness configs that is NOT already managed by
// tackroom. Managed items (symlinks, owned roles, configured MCP) are
// pre-filtered during detection. Hooks are excluded: they flow FROM
// tackroom.yaml INTO harnesses, not the other direction.
type detectedItem struct {
	Surface   string           `json:"surface"`
	Name      string           `json:"name"`
	Sources   []detectedSource `json:"sources"`
	Identical bool             `json:"identical"`
}

// detectionResult is the output of runDetection, suitable for JSON serialization.
// Resolved holds items identical across all sources; they are auto-promoted to
// shared without review. AutoResolved is len(Resolved), kept for JSON consumers.
type detectionResult struct {
	Harnesses    []string       `json:"harnesses"`
	Items        []detectedItem `json:"items"`
	Resolved     []detectedItem `json:"resolved,omitempty"`
	AutoResolved int            `json:"auto_resolved"`
}

// runDetection scans all detected harnesses for non-managed skills, roles, and
// MCP servers. Items already in the canonical config root or identical across
// all sources are counted as auto-resolved and excluded from Items.
func runDetection(cfg config, detected []agentConfig, repoRoot string, home string) (*detectionResult, error) {
	canonicalSkills := filepath.Join(repoRoot, "skills")
	canonicalAgents := filepath.Join(repoRoot, "agents")

	result := &detectionResult{}
	for _, agent := range detected {
		result.Harnesses = append(result.Harnesses, agent.Name)
	}

	itemIndex := make(map[string]*detectedItem)
	key := func(surface, name string) string { return surface + ":" + name }

	for _, agent := range detected {
		skillRoot := expandPath(agent.SkillRoot, home)
		agentRoot := expandPath(agent.AgentRoot, home)

		skills, err := detectNativeSkills(agent, skillRoot, canonicalSkills)
		if err != nil {
			return nil, fmt.Errorf("detect skills for %s: %w", agent.Name, err)
		}
		for _, s := range skills {
			k := key("skill", s.Name)
			item, ok := itemIndex[k]
			if !ok {
				item = &detectedItem{Surface: "skill", Name: s.Name}
				itemIndex[k] = item
			}
			item.Sources = append(item.Sources, detectedSource{
				Harness: agent.Name,
				Path:    s.Path,
				Hash:    s.Hash,
			})
		}

		roles, err := detectNativeRoles(agent, agentRoot, canonicalAgents)
		if err != nil {
			return nil, fmt.Errorf("detect roles for %s: %w", agent.Name, err)
		}
		for _, r := range roles {
			k := key("role", r.Name)
			item, ok := itemIndex[k]
			if !ok {
				item = &detectedItem{Surface: "role", Name: r.Name}
				itemIndex[k] = item
			}
			item.Sources = append(item.Sources, detectedSource{
				Harness: agent.Name,
				Path:    r.Path,
				Hash:    r.Hash,
			})
		}

		mcps, err := detectNativeMCPServers(agent, cfg, home)
		if err != nil {
			return nil, fmt.Errorf("detect MCP for %s: %w", agent.Name, err)
		}
		for _, m := range mcps {
			k := key("mcp", m.Name)
			item, ok := itemIndex[k]
			if !ok {
				item = &detectedItem{Surface: "mcp", Name: m.Name}
				itemIndex[k] = item
			}
			item.Sources = append(item.Sources, detectedSource{
				Harness: agent.Name,
				Path:    m.Path,
				Hash:    m.Hash,
			})
		}
	}

	for _, item := range itemIndex {
		markIdentical(item)
	}

	keys := make([]string, 0, len(itemIndex))
	for k := range itemIndex {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		item := itemIndex[k]
		if item.Identical {
			result.Resolved = append(result.Resolved, *item)
			result.AutoResolved++
			continue
		}
		result.Items = append(result.Items, *item)
	}

	return result, nil
}

type detectedEntry struct {
	Name string
	Path string
	Hash string
}

func detectNativeSkills(agent agentConfig, skillRoot string, canonicalSkills string) ([]detectedEntry, error) {
	if skillRoot == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(skillRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", skillRoot, err)
	}
	var out []detectedEntry
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(skillRoot, entry.Name())
		// Skip symlinks (managed by tackroom) and entries resolving to canonical.
		if isSymlinkOrResolvesTo(path, filepath.Join(canonicalSkills, entry.Name())) {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		if !hasFile(filepath.Join(path, "SKILL.md")) {
			continue
		}
		hash, err := hashDir(path)
		if err != nil {
			return nil, err
		}
		out = append(out, detectedEntry{Name: entry.Name(), Path: path, Hash: hash})
	}
	return out, nil
}

func detectNativeRoles(agent agentConfig, agentRoot string, canonicalAgents string) ([]detectedEntry, error) {
	h := harnessFor(agent.Name)
	if h == nil || h.roles == nil || agentRoot == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(agentRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", agentRoot, err)
	}
	var out []detectedEntry
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || filepath.Ext(entry.Name()) != h.roles.Extension() {
			continue
		}
		path := filepath.Join(agentRoot, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if isManagedAgentFile(path, data, filepath.Dir(filepath.Dir(canonicalAgents))) {
			continue
		}
		name := normalizeAgentName(strings.TrimSuffix(entry.Name(), h.roles.Extension()))
		hash := hashBytes(data)
		out = append(out, detectedEntry{Name: name, Path: path, Hash: hash})
	}
	return out, nil
}

func detectNativeMCPServers(agent agentConfig, cfg config, home string) ([]detectedEntry, error) {
	if !hasMCPSupport(agent.Name) {
		return nil, nil
	}
	names, err := nativeMCPServerNames(agent.Name, home)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]struct{}, len(cfg.MCPServers))
	for _, server := range cfg.MCPServers {
		existing[server.Name] = struct{}{}
	}
	var out []detectedEntry
	for _, name := range names {
		if _, ok := existing[name]; ok {
			continue
		}
		server, err := readNativeMCPServer(agent.Name, name, home)
		if err != nil {
			continue
		}
		hash := hashMCPServer(server)
		out = append(out, detectedEntry{Name: name, Path: name, Hash: hash})
	}
	return out, nil
}

func markIdentical(item *detectedItem) {
	if len(item.Sources) < 2 {
		item.Identical = false
		return
	}
	first := item.Sources[0].Hash
	for _, src := range item.Sources[1:] {
		if src.Hash != first {
			item.Identical = false
			return
		}
	}
	item.Identical = true
}

func isSymlinkOrResolvesTo(path string, target string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	return sameResolvedPath(path, target)
}

// hashDir produces a deterministic hash of a directory tree: sorted walk,
// each entry contributes its relative path and file content.
func hashDir(dir string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		h.Write([]byte(rel + "\x00"))
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		h.Write(data)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func hashMCPServer(server mcpServerConfig) string {
	parts := []string{server.Command}
	parts = append(parts, server.Args...)
	return hashBytes([]byte(strings.Join(parts, "\x00")))
}
