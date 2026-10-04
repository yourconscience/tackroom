package agentrole

import "strings"

// grokEfforts are the effort values Grok Build accepts; others would make it
// reject the agent file.
var grokEfforts = map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true}

// renderGrok writes Claude tool names: Grok Build resolves them in `tools:`.
func renderGrok(role Role) string {
	model := strings.TrimSpace(role.Grok.Model)
	if model == "" {
		if generic := strings.TrimSpace(role.Model); generic != "" && !canonicalModelTier(generic) {
			model = generic
		}
	}
	effort := strings.ToLower(strings.TrimSpace(role.Effort))
	if !grokEfforts[effort] {
		effort = ""
	}
	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLScalar(&b, "name", role.Name)
	writeYAMLScalar(&b, "description", role.Description)
	writeYAMLScalar(&b, "model", model)
	writeYAMLScalar(&b, "effort", effort)
	if len(role.Tools) > 0 {
		b.WriteString("tools:\n")
		for _, tool := range role.Tools {
			writeYAMLListItem(&b, tool)
		}
	}
	writeYAMLScalar(&b, "color", role.Color)
	writeGeneratedMarkdownBody(&b, role)
	return b.String()
}
