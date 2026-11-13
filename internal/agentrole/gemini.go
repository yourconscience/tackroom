package agentrole

import "strings"

// geminiToolMapping maps canonical tool names to Gemini CLI built-in tools.
var geminiToolMapping = map[string]string{
	"bash": "run_shell_command", "edit": "replace", "glob": "glob", "grep": "grep_search",
	"read": "read_file", "webfetch": "web_fetch", "websearch": "google_web_search", "write": "write_file",
}

// renderGemini writes a Gemini CLI local subagent (~/.gemini/agents/*.md).
// Without a model the subagent uses the session's model.
func renderGemini(role Role) (string, error) {
	model := strings.TrimSpace(role.Gemini.Model)
	if model == "" {
		if generic := strings.TrimSpace(role.Model); generic != "" && !canonicalModelTier(generic) {
			model = generic
		}
	}
	tools := role.Gemini.Tools
	if len(tools) == 0 {
		tools = mappedTools(role.Tools, geminiToolMapping)
	}
	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLScalar(&b, "name", role.Name)
	writeYAMLScalar(&b, "description", role.Description)
	b.WriteString("kind: local\n")
	writeYAMLScalar(&b, "model", model)
	if len(tools) > 0 {
		b.WriteString("tools:\n")
		for _, tool := range tools {
			writeYAMLListItem(&b, tool)
		}
	}
	writeGeneratedMarkdownBody(&b, role)
	return b.String(), nil
}
