package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func writeCanonicalTestConfig(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "tackroom.yaml")
	data := []byte("# canonical comment\nversion: 1\nfuture_key: preserve\nagents:\n  - name: Codex\n    enabled: false\n    skill_root: ~/.codex/skills\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfigDocumentPreservesUnknownFieldsAndMode(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	path := writeCanonicalTestConfig(t, root)
	doc, err := newConfigDocument(path, home)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal(true)
	saved, err := doc.applyOperations(configLayerShared, doc.revision(configLayerShared), []configOperation{{Path: "/agents/codex/enabled", Op: "set", Value: value}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved.After), "future_key: preserve") || !strings.Contains(string(saved.After), "canonical comment") {
		t.Fatalf("node edit dropped unknown field or comment:\n%s", saved.After)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
	if saved.Diff == "" || !strings.Contains(saved.Diff, "enabled: true") {
		t.Fatalf("diff does not show changed YAML:\n%s", saved.Diff)
	}
}

func TestConfigDocumentRejectsInvalidAndStaleWrites(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	path := writeCanonicalTestConfig(t, root)
	doc, err := newConfigDocument(path, home)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if _, err := doc.saveRaw(configLayerShared, doc.revision(configLayerShared), []byte("version: [")); err == nil {
		t.Fatal("invalid YAML was accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("invalid write changed canonical bytes")
	}
	if err := os.WriteFile(path, append(before, []byte("# external\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := doc.saveRaw(configLayerShared, doc.revision(configLayerShared), before); !errors.Is(err, errStaleRevision) {
		t.Fatalf("stale write error = %v, want stale revision", err)
	}
}

func TestConfigDocumentLocalOverlayIsolatedAndUIWholeEntry(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	path := writeCanonicalTestConfig(t, root)
	localPath := filepath.Join(root, "tackroom.local.yaml")
	local := []byte("ui:\n  links:\n    - name: Usage\n      url: /usage\n")
	if err := os.WriteFile(localPath, local, 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	doc, err := newConfigDocument(path, home)
	if err != nil {
		t.Fatal(err)
	}
	if doc.effective.UI == nil || doc.effective.UI.Links[0].URL != "/usage" {
		t.Fatalf("effective UI overlay missing: %#v", doc.effective.UI)
	}
	value, _ := json.Marshal("/dashboard")
	if _, err := doc.applyOperations(configLayerLocal, doc.revision(configLayerLocal), []configOperation{{Path: "/ui/links/0/url", Op: "set", Value: value}}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("local edit changed shared YAML")
	}
}

func TestConfigWebRequiresSessionOriginAndCSRF(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	path := writeCanonicalTestConfig(t, root)
	doc, err := newConfigDocument(path, home)
	if err != nil {
		t.Fatal(err)
	}
	server := &configWebServer{doc: doc, origin: "http://127.0.0.1:8765", token: "session", csrf: "csrf"}
	handler := server.handler()
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8765/api/state", nil)
	request.AddCookie(&http.Cookie{Name: "tackroom_session", Value: "session"})
	request.Header.Set("Origin", server.origin)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "future_key") {
		t.Fatalf("authenticated state response = %d %s", response.Code, response.Body.String())
	}
	mutation := httptest.NewRequest(http.MethodPatch, "http://127.0.0.1:8765/api/config", strings.NewReader(`{"layer":"shared","expected_revision":"x","operations":[]}`))
	mutation.AddCookie(&http.Cookie{Name: "tackroom_session", Value: "session"})
	mutation.AddCookie(&http.Cookie{Name: "tackroom_csrf", Value: "csrf"})
	mutation.Header.Set("Origin", server.origin)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, mutation)
	if response.Code != http.StatusForbidden {
		t.Fatalf("CSRF-less mutation status = %d, want 403", response.Code)
	}
	value, _ := json.Marshal(true)
	body := `{"layer":"shared","expected_revision":"` + doc.revision(configLayerShared) + `","operations":[{"op":"set","path":"/agents/codex/enabled","value":` + string(value) + `}]}`
	mutation = httptest.NewRequest(http.MethodPatch, "http://127.0.0.1:8765/api/config", strings.NewReader(body))
	mutation.AddCookie(&http.Cookie{Name: "tackroom_session", Value: "session"})
	mutation.AddCookie(&http.Cookie{Name: "tackroom_csrf", Value: "csrf"})
	mutation.Header.Set("Origin", server.origin)
	mutation.Header.Set("X-Tackroom-CSRF", "csrf")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, mutation)
	if response.Code != http.StatusOK {
		t.Fatalf("authorized mutation status = %d: %s", response.Code, response.Body.String())
	}
	updated, _ := os.ReadFile(path)
	if !strings.Contains(string(updated), "enabled: true") {
		t.Fatalf("authorized structured mutation did not persist: %s", updated)
	}
}

func TestConfigWebAcceptsMatchingHTTPSProxyOrigin(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	path := writeCanonicalTestConfig(t, root)
	doc, err := newConfigDocument(path, home)
	if err != nil {
		t.Fatal(err)
	}
	server := &configWebServer{doc: doc, secureCookie: true, token: "session", csrf: "csrf"}
	handler := server.handler()

	request := httptest.NewRequest(http.MethodGet, "https://macbook.example.ts.net/api/state", nil)
	request.Host = "macbook.example.ts.net"
	request.AddCookie(&http.Cookie{Name: "tackroom_session", Value: "session"})
	request.Header.Set("Origin", "https://macbook.example.ts.net")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("matching proxy origin status = %d: %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "https://macbook.example.ts.net/api/state", nil)
	request.Host = "macbook.example.ts.net"
	request.AddCookie(&http.Cookie{Name: "tackroom_session", Value: "session"})
	request.Header.Set("Referer", "https://macbook.example.ts.net/tackroom/")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("same-origin referer status = %d: %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "https://macbook.example.ts.net/api/state", nil)
	request.Host = "macbook.example.ts.net"
	request.AddCookie(&http.Cookie{Name: "tackroom_session", Value: "session"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("headerless same-origin GET status = %d: %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "https://macbook.example.ts.net/api/state", nil)
	request.Host = "macbook.example.ts.net"
	request.AddCookie(&http.Cookie{Name: "tackroom_session", Value: "session"})
	request.Header.Set("Origin", "https://other.example.ts.net")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("mismatched proxy origin status = %d, want 403", response.Code)
	}
}

func TestConfigWebRawYAMLPreservesSecrets(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	path := writeCanonicalTestConfig(t, root)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("mcp_servers:\n  - name: secret\n    enabled: false\n    command: secret-tool\n    env:\n      TOKEN: secret-value\n    agents: [Codex]\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	doc, err := newConfigDocument(path, home)
	if err != nil {
		t.Fatal(err)
	}
	server := &configWebServer{doc: doc, origin: "http://127.0.0.1:8765", token: "session", csrf: "csrf"}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8765/api/state", nil)
	request.AddCookie(&http.Cookie{Name: "tackroom_session", Value: "session"})
	request.Header.Set("Origin", server.origin)
	response := httptest.NewRecorder()
	server.handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "secret-value") {
		t.Fatalf("raw YAML lost secret: %d %s", response.Code, response.Body.String())
	}
}

func TestConfigValidationDoesNotExpandEditableLayers(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	path := writeCanonicalTestConfig(t, root)
	doc, err := newConfigDocument(path, home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.validateRaw(configLayerLocal, []byte("ui:\n  links:\n    - name: Usage\n      url: /usage\n")); err != nil {
		t.Fatal(err)
	}
	if got := doc.shared.Agents[0].SkillRoot; got != "~/.codex/skills" {
		t.Fatalf("shared skill root mutated to %q", got)
	}
	candidate, err := doc.validateRaw(configLayerShared, doc.sharedBytes)
	if err != nil {
		t.Fatal(err)
	}
	if got := candidate.Agents[0].SkillRoot; got != "~/.codex/skills" {
		t.Fatalf("shared candidate skill root expanded to %q", got)
	}
}

func TestConfigServeRejectsWildcardAddresses(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8765", ":8765", "192.0.2.1:8765", "[::]:8765"} {
		if err := validateLoopbackAddr(addr); err == nil {
			t.Fatalf("validateLoopbackAddr(%q) accepted non-loopback bind", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:8765", "[::1]:8765", "localhost:8765"} {
		if err := validateLoopbackAddr(addr); err != nil {
			t.Fatalf("validateLoopbackAddr(%q) = %v", addr, err)
		}
	}
}

// Blocker #1: a typed rewrite that clears an omitempty field (e.g. re-adding an
// MCP server with no args/env) must drop the stale value, not silently keep it,
// while preserving unknown keys and comments.
func TestReplaceTypedClearsOmittedSchemaFields(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	path := filepath.Join(root, "tackroom.yaml")
	data := []byte("# keep this comment\nversion: 1\nfuture_key: preserve\nagents:\n  - name: codex\n    enabled: true\n    skill_root: ~/.codex/skills\nmcp_servers:\n  - name: linkedin\n    enabled: true\n    command: old-command\n    args:\n      - --port\n      - \"9999\"\n    env:\n      SECRET_TOKEN: sk-super-secret-old\n    agents:\n      - codex\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := newConfigDocument(path, home)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := cloneConfig(doc.shared)
	if err != nil {
		t.Fatal(err)
	}
	for i := range cfg.MCPServers {
		if cfg.MCPServers[i].Name == "linkedin" {
			cfg.MCPServers[i].Command = "new-command"
			cfg.MCPServers[i].Args = nil
			cfg.MCPServers[i].Env = nil
		}
	}
	if _, err := doc.replaceTyped(configLayerShared, doc.revision(configLayerShared), cfg); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(path)
	s := string(saved)
	if strings.Contains(s, "SECRET_TOKEN") || strings.Contains(s, "sk-super-secret-old") {
		t.Fatalf("stale env survived the typed rewrite:\n%s", s)
	}
	if strings.Contains(s, "args:") || strings.Contains(s, "9999") {
		t.Fatalf("stale args survived the typed rewrite:\n%s", s)
	}
	if !strings.Contains(s, "command: new-command") {
		t.Fatalf("command was not updated:\n%s", s)
	}
	if !strings.Contains(s, "future_key: preserve") || !strings.Contains(s, "keep this comment") {
		t.Fatalf("typed rewrite dropped an unknown key or comment:\n%s", s)
	}
}

// Blocker #4: two concurrent saves with the same expected revision must not both
// win; exactly one succeeds and the other gets a stale-revision error.
func TestSaveRawSerializesConcurrentWrites(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	path := writeCanonicalTestConfig(t, root)
	doc, err := newConfigDocument(path, home)
	if err != nil {
		t.Fatal(err)
	}
	rev := doc.revision(configLayerShared)
	base := doc.bytes(configLayerShared)
	candidate := func(n string) []byte {
		return append(append([]byte(nil), base...), []byte("context_note_tokens: "+n+"\n")...)
	}
	cands := [][]byte{candidate("111"), candidate("222")}
	results := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			_, results[i] = doc.saveRaw(configLayerShared, rev, cands[i])
		}(i)
	}
	wg.Wait()
	ok, stale := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, errStaleRevision):
			stale++
		default:
			t.Fatalf("unexpected save error: %v", err)
		}
	}
	if ok != 1 || stale != 1 {
		t.Fatalf("concurrent same-revision saves: ok=%d stale=%d, want 1/1", ok, stale)
	}
}

// Blocker #2: the web state read reflects an external on-disk change on the next
// request, instead of serving a stale revision that then wedges saves on 409.
func TestConfigWebStateReloadsExternalChanges(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	path := writeCanonicalTestConfig(t, root)
	doc, err := newConfigDocument(path, home)
	if err != nil {
		t.Fatal(err)
	}
	server := &configWebServer{doc: doc, origin: "http://127.0.0.1:8765", token: "session", csrf: "csrf"}
	handler := server.handler()

	stateReq := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8765/api/state", nil)
		request.AddCookie(&http.Cookie{Name: "tackroom_session", Value: "session"})
		request.Header.Set("Origin", server.origin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	first := stateReq()
	if first.Code != http.StatusOK {
		t.Fatalf("first state = %d: %s", first.Code, first.Body.String())
	}
	// External edit under the running server (another pane, mcp add, editor, git).
	external := append([]byte("# external edit\n"), doc.bytes(configLayerShared)...)
	if err := os.WriteFile(path, external, 0o600); err != nil {
		t.Fatal(err)
	}
	second := stateReq()
	if second.Code != http.StatusOK {
		t.Fatalf("second state = %d: %s", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), "external edit") {
		t.Fatalf("state read did not observe the external change:\n%s", second.Body.String())
	}
}

// Finding #5: a one-field structured edit produces a diff touching only the
// changed line(s), not the whole file (indent preserved + minimal diff).
func TestOneFieldEditProducesMinimalDiff(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	path := writeCanonicalTestConfig(t, root)
	doc, err := newConfigDocument(path, home)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal(true)
	saved, err := doc.applyOperations(configLayerShared, doc.revision(configLayerShared), []configOperation{{Path: "/agents/codex/enabled", Op: "set", Value: value}})
	if err != nil {
		t.Fatal(err)
	}
	removed, added := 0, 0
	for _, line := range strings.Split(saved.Diff, "\n") {
		switch {
		case strings.HasPrefix(line, "---"), strings.HasPrefix(line, "+++"):
			continue
		case strings.HasPrefix(line, "-"):
			removed++
		case strings.HasPrefix(line, "+"):
			added++
		}
	}
	if removed > 1 || added > 1 {
		t.Fatalf("one-field toggle diff changed %d removed / %d added line(s), want <=1 each:\n%s", removed, added, saved.Diff)
	}
	if !strings.Contains(saved.Diff, "enabled: true") {
		t.Fatalf("diff does not show the changed line:\n%s", saved.Diff)
	}
}

func TestSaveConfigDocumentExpandsEmptyFlowPlaceholder(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	path := filepath.Join(root, "tackroom.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nagents: [] # populated by tackroom setup\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config{Version: 1, Agents: []agentConfig{
		{Name: agentClaudeCode, Enabled: true, SkillRoot: "~/.claude/skills", AgentRoot: "~/.claude/agents", Detect: "claude"},
		{Name: agentCodex, Enabled: true, SkillRoot: "~/.codex/skills", AgentRoot: "~/.codex/agents", Detect: "codex"},
	}}
	if err := saveConfigDocument(path, home, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "[{") || !strings.Contains(got, "\n  - name: claude\n") {
		t.Fatalf("setup agents should be written as a block list:\n%s", got)
	}
}
