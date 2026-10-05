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
    let skills: [InventorySkill]
    let mcp: [InventoryEntry]
    let hooks: [InventoryEntry]
    let unmanaged: [UnmanagedItem]
}

struct InventorySkill: Decodable {
    let name: String
    let description: String?
    let origin: String?
    let tokens: Int?
}

struct InventoryEntry: Decodable {
    let name: String
    let command: String?
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

/// A row in the Skills view: one canonical skill joined with its AgentsView usage.
struct SkillRow: Identifiable {
    var id: String { name }
    let name: String
    let tokens: Int
    let calls: Int
    let sessions: Int
    let lastUsed: Date?
    let agents: String

    var lastUsedSortKey: Date { lastUsed ?? .distantPast }
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
            if let date = try? Date.ISO8601FormatStyle(includingFractionalSeconds: true).parse(text) { return date }
            return try Date.ISO8601FormatStyle().parse(text)
        }
        return decoder
    }()

    private struct LowercasedKey: CodingKey {
        let stringValue: String
        var intValue: Int? { nil }
        init(_ value: String) { stringValue = value }
        init?(stringValue: String) { self.stringValue = stringValue }
        init?(intValue: Int) { nil }
    }
}
