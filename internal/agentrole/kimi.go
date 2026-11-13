package agentrole

import "strings"

// kimiToolMapping maps canonical tool names to Kimi Code CLI built-in tools.
var kimiToolMapping = map[string]string{
	"bash": "Bash", "edit": "Edit", "glob": "Glob", "grep": "Grep",
	"read": "Read", "webfetch": "FetchURL", "websearch": "WebSearch", "write": "Write",
}

// renderKimi writes a Kimi Code CLI agent file (~/.kimi-code/agents/*.md).
// Kimi ignores fields it does not know, so no model is written; without
// `tools` the agent gets every tool.
func renderKimi(role Role) string {
	tools := mappedTools(role.Tools, kimiToolMapping)
	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLScalar(&b, "name", role.Name)
	writeYAMLScalar(&b, "description", role.Description)
	if len(tools) > 0 {
		b.WriteString("tools:\n")
		for _, tool := range tools {
			writeYAMLListItem(&b, tool)
		}
	}
	writeGeneratedMarkdownBody(&b, role)
	return b.String()
}
