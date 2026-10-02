package app

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func runPromote(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("promote requires a skill name or path: tackroom promote <name-or-path>")
	}

	source := args[0]

	fs := flag.NewFlagSet("promote", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var branch string
	fs.StringVar(&branch, "branch", "", "Branch name (default: promote/<skill-name>)")
	var dryRun bool
	fs.BoolVar(&dryRun, "dry-run", false, "Copy only, skip git/PR operations")

	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	repoRoot, _, _, _, err := loadContext(runOptions{})
	if err != nil {
		return fmt.Errorf("load context: %w", err)
	}

	// Resolve source skill directory
	srcDir, name, err := resolveSkillSource(source)
	if err != nil {
		return err
	}

	// Check it doesn't already exist in tackroom
	dstDir := filepath.Join(repoRoot, "skills", name)
	if _, err := os.Stat(dstDir); err == nil {
		return fmt.Errorf("skill %q already exists in tackroom at %s", name, dstDir)
	}

	// Validate the skill has a SKILL.md
	srcSKILL := filepath.Join(srcDir, "SKILL.md")
	if _, err := os.Stat(srcSKILL); os.IsNotExist(err) {
		return fmt.Errorf("no SKILL.md found at %s", srcSKILL)
	}

	// Copy skill directory
	if err := copyDir(srcDir, dstDir); err != nil {
		return fmt.Errorf("copy skill: %w", err)
	}

	fmt.Printf("copied: %s -> skills/%s/\n", srcDir, name)

	if dryRun {
		fmt.Println("\n[dry-run] skipped git operations")
		return nil
	}

	// Git operations
	if branch == "" {
		branch = fmt.Sprintf("promote/%s", name)
	}

	commands := []struct {
		name string
		args []string
	}{
		{"git", []string{"-C", repoRoot, "checkout", "-b", branch}},
		{"git", []string{"-C", repoRoot, "add", filepath.Join("skills", name)}},
		{"git", []string{"-C", repoRoot, "commit", "-m", fmt.Sprintf("add %s skill", name)}},
		{"git", []string{"-C", repoRoot, "push", "-u", "origin", branch}},
	}

	for _, cmd := range commands {
		out, err := exec.Command(cmd.name, cmd.args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s %s: %w\n%s", cmd.name, strings.Join(cmd.args, " "), err, out)
		}
	}

	// Create PR
	prTitle := fmt.Sprintf("Add %s skill", name)
	prBody := fmt.Sprintf("Promotes `%s` skill to tackroom shared skills.\n\nSource: local Hermes skill\nPromoted: %s", name, time.Now().Format("2006-01-02"))

	repoSlug, err := gitRepoSlug(repoRoot)
	if err != nil {
		return fmt.Errorf("detect repo slug: %w (set a git remote pointing to GitHub)", err)
	}

	prOut, err := exec.Command("gh", "pr", "create",
		"--repo", repoSlug,
		"--base", "main",
		"--head", branch,
		"--title", prTitle,
		"--body", prBody,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh pr create: %w\n%s", err, prOut)
	}

	prURL := strings.TrimSpace(string(prOut))
	fmt.Printf("\nPR created: %s\n", prURL)
	fmt.Println("Run pr-triage to inspect, then merge when ready.")

	return nil
}

func resolveSkillSource(source string) (dir string, name string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}

	// If source is an absolute path, use it directly
	if filepath.IsAbs(source) {
		if fi, err := os.Stat(source); err == nil && fi.IsDir() {
			return source, filepath.Base(source), nil
		}
		return "", "", fmt.Errorf("not a directory: %s", source)
	}

	// If source contains a slash, treat as relative path under ~/.hermes/skills/
	if strings.Contains(source, "/") {
		full := filepath.Join(home, ".hermes", "skills", source)
		if fi, err := os.Stat(full); err == nil && fi.IsDir() {
			return full, filepath.Base(source), nil
		}
		return "", "", fmt.Errorf("skill not found: %s", full)
	}

	// Search ~/.hermes/skills/ for a directory matching the name
	skillsRoot := filepath.Join(home, ".hermes", "skills")
	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		return "", "", fmt.Errorf("read skills dir: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Check if this directory itself is the skill
		if entry.Name() == source {
			full := filepath.Join(skillsRoot, source)
			if _, err := os.Stat(filepath.Join(full, "SKILL.md")); err == nil {
				return full, source, nil
			}
		}
		// Check subdirectories (categorized skills)
		subEntries, err := os.ReadDir(filepath.Join(skillsRoot, entry.Name()))
		if err != nil {
			continue
		}
		for _, sub := range subEntries {
			if sub.IsDir() && sub.Name() == source {
				full := filepath.Join(skillsRoot, entry.Name(), source)
				if _, err := os.Stat(filepath.Join(full, "SKILL.md")); err == nil {
					return full, source, nil
				}
			}
		}
	}

	return "", "", fmt.Errorf("skill %q not found in ~/.hermes/skills/", source)
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}

	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		dstPath := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		return copyFile(path, dstPath)
	})
}

func gitRepoSlug(repoDir string) (string, error) {
	out, err := exec.Command("git", "-C", repoDir, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", fmt.Errorf("git remote get-url origin: %w", err)
	}
	raw := strings.TrimSpace(string(out))
	slug, ok := parseGitHubSlug(raw)
	if !ok {
		return "", fmt.Errorf("remote %q is not a GitHub URL; promote requires a GitHub remote", raw)
	}
	return slug, nil
}

func parseGitHubSlug(remote string) (string, bool) {
	remote = strings.TrimSuffix(remote, ".git")
	// SCP-style SSH: git@github.com:owner/repo or user@github.com:owner/repo
	if i := strings.Index(remote, "@github.com:"); i >= 0 && !strings.Contains(remote[:i], "/") {
		path := remote[i+len("@github.com:"):]
		return slugFromPath(path)
	}
	// URL-style: https://github.com/..., ssh://user@github.com/..., https://user@github.com/...
	for _, scheme := range []string{"https://", "http://", "ssh://"} {
		if !strings.HasPrefix(remote, scheme) {
			continue
		}
		rest := strings.TrimPrefix(remote, scheme)
		// Strip optional user@ prefix
		if at := strings.Index(rest, "@"); at >= 0 && at < strings.Index(rest, "/") {
			rest = rest[at+1:]
		}
		if strings.HasPrefix(rest, "github.com/") {
			return slugFromPath(strings.TrimPrefix(rest, "github.com/"))
		}
	}
	return "", false
}

func slugFromPath(path string) (string, bool) {
	parts := strings.SplitN(path, "/", 3)
	if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
		return parts[0] + "/" + parts[1], true
	}
	return "", false
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	return out.Close()
}
