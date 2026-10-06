import Foundation

/// What the Test button found out about an MCP server.
enum MCPProbeOutcome: Equatable {
    case ok(server: String, version: String, tools: Int)
    case failed(String)

    var text: String {
        switch self {
        case .ok(let server, let version, let tools):
            let name = version.isEmpty ? server : "\(server) \(version)"
            return "\(name): \(tools) tool\(tools == 1 ? "" : "s")"
        case .failed(let reason):
            return reason
        }
    }
}

/// Starts an MCP server over stdio, asks it who it is and which tools it has, then stops it.
/// Messages are one JSON object per line, as the MCP stdio transport defines.
/// The config's env values are masked, so the server runs with the app's own environment only.
enum MCPProbe {
    static let timeout: TimeInterval = 10

    private static let initialize = #"{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"tackroom","version":"1"}}}"#
    private static let initialized = #"{"jsonrpc":"2.0","method":"notifications/initialized"}"#

    static func run(command: String, args: [String], timeout: TimeInterval = MCPProbe.timeout) async -> MCPProbeOutcome {
        await withCheckedContinuation { continuation in
            DispatchQueue.global().async {
                continuation.resume(returning: runBlocking(command: command, args: args, timeout: timeout))
            }
        }
    }

    /// The command as the shell would find it: a path, or a name on the app's search path.
    static func resolve(_ command: String) -> String? {
        let expanded = NSString(string: command).expandingTildeInPath
        if expanded.contains("/") { return FileManager.default.isExecutableFile(atPath: expanded) ? expanded : nil }
        return Tools.find(expanded)
    }

    static func toolsRequest(id: Int, cursor: String?) -> String {
        let params = cursor.map { ",\"params\":{\"cursor\":\(JSONValue.quote($0))}" } ?? ""
        return "{\"jsonrpc\":\"2.0\",\"id\":\(id),\"method\":\"tools/list\"\(params)}"
    }

    // MARK: Blocking part, on its own thread

    private final class Flag: @unchecked Sendable {
        private let lock = NSLock()
        private var value = false
        func set() { lock.lock(); value = true; lock.unlock() }
        var isSet: Bool { lock.lock(); defer { lock.unlock() }; return value }
    }

    private static func runBlocking(command: String, args: [String], timeout: TimeInterval) -> MCPProbeOutcome {
        guard let executable = resolve(command) else { return .failed("\(command) was not found.") }
        signal(SIGPIPE, SIG_IGN) // a server that exits early must not take the app down when we write to it

        let process = Process()
        process.executableURL = URL(fileURLWithPath: executable)
        process.arguments = args
        process.environment = Tools.environment
        let input = Pipe(), output = Pipe(), errors = Pipe()
        process.standardInput = input
        process.standardOutput = output
        process.standardError = errors
        do { try process.run() } catch { return .failed("Could not start \(command): \(error.localizedDescription)") }

        let timedOut = Flag()
        DispatchQueue.global().asyncAfter(deadline: .now() + timeout) {
            if process.isRunning {
                timedOut.set()
                process.terminate()
            }
        }
        var errorTail = Data()
        let errorsDone = DispatchGroup()
        errorsDone.enter()
        DispatchQueue.global().async {
            errorTail = errors.fileHandleForReading.readDataToEndOfFile().suffix(2_000)
            errorsDone.leave()
        }
        defer {
            try? input.fileHandleForWriting.close()
            if process.isRunning { process.terminate() }
        }

        func send(_ line: String) -> Bool {
            (try? input.fileHandleForWriting.write(contentsOf: Data((line + "\n").utf8))) != nil
        }
        var buffer = Data()
        /// The next JSON message with this id, skipping notifications and anything that is not JSON.
        func reply(id: Int) -> [String: Any]? {
            while true {
                while let newline = buffer.firstIndex(of: 10) {
                    let line = buffer[buffer.startIndex..<newline]
                    buffer = Data(buffer[buffer.index(after: newline)...])
                    if let object = (try? JSONSerialization.jsonObject(with: line)) as? [String: Any], object["id"] as? Int == id { return object }
                }
                let chunk = output.fileHandleForReading.availableData
                if chunk.isEmpty { return nil } // the server closed its output
                buffer.append(chunk)
            }
        }
        func failure() -> MCPProbeOutcome {
            if timedOut.isSet { return .failed("No answer within \(Int(timeout)) s.") }
            _ = errorsDone.wait(timeout: .now() + 1)
            let note = String(decoding: errorTail, as: UTF8.self).split(whereSeparator: \.isNewline).last.map { ": " + $0.prefix(200) } ?? ""
            return .failed("The server stopped before it answered\(note)")
        }

        guard send(initialize), let hello = reply(id: 1) else { return failure() }
        if let error = hello["error"] as? [String: Any] { return .failed("initialize failed: \(error["message"] as? String ?? "unknown error")") }
        let info = (hello["result"] as? [String: Any])?["serverInfo"] as? [String: Any]
        guard send(initialized) else { return failure() }

        var tools = 0
        var cursor: String?
        for page in 0..<20 {
            guard send(toolsRequest(id: 2 + page, cursor: cursor)), let listing = reply(id: 2 + page) else { return failure() }
            if let error = listing["error"] as? [String: Any] { return .failed("tools/list failed: \(error["message"] as? String ?? "unknown error")") }
            let result = listing["result"] as? [String: Any]
            tools += (result?["tools"] as? [Any])?.count ?? 0
            cursor = result?["nextCursor"] as? String
            if cursor == nil { break }
        }
        return .ok(server: info?["name"] as? String ?? "unnamed server", version: info?["version"] as? String ?? "", tools: tools)
    }
}
