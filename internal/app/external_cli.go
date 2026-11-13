package app

import (
	"flag"
	"os"
)

func runExternalUpdate(args []string) error {
	fs := flag.NewFlagSet("skill update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var opts runOptions
	addAllowFreshFlags(fs, &opts)
	names, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	repoRoot, home, cfg, _, err := loadContext(runOptions{})
	if err != nil {
		return err
	}
	return updateExternalRepos(cfg.ExternalSkills, home, repoRoot, names, gateOptionsFor(opts))
}

func shortCommit(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
