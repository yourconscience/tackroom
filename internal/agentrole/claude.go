package agentrole

import "strings"

func renderClaude(role Role) (string, error) {
	model := strings.TrimSpace(role.Claude.Model)
	if model == "" {
		model = strings.TrimSpace(role.Model)
	}
	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLScalar(&b, "name", role.Name)
	writeYAMLScalar(&b, "description", role.Description)
	writeYAMLScalar(&b, "model", model)
	writeYAMLScalar(&b, "effort", role.Effort)
	if len(role.Tools) > 0 {
		b.WriteString("tools: ")
		b.WriteString(strings.Join(role.Tools, ", "))
		b.WriteString("\n")
	}
	writeYAMLScalar(&b, "color", role.Color)
	writeGeneratedMarkdownBody(&b, role)
	return b.String(), nil
}
