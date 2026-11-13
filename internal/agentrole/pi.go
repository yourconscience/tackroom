package agentrole

import "strings"

var piToolMapping = map[string]string{
	"bash": "bash", "edit": "edit", "glob": "find", "grep": "grep", "read": "read",
	"webfetch": "fetch_content", "websearch": "web_search", "write": "write",
}

// renderPi emits the user-agent format consumed by pi-subagents. Vanilla Pi
// ignores this directory when the extension is absent.
func renderPi(role Role) (string, error) {
	model := strings.TrimSpace(role.Pi.Model)
	if model == "" && !canonicalModelTier(role.Model) {
		model = strings.TrimSpace(role.Model)
	}
	thinking := strings.TrimSpace(role.Pi.Thinking)
	if thinking == "" {
		thinking = strings.TrimSpace(role.Effort)
	}
	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLScalar(&b, "name", role.Name)
	writeYAMLScalar(&b, "description", role.Description)
	writeYAMLScalar(&b, "model", model)
	writeYAMLScalar(&b, "thinking", thinking)
	if tools := mappedTools(role.Tools, piToolMapping); len(tools) > 0 {
		b.WriteString("tools:\n")
		for _, tool := range tools {
			writeYAMLListItem(&b, tool)
		}
	}
	writeGeneratedMarkdownBody(&b, role)
	return b.String(), nil
}
