package agentrole

import "strings"

var ompToolMapping = map[string]string{
	"bash": "bash", "edit": "edit", "glob": "glob", "grep": "grep", "read": "read",
	"webfetch": "read", "websearch": "web_search", "write": "write",
}

func renderOMP(role Role) (string, error) {
	model := strings.TrimSpace(role.OMP.Model)
	if model == "" {
		model = strings.TrimSpace(role.Model)
	}
	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLScalar(&b, "name", role.Name)
	writeYAMLScalar(&b, "description", role.Description)
	if model != "" {
		b.WriteString("model:\n")
		writeYAMLListItem(&b, model)
	}
	writeYAMLScalar(&b, "thinking-level", role.OMP.ThinkingLevel)
	if tools := mappedTools(role.Tools, ompToolMapping); len(tools) > 0 {
		b.WriteString("tools:\n")
		for _, tool := range tools {
			writeYAMLListItem(&b, tool)
		}
	}
	writeGeneratedMarkdownBody(&b, role)
	return b.String(), nil
}
