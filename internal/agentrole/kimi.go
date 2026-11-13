package agentrole

import (
	"fmt"
	"strings"
)

// kimiToolMapping maps canonical tool names to Kimi Code CLI built-in tools.
var kimiToolMapping = map[string]string{
	"bash": "Bash", "edit": "Edit", "glob": "Glob", "grep": "Grep",
	"read": "Read", "webfetch": "FetchURL", "websearch": "WebSearch", "write": "Write",
}

// renderKimi writes a Kimi Code CLI agent file (~/.kimi-code/agents/*.md).
// Kimi ignores fields it does not know, so no model is written. Kimi grants
// every tool to an agent without a `tools` key, so a role that names tools but
// maps none of them to a Kimi built-in is rejected instead of widened.
func renderKimi(role Role) (string, error) {
	tools := mappedTools(role.Tools, kimiToolMapping)
	if len(role.Tools) > 0 && len(tools) == 0 {
		return "", fmt.Errorf("render kimi role %q: tools %s have no Kimi built-in equivalent, and Kimi would grant every tool without a tools key", role.Name, strings.Join(role.Tools, ", "))
	}
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
	return b.String(), nil
}
