import Foundation
import Testing
@testable import Tackroom

// MARK: Request JSON

@Test func encodesOperationsTheWayTheServerReadsThem() {
    #expect(ConfigEdit.mcpEnabled("linkedin", false).json == #"{"op":"set","path":"/mcp_servers/linkedin/enabled","value":false}"#)
    #expect(ConfigEdit.mcpAgents("linkedin", ["claude-code", "codex"]).json ==
        #"{"op":"set","path":"/mcp_servers/linkedin/agents","value":["claude-code","codex"]}"#)
    #expect(ConfigEdit.agentEnabled("codex", false).json == #"{"op":"set","path":"/agents/codex/enabled","value":false}"#)
    #expect(ConfigEdit.mcpCommand("x", "uvx").json == #"{"op":"set","path":"/mcp_servers/x/command","value":"uvx"}"#)
    #expect(ConfigEdit.mcpArgs("x", ["--with", "fastmcp<4"]).json == #"{"op":"set","path":"/mcp_servers/x/args","value":["--with","fastmcp<4"]}"#)
}

@Test func envKeyOpDependsOnWhetherAnEnvBlockExists() {
    #expect(ConfigEdit.mcpEnv("x", key: "TOKEN", value: "a\"b", hasEnv: true).json ==
        #"{"op":"set","path":"/mcp_servers/x/env/TOKEN","value":"a\"b"}"#)
    #expect(ConfigEdit.mcpEnv("x", key: "TOKEN", value: "v", hasEnv: false).json ==
        #"{"op":"set","path":"/mcp_servers/x/env","value":{"TOKEN":"v"}}"#)
}

@Test func addOpKeepsTheKeyOrderOfExistingEntries() {
    let op = ConfigEdit.addMCP(name: "new", command: "npx", args: ["-y", "pkg"], env: ("K", "v"), agents: ["claude", "codex"])
    #expect(op.json == #"{"op":"add","path":"/mcp_servers/-","value":{"name":"new","enabled":true,"command":"npx","args":["-y","pkg"],"env":{"K":"v"},"agents":["claude","codex"]}}"#)
    let bare = ConfigEdit.addMCP(name: "new", command: "npx", args: [], env: nil, agents: ["claude"])
    #expect(bare.json == #"{"op":"add","path":"/mcp_servers/-","value":{"name":"new","enabled":true,"command":"npx","agents":["claude"]}}"#)
}

@Test func requestBodyCarriesLayerRevisionAndOperations() throws {
    let body = ConfigEdit.requestBody(expectedRevision: "abc", operations: [ConfigEdit.mcpEnabled("a", true), ConfigEdit.agentEnabled("codex", false)])
    let object = try #require(JSONSerialization.jsonObject(with: body) as? [String: Any])
    #expect(Set(object.keys) == ["layer", "expected_revision", "operations"]) // the server rejects unknown fields
    #expect(object["layer"] as? String == "shared")
    #expect(object["expected_revision"] as? String == "abc")
    #expect((object["operations"] as? [[String: Any]])?.count == 2)
}

@Test func validatesNamesAndKeysBeforeTheyBecomePathSegments() {
    #expect(ConfigEdit.isValidNewName("my-server_2.0"))
    #expect(!ConfigEdit.isValidNewName("a/b"))
    #expect(!ConfigEdit.isValidNewName("two words"))
    #expect(!ConfigEdit.isValidNewName(""))
    #expect(ConfigEdit.canAddress("linkedin"))
    #expect(!ConfigEdit.canAddress("a/b"))
    #expect(ConfigEdit.isValidEnvKey("UV_HTTP_TIMEOUT"))
    #expect(!ConfigEdit.isValidEnvKey("A/B"))
    #expect(!ConfigEdit.isValidEnvKey(""))
    #expect(!ConfigEdit.isValidEnvKey("A B"))
}

// MARK: Agent names

@Test func canonicalAgentNames() {
    #expect(AgentNames.canonical("claude-code") == "claude")
    #expect(AgentNames.canonical(" Claude ") == "claude")
    #expect(AgentNames.canonical("codex") == "codex")
}

@Test func togglingKeepsTheSpellingTheFileUses() {
    let capable = ["claude", "codex", "hermes", "pi"]
    // Adding one agent leaves "claude-code" as written.
    #expect(AgentNames.toggled(written: ["claude-code", "codex"], capable: capable, agent: "hermes", on: true) == ["claude-code", "codex", "hermes"])
    // Removing another leaves it too.
    #expect(AgentNames.toggled(written: ["claude-code", "codex"], capable: capable, agent: "codex", on: false) == ["claude-code"])
    // Removing the agent that is spelled the old way matches it by its canonical name.
    #expect(AgentNames.toggled(written: ["claude-code", "codex"], capable: capable, agent: "claude", on: false) == ["codex"])
    // Turning on an agent that is already listed changes nothing; a new one is written canonically.
    #expect(AgentNames.toggled(written: ["claude-code"], capable: capable, agent: "claude", on: true) == ["claude-code"])
    #expect(AgentNames.toggled(written: ["codex"], capable: capable, agent: "claude", on: true) == ["codex", "claude"])
}

@Test func anEmptyListMeansEveryAgent() {
    let capable = ["claude", "codex", "pi"]
    #expect(AgentNames.toggled(written: [], capable: capable, agent: "codex", on: false) == ["claude", "pi"])
    #expect(AgentNames.toggled(written: [], capable: capable, agent: "codex", on: true) == ["claude", "codex", "pi"])
}

@Test func refusesToEmptyTheList() {
    let capable = ["claude", "codex"]
    #expect(AgentNames.toggled(written: ["codex"], capable: capable, agent: "codex", on: false) == nil)
    // A name tackroom ignores (no MCP support) does not count as a target, and an empty list would mean "all".
    #expect(AgentNames.toggled(written: ["codex", "cursor"], capable: capable, agent: "codex", on: false) == nil)
}

// MARK: Reading /api/state

// Shape of a live /api/state: snake_case envelope, Go field names inside typed_config, env values masked.
private let stateJSON = #"""
{"active_layer":"shared","effective_ui":null,"read_only":false,"revision":"rev1",
 "paths":{"local":"/h/.agents/tackroom.local.yaml","shared":"/h/.agents/tackroom.yaml"},
 "raw_yaml":"version: 1\nmcp_servers:\n  - name: linkedin\n    enabled: true\n    command: uvx\n    env:\n      TOKEN: hunter2\n    agents:\n      - claude-code\n      - codex\n  - name: agentsview\n    command: agentsview\n    agents: [claude-code, \"codex\"] # both\nhooks: []\n",
 "typed_config":{"Version":1,
  "Agents":[{"Name":"claude","Enabled":true,"SkillRoot":"/x"},{"Name":"codex","Enabled":false,"SkillRoot":"/y"}],
  "MCPServers":[
   {"Name":"linkedin","Enabled":true,"Command":"uvx","Args":["--with","fastmcp<4"],"Env":{"TOKEN":"***","CHROME_PATH":"***"},"Agents":["claude","codex"]},
   {"Name":"agentsview","Enabled":true,"Command":"agentsview","Args":null,"Env":null,"Agents":["claude","codex"]}]}}
"""#

@Test func decodesTheStateAndKeepsNoEnvValues() throws {
    let state = try JSONDecoder().decode(ConfigState.self, from: Data(stateJSON.utf8))
    #expect(state.revision == "rev1")
    #expect(state.sharedPath == "/h/.agents/tackroom.yaml")
    #expect(state.agents == [.init(name: "claude", enabled: true), .init(name: "codex", enabled: false)])
    #expect(state.servers.map(\.name) == ["linkedin", "agentsview"])
    #expect(state.servers[0].envKeys == ["CHROME_PATH", "TOKEN"])
    #expect(state.servers[0].args == ["--with", "fastmcp<4"])
    #expect(state.servers[1].args.isEmpty && state.servers[1].envKeys.isEmpty)
    // The file spells Claude Code the old way; the typed config says "claude".
    #expect(state.writtenAgents["linkedin"] == ["claude-code", "codex"])
    #expect(state.writtenAgents["agentsview"] == ["claude-code", "codex"])
    #expect(!String(describing: state).contains("hunter2"))
}

@Test func decodesTheInventoryWithNullAgents() throws {
    let json = #"{"revision":"r","agents":null,"skills":[{"name":"a","description":"d","origin":"local","path":"/p","tokens":3,"states":{"claude":"ok"}}],"mcp":[],"hooks":[],"unmanaged":[]}"#
    let inventory = try JSONDecoder().decode(Inventory.self, from: Data(json.utf8))
    #expect(inventory.agents.isEmpty)
    #expect(inventory.skills[0].states?["claude"] == "ok")
    let full = #"{"revision":"r","agents":[{"name":"claude","enabled":true,"detected":true,"synced":false,"supports_mcp":true}],"skills":[],"mcp":[{"name":"m","enabled":true,"command":"c","explicit":false,"targets":[],"states":{"claude":"ok"}}],"hooks":[],"unmanaged":[]}"#
    let decoded = try JSONDecoder().decode(Inventory.self, from: Data(full.utf8))
    #expect(decoded.agents[0].supportsMcp)
    #expect(decoded.mcp[0].explicit == false)
}

@Test func namesTheExternalSourceFromAnOriginLabel() {
    func skill(_ origin: String?) -> InventorySkill {
        InventorySkill(name: "n", description: nil, origin: origin, path: nil, tokens: nil, states: nil)
    }
    #expect(skill("mattpocock/skills@959a8e9").sourceName == "skills")
    #expect(skill("latent-spaces/brag (unpinned)").sourceName == "brag")
    #expect(skill("local").sourceName == nil)
    #expect(skill(nil).sourceName == nil)
    #expect(skill("local").isExternal == false)
    #expect(skill("a/b@c").isExternal)
}

// MARK: Reading the YAML

@Test func readsBlockAndFlowAgentLists() {
    let yaml = """
    version: 1
    mcp_servers:
      - name: block
        enabled: true
        agents:
          - claude-code   # old spelling
          - "codex"
          - 'pi'
      - name: flow
        agents: [claude, codex]
      - name: later-key
        command: x
        agents:
          - hermes
        args:
          - --one
      - name: none
        command: y
      - name: all
        agents: []
    hooks:
      - name: not-a-server
        agents:
          - claude
    """
    let spelled = ConfigText.mcpAgentSpellings(inYAML: yaml)
    #expect(spelled["block"] == ["claude-code", "codex", "pi"])
    #expect(spelled["flow"] == ["claude", "codex"])
    #expect(spelled["later-key"] == ["hermes"])
    #expect(spelled["none"] == nil)
    #expect(spelled["all"] == [])
    #expect(spelled["not-a-server"] == nil)
}

@Test func readsListsWithTheDashesAtTheKeyIndent() {
    let yaml = """
    mcp_servers:
    - name: a
      agents:
      - claude-code
      - codex
    - name: b
      agents: [omp]
    """
    let spelled = ConfigText.mcpAgentSpellings(inYAML: yaml)
    #expect(spelled["a"] == ["claude-code", "codex"])
    #expect(spelled["b"] == ["omp"])
}

@Test func readsNothingWhenThereAreNoServers() {
    #expect(ConfigText.mcpAgentSpellings(inYAML: "version: 1\nagents: []\n").isEmpty)
    #expect(ConfigText.mcpAgentSpellings(inYAML: "").isEmpty)
    #expect(ConfigText.mcpAgentSpellings(inYAML: "mcp_servers: []\n").isEmpty)
}

// MARK: Diff redaction

private let secret = "hunter2-secret"

@Test func masksEnvValuesAndKeepsOnlyContextAroundChanges() {
    let diff = """
    --- /h/tackroom.yaml
    +++ /h/tackroom.yaml
     version: 1
     mcp_servers:
       - name: linkedin
         enabled: true
         command: uvx
         env:
           UV_HTTP_TIMEOUT: "300"
    -      CHROME_PATH: /old/\(secret)
    +      CHROME_PATH: /new/\(secret)
           OTHER: \(secret)
         agents:
           - claude
       - name: other
         command: x
         args:
           - one
     hooks: []
    """
    let result = ConfigText.redactedDiff(diff)
    #expect(!result.text.contains(secret))
    #expect(result.text.contains("-      CHROME_PATH: ***"))
    #expect(result.text.contains("+      CHROME_PATH: ***"))
    #expect(result.text.contains("       OTHER: ***"))
    #expect(result.text.hasPrefix("--- /h/tackroom.yaml\n+++ /h/tackroom.yaml\n"))
    #expect(result.masked == 4) // UV_HTTP_TIMEOUT, both CHROME_PATH lines, OTHER
    // Two context lines each side of the change: the far top and bottom are folded.
    #expect(!result.text.contains("hooks: []"))
    #expect(!result.text.contains("version: 1"))
    #expect(result.text.contains(" ..."))
}

@Test func masksFlowStyleEnvAndAddedEntries() {
    let diff = """
    --- /h/t.yaml
    +++ /h/t.yaml
     mcp_servers:
    +  - {name: n, enabled: true, command: c, env: {TOKEN: \(secret), B: x}, agents: [claude]}
       - name: keep
         env: {}
    """
    let result = ConfigText.redactedDiff(diff)
    #expect(!result.text.contains(secret))
    #expect(result.text.contains("env: {***}, agents: [claude]}"))
    #expect(result.text.contains("env: {}")) // nothing to hide
    #expect(result.masked == 1)
}

@Test func anEmptyDiffStaysEmpty() {
    #expect(ConfigText.redactedDiff("").text == "")
    #expect(ConfigText.redactedDiff(" a\n b\n").text == "") // no change lines
}

// MARK: Import

@Test func scopesAnImportToTheAgentsThatHaveTheServer() throws {
    let plan = try #require(MCPImportPlan.make(name: "node_repl", foundIn: ["codex", "claude-code", "cursor", "codex"],
                                               configured: ["claude", "codex", "pi"], config: "/h/.agents/tackroom.yaml"))
    #expect(plan.agent == "codex")
    #expect(plan.targets == ["codex", "claude"])
    #expect(plan.arguments == ["mcp", "import", "codex", "node_repl", "--agents", "codex,claude", "--config", "/h/.agents/tackroom.yaml"])
    #expect(plan.command == "tackroom mcp import codex node_repl --agents codex,claude --config /h/.agents/tackroom.yaml")
}

@Test func importIsNotOfferedWithoutACapableAgent() {
    #expect(MCPImportPlan.make(name: "x", foundIn: ["cursor"], configured: ["claude"], config: nil) == nil)
    #expect(MCPImportPlan.make(name: "x", foundIn: [], configured: ["claude"], config: nil) == nil)
}

@Test func quotesTheCommandForTheShell() throws {
    let plan = try #require(MCPImportPlan.make(name: "my server's", foundIn: ["claude"], configured: ["claude"], config: "/a b/t.yaml"))
    #expect(plan.command == #"tackroom mcp import claude 'my server'\''s' --agents claude --config '/a b/t.yaml'"#)
}
