package app

import (
	"fmt"
	"path/filepath"
)

// checkSkillRoots refuses a skill_root that resolves to the canonical skills directory, where sync would replace each skill with a link to itself.
func checkSkillRoots(repoRoot string, selected []agentConfig) error {
	canonical := filepath.Join(repoRoot, "skills")
	for _, agent := range selected {
		if sameResolvedPath(agent.SkillRoot, canonical) {
			return fmt.Errorf("config agent %s: skill_root %s is the canonical skills directory, so sync would replace its skills with links to themselves; point skill_root elsewhere", agent.Name, agent.SkillRoot)
		}
	}
	return nil
}
