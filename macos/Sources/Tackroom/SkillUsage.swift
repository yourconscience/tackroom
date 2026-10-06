import Foundation

// Two signals say a skill was used, and AgentsView only counts one of them:
//  - Invoked: explicit calls (Claude's Skill tool, slash commands, Hermes), from
//    GET /api/v1/analytics/skills.
//  - Loaded: Codex, Pi and OMP load a skill by reading skills/<name>/SKILL.md,
//    which that endpoint ignores. `agentsview session search` finds those reads.

/// One hit from `agentsview session search`: a tool call whose input names a SKILL.md path.
struct SearchMatch: Equatable {
    let sessionID: String
    let agent: String
    let timestamp: Date?
    let snippet: String
}

struct SearchPage: Equatable {
    let matches: [SearchMatch]
    let nextCursor: Int?
}

/// The sessions of one agent that read one skill's SKILL.md.
struct SkillLoad: Equatable {
    let skill: String
    let agent: String
    let sessions: Int
    let last: Date?
}

enum SkillSearch {
    static let query = #"skills/[a-z0-9-]+/SKILL\.md"#
    static let pageSize = 500
    /// Counting must never stop early: a partial count would flag used skills as unused.
    static let maxPages = 40

    private static let path = try! NSRegularExpression(pattern: #"skills/([a-z0-9][a-z0-9-]*)/SKILL\.md"#)

    private struct RawPage: Decodable {
        struct Match: Decodable {
            let session_id: String
            let agent: String
            let timestamp: String?
            let snippet: String
        }
        let matches: [Match]?
        let next_cursor: Int?
    }

    static func parsePage(_ data: Data) throws -> SearchPage {
        let raw = try JSONDecoder().decode(RawPage.self, from: data)
        let matches = (raw.matches ?? []).map {
            SearchMatch(sessionID: $0.session_id, agent: $0.agent, timestamp: $0.timestamp.flatMap(Decoders.timestamp), snippet: $0.snippet)
        }
        return SearchPage(matches: matches, nextCursor: raw.next_cursor)
    }

    /// Skill names in one snippet, in order of appearance. The snippet wraps the hit in
    /// <mark> tags and can hold several paths.
    static func skillNames(inSnippet snippet: String) -> [String] {
        let text = snippet.replacingOccurrences(of: "<mark>", with: "").replacingOccurrences(of: "</mark>", with: "")
        let range = NSRange(text.startIndex..., in: text)
        var seen = Set<String>()
        var names: [String] = []
        for match in path.matches(in: text, range: range) {
            guard let nameRange = Range(match.range(at: 1), in: text) else { continue }
            let name = String(text[nameRange])
            if seen.insert(name).inserted { names.append(name) }
        }
        return names
    }

    /// Counts distinct sessions per (skill, agent) and keeps the latest read time.
    static func loads(from matches: [SearchMatch]) -> [SkillLoad] {
        struct Key: Hashable { let skill: String, agent: String }
        var sessions: [Key: Set<String>] = [:]
        var last: [Key: Date] = [:]
        for match in matches {
            for name in skillNames(inSnippet: match.snippet) {
                let key = Key(skill: name, agent: match.agent)
                sessions[key, default: []].insert(match.sessionID)
                if let time = match.timestamp, time > (last[key] ?? .distantPast) { last[key] = time }
            }
        }
        return sessions.map { SkillLoad(skill: $0.key.skill, agent: $0.key.agent, sessions: $0.value.count, last: last[$0.key]) }
            .sorted { ($0.skill, $0.agent) < ($1.skill, $1.agent) }
    }

    static func arguments(days: Int, machine: String?, cursor: Int?) -> [String] {
        var args = ["session", "search", query, "--regex", "--in", "tool_input", "--since", "\(days)d", "--limit", "\(pageSize)", "--json"]
        if let cursor { args += ["--cursor", "\(cursor)"] }
        if let machine { args += ["--machine", machine] }
        return args
    }

    /// The cursor for the next page, or nil when this was the last one. The last page reports
    /// `next_cursor` 0 rather than null (agentsview 0.42); looping on that would restart at the
    /// first page forever, so a cursor that does not move forward ends the search.
    static func advance(_ page: SearchPage, from cursor: Int?) -> Int? {
        guard let next = page.nextCursor, next > (cursor ?? 0), !page.matches.isEmpty else { return nil }
        return next
    }

    /// Runs the search page by page.
    static func fetch(days: Int, machine: String?) async throws -> [SearchMatch] {
        guard let agentsview = Tools.find("agentsview") else { throw ServerError(message: "agentsview not found") }
        var all: [SearchMatch] = []
        var cursor: Int?
        for _ in 0..<maxPages {
            let output = try await Tools.run(agentsview, arguments(days: days, machine: machine, cursor: cursor))
            guard output.status == 0 else {
                throw ServerError(message: "agentsview session search failed: \(output.stderr.trimmingCharacters(in: .whitespacesAndNewlines))")
            }
            let page = try parsePage(output.stdout)
            all += page.matches
            guard let next = advance(page, from: cursor) else { return all }
            cursor = next
        }
        throw ServerError(message: "More than \(maxPages * pageSize) SKILL.md reads in this window. Pick a shorter window.")
    }
}

/// A row in the Skill usage view: one canonical skill joined with both signals.
struct SkillRow: Identifiable, Equatable {
    var id: String { name }
    let name: String
    let tokens: Int
    let invoked: Int
    let loaded: Int
    let lastUsed: Date?
    /// "claude 3 · codex 6": invocations plus SKILL.md loads, per agent.
    let agents: String

    var isUnused: Bool { invoked == 0 && loaded == 0 }
    var uses: Int { invoked + loaded }
    var lastUsedSortKey: Date { lastUsed ?? .distantPast }
}

/// A name that shows up in either signal but is not a tackroom skill.
struct UntrackedSkill: Equatable {
    let name: String
    let invoked: Int
    let loaded: Int
}

enum SkillUsageMerger {
    /// AgentsView keeps namespaced names ("plugin:skill") apart; tackroom names are bare.
    static func bareName(_ name: String) -> String {
        name.split(separator: ":").last.map(String.init) ?? name
    }

    /// The page subtitle: what is counted, where and for how long.
    static func scope(days: Int, machine: String?, machines: [String]) -> String {
        let names = machine.map { [$0] } ?? machines
        var place = "all machines"
        switch names.count {
        case 0: break
        case 1: place = names[0]
        default: place = names.dropLast().joined(separator: ", ") + " and " + names[names.count - 1]
        }
        return "Invocations and SKILL.md loads from AgentsView on \(place), last \(days) days. Loads include editing the skill itself."
    }

    static func merge(skills: [(name: String, tokens: Int)], invoked: [SkillUsage], loads: [SkillLoad]) -> (rows: [SkillRow], untracked: [UntrackedSkill]) {
        struct Entry {
            var invoked = 0, loaded = 0
            var last: Date?
            var agents: [String: Int] = [:]
            mutating func see(_ date: Date?) {
                if let date, date > (last ?? .distantPast) { last = date }
            }
        }
        var entries: [String: Entry] = [:]
        for usage in invoked {
            let name = bareName(usage.skillName)
            var entry = entries[name, default: Entry()]
            entry.invoked += usage.callCount
            entry.see(usage.lastUsedAt)
            for agent in usage.agentBreakdown ?? [] { entry.agents[agent.agent, default: 0] += agent.count }
            entries[name] = entry
        }
        for load in loads {
            var entry = entries[load.skill, default: Entry()]
            entry.loaded += load.sessions
            entry.see(load.last)
            entry.agents[load.agent, default: 0] += load.sessions
            entries[load.skill] = entry
        }

        let canonical = Set(skills.map(\.name))
        let rows = skills.map { skill -> SkillRow in
            let entry = entries[skill.name] ?? Entry()
            let agents = entry.agents.sorted { ($1.value, $0.key) < ($0.value, $1.key) }.map { "\($0.key) \($0.value)" }.joined(separator: " · ")
            return SkillRow(name: skill.name, tokens: skill.tokens, invoked: entry.invoked, loaded: entry.loaded, lastUsed: entry.last, agents: agents)
        }
        let untracked = entries.filter { !canonical.contains($0.key) && ($0.value.invoked + $0.value.loaded) > 0 }
            .map { UntrackedSkill(name: $0.key, invoked: $0.value.invoked, loaded: $0.value.loaded) }
            .sorted { ($1.invoked + $1.loaded, $0.name) < ($0.invoked + $0.loaded, $1.name) }
        return (rows, untracked)
    }
}
