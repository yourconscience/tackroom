import Foundation

/// tackroom view: GET /api/state for the shared layer.
///
/// The server's `raw_yaml` carries env values in clear, so it is read once for the one thing
/// the typed config lacks (how each entry spells its agent names) and then dropped.
struct ConfigState: Decodable {
    struct Server: Identifiable, Equatable {
        var id: String { name }
        let name: String
        let enabled: Bool
        let command: String
        let args: [String]
        /// Env values come masked ("***"), so only the keys are kept.
        let envKeys: [String]
        let agents: [String]
    }

    struct Agent: Equatable {
        let name: String
        let enabled: Bool
    }

    let revision: String
    let sharedPath: String
    let servers: [Server]
    let agents: [Agent]
    /// Per MCP server, the agent names as the YAML spells them ("claude-code" for typed "claude").
    let writtenAgents: [String: [String]]

    // The Go config structs have no json tags, so typed_config keys are the Go field names.
    private struct Raw: Decodable {
        struct Paths: Decodable { let shared: String }
        struct Typed: Decodable {
            struct Server: Decodable {
                let Name: String
                let Enabled: Bool
                let Command: String
                let Args: [String]?
                let Env: [String: String]?
                let Agents: [String]?
            }
            struct Agent: Decodable {
                let Name: String
                let Enabled: Bool
            }
            let MCPServers: [Server]?
            let Agents: [Agent]?
        }
        let paths: Paths
        let typed_config: Typed
        let revision: String
        let raw_yaml: String
    }

    init(from decoder: Decoder) throws {
        let raw = try Raw(from: decoder)
        revision = raw.revision
        sharedPath = raw.paths.shared
        servers = (raw.typed_config.MCPServers ?? []).map {
            Server(name: $0.Name, enabled: $0.Enabled, command: $0.Command, args: $0.Args ?? [],
                   envKeys: ($0.Env ?? [:]).keys.sorted(), agents: $0.Agents ?? [])
        }
        agents = (raw.typed_config.Agents ?? []).map { Agent(name: $0.Name, enabled: $0.Enabled) }
        writtenAgents = ConfigText.mcpAgentSpellings(inYAML: raw.raw_yaml)
    }
}

/// Reading config text without a YAML library: just the two things the app needs.
enum ConfigText {
    static let mask = "***"

    // MARK: Agent spellings

    /// For each entry under `mcp_servers:` that has an `agents:` list, the names exactly as written.
    /// Handles block lists (dashes indented or not) and one-line flow lists; entries without an
    /// `agents` key are left out.
    static func mcpAgentSpellings(inYAML yaml: String) -> [String: [String]] {
        let lines = yaml.components(separatedBy: "\n").map { $0.hasSuffix("\r") ? String($0.dropLast()) : $0 }
        guard let start = lines.firstIndex(where: { isKey($0, "mcp_servers", indent: 0) && value(of: $0).isEmpty }) else { return [:] }
        var end = lines.count
        for index in lines.indices.dropFirst(start + 1) where isTopLevelKey(lines[index]) {
            end = index
            break
        }

        // Entry boundaries: lines holding a dash at the first dash's indent.
        let body = Array(start + 1 ..< end)
        guard let entryIndent = body.map({ lines[$0] }).first(where: { dashContent($0) != nil }).map(indent) else { return [:] }
        var starts = body.filter { indent(lines[$0]) == entryIndent && dashContent(lines[$0]) != nil }
        starts.append(end)

        var result: [String: [String]] = [:]
        for (first, next) in zip(starts, starts.dropFirst()) {
            // Treat the dash as an extra indent so every key of the entry lines up.
            var entry = Array(lines[first ..< next])
            let content = dashContent(entry[0]) ?? ""
            let gap = entry[0].dropFirst(entryIndent + 1).prefix(while: { $0 == " " }).count
            let keyIndent = entryIndent + 1 + gap
            entry[0] = String(repeating: " ", count: keyIndent) + content

            var name: String?
            var agents: [String]?
            var index = 0
            while index < entry.count {
                let line = entry[index]
                defer { index += 1 }
                guard indent(line) == keyIndent else { continue }
                if isKey(line, "name", indent: keyIndent) {
                    name = unquote(value(of: line))
                } else if isKey(line, "agents", indent: keyIndent) {
                    let inline = value(of: line)
                    if inline.hasPrefix("[") {
                        agents = flowItems(inline)
                    } else if inline.isEmpty {
                        var items: [String] = []
                        var next = index + 1
                        while next < entry.count {
                            let candidate = entry[next]
                            let trimmed = candidate.trimmingCharacters(in: .whitespaces)
                            if trimmed.isEmpty || trimmed.hasPrefix("#") { next += 1; continue }
                            guard indent(candidate) >= keyIndent, trimmed.hasPrefix("-"), let item = dashContent(candidate) else { break }
                            items.append(unquote(stripComment(item)))
                            next += 1
                        }
                        agents = items
                    }
                }
            }
            if let name, let agents { result[name] = agents }
        }
        return result
    }

    private static func indent(_ line: String) -> Int { line.prefix(while: { $0 == " " }).count }

    /// The text after a leading "- ", or nil when the line is not a sequence item.
    private static func dashContent(_ line: String) -> String? {
        let trimmed = line.drop(while: { $0 == " " })
        if trimmed == "-" { return "" }
        guard trimmed.hasPrefix("- ") else { return nil }
        return String(trimmed.dropFirst(2)).trimmingCharacters(in: .whitespaces)
    }

    private static func isKey(_ line: String, _ key: String, indent: Int) -> Bool {
        Self.indent(line) == indent && line.dropFirst(indent).hasPrefix(key + ":")
    }

    private static func isTopLevelKey(_ line: String) -> Bool {
        guard let first = line.first else { return false }
        return first != " " && first != "#" && first != "-"
    }

    /// What follows the first colon, without a trailing comment.
    private static func value(of line: String) -> String {
        guard let colon = line.firstIndex(of: ":") else { return "" }
        return stripComment(String(line[line.index(after: colon)...]))
    }

    private static func stripComment(_ text: String) -> String {
        let trimmed = text.trimmingCharacters(in: .whitespaces)
        if let quote = trimmed.first, quote == "\"" || quote == "'", let close = trimmed.dropFirst().firstIndex(of: quote) {
            return String(trimmed[...close])
        }
        if trimmed.hasPrefix("#") { return "" }
        if let hash = trimmed.range(of: " #") { return String(trimmed[..<hash.lowerBound]).trimmingCharacters(in: .whitespaces) }
        return trimmed
    }

    private static func unquote(_ text: String) -> String {
        let trimmed = text.trimmingCharacters(in: .whitespaces)
        guard trimmed.count >= 2, let quote = trimmed.first, quote == "\"" || quote == "'", trimmed.last == quote else { return trimmed }
        return String(trimmed.dropFirst().dropLast())
    }

    private static func flowItems(_ text: String) -> [String] {
        guard let close = text.firstIndex(of: "]") else { return [] }
        return text[text.index(after: text.startIndex)..<close].split(separator: ",").map { unquote(String($0)) }.filter { !$0.isEmpty }
    }

    // MARK: Diff redaction

    private static let flowEnv = try! NSRegularExpression(pattern: #"env:\s*\{[^}]*(\}|$)"#)

    /// The server's save diff holds the whole file, env values included. This masks every env
    /// value, drops the unchanged lines except two around each change, and counts the lines it masked.
    static func redactedDiff(_ diff: String, context: Int = 2) -> (text: String, masked: Int) {
        var lines = diff.components(separatedBy: "\n")
        if lines.last == "" { lines.removeLast() }
        var header: [String] = []
        while let first = lines.first, first.hasPrefix("--- ") || first.hasPrefix("+++ ") {
            header.append(first)
            lines.removeFirst()
        }

        var body: [(marker: Character, text: String)] = []
        var masked = 0
        var envIndent: Int?
        for line in lines {
            let marker = line.first ?? " "
            var text = line.isEmpty ? "" : String(line.dropFirst())
            let trimmed = text.trimmingCharacters(in: .whitespaces)
            let depth = indent(text)
            if let outer = envIndent, !trimmed.isEmpty {
                if depth > outer {
                    text = maskEntry(text, depth: depth)
                    masked += 1
                } else {
                    envIndent = nil
                }
            }
            if envIndent == nil, trimmed == "env:" || trimmed.hasPrefix("env: #") { envIndent = depth }
            let range = NSRange(text.startIndex..., in: text)
            if let hit = flowEnv.firstMatch(in: text, range: range), !isEmptyFlowEnv(text, hit) {
                text = flowEnv.stringByReplacingMatches(in: text, range: range, withTemplate: "env: {\(mask)}")
                masked += 1
            }
            body.append((marker, text))
        }

        let changed = body.indices.filter { body[$0].marker == "+" || body[$0].marker == "-" }
        if changed.isEmpty { return ("", masked) }
        var keep = Set<Int>()
        for index in changed { for near in max(0, index - context)...min(body.count - 1, index + context) { keep.insert(near) } }
        var out = header
        var gap = false
        for index in body.indices {
            if keep.contains(index) {
                out.append(String(body[index].marker) + body[index].text)
                gap = false
            } else if !gap {
                out.append(" ...")
                gap = true
            }
        }
        return (out.joined(separator: "\n"), masked)
    }

    private static func maskEntry(_ text: String, depth: Int) -> String {
        let pad = String(repeating: " ", count: depth)
        guard let colon = text.firstIndex(of: ":") else { return pad + mask }
        return pad + text[text.index(text.startIndex, offsetBy: depth)..<colon].trimmingCharacters(in: .whitespaces) + ": " + mask
    }

    private static func isEmptyFlowEnv(_ text: String, _ match: NSTextCheckingResult) -> Bool {
        guard let range = Range(match.range, in: text) else { return false }
        let inner = text[range].drop(while: { $0 != "{" }).dropFirst().prefix(while: { $0 != "}" })
        return inner.trimmingCharacters(in: .whitespaces).isEmpty
    }
}
