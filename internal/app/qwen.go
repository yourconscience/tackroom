package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func qwenSettingsPath(home string) string {
	return filepath.Join(home, ".qwen", "settings.json")
}

func patchQwenConfig(home string, repoRoot string, _ config) (bool, error) {
	configPath := qwenSettingsPath(home)
	raw := map[string]interface{}{}
	data, err := os.ReadFile(configPath)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Errorf("read %s: %w", configPath, err)
		}
	} else if err := parseJSONConfig(configPath, data, &raw); err != nil {
		return false, fmt.Errorf("parse %s: %w", configPath, err)
	}

	skillsValue, skillsExists := raw["skills"]
	skills, skillsValid := skillsValue.(map[string]interface{})
	if skillsExists && skillsValue != nil && !skillsValid {
		return false, fmt.Errorf("skills key in %s is not an object", configPath)
	}
	if !skillsExists || skillsValue == nil {
		skills = map[string]interface{}{}
		raw["skills"] = skills
	}

	target := filepath.Join(repoRoot, "skills")
	directoriesValue, directoriesExists := skills["directories"]
	directories, directoriesValid := directoriesValue.([]interface{})
	if directoriesExists && directoriesValue != nil && !directoriesValid {
		return false, fmt.Errorf("skills.directories in %s is not an array", configPath)
	}
	for _, value := range directories {
		directory, ok := value.(string)
		if ok && filepath.Clean(expandPath(directory, home)) == filepath.Clean(target) {
			return false, nil
		}
	}
	skills["directories"] = append(directories, configSkillsDirValue(home, target))

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal %s: %w", configPath, err)
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return false, fmt.Errorf("create %s: %w", filepath.Dir(configPath), err)
	}
	if err := os.WriteFile(configPath, out, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", configPath, err)
	}
	return true, nil
}

func qwenHasSkillsDirectory(home string, target string) (bool, error) {
	configPath := qwenSettingsPath(home)
	data, err := os.ReadFile(configPath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", configPath, err)
	}
	var raw map[string]interface{}
	if err := parseJSONConfig(configPath, data, &raw); err != nil {
		return false, fmt.Errorf("parse %s: %w", configPath, err)
	}
	skills, _ := raw["skills"].(map[string]interface{})
	directories, _ := skills["directories"].([]interface{})
	for _, value := range directories {
		directory, ok := value.(string)
		if ok && filepath.Clean(expandPath(directory, home)) == filepath.Clean(target) {
			return true, nil
		}
	}
	return false, nil
}

func inspectQwenAgent(agent agentConfig, expected map[string]string, agentsSkillRoot string, cfg config, home string) (agentReport, error) {
	report := agentReport{
		Name:           agent.Name,
		SkillRoot:      agent.SkillRoot,
		AgentRoot:      agent.AgentRoot,
		ExpectedSkills: expected,
		Detected:       isDetected(agent),
	}
	if !report.Detected {
		return report, nil
	}

	entries, err := os.ReadDir(agent.SkillRoot)
	if err == nil {
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), ".") {
				report.External = append(report.External, entry.Name())
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return agentReport{}, fmt.Errorf("read %s: %w", agent.SkillRoot, err)
	}

	configured, err := qwenHasSkillsDirectory(home, agentsSkillRoot)
	if err != nil {
		return agentReport{}, err
	}
	if !configured {
		report.Missing = append(report.Missing, "config skills.directories")
		report.Adds = append(report.Adds, "config skills.directories")
	}
	report.Managed = append(report.Managed, sortedKeys(expected)...)
	if err := augmentMCPReport(&report, agent, cfg, home); err != nil {
		return agentReport{}, err
	}
	if err := augmentHookReport(&report, agent, cfg, home); err != nil {
		return agentReport{}, err
	}
	if err := inspectAgentRoles(&report, filepath.Dir(agentsSkillRoot), agent); err != nil {
		return agentReport{}, err
	}
	if h := harnessFor(agent.Name); h != nil && h.RootInstructions != nil {
		if err := inspectRootInstructions(&report, h.RootInstructions, filepath.Dir(agentsSkillRoot), home, cfg.Instructions); err != nil {
			return agentReport{}, err
		}
	}

	sortReportLists(&report)
	report.Synced = isReportSynced(report)
	return report, nil
}
