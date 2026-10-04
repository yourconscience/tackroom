package agentrole

import "strings"

func renderCursor(role Role) string {
	model := strings.TrimSpace(role.Cursor.Model)
	if model == "" {
		if generic := strings.TrimSpace(role.Model); generic != "" && !canonicalModelTier(generic) {
			model = generic
		} else {
			model = "inherit"
		}
	}
	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLScalar(&b, "name", role.Name)
	writeYAMLScalar(&b, "description", role.Description)
	writeYAMLScalar(&b, "model", model)
	if role.Cursor.Readonly {
		b.WriteString("readonly: true\n")
	}
	writeGeneratedMarkdownBody(&b, role)
	return b.String()
}
