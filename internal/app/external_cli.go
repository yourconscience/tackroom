package app

func runExternalUpdate(names []string) error {
	repoRoot, home, cfg, _, err := loadContext(runOptions{})
	if err != nil {
		return err
	}
	return updateExternalRepos(cfg.ExternalSkills, home, repoRoot, names)
}

func shortCommit(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
