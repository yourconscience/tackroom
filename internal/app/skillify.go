package app

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var validSkillName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,48}[a-z0-9]$`)

func runSkillify(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("skillify requires a skill name: tackroom skillify <name>")
	}
	name := args[0]

	fs := flag.NewFlagSet("skillify", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var description string
	fs.StringVar(&description, "description", "", "Short description of the skill")

	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	if err := validateSkillName(name); err != nil {
		return err
	}

	repoRoot, _, _, _, err := loadContext(runOptions{})
	if err != nil {
		return fmt.Errorf("load context: %w", err)
	}

	skillDir := filepath.Join(repoRoot, "skills", name)
	if _, err := os.Stat(skillDir); err == nil {
		return fmt.Errorf("skill %q already exists at %s", name, skillDir)
	}

	if description == "" {
		description = "TODO: add description"
	}

	skillMD := fmt.Sprintf(`---
name: %s
description: %s
---

# %s

Use when TODO.

## Steps

1. TODO

## Common Mistakes

- TODO
`, name, description, name)

	skillMDPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", skillDir, err)
	}
	if err := os.WriteFile(skillMDPath, []byte(skillMD), 0o644); err != nil {
		return fmt.Errorf("write SKILL.md: %w", err)
	}

	fmt.Printf("created: skills/%s/SKILL.md\n", name)
	fmt.Println()
	fmt.Println("edit the skill, then run:")
	fmt.Println("  tackroom sync")

	return nil
}

func validateSkillName(name string) error {
	if len(name) < 2 || len(name) > 50 {
		return fmt.Errorf("skill name must be 2-50 characters, got %d", len(name))
	}
	if !validSkillName.MatchString(name) {
		return fmt.Errorf("skill name must be lowercase alphanumeric and hyphens, no leading/trailing hyphens: %q", name)
	}
	return nil
}
