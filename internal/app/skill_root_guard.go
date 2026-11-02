package app

import (
	"fmt"
	"os"
)

// checkSkillRoot refuses a mirrored skill_root that is the canonical skills directory, where sync would replace each skill with a link to itself.
func checkSkillRoot(agent agentConfig, canonical string) error {
	root, err := os.Stat(agent.SkillRoot)
	if err != nil {
		return nil
	}
	skills, err := os.Stat(canonical)
	if err != nil || !os.SameFile(root, skills) {
		return nil
	}
	return fmt.Errorf("skill_root %s is the canonical skills directory, so sync would replace its skills with links to themselves; point skill_root elsewhere", agent.SkillRoot)
}
