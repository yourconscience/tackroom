package app

import (
	"fmt"
	"path/filepath"
)

// Context-cost readout: harnesses inject each synced skill's name + description
// into the model context at session start, so a bloated skill set silently eats
// context. These helpers estimate that cost in absolute terms only.
//
// CORRECTNESS: we deliberately never show a percentage of a context window and
// never hardcode a model context size. Model context windows range from 256K to
// 1M+ tokens and tackroom cannot know which model a harness session uses, so a
// wrong percentage would be worse than no percentage. Absolute estimates only.
const (
	// contextNoteTokensDefault is the doctor advisory threshold when the config
	// does not set context_note_tokens.
	contextNoteTokensDefault = 8000
	// contextTokenCharsPerToken is the rough chars-per-token estimate. This is
	// an approximation, not a real tokenizer; output is always labeled "(est.)".
	contextTokenCharsPerToken = 4
)

// harnessContextCost is the estimated skill-listing context cost for one harness.
type harnessContextCost struct {
	Name   string
	Skills int
	Bytes  int
	Tokens int
}

// estimateTokens converts a byte count into an estimated token count using a
// fixed chars-per-token ratio. This is an estimate, never an exact tokenization.
func estimateTokens(bytes int) int {
	if bytes <= 0 {
		return 0
	}
	return bytes / contextTokenCharsPerToken
}

// formatTokenEstimate renders an estimated token count, always labeled as an
// estimate, e.g. "~1.2k tokens (est.)" or "~840 tokens (est.)".
func formatTokenEstimate(tokens int) string {
	if tokens >= 1000 {
		return fmt.Sprintf("~%.1fk tokens (est.)", float64(tokens)/1000.0)
	}
	return fmt.Sprintf("~%d tokens (est.)", tokens)
}

// contextNoteThreshold resolves the configured doctor advisory threshold in
// estimated tokens. Absent config uses the default; 0 or negative disables it.
func contextNoteThreshold(cfg config) int {
	if cfg.ContextNoteTokens == nil {
		return contextNoteTokensDefault
	}
	if *cfg.ContextNoteTokens < 0 {
		return 0
	}
	return *cfg.ContextNoteTokens
}

// skillListingBytes sums the bytes of (name + description) across the skills in
// the given expected map (skill name -> skill directory). It reads each skill's
// SKILL.md frontmatter; skills whose frontmatter cannot be read are skipped.
func skillListingBytes(expected map[string]string) int {
	total := 0
	for _, dir := range expected {
		fm, err := parseSkillFrontmatter(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			continue
		}
		total += len(fm.Name) + len(fm.Description)
	}
	return total
}

// harnessContextCosts computes the estimated skill-listing context cost for each
// detected, selected harness, using the per-harness expected skill set.
func harnessContextCosts(cfg config, repoRoot string, home string, selected []agentConfig) ([]harnessContextCost, error) {
	base, err := expectedSkills(repoRoot, home, cfg)
	if err != nil {
		return nil, err
	}
	var costs []harnessContextCost
	for _, agent := range selected {
		if !isDetected(agent) {
			continue
		}
		expected, err := expectedSkillsForAgent(base, home, cfg, agent.Name)
		if err != nil {
			return nil, err
		}
		bytes := skillListingBytes(expected)
		costs = append(costs, harnessContextCost{
			Name:   agent.Name,
			Skills: len(expected),
			Bytes:  bytes,
			Tokens: estimateTokens(bytes),
		})
	}
	return costs, nil
}

func contextSkillsForReport(_ config, _ string, _ string, report agentReport) map[string]string {
	return report.ExpectedSkills
}

// contextNoteLines returns a soft advisory note for each harness whose estimated
// skill-listing tokens exceed the threshold. A threshold <= 0 disables the note.
// Notes are prefixed with "note:" and are purely informational: callers must not
// let them affect exit codes.
func contextNoteLines(threshold int, costs []harnessContextCost) []string {
	if threshold <= 0 {
		return nil
	}
	var lines []string
	for _, c := range costs {
		if c.Tokens > threshold {
			lines = append(lines, fmt.Sprintf(
				"note: %s skill listing %s exceeds context_note_tokens=%d; consider per-harness skill scoping in tackroom.yaml",
				c.Name, formatTokenEstimate(c.Tokens), threshold))
		}
	}
	return lines
}
