package agentrole

import "strings"

var droidToolMapping = map[string][]string{
	"bash": {"Execute"}, "edit": {"Edit"}, "glob": {"Glob"}, "grep": {"Grep"},
	"read": {"Read"}, "webfetch": {"FetchUrl"}, "websearch": {"WebSearch"}, "write": {"Create", "Edit"},
}
var droidFallbackTools = []string{"Read", "LS", "Grep", "Glob"}

func renderDroid(role Role) (string, error) {
	model := strings.TrimSpace(role.Droid.Model)
	if model == "" {
		model = droidModelFor(role.Model)
	}
	effort := strings.TrimSpace(role.Droid.ReasoningEffort)
	if effort == "" {
		effort = strings.TrimSpace(role.Effort)
	}
	tools := role.Droid.Tools
	if len(tools) == 0 {
		tools = droidToolsFor(role.Tools)
	}
	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLScalar(&b, "name", role.Name)
	writeYAMLScalar(&b, "description", role.Description)
	writeYAMLScalar(&b, "model", model)
	writeYAMLScalar(&b, "reasoningEffort", effort)
	if len(tools) > 0 {
		b.WriteString("tools:\n")
		for _, tool := range tools {
			writeYAMLListItem(&b, tool)
		}
	}
	writeGeneratedMarkdownBody(&b, role)
	return b.String(), nil
}

func droidModelFor(model string) string {
	model = strings.TrimSpace(model)
	switch strings.ToLower(model) {
	case "haiku":
		return "custom:gpt-5.5(low)"
	case "sonnet":
		return "custom:gpt-5.5(medium)"
	case "opus":
		return "custom:gpt-5.5(high)"
	default:
		if model == "" {
			return "inherit"
		}
		return model
	}
}

func droidToolsFor(tools []string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, tool := range tools {
		for _, mapped := range droidToolMapping[strings.ToLower(strings.TrimSpace(tool))] {
			if _, ok := seen[mapped]; ok {
				continue
			}
			seen[mapped] = struct{}{}
			out = append(out, mapped)
		}
	}
	if len(tools) > 0 && len(out) == 0 {
		return append([]string(nil), droidFallbackTools...)
	}
	return out
}
