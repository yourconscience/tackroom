package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	readmeSkillsBeginMarker = "<!-- BEGIN GENERATED SKILLS -->"
	readmeSkillsEndMarker   = "<!-- END GENERATED SKILLS -->"
)

func expectedREADMESkillsBlock(repoRoot string) (string, int, error) {
	entries, err := os.ReadDir(filepath.Join(repoRoot, "skills"))
	if errors.Is(err, fs.ErrNotExist) {
		entries = nil
	} else if err != nil {
		return "", 0, fmt.Errorf("read skills/: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if !hasFile(filepath.Join(repoRoot, "skills", entry.Name(), "SKILL.md")) {
			continue
		}
		// grill-me is a packaged command alias for grilling, not a standalone
		// model-invocable skill in the public inventory.
		if entry.Name() == "grill-me" {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString(readmeSkillsBeginMarker)
	b.WriteString("\n")
	fmt.Fprintf(&b, "%d skills ship with this repo:\n\n", len(names))
	b.WriteString("`")
	b.WriteString(strings.Join(names, "` `"))
	b.WriteString("`\n")
	b.WriteString(readmeSkillsEndMarker)
	return b.String(), len(names), nil
}

func locateREADMESkillsBlock(content string) (int, int, error) {
	if strings.Count(content, readmeSkillsBeginMarker) != 1 || strings.Count(content, readmeSkillsEndMarker) != 1 {
		return 0, 0, errors.New("README skills markers must each appear exactly once")
	}
	start := strings.Index(content, readmeSkillsBeginMarker)
	end := strings.Index(content, readmeSkillsEndMarker)
	if end < start {
		return 0, 0, errors.New("README skills end marker precedes begin marker")
	}
	return start, end + len(readmeSkillsEndMarker), nil
}

func renderREADMESkills(repoRoot string) error {
	path := filepath.Join(repoRoot, "README.md")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read README.md: %w", err)
	}
	expected, count, err := expectedREADMESkillsBlock(repoRoot)
	if err != nil {
		return err
	}
	content := string(data)
	if !strings.Contains(content, readmeSkillsBeginMarker) && !strings.Contains(content, readmeSkillsEndMarker) {
		return nil
	}
	start, end, err := locateREADMESkillsBlock(content)
	if err != nil {
		return err
	}
	updated := content[:start] + expected + content[end:]
	if updated == content {
		fmt.Printf("rendered README skills: %d unchanged\n", count)
		return nil
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write README.md: %w", err)
	}
	fmt.Printf("rendered README skills: %d written\n", count)
	return nil
}

func renderCommittedArtifacts(repoRoot string) error {
	return renderREADMESkills(repoRoot)
}
