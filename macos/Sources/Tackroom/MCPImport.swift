import Foundation

/// The output of a CLI the app ran for the user, shown until dismissed.
struct ActionResult: Equatable {
    let title: String
    let text: String
    let failed: Bool
}

/// `tackroom mcp import <agent> <name> --agents a,b --config PATH`: copies one MCP server that an
/// agent has in its own config into tackroom.yaml.
///
/// Read from internal/app/mcp_cli.go: it reads one named server from one agent, writes only
/// the canonical config (no agent file changes until the next sync), refuses unknown or
/// MCP-less agents, and replaces env values with ${KEY} references, so secrets are not copied.
/// The Go usage text has no form without the agent and name.
struct MCPImportPlan: Equatable {
    let agent: String
    let name: String
    let targets: [String]
    let config: String?

    var arguments: [String] {
        var args = ["mcp", "import", agent, name, "--agents", targets.joined(separator: ",")]
        if let config { args += ["--config", config] }
        return args
    }

    /// The command as a person would paste it into a shell.
    var command: String { (["tackroom"] + arguments).map(Self.shellQuoted).joined(separator: " ") }

    /// Scopes the import to the agents that already have the server: the first one is the
    /// source and all of them become its targets. Nil when none of them can take MCP servers.
    /// - `configured`: agents in tackroom.yaml that support MCP.
    static func make(name: String, foundIn agents: [String], configured: [String], config: String?) -> MCPImportPlan? {
        let known = Set(configured.map(AgentNames.canonical))
        var seen = Set<String>()
        let targets = agents.map(AgentNames.canonical).filter { known.contains($0) && seen.insert($0).inserted }
        guard let source = targets.first else { return nil }
        return MCPImportPlan(agent: source, name: name, targets: targets, config: config)
    }

    private static func shellQuoted(_ word: String) -> String {
        let plain = CharacterSet(charactersIn: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-")
        if !word.isEmpty, word.unicodeScalars.allSatisfy(plain.contains) { return word }
        return "'" + word.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }
}
