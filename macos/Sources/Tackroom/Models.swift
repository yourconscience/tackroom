import Foundation

// tackroom view: /api/status. The Go report structs carry no json tags, so
// their keys are PascalCase; `tackroomStatusDecoder` lowercases the first letter.
struct StatusResponse: Decodable {
    let repo: RepoLink
    let reports: [AgentReport]
    let revision: String
}

struct RepoLink: Decodable {
    let path: String
    let actualTarget: String
    let state: String
}

struct AgentReport: Decodable, Identifiable {
    var id: String { name }
    let name: String
    let error: String?
    let detected: Bool
    let synced: Bool
    let rootState: String?
    let missing: [String]?
    let missingAgent: [String]?
    let missingMCP: [String]?
    let missingHook: [String]?
    let drifted: [String]?
    let driftedAgent: [String]?
    let driftedMCP: [String]?
    let driftedHook: [String]?
    let driftedPackage: [String]?
    let conflicts: [String]?

    enum State { case synced, drifted, notInstalled, unreadable }

    var state: State {
        if let error, !error.isEmpty { return .unreadable }
        if !detected { return .notInstalled }
        return synced ? .synced : .drifted
    }

    /// One short line naming what is out of sync, e.g. "MCP linkedin · 2 hooks drifted".
    var issueSummary: String {
        if let error, !error.isEmpty { return "config unreadable" }
        var parts: [String] = []
        func count(_ items: [String]?, _ noun: String, _ verb: String) {
            guard let n = items?.count, n > 0 else { return }
            parts.append("\(n) \(noun)\(n == 1 ? "" : "s") \(verb)")
        }
        let mcp = (missingMCP ?? []) + (driftedMCP ?? [])
        if !mcp.isEmpty { parts.append("MCP " + mcp.joined(separator: ", ")) }
        count(missing, "skill", "missing")
        count(drifted, "skill", "drifted")
        count(missingHook, "hook", "missing")
        count(driftedHook, "hook", "drifted")
        count(missingAgent, "role", "missing")
        count(driftedAgent, "role", "drifted")
        count(driftedPackage, "package", "drifted")
        count(conflicts, "conflict", "found")
        if let rootState, !rootState.isEmpty, rootState != "synced" { parts.append("root doc \(rootState)") }
        if parts.isEmpty && detected && !synced { return "needs sync" }
        return parts.joined(separator: " · ")
    }

    /// Every out-of-sync item, for the Overview detail list.
    var issueItems: [String] {
        var items: [String] = []
        func add(_ list: [String]?, _ label: String) { items += (list ?? []).map { "\(label): \($0)" } }
        add(missing, "missing skill")
        add(drifted, "drifted skill")
        add(missingMCP, "missing MCP")
        add(driftedMCP, "drifted MCP")
        add(missingHook, "missing hook")
        add(driftedHook, "drifted hook")
        add(missingAgent, "missing role")
        add(driftedAgent, "drifted role")
        add(driftedPackage, "drifted package")
        add(conflicts, "conflict")
        return items
    }
}

// tackroom view: /api/inventory (snake_case JSON).
struct Inventory: Decodable {
    let revision: String
    let agents: [InventoryAgent]
    let skills: [InventorySkill]
    let mcp: [InventoryEntry]
    let hooks: [InventoryEntry]
    let unmanaged: [UnmanagedItem]

    private enum CodingKeys: String, CodingKey { case revision, agents, skills, mcp, hooks, unmanaged }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        revision = try container.decode(String.self, forKey: .revision)
        // The server sends null, not [], when the config lists no agents.
        agents = try container.decodeIfPresent([InventoryAgent].self, forKey: .agents) ?? []
        skills = try container.decode([InventorySkill].self, forKey: .skills)
        mcp = try container.decode([InventoryEntry].self, forKey: .mcp)
        hooks = try container.decode([InventoryEntry].self, forKey: .hooks)
        unmanaged = try container.decode([UnmanagedItem].self, forKey: .unmanaged)
    }
}

struct InventoryAgent: Decodable, Identifiable {
    var id: String { name }
    let name: String
    let enabled: Bool
    let detected: Bool
    let synced: Bool
    let supportsMcp: Bool

    private enum CodingKeys: String, CodingKey {
        case name, enabled, detected, synced
        case supportsMcp = "supports_mcp"
    }

    /// This agent's cell in a skill's or server's `states`. tackroom does not inspect agents
    /// that are turned off, so they have no entry.
    func state(in states: [String: String]?) -> CellState {
        enabled ? CellState(states?[name] ?? "unknown") : .agentOff
    }
}

struct InventorySkill: Decodable, Identifiable {
    var id: String { name }
    let name: String
    let description: String?
    /// "local", or "owner/repo@abc1234" for a skill that comes from an external repo.
    let origin: String?
    let path: String?
    let tokens: Int?
    /// Per agent: ok, drift, missing, conflict, unknown, absent or error. Disabled agents have no entry.
    let states: [String: String]?

    var isExternal: Bool { origin != nil && origin != "local" }

    /// `tackroom skill update` takes the external repo's name, not the skill's:
    /// "owner/repo@abc1234" and "owner/repo (unpinned)" both give "repo".
    var sourceName: String? {
        guard isExternal, let origin else { return nil }
        let label = origin.split(whereSeparator: { $0 == "@" || $0 == " " }).first.map(String.init) ?? origin
        return label.split(separator: "/").last.map(String.init)
    }
}

/// An MCP server or hook as the inventory lists it.
struct InventoryEntry: Decodable {
    let name: String
    let enabled: Bool?
    let command: String?
    /// False when the entry lists no agents, which tackroom reads as "every agent".
    let explicit: Bool?
    let targets: [String]?
    let states: [String: String]?
}

struct UnmanagedItem: Decodable {
    let kind: String
    let name: String
    let detail: String
    let agents: [String]
    let hint: String
}

// tackroom view: POST /api/sync/preview.
struct SyncPreview: Decodable {
    struct Plan: Decodable {
        let summary: [String]?
        let destructive: [String]?
    }
    let plan: Plan
    let revision: String
    let digest: String
}

struct SyncApplyRequest: Encodable {
    let expected_revision: String
    let plan_digest: String
    let confirmed_destructive: [String]
}

// AgentsView: GET /api/v1/analytics/skills.
struct SkillAnalytics: Decodable {
    let totalSkillCalls: Int
    let distinctSkills: Int
    let bySkill: [SkillUsage]
}

struct SkillUsage: Decodable {
    struct AgentCount: Decodable {
        let agent: String
        let count: Int
    }
    let skillName: String
    let callCount: Int
    let sessionCount: Int
    let agentBreakdown: [AgentCount]?
    let lastUsedAt: Date?
}

// HarnessKit: `hk list --json`.
struct HKList: Decodable {
    let rows: [HKRow]
}

struct HKRow: Decodable {
    let name: String
    let kind: String
    let agents: [String]
    let pack: String?
}

/// A row in the Foreign view: something in a harness that tackroom does not manage.
struct ForeignItem: Identifiable {
    var id: String { "\(source)/\(kind)/\(name)/\(agents.joined(separator: ","))" }
    let kind: String
    let name: String
    let agents: [String]
    let detail: String
    let source: String
    let hint: String
}

enum Decoders {
    static let tackroomStatus: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .custom { path in
            let key = path.last!.stringValue
            return LowercasedKey(key.prefix(1).lowercased() + key.dropFirst())
        }
        return decoder
    }()

    static let snakeCase: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        decoder.dateDecodingStrategy = .custom { decoder in
            let text = try decoder.singleValueContainer().decode(String.self)
            guard let date = timestamp(text) else {
                throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "bad timestamp \(text)"))
            }
            return date
        }
        return decoder
    }()

    /// AgentsView writes RFC 3339 with any number of fractional digits ("…02.5Z", "…21.572Z", "…47Z").
    static func timestamp(_ text: String) -> Date? {
        if let date = try? Date.ISO8601FormatStyle(includingFractionalSeconds: true).parse(text) { return date }
        return try? Date.ISO8601FormatStyle().parse(text)
    }

    private struct LowercasedKey: CodingKey {
        let stringValue: String
        var intValue: Int? { nil }
        init(_ value: String) { stringValue = value }
        init?(stringValue: String) { self.stringValue = stringValue }
        init?(intValue: Int) { nil }
    }
}
