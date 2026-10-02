package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	mcpTestNodeCommand      = "node"
	mcpTestTokenPlaceholder = "${TOKEN}"
	mcpTestUVXCommand       = "uvx"
)

func writeTestTackroomConfig(t *testing.T, path string) {
	t.Helper()
	data := []byte(`version: 1
agents:
  - name: claude-code
    enabled: true
    skill_root: ~/.claude/skills
  - name: codex
    enabled: true
    skill_root: ~/.codex/skills
  - name: hermes
    enabled: true
    skill_root: ~/.hermes/skills
  - name: droid
    enabled: true
    skill_root: ~/.factory/skills
mcp_servers: []
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.String(), runErr
}

func TestMCPCLIAddListRemove(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "tackroom.yaml")
	writeTestTackroomConfig(t, configPath)

	if err := runMCP([]string{"add", "local", "--command", mcpTestUVXCommand, "--arg", "pkg@latest", "--env", "SECRET=value", "--agents", "claude-code,droid", "--config", configPath}); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := loadEditableMCPConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MCPServers) != 1 {
		t.Fatalf("MCP server count = %d, want 1", len(cfg.MCPServers))
	}
	server := cfg.MCPServers[0]
	if server.Name != "local" || server.Command != mcpTestUVXCommand || !stringSlicesEqual(server.Args, []string{"pkg@latest"}) {
		t.Fatalf("unexpected server: %#v", server)
	}
	if !stringSlicesEqual(server.Agents, []string{"claude-code", "droid"}) {
		t.Fatalf("agents = %#v", server.Agents)
	}
	if server.Env["SECRET"] != "value" {
		t.Fatalf("env not stored: %#v", server.Env)
	}

	out, err := captureStdout(t, func() error { return runMCP([]string{"list", "--config", configPath}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "local") || !strings.Contains(out, "SECRET") {
		t.Fatalf("list output missing expected metadata: %s", out)
	}
	if strings.Contains(out, "value") {
		t.Fatalf("list output leaked env value: %s", out)
	}

	if err := runMCP([]string{"remove", "local", "--config", configPath}); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = loadEditableMCPConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MCPServers) != 0 {
		t.Fatalf("MCP server count after remove = %d, want 0", len(cfg.MCPServers))
	}
}

func TestMCPCLIImport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(t.TempDir(), "tackroom.yaml")
	writeTestTackroomConfig(t, configPath)

	claudePath := filepath.Join(home, ".claude.json")
	if err := os.MkdirAll(filepath.Dir(claudePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudePath, []byte(fmt.Sprintf(`{"mcpServers":{"imported":{"type":"stdio","command":%q,"args":["server.js"],"env":{"TOKEN":"secret"}}}}`, mcpTestNodeCommand)), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runMCP([]string{"import", "claude-code", "imported", "--agents", "codex,hermes", "--config", configPath}); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := loadEditableMCPConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MCPServers) != 1 {
		t.Fatalf("MCP server count = %d, want 1", len(cfg.MCPServers))
	}
	server := cfg.MCPServers[0]
	if server.Name != "imported" || server.Command != mcpTestNodeCommand || !stringSlicesEqual(server.Args, []string{"server.js"}) {
		t.Fatalf("unexpected imported server: %#v", server)
	}
	if !stringSlicesEqual(server.Agents, []string{"codex", "hermes"}) {
		t.Fatalf("agents = %#v", server.Agents)
	}
	if server.Env["TOKEN"] != mcpTestTokenPlaceholder {
		t.Fatalf("env not redacted: %#v", server.Env)
	}
	if strings.Contains(server.Env["TOKEN"], "secret") {
		t.Fatalf("env leaked raw secret: %#v", server.Env)
	}
}

func TestMCPCLIImportCodexMultiline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(t.TempDir(), "tackroom.yaml")
	writeTestTackroomConfig(t, configPath)

	codexPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte(fmt.Sprintf(`[mcp_servers.foo]
command = %q
args = [
  "server.js", # entry comment
  "--flag", # flag comment
  "value#kept", # hash inside quoted strings is not a comment
]
env = {
  TOKEN = "secret", # token comment
  EXISTING = "${EXISTING_TOKEN}", # existing reference comment
  HASH = "keep#value", # hash inside quoted strings is not a comment
}
`, mcpTestNodeCommand)), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runMCP([]string{"import", "codex", "foo", "--agents", "claude-code", "--config", configPath}); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := loadEditableMCPConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MCPServers) != 1 {
		t.Fatalf("MCP server count = %d, want 1", len(cfg.MCPServers))
	}
	server := cfg.MCPServers[0]
	if server.Name != "foo" || server.Command != mcpTestNodeCommand || !stringSlicesEqual(server.Args, []string{"server.js", "--flag", "value#kept"}) {
		t.Fatalf("unexpected imported server: %#v", server)
	}
	if server.Env["TOKEN"] != mcpTestTokenPlaceholder {
		t.Fatalf("env TOKEN not redacted: %#v", server.Env)
	}
	if server.Env["EXISTING"] != "${EXISTING_TOKEN}" {
		t.Fatalf("env reference not preserved: %#v", server.Env)
	}
	if server.Env["HASH"] != "${HASH}" {
		t.Fatalf("env HASH *** redacted: %#v", server.Env)
	}
	if strings.Contains(server.Env["TOKEN"], "secret") {
		t.Fatalf("env leaked raw secret: %#v", server.Env)
	}
}

func TestMCPCLIImportCodexLiteralStrings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(t.TempDir(), "tackroom.yaml")
	writeTestTackroomConfig(t, configPath)

	codexPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte(`[mcp_servers.foo]
command = 'node#bin'
args = ['server#js', '--literal=\path']
env = { TOKEN = 'secret', HASH = 'keep#value' }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runMCP([]string{"import", "codex", "foo", "--agents", "claude-code", "--config", configPath}); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := loadEditableMCPConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MCPServers) != 1 {
		t.Fatalf("MCP server count = %d, want 1", len(cfg.MCPServers))
	}
	server := cfg.MCPServers[0]
	if server.Name != "foo" || server.Command != "node#bin" || !stringSlicesEqual(server.Args, []string{"server#js", `--literal=\path`}) {
		t.Fatalf("unexpected imported server: %#v", server)
	}
	if server.Env["TOKEN"] != mcpTestTokenPlaceholder {
		t.Fatalf("env TOKEN not redacted: %#v", server.Env)
	}
	if server.Env["HASH"] != "${HASH}" {
		t.Fatalf("env HASH not parsed/redacted: %#v", server.Env)
	}
}

func TestMCPCLIRejectsUnsupportedAgent(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "tackroom.yaml")
	writeTestTackroomConfig(t, configPath)
	if err := runMCP([]string{"add", "bad", "--command", mcpTestUVXCommand, "--agents", "unsupported-agent", "--config", configPath}); err == nil {
		t.Fatal("mcp add with unknown agent succeeded, want error")
	}
}
