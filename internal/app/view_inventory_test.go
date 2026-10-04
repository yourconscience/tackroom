package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func inventoryFixtureConfig() config {
	return config{
		Agents: []agentConfig{
			{Name: "claude-code", Enabled: true, SkillRoot: "/h/.claude/skills", AgentRoot: "/h/.claude/agents"},
			{Name: "pi", Enabled: true, SkillRoot: "/h/.pi/agent/skills"},
			{Name: "droid", Enabled: true, SkillRoot: "/h/.factory/skills"},
			{Name: "hermes", Enabled: true, SkillRoot: "/h/.hermes/skills"},
			{Name: "codex", Enabled: false, SkillRoot: "/h/.codex/skills"},
		},
		MCPServers: []mcpServerConfig{
			{Name: "everywhere", Enabled: true, Command: "a"},
			{Name: "claude-only", Enabled: true, Command: "b", Agents: []string{"claude-code"}},
			{Name: "parked", Enabled: false, Command: "c"},
		},
		Hooks: []hookConfig{{Name: "on-stop", Enabled: true, Event: "Stop", Command: "d"}},
	}
}

func inventoryFixtureReports() []agentReport {
	return []agentReport{
		{
			Name: "claude-code", Detected: true, SkillRoot: "/h/.claude/skills", AgentRoot: "/h/.claude/agents",
			ExpectedSkills: map[string]string{"alpha": "/r/skills/alpha", "beta": "/r/skills/beta", "gamma": "/r/skills/gamma", "delta": "/r/skills/delta"},
			Managed:        []string{"alpha"},
			Drifted:        []string{"beta"},
			Missing:        []string{"gamma"},
			Conflicts:      []string{"/h/.claude/skills/delta exists and differs from canonical"},
			External:       []string{"foreign-skill", ".system"},
			StaleManaged:   []string{"old-skill"},
			Adds:           []string{"gamma"},
			Updates:        []string{"beta"},
			ManagedAgent:   []string{"builder"},
			MissingAgent:   []string{"reviewer"},
			ManagedMCP:     []string{"everywhere"},
			MissingMCP:     []string{"claude-only"},
			ManagedHook:    []string{"on-stop"},
		},
		{
			Name: "pi", Detected: true, Synced: true, SkillRoot: "/h/.pi/agent/skills",
			ExpectedSkills: map[string]string{"alpha": "/r/skills/alpha"},
			Managed:        []string{"alpha", "beta", "gamma", "delta"},
			External:       []string{"foreign-skill"},
			DriftedMCP:     []string{"everywhere"},
		},
		{Name: "droid", Detected: false},
		{Name: "hermes", Detected: true, Error: "parse config.yaml: line 4"},
	}
}

func TestAssembleInventoryMapsSkillAndRoleStates(t *testing.T) {
	skills := map[string]skillMeta{
		"alpha": {Description: "Alpha does things", Origin: "local"},
		"beta":  {Origin: "owner/repo@abc1234"},
		"gamma": {}, "delta": {},
	}
	inv := assembleInventory(inventoryFixtureConfig(), inventoryFixtureReports(), skills, nil, nil)

	if len(inv.Agents) != 5 {
		t.Fatalf("agents = %d, want every configured agent", len(inv.Agents))
	}
	claude := inv.Agents[0]
	if !claude.Inspected || claude.Pending != 2 || claude.Counts["drifted"] != 1 || claude.Counts["missing"] != 1 || claude.Counts["unmanaged"] != 2 {
		t.Fatalf("claude-code agent = %+v", claude)
	}
	if !claude.SupportsMCP || !claude.SupportsHooks || !claude.SupportsRoles {
		t.Fatalf("claude-code supports = %+v", claude)
	}
	if codex := inv.Agents[4]; codex.Enabled || codex.Inspected {
		t.Fatalf("disabled codex should be listed but not inspected: %+v", codex)
	}

	states := map[string]map[string]string{}
	for _, skill := range inv.Skills {
		states[skill.Name] = skill.States
	}
	want := map[string]map[string]string{
		"alpha": {"claude-code": cellOK, "pi": cellOK, "droid": cellAbsent, "hermes": cellError},
		"beta":  {"claude-code": cellDrift},
		"gamma": {"claude-code": cellMissing},
		"delta": {"claude-code": cellConflict},
	}
	for skill, agents := range want {
		for agent, state := range agents {
			if got := states[skill][agent]; got != state {
				t.Errorf("skill %s on %s = %q, want %q", skill, agent, got, state)
			}
		}
	}
	if _, ok := states["alpha"]["codex"]; ok {
		t.Error("disabled agent must not get a matrix column")
	}
	if inv.Skills[0].Name != "alpha" || inv.Skills[0].Tokens == 0 {
		t.Errorf("skills should be sorted with token estimates: %+v", inv.Skills[0])
	}

	roles := map[string]map[string]string{}
	for _, role := range inv.Roles {
		roles[role.Name] = role.States
	}
	if roles["builder"]["claude-code"] != cellOK || roles["reviewer"]["claude-code"] != cellMissing || roles["builder"]["pi"] != cellUnsupported {
		t.Errorf("roles = %+v", roles)
	}
}

func TestAssembleInventoryWiring(t *testing.T) {
	inv := assembleInventory(inventoryFixtureConfig(), inventoryFixtureReports(), map[string]skillMeta{}, nil, nil)
	mcp := map[string]viewWired{}
	for _, row := range inv.MCP {
		mcp[row.Name] = row
	}
	cases := []struct{ server, agent, want string }{
		{"everywhere", "claude-code", cellOK},
		{"everywhere", "pi", cellDrift},
		{"claude-only", "claude-code", cellMissing},
		{"claude-only", "pi", cellOff},
		{"parked", "claude-code", cellDisabled},
		{"everywhere", "droid", cellAbsent},
	}
	for _, c := range cases {
		if got := mcp[c.server].States[c.agent]; got != c.want {
			t.Errorf("mcp %s on %s = %q, want %q", c.server, c.agent, got, c.want)
		}
	}
	if mcp["everywhere"].Explicit || !mcp["claude-only"].Explicit {
		t.Errorf("explicit flags wrong: %+v", mcp)
	}
	hook := inv.Hooks[0]
	if hook.States["claude-code"] != cellOK || hook.States["pi"] != cellUnsupported || hook.Event != "Stop" {
		t.Errorf("hook = %+v", hook)
	}

	custom := assembleInventory(config{
		Agents:     []agentConfig{{Name: "custom-agent", Enabled: true}},
		MCPServers: []mcpServerConfig{{Name: "x", Enabled: true}},
	}, []agentReport{{Name: "custom-agent", Detected: true}}, map[string]skillMeta{}, nil, nil)
	if got := custom.MCP[0].States["custom-agent"]; got != cellUnsupported {
		t.Errorf("unknown harness MCP = %q, want unsupported", got)
	}
}

func TestAssembleInventoryGroupsUnmanaged(t *testing.T) {
	describe := func(report agentReport, name string) string { return report.Name + ":" + name }
	inv := assembleInventory(inventoryFixtureConfig(), inventoryFixtureReports(), map[string]skillMeta{}, nil, describe)
	var foreign *viewUnmanaged
	kinds := map[string]int{}
	for i := range inv.Unmanaged {
		item := inv.Unmanaged[i]
		kinds[item.Kind]++
		if item.Kind == "skill" && item.Name == "foreign-skill" {
			foreign = &inv.Unmanaged[i]
		}
	}
	if foreign == nil || strings.Join(foreign.Agents, ",") != "claude-code,pi" {
		t.Fatalf("foreign skill should be one row across agents: %+v", inv.Unmanaged)
	}
	if !strings.HasPrefix(foreign.Hint, "tackroom skill promote ") || foreign.Detail != "claude-code:foreign-skill" {
		t.Errorf("foreign row = %+v", foreign)
	}
	for _, item := range inv.Unmanaged {
		if item.Name == ".system" && item.Hint != "" {
			t.Errorf("hidden harness folders must not suggest promote: %+v", item)
		}
	}
	if kinds["stale"] != 1 || kinds["conflict"] != 1 {
		t.Errorf("kinds = %v", kinds)
	}
	for _, item := range inv.Unmanaged {
		if containsString(item.Agents, "hermes") || containsString(item.Agents, "droid") {
			t.Errorf("unreadable or missing agents must not report unmanaged items: %+v", item)
		}
	}
}

func TestConfigWebInventoryEndpoint(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	skillDir := filepath.Join(root, "skills", "alpha")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: alpha\ndescription: Alpha checks the inventory.\n---\n# Alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "tackroom.yaml")
	writeConfig := func(enabled bool) {
		data := "version: 1\nagents:\n  - name: claude-code\n    enabled: " + map[bool]string{true: "true", false: "false"}[enabled] + "\n    skill_root: ~/.claude/skills\n"
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig(true)
	doc, err := newConfigDocument(path, home)
	if err != nil {
		t.Fatal(err)
	}
	server := &configWebServer{doc: doc, origin: "http://127.0.0.1:8765", token: "session", csrf: "csrf"}
	handler := server.handler()
	get := func(withSession bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8765/api/inventory", nil)
		if withSession {
			request.AddCookie(&http.Cookie{Name: "tackroom_session", Value: "session"})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	if response := get(false); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated inventory = %d", response.Code)
	}
	response := get(true)
	if response.Code != http.StatusOK {
		t.Fatalf("inventory = %d %s", response.Code, response.Body.String())
	}
	var inv viewInventory
	if err := json.Unmarshal(response.Body.Bytes(), &inv); err != nil {
		t.Fatal(err)
	}
	if len(inv.Skills) != 1 || inv.Skills[0].Description != "Alpha checks the inventory." || inv.Skills[0].Origin != "local" {
		t.Fatalf("skills = %+v", inv.Skills)
	}
	if inv.Skills[0].States["claude-code"] != cellMissing || inv.Revision == "" {
		t.Fatalf("expected an unsynced skill and a revision: %+v rev=%q", inv.Skills[0], inv.Revision)
	}

	writeConfig(false)
	response = get(true)
	if response.Code != http.StatusOK {
		t.Fatalf("inventory with no enabled agents = %d %s", response.Code, response.Body.String())
	}
	inv = viewInventory{}
	if err := json.Unmarshal(response.Body.Bytes(), &inv); err != nil {
		t.Fatal(err)
	}
	if len(inv.Agents) != 1 || inv.Agents[0].Enabled || len(inv.Skills[0].States) != 0 {
		t.Fatalf("disabled-only inventory = %+v", inv)
	}
}

func TestConfigWebIndexReissuesCSRFCookie(t *testing.T) {
	root := t.TempDir()
	doc, err := newConfigDocument(writeCanonicalTestConfig(t, root), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &configWebServer{doc: doc, origin: "http://127.0.0.1:8765", token: "session", csrf: "fresh"}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8765/", nil)
	request.AddCookie(&http.Cookie{Name: "tackroom_session", Value: "session"})
	request.AddCookie(&http.Cookie{Name: "tackroom_csrf", Value: "stale-from-previous-process"})
	response := httptest.NewRecorder()
	server.handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("index = %d", response.Code)
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "tackroom_csrf" && cookie.Value == "fresh" {
			return
		}
	}
	t.Fatalf("index did not reissue the current CSRF cookie: %v", response.Result().Cookies())
}

func TestListEditWritesPlainStrings(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tackroom.yaml")
	data := "version: 1\nagents:\n  - name: claude-code\n    enabled: true\n    skill_root: ~/.claude/skills\n  - name: codex\n    enabled: true\n    skill_root: ~/.codex/skills\nmcp_servers:\n  - name: tracker\n    enabled: true\n    command: echo\n    args: [x]\n    agents:\n      - claude-code\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := newConfigDocument(path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	targets, _ := json.Marshal([]string{"claude-code", "codex"})
	args, _ := json.Marshal([]string{"true"})
	saved, err := doc.applyOperations(configLayerShared, doc.revision(configLayerShared), []configOperation{
		{Path: "/mcp_servers/tracker/agents", Op: "set", Value: targets},
		{Path: "/mcp_servers/tracker/args", Op: "set", Value: args},
	})
	if err != nil {
		t.Fatal(err)
	}
	after := string(saved.After)
	if !strings.Contains(after, "      - claude-code\n      - codex\n") {
		t.Fatalf("list items should be written unquoted:\n%s", after)
	}
	if !strings.Contains(after, `"true"`) {
		t.Fatalf("a string that reads as a bool must stay quoted:\n%s", after)
	}
}

func TestAssembleInventoryUsesCanonicalRolesAndExactConflicts(t *testing.T) {
	cfg := config{Agents: []agentConfig{{Name: "pi", Enabled: true, SkillRoot: "/h/.pi/agent/skills"}, {Name: "claude-code", Enabled: true, SkillRoot: "/h/.claude/skills", AgentRoot: "/h/.claude/agents"}}}
	reports := []agentReport{
		{Name: "pi", Detected: true, SkillRoot: "/h/.pi/agent/skills", Managed: []string{"foo"}, Conflicts: []string{"/h/.pi/agent/skills/foo-bar is a real directory that differs from canonical"}},
		{Name: "claude-code", Detected: true, SkillRoot: "/h/.claude/skills", AgentRoot: "/h/.claude/agents", Conflicts: []string{"agent /h/.claude/agents/builder.md exists but is not tackroom-managed"}},
	}
	inv := assembleInventory(cfg, reports, map[string]skillMeta{"foo": {}}, []string{"builder", "tester"}, nil)
	if got := inv.Skills[0].States["pi"]; got != cellOK {
		t.Errorf("managed foo next to a foo-bar conflict = %q, want ok", got)
	}
	roles := map[string]map[string]string{}
	for _, role := range inv.Roles {
		roles[role.Name] = role.States
	}
	if len(roles) != 2 || roles["tester"]["pi"] != cellUnsupported {
		t.Fatalf("canonical roles must show even where unsupported: %+v", roles)
	}
	if roles["builder"]["claude-code"] != cellConflict {
		t.Errorf("unmanaged role file = %q, want conflict", roles["builder"]["claude-code"])
	}
}
