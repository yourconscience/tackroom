package agentrole

import "strings"

var qwenToolMapping = map[string]string{
	"bash": "run_shell_command", "edit": "replace", "glob": "glob", "grep": "grep_search",
	"read": "read_file", "webfetch": "web_fetch", "websearch": "web_search", "write": "write_file",
}

func renderQwen(role Role) (string, error) {
	model := strings.TrimSpace(role.Qwen.Model)
	if model == "" {
		if generic := strings.TrimSpace(role.Model); generic != "" && !canonicalModelTier(generic) {
			model = generic
		} else {
			model = "inherit"
		}
	}
	tools := role.Qwen.Tools
	if len(tools) == 0 {
		tools = mappedTools(role.Tools, qwenToolMapping)
	}
	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLScalar(&b, "name", role.Name)
	writeYAMLScalar(&b, "description", role.Description)
	writeYAMLScalar(&b, "model", model)
	writeYAMLScalar(&b, "approvalMode", role.Qwen.ApprovalMode)
	if len(tools) > 0 {
		b.WriteString("tools:\n")
		for _, tool := range tools {
			writeYAMLListItem(&b, tool)
		}
	}
	writeGeneratedMarkdownBody(&b, role)
	return b.String(), nil
}
