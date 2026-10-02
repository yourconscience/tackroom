package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

func piSettingsPath(home string) string {
	return filepath.Join(home, ".pi", "agent", "settings.json")
}

func augmentPiPackageReport(report *agentReport, agent agentConfig, home string) error {
	if agent.Name != agentPi || agent.Packages == nil {
		return nil
	}
	packages := *agent.Packages

	actual, exact, err := readPiPackages(home)
	if err != nil {
		return err
	}
	if exact && slices.Equal(actual, packages) {
		report.ManagedPackage = append(report.ManagedPackage, packages...)
		return nil
	}

	report.DriftedPackage = append(report.DriftedPackage, packages...)
	if len(packages) == 0 {
		report.DriftedPackage = append(report.DriftedPackage, "settings.json packages")
	}
	if !exact {
		report.RemovesPackage = append(report.RemovesPackage, "filtered package entries")
	} else {
		for _, pkg := range actual {
			if !slices.Contains(packages, pkg) {
				report.RemovesPackage = append(report.RemovesPackage, pkg)
			}
		}
	}
	report.UpdatesPackage = append(report.UpdatesPackage, "settings.json packages")
	return nil
}

// readPiPackages returns exact=false when settings contain filtered object-form
// package entries. Tackroom' string-list declaration intentionally replaces
// those entries so the canonical machine setup remains reproducible.
func readPiPackages(home string) ([]string, bool, error) {
	path := piSettingsPath(home)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}

	var raw map[string]interface{}
	if err := parseJSONConfig(path, data, &raw); err != nil {
		return nil, false, fmt.Errorf("parse %s: %w", path, err)
	}
	if raw == nil {
		return nil, false, fmt.Errorf("parse %s: settings must be a JSON object", path)
	}
	value, ok := raw["packages"]
	if !ok {
		return nil, true, nil
	}
	entries, ok := value.([]interface{})
	if !ok {
		return nil, false, nil
	}
	packages := make([]string, 0, len(entries))
	for _, entry := range entries {
		pkg, ok := entry.(string)
		if !ok {
			return nil, false, nil
		}
		packages = append(packages, pkg)
	}
	return packages, true, nil
}

func syncPiPackages(home string, packages []string) error {
	path := piSettingsPath(home)
	raw := map[string]interface{}{}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err == nil {
		if err := parseJSONConfig(path, data, &raw); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if raw == nil {
			return fmt.Errorf("parse %s: settings must be a JSON object", path)
		}
	}

	managed := make([]string, len(packages))
	copy(managed, packages)
	raw["packages"] = managed
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func applyAgentPackageSync(reports []agentReport, selected []agentConfig, home string) error {
	byName := make(map[string]agentConfig, len(selected))
	for _, agent := range selected {
		byName[agent.Name] = agent
	}
	for _, report := range reports {
		agent, ok := byName[report.Name]
		if !ok || !report.Detected || agent.Name != agentPi || agent.Packages == nil || len(report.UpdatesPackage) == 0 {
			continue
		}
		if err := syncPiPackages(home, *agent.Packages); err != nil {
			return err
		}
	}
	return nil
}
