package app

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAmpMemoryHookE2ERecognizesLegacyReplayMarker(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	home := t.TempDir()
	vault := filepath.Join(home, "knowledge")
	fakeBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	memsearch := filepath.Join(fakeBin, "memsearch")
	if err := os.WriteFile(memsearch, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	payload := `{
  "platform":"amp",
  "session_id":"T-amp-e2e",
  "session_start":"2026-05-17T01:02:03+04:00",
  "messages":[
    {"role":"user","content":"wire Amp memory from $TACKROOM_REPO"},
    {"role":"assistant","content":"done"}
  ]
}`
	cmd := exec.Command("bash", filepath.Join(repoRoot, "memory", "hooks", "session-end.sh"))
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"KNOWLEDGE_DIR="+vault,
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	digestPath := filepath.Join(vault, "sessions", "2026-05-17.md")
	if err := os.MkdirAll(filepath.Dir(digestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	const existingSession = "<!-- amp-session:T-existing:start -->\n## Existing\n\n<!-- amp-session:T-existing:end -->\n"
	if err := os.WriteFile(digestPath, []byte(existingSession), 0o644); err != nil {
		t.Fatal(err)
	}
	previousDigestPath := filepath.Join(vault, "sessions", "2026-05-16.md")
	const staleSameSession = "<!-- amp-session:T-amp-e2e:start -->\n## Stale\n\n<!-- amp-session:T-amp-e2e:end -->\n"
	if err := os.WriteFile(previousDigestPath, []byte(staleSameSession), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd.Stdin = strings.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("session-end.sh failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "memory skipped replayed Amp session T-amp-e2e") {
		t.Fatalf("unexpected hook output: %s", out)
	}
	digest, err := os.ReadFile(digestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(digest) != existingSession {
		t.Fatalf("replayed payload changed target digest:\n%s", digest)
	}
	previousDigest, err := os.ReadFile(previousDigestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(previousDigest), "amp-session:T-amp-e2e") {
		t.Fatalf("legacy replay marker was unexpectedly removed:\n%s", previousDigest)
	}
}
