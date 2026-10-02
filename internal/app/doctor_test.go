package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseMemsearchChunks(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   int
		okWant bool
	}{
		{
			name:   "parses chunks line",
			input:  "Total indexed chunks: 734\n",
			want:   734,
			okWant: true,
		},
		{
			name:   "parses with extra lines",
			input:  "foo\nTotal indexed chunks: 12\nbar\n",
			want:   12,
			okWant: true,
		},
		{
			name:   "missing line",
			input:  "no stats here",
			want:   0,
			okWant: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseMemsearchChunks(tc.input)
			if ok != tc.okWant {
				t.Fatalf("ok=%v want %v", ok, tc.okWant)
			}
			if got != tc.want {
				t.Fatalf("got=%d want %d", got, tc.want)
			}
		})
	}
}

func TestParseAgnixReport(t *testing.T) {
	input := []byte(`{
		"summary": {"errors": 1, "warnings": 2, "info": 3},
		"diagnostics": [
			{"level": "error", "file": "skills/foo/SKILL.md", "line": 2, "message": "bad skill"},
			{"level": "warning", "file": "AGENTS.md", "line": 1, "message": "noisy"}
		]
	}`)
	report, err := parseAgnixReport(input)
	if err != nil {
		t.Fatalf("parseAgnixReport returned error: %v", err)
	}
	if report.Summary.Errors != 1 || report.Summary.Warnings != 2 || report.Summary.Info != 3 {
		t.Fatalf("summary = %+v", report.Summary)
	}
	want := "1 errors, 2 warnings, 3 info; first error: skills/foo/SKILL.md:2: bad skill"
	if got := agnixDetail(report); got != want {
		t.Fatalf("detail = %q want %q", got, want)
	}
}

func TestCollectDirectSkillNamesIgnoresCategoryDirs(t *testing.T) {
	root := t.TempDir()
	category := filepath.Join(root, "jobs", "interview-preparation")
	if err := os.MkdirAll(category, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(category, "SKILL.md"), []byte(`---
name: interview-preparation
description: Use when preparing for interviews.
---

# Interview Preparation
`), 0o644); err != nil {
		t.Fatal(err)
	}

	names, err := collectDirectSkillNames(root)
	if err != nil {
		t.Fatalf("collectDirectSkillNames returned error: %v", err)
	}
	if _, ok := names["jobs"]; ok {
		t.Fatalf("category directory was treated as a skill name: %+v", names)
	}
	if _, ok := names["interview-preparation"]; ok {
		t.Fatalf("nested category skill was treated as direct mirror: %+v", names)
	}
}

func TestScopeAgnixToCanonicalIgnoresExternalClones(t *testing.T) {
	report := agnixReport{
		Summary: agnixSummary{Errors: 2, Warnings: 1, Info: 1},
		Diagnostics: []agnixDiagnostic{
			{Level: "error", File: "external/skills/.agents/writing-docs.md", Line: 17, Message: "Unclosed XML tag"},
			{Level: "warning", File: "external/skills/README.md", Line: 3, Message: "style"},
			{Level: "error", File: "agents/tester.md", Line: 6, Message: "bad frontmatter"},
			{Level: "info", File: "skills/tackroom/SKILL.md", Line: 1, Message: "note"},
		},
	}
	scoped, ignored := scopeAgnixToCanonical(report)
	if ignored != 2 {
		t.Fatalf("ignored = %d, want 2", ignored)
	}
	if scoped.Summary.Errors != 1 || scoped.Summary.Warnings != 0 || scoped.Summary.Info != 1 {
		t.Fatalf("scoped summary = %+v", scoped.Summary)
	}
	if len(scoped.Diagnostics) != 2 || scoped.Diagnostics[0].File != "agents/tester.md" {
		t.Fatalf("scoped diagnostics = %+v", scoped.Diagnostics)
	}
}

func TestCheckAgnixMissingBinaryWarns(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	res := checkAgnix(t.TempDir())
	if res.status != checkStatusWarn {
		t.Fatalf("agnix missing-binary status = %q, want %q (%+v)", res.status, checkStatusWarn, res)
	}
}
