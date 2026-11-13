package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"Prefer pnpm for Node work.": "prefer pnpm for node work",
		"Do  NOT  use cmux!":         "do not use cmux",
	}
	for in, want := range cases {
		if got := normalize(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAddDedupesAcrossDays(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KNOWLEDGE_DIR", dir)
	write(t, filepath.Join(dir, "ai", "2026-08-20.md"), "# 2026-08-20\n\n- candidate: Prefer pnpm for Node work (via claude)\n")
	if err := cmdAdd([]string{"Prefer pnpm for Node work."}); err != nil {
		t.Fatalf("add: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "ai", today()+".md"))
	if data != nil {
		t.Errorf("duplicate candidate written: %s", data)
	}
	if err := cmdAdd([]string{"-src", "codex", "Brand new fact"}); err != nil {
		t.Fatalf("add new: %v", err)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "ai", today()+".md"))
	if !strings.Contains(string(data), "- candidate: Brand new fact (via codex)\n") {
		t.Errorf("new candidate missing: %s", data)
	}
}

func TestCollapseExactDuplicatesKeepFirst(t *testing.T) {
	in := "# Memory Export\n\nAuto-synced.\n\n## Sync 2026-05-01 00:00 UTC\n\n- unique fact A\n\n## Sync 2026-05-02 00:00 UTC\n\n- fact B\n- fact C\n\n## Sync 2026-05-03 00:00 UTC\n\n- FACT B!\n- fact C\n"
	got, dropped := collapseExactDuplicates(in)
	want := "# Memory Export\n\nAuto-synced.\n\n## Sync 2026-05-01 00:00 UTC\n\n- unique fact A\n\n## Sync 2026-05-02 00:00 UTC\n\n- fact B\n- fact C\n"
	if dropped != 1 || got != want {
		t.Errorf("dropped=%d got=%q want=%q", dropped, got, want)
	}
}

func TestClusterCandidatesRepeatsAndConflicts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ai")
	write(t, filepath.Join(dir, "2026-08-01.md"), "# d\n\n- candidate: Do not recommend cctop because coverage is too narrow\n")
	write(t, filepath.Join(dir, "2026-08-05.md"), "# d\n\n- candidate: do not always run gofmt before commit (via codex)\n")
	write(t, filepath.Join(dir, "2026-08-07.md"), "# d\n\n- candidate: always run gofmt before commit\n")
	cands, err := loadCandidates(dir)
	if err != nil || len(cands) != 3 {
		t.Fatalf("load: %v %d", err, len(cands))
	}
	clusters := clusterCandidates(cands)
	var repeated, conflicted int
	for _, cl := range clusters {
		if cl.Distinct >= 2 {
			repeated++
			if cl.Positive > 0 && cl.Negative > 0 {
				conflicted++
			}
		}
	}
	if repeated != 1 || conflicted != 1 {
		t.Errorf("repeated=%d conflicted=%d, want 1/1; clusters=%+v", repeated, conflicted, clusters)
	}
}

func TestDreamApplyCollapsesWithBackupAndGuards(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KNOWLEDGE_DIR", root)
	run := func(args ...string) (string, error) {
		c := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := c.CombinedOutput()
		return string(out), err
	}
	mustRun := func(args ...string) string {
		t.Helper()
		out, err := run(args...)
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return out
	}
	if out, err := run("init"); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	mustRun("config", "user.email", "t@t")
	mustRun("config", "user.name", "t")
	write(t, filepath.Join(root, "sessions", "other.md"), "- clean\n")
	kb := "# H\n\n## Sync 2026-05-01 00:00 UTC\n\n- fact A\n\n## Sync 2026-05-02 00:00 UTC\n\n- fact A\n"
	write(t, filepath.Join(root, "sessions", "knowledge.md"), kb)
	mustRun("add", "-A")
	mustRun("commit", "-m", "init")
	mustRun("branch", "-M", "main")

	// Guard: uncommitted change blocks apply.
	appendLine(t, filepath.Join(root, "sessions", "other.md"), "- dirty\n")
	if err := dreamApply(nil); err == nil {
		t.Error("apply should refuse on dirty tree")
	}
	mustRun("reset", "--hard")

	if err := dreamApply(nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "sessions", "knowledge.md"))
	if strings.Count(string(data), "## Sync") != 1 || !strings.Contains(string(data), "- fact A") {
		t.Errorf("collapse wrong: %s", data)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "sessions", "knowledge.md.bak-*"))
	if len(matches) != 1 {
		t.Errorf("expected one backup, got %v", matches)
	}
	out := mustRun("log", "--oneline")
	if !strings.Contains(out, "rem dream: collapse 1 duplicate") {
		t.Errorf("commit message missing: %s", out)
	}
	// Idempotent second run.
	if err := dreamApply(nil); err != nil {
		t.Fatalf("second apply: %v", err)
	}
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
