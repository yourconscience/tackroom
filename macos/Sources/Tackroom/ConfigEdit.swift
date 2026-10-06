import Foundation

/// A JSON value with a fixed key order. tackroom turns the JSON of an `add` operation into
/// YAML in the order it reads it, so a dictionary (unordered) would shuffle the new entry.
indirect enum JSONValue: Equatable {
    case bool(Bool)
    case string(String)
    case array([JSONValue])
    case object([(key: String, value: JSONValue)])

    static func == (lhs: JSONValue, rhs: JSONValue) -> Bool { lhs.json == rhs.json }

    var json: String {
        switch self {
        case .bool(let value): value ? "true" : "false"
        case .string(let value): Self.quote(value)
        case .array(let items): "[" + items.map(\.json).joined(separator: ",") + "]"
        case .object(let members): "{" + members.map { "\(Self.quote($0.key)):\($0.value.json)" }.joined(separator: ",") + "}"
        }
    }

    static func strings(_ values: [String]) -> JSONValue { .array(values.map { .string($0) }) }

    static func quote(_ text: String) -> String {
        let encoder = JSONEncoder()
        encoder.outputFormatting = .withoutEscapingSlashes
        return String(decoding: (try? encoder.encode(text)) ?? Data("\"\"".utf8), as: UTF8.self)
    }
}

/// One entry of PATCH /api/config `operations`: {"op", "path", "value"}.
struct ConfigOperation: Equatable {
    let op: String
    let path: String
    let value: JSONValue

    var json: String { "{\"op\":\(JSONValue.quote(op)),\"path\":\(JSONValue.quote(path)),\"value\":\(value.json)}" }
}

/// What a saved edit returns. `diff` is already redacted.
struct ConfigSave: Equatable {
    let revision: String
    let diff: String
    /// How many env lines the redaction had to mask. The server's diff carries them in clear.
    let maskedEnvLines: Int
}

/// Builds the edit operations the app sends. Paths follow applyConfigOperation in
/// internal/app/config_document.go: "/<section>/<entry name>/<field>", and "/<section>/-" to append.
enum ConfigEdit {
    static func requestBody(expectedRevision: String, operations: [ConfigOperation]) -> Data {
        let json = "{\"layer\":\"shared\",\"expected_revision\":\(JSONValue.quote(expectedRevision)),\"operations\":[\(operations.map(\.json).joined(separator: ","))]}"
        return Data(json.utf8)
    }

    /// The path syntax has no escape for "/", so such a name cannot be addressed.
    static func canAddress(_ name: String) -> Bool { !name.isEmpty && !name.contains("/") }

    /// Letters, digits, dot, dash and underscore: safe as a path segment and as a config name.
    static func isValidNewName(_ name: String) -> Bool {
        !name.isEmpty && name.unicodeScalars.allSatisfy { CharacterSet.alphanumerics.contains($0) || "._-".unicodeScalars.contains($0) }
    }

    static func agentEnabled(_ agent: String, _ enabled: Bool) -> ConfigOperation {
        ConfigOperation(op: "set", path: "/agents/\(agent)/enabled", value: .bool(enabled))
    }

    static func mcpEnabled(_ server: String, _ enabled: Bool) -> ConfigOperation {
        ConfigOperation(op: "set", path: "/mcp_servers/\(server)/enabled", value: .bool(enabled))
    }

    static func mcpAgents(_ server: String, _ agents: [String]) -> ConfigOperation {
        ConfigOperation(op: "set", path: "/mcp_servers/\(server)/agents", value: .strings(agents))
    }

    static func mcpCommand(_ server: String, _ command: String) -> ConfigOperation {
        ConfigOperation(op: "set", path: "/mcp_servers/\(server)/command", value: .string(command))
    }

    static func mcpArgs(_ server: String, _ args: [String]) -> ConfigOperation {
        ConfigOperation(op: "set", path: "/mcp_servers/\(server)/args", value: .strings(args))
    }

    /// Adds or replaces one env key, leaving the others alone. Without an `env` block yet
    /// the server has nothing to descend into, so the block is created with this one key.
    static func mcpEnv(_ server: String, key: String, value: String, hasEnv: Bool) -> ConfigOperation {
        if hasEnv {
            return ConfigOperation(op: "set", path: "/mcp_servers/\(server)/env/\(key)", value: .string(value))
        }
        return ConfigOperation(op: "set", path: "/mcp_servers/\(server)/env", value: .object([(key, .string(value))]))
    }

    static func addMCP(name: String, command: String, args: [String], env: (key: String, value: String)?, agents: [String]) -> ConfigOperation {
        var members: [(key: String, value: JSONValue)] = [("name", .string(name)), ("enabled", .bool(true)), ("command", .string(command))]
        if !args.isEmpty { members.append(("args", .strings(args))) }
        if let env { members.append(("env", .object([(env.key, .string(env.value))]))) }
        members.append(("agents", .strings(agents)))
        return ConfigOperation(op: "add", path: "/mcp_servers/-", value: .object(members))
    }

    /// An env key goes into a path segment, so it cannot hold "/" or be empty.
    static func isValidEnvKey(_ key: String) -> Bool { !key.isEmpty && !key.contains("/") && !key.contains(where: \.isWhitespace) }
}

/// Agent names as tackroom spells them, and as a config file may.
enum AgentNames {
    /// Mirrors normalizeAgentName in internal/app/config.go: lowercase, and the old
    /// "claude-code" is now "claude".
    static func canonical(_ name: String) -> String {
        let lowered = name.trimmingCharacters(in: .whitespaces).lowercased()
        return lowered == "claude-code" ? "claude" : lowered
    }

    /// The agent list after turning one agent on or off for an MCP server.
    /// - `written`: the names exactly as the entry's YAML spells them. Names other than the
    ///   toggled one keep their spelling, so a `claude-code` entry stays `claude-code`.
    ///   An empty list means "every agent", so toggling starts from `capable`.
    /// - `capable`: agents that can take MCP servers.
    /// Returns nil when the result would target no capable agent: an empty list means
    /// "every agent" to tackroom, so saving it would turn the server on everywhere.
    static func toggled(written: [String], capable: [String], agent: String, on: Bool) -> [String]? {
        let target = canonical(agent)
        var names = written.isEmpty ? capable : written
        if on {
            if !names.contains(where: { canonical($0) == target }) { names.append(target) }
        } else {
            names.removeAll { canonical($0) == target }
        }
        let capableSet = Set(capable.map(canonical))
        guard names.contains(where: { capableSet.contains(canonical($0)) }) else { return nil }
        return names
    }
}
