package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type gatewayHarnessCase struct {
	name, skillRoot, mcpFile, rootLink string
	preexisting                        string
	mcpWant                            []string
}

func gatewayHarnessCases(home string) []gatewayHarnessCase {
	return []gatewayHarnessCase{
		{
			name: agentOpenClaw, skillRoot: "~/.openclaw/skills",
			mcpFile:     filepath.Join(home, ".openclaw", "openclaw.json"),
			preexisting: `{"gateway":{"port":18789},"mcp":{"servers":{"mine":{"command":"mine-mcp"}}}}`,
			mcpWant:     []string{`"port": 18789`, `"mine-mcp"`, `"command": "local-mcp"`, `"LOCAL_TOKEN": "x"`},
		},
		{
			name: agentDSH, skillRoot: "~/.dsh/skills",
			mcpFile:  filepath.Join(home, ".dsh", "cordis.patch.yml"),
			rootLink: filepath.Join(home, ".dsh", "AGENTS.md"),
			preexisting: `# my memory server
- insert:
    - id: memory-mcp-reference
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: reference_memory
        transport: stdio
        command: mcp-server-memory
        cwd: !!js process.cwd()
`,
			mcpWant: []string{"# my memory server", "cwd: !!js process.cwd()", "id: tackroom-mcp-local",
				"name: '@deepseek-ai/dsh-mcp-client'", "serverName: local", "transport: stdio", "command: local-mcp", "LOCAL_TOKEN: x"},
		},
	}
}

func writeGatewayHarnessRoot(t *testing.T, repoRoot string, c gatewayHarnessCase) {
	t.Helper()
	writeSyncTestFile(t, filepath.Join(repoRoot, "tackroom.yaml"), []byte(`version: 1
agents:
  - name: `+c.name+`
    enabled: true
    skill_root: `+c.skillRoot+`
    detect: `+c.name+`
mcp_servers:
  - name: local
    enabled: true
    command: local-mcp
    args: [serve]
    env:
      LOCAL_TOKEN: x
    agents: [`+c.name+`]
`))
	writeSyncTestFile(t, filepath.Join(repoRoot, "AGENTS.md"), []byte("# Shared instructions\n"))
	writeSyncTestFile(t, filepath.Join(repoRoot, "skills", "sample", "SKILL.md"), []byte("---\nname: sample\ndescription: sample skill\n---\n"))
}

func gatewayCase(home, name string) gatewayHarnessCase {
	for _, c := range gatewayHarnessCases(home) {
		if c.name == name {
			return c
		}
	}
	return gatewayHarnessCase{}
}

func clearGatewayEnv(t *testing.T) {
	for _, key := range []string{"OPENCLAW_STATE_DIR", "OPENCLAW_CONFIG_PATH", "DSH_HOME", "DSH_AGENTS_HOME"} {
		t.Setenv(key, "")
	}
}

func TestOpenClawAndDSHSyncEndToEnd(t *testing.T) {
	for _, probe := range gatewayHarnessCases("") {
		t.Run(probe.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			clearGatewayEnv(t)
			c := gatewayCase(home, probe.name)
			fakePath(t, c.name)
			repoRoot := filepath.Join(home, ".agents")
			writeGatewayHarnessRoot(t, repoRoot, c)
			writeSyncTestFile(t, c.mcpFile, []byte(c.preexisting))
			configPath := filepath.Join(repoRoot, "tackroom.yaml")

			if err := runSync(runOptions{ConfigPath: configPath, Agents: c.name}); err != nil {
				t.Fatal(err)
			}
			assertFileContains(t, c.mcpFile, c.mcpWant)
			if c.name == agentOpenClaw {
				data, _ := os.ReadFile(c.mcpFile)
				var parsed struct {
					MCP struct {
						Servers map[string]map[string]interface{} `json:"servers"`
					} `json:"mcp"`
				}
				if err := json.Unmarshal(data, &parsed); err != nil {
					t.Fatalf("openclaw.json is not valid JSON: %v\n%s", err, data)
				}
				if parsed.MCP.Servers["local"]["command"] != "local-mcp" || parsed.MCP.Servers["mine"] == nil {
					t.Fatalf("mcp.servers = %v", parsed.MCP.Servers)
				}
			}
			if c.rootLink != "" {
				if target, err := os.Readlink(c.rootLink); err != nil || target != filepath.Join(repoRoot, "AGENTS.md") {
					t.Fatalf("root instructions link %s = %q, %v", c.rootLink, target, err)
				}
			}
			mirror := filepath.Join(expandPath(c.skillRoot, home), "sample")
			if _, err := os.Lstat(mirror); !os.IsNotExist(err) {
				t.Fatalf("%s reads ~/.agents/skills natively; no mirror expected at %s (%v)", c.name, mirror, err)
			}

			before := snapshotFiles(t, c.mcpFile)
			if err := runSync(runOptions{ConfigPath: configPath, Agents: c.name}); err != nil {
				t.Fatal(err)
			}
			if after := snapshotFiles(t, c.mcpFile); after != before {
				t.Fatalf("second sync changed %s", c.mcpFile)
			}

			cfg, err := loadConfig(repoRoot, home, configPath)
			if err != nil {
				t.Fatal(err)
			}
			expected, err := expectedSkills(repoRoot, home, cfg)
			if err != nil {
				t.Fatal(err)
			}
			reports, err := inspectAgents(cfg.Agents, expected, repoRoot, home, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if r := reports[0]; !r.Synced || len(r.ManagedMCP) != 1 {
				t.Fatalf("after sync: synced=%v mcp=%v", r.Synced, r.ManagedMCP)
			}

			got, err := readNativeMCPServer(c.name, "local", home)
			if err != nil || got.Command != "local-mcp" || !stringSlicesEqual(got.Args, []string{"serve"}) || got.Env["LOCAL_TOKEN"] != "x" {
				t.Fatalf("read back = %+v, %v", got, err)
			}
		})
	}
}

func TestOpenClawAndDSHMirrorSkillsWhenNotReadNatively(t *testing.T) {
	cases := map[string]func(home string){
		agentOpenClaw + " other root": func(string) {},
		agentDSH + " other root":      func(string) {},
		// OpenClaw drops ~/.agents/skills when its state dir is relocated.
		agentOpenClaw + " custom state": func(home string) { t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(home, "oc-state")) },
	}
	for label, setEnv := range cases {
		t.Run(label, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			clearGatewayEnv(t)
			name := strings.Fields(label)[0]
			c := gatewayCase(home, name)
			fakePath(t, c.name)
			repoRoot := filepath.Join(home, ".agents")
			if strings.HasSuffix(label, "other root") {
				repoRoot = filepath.Join(home, "elsewhere")
			}
			setEnv(home)
			writeGatewayHarnessRoot(t, repoRoot, c)
			if err := runSync(runOptions{ConfigPath: filepath.Join(repoRoot, "tackroom.yaml"), Agents: c.name}); err != nil {
				t.Fatal(err)
			}
			mirror := filepath.Join(expandPath(c.skillRoot, home), "sample", "SKILL.md")
			if _, err := os.Stat(mirror); err != nil {
				t.Fatalf("%s needs a skill mirror here: %v", label, err)
			}
		})
	}
}

func TestDSHMCPGroupsTackroomRowsAndRejectsBadNames(t *testing.T) {
	home := t.TempDir()
	clearGatewayEnv(t)
	target, err := mcpTargetForHarness(agentDSH)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two"} {
		if err := target.patch(target, mcpServerConfig{Name: name, Command: name + "-mcp"}, home); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(dshPatchPath(home))
	if n := strings.Count(string(data), "- insert:"); n != 1 {
		t.Fatalf("want one insert op for tackroom rows, got %d:\n%s", n, data)
	}
	if err := target.patch(target, mcpServerConfig{Name: "bad name", Command: "x"}, home); err == nil {
		t.Fatal("server names outside [A-Za-z0-9_-]{1,32} must be rejected")
	}
}

func TestOpenClawMCPRefusesNonObjectParent(t *testing.T) {
	home := t.TempDir()
	clearGatewayEnv(t)
	writeSyncTestFile(t, openClawConfigPath(home), []byte(`{"mcp": "oops"}`))
	target, err := mcpTargetForHarness(agentOpenClaw)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.patch(target, mcpServerConfig{Name: "local", Command: "x"}, home); err == nil {
		t.Fatal(`expected an error when "mcp" is not an object`)
	}
}
