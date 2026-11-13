package agentrole

import (
	"strconv"
	"strings"
)

func renderCodex(role Role) (string, error) {
	model := strings.TrimSpace(role.Codex.Model)
	if model == "" {
		model = codexModelFor(role.Model)
	}
	effort := strings.TrimSpace(role.Codex.ModelReasoningEffort)
	if effort == "" {
		effort = strings.TrimSpace(role.Effort)
	}
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(GeneratedMarker)
	b.WriteString(" from ")
	b.WriteString(sourceLabel(role))
	b.WriteString("; do not edit directly.\n")
	writeTOMLString(&b, "name", role.Name)
	writeTOMLString(&b, "description", role.Description)
	writeTOMLString(&b, "model", model)
	writeTOMLString(&b, "model_reasoning_effort", effort)
	b.WriteString("developer_instructions = ")
	b.WriteString(strconv.Quote(role.Instructions))
	b.WriteString("\n")
	return b.String(), nil
}

func codexModelFor(model string) string {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "", "haiku", "sonnet", "opus":
		return ""
	default:
		return strings.TrimSpace(model)
	}
}
