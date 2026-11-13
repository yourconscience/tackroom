package agentrole

import "strings"

func renderOpenCode(role Role) (string, error) {
	mode := strings.TrimSpace(role.Opencode.Mode)
	if mode == "" {
		mode = "subagent"
	}
	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLScalar(&b, "description", role.Description)
	b.WriteString("mode: ")
	b.WriteString(mode)
	b.WriteString("\n")
	if model := strings.TrimSpace(role.Opencode.Model); model != "" {
		writeYAMLScalar(&b, "model", model)
	} else if model := strings.TrimSpace(role.Model); model != "" && !canonicalModelTier(model) {
		writeYAMLScalar(&b, "model", model)
	}
	if temperature := strings.TrimSpace(role.Opencode.Temperature); temperature != "" {
		b.WriteString("temperature: ")
		b.WriteString(temperature)
		b.WriteString("\n")
	}
	writeGeneratedMarkdownBody(&b, role)
	return b.String(), nil
}
