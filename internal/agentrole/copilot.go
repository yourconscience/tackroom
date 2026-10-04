package agentrole

import "strings"

// copilotToolMapping maps canonical (Claude) tool names to Copilot's
// documented tool aliases.
var copilotToolMapping = map[string]string{
	"bash": "execute", "read": "read", "edit": "edit", "multiedit": "edit", "write": "edit",
	"notebookedit": "edit", "glob": "search", "grep": "search", "webfetch": "web",
	"websearch": "web", "task": "agent", "todowrite": "todo",
}

func renderCopilot(role Role) string {
	model := strings.TrimSpace(role.Copilot.Model)
	if model == "" {
		if generic := strings.TrimSpace(role.Model); generic != "" && !canonicalModelTier(generic) {
			model = generic
		}
	}
	tools := mappedTools(role.Tools, copilotToolMapping)
	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLScalar(&b, "name", role.Name)
	writeYAMLScalar(&b, "description", role.Description)
	writeYAMLScalar(&b, "model", model)
	if len(tools) > 0 {
		b.WriteString("tools:\n")
		for _, tool := range tools {
			writeYAMLListItem(&b, tool)
		}
	}
	writeGeneratedMarkdownBody(&b, role)
	return b.String()
}
