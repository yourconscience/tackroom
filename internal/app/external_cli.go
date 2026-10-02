package app

import (
	"fmt"
	"path/filepath"
)

func runExternal(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tackroom external <list|update> [name ...]")
	}
	switch args[0] {
	case "list":
		repoRoot, home, cfg, _, err := loadContext(runOptions{})
		if err != nil {
			return err
		}
		return externalList(cfg, home, repoRoot)
	case "update":
		return runExternalUpdate(args[1:])
	default:
		return fmt.Errorf("unknown external subcommand %q; expected list or update", args[0])
	}
}

func runExternalUpdate(names []string) error {
	repoRoot, home, cfg, _, err := loadContext(runOptions{})
	if err != nil {
		return err
	}
	return updateExternalRepos(cfg.ExternalSkills, home, repoRoot, names)
}

func externalList(cfg config, home string, repoRoot string) error {
	if len(cfg.ExternalSkills) == 0 {
		fmt.Println("no external skill sources configured")
		return nil
	}
	lock, err := readLockFile(repoRoot)
	if err != nil {
		return err
	}
	cacheRoot := externalCacheDir(home)
	for _, src := range cfg.ExternalSkills {
		name := repoName(src.URL)
		cachePath := filepath.Join(cacheRoot, name)
		state := "not cloned"
		if hasDir(filepath.Join(cachePath, ".git")) {
			state = "synced (" + externalSkillCommit(cachePath) + ")"
		}
		pinState := "unpinned"
		if pin := lockEntryFor(lock, src); pin != nil {
			pinState = "pinned " + shortCommit(pin.Commit)
			if head := externalSkillCommitFull(cachePath); head != "" && head != pin.Commit {
				pinState += " (cache drifted to " + shortCommit(head) + ")"
			}
		}
		fmt.Printf("  %s  %s@%s  %s  %s\n", name, src.URL, src.Branch, state, pinState)
	}
	return nil
}

func shortCommit(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
