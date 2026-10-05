import Foundation

/// Finding and running the companion CLIs (tackroom, agentsview, hk).
enum Tools {
    static let home = FileManager.default.homeDirectoryForCurrentUser.path

    // An app launched from Finder or at login gets a minimal PATH. Prepend the
    // places these CLIs live, so `tackroom view` can also reach git, go and uvx
    // when it applies a sync.
    static let searchPath: String = {
        let preferred = ["\(home)/.local/bin", "/opt/homebrew/bin", "/usr/local/bin", "\(home)/go/bin", "/usr/local/go/bin"]
        let inherited = (ProcessInfo.processInfo.environment["PATH"] ?? "").split(separator: ":").map(String.init)
        let system = ["/usr/bin", "/bin", "/usr/sbin", "/sbin"]
        var seen = Set<String>()
        return (preferred + inherited + system).filter { !$0.isEmpty && seen.insert($0).inserted }.joined(separator: ":")
    }()

    static var environment: [String: String] {
        var env = ProcessInfo.processInfo.environment
        env["PATH"] = searchPath
        return env
    }

    static func find(_ name: String) -> String? {
        for dir in searchPath.split(separator: ":") {
            let path = "\(dir)/\(name)"
            if FileManager.default.isExecutableFile(atPath: path) { return path }
        }
        return nil
    }

    struct Output {
        let status: Int32
        let stdout: Data
        let stderr: String
        var text: String { String(decoding: stdout, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines) }
    }

    /// Runs a short command to completion and captures its output.
    static func run(_ executable: String, _ arguments: [String]) async throws -> Output {
        try await withCheckedThrowingContinuation { continuation in
            let process = Process()
            process.executableURL = URL(fileURLWithPath: executable)
            process.arguments = arguments
            process.environment = environment
            process.standardInput = FileHandle.nullDevice
            let out = Pipe()
            let err = Pipe()
            process.standardOutput = out
            process.standardError = err
            do {
                try process.run()
            } catch {
                continuation.resume(throwing: error)
                return
            }
            DispatchQueue.global().async {
                // Drain stderr concurrently so a chatty command cannot block on a full pipe.
                var errData = Data()
                let group = DispatchGroup()
                group.enter()
                DispatchQueue.global().async {
                    errData = err.fileHandleForReading.readDataToEndOfFile()
                    group.leave()
                }
                let outData = out.fileHandleForReading.readDataToEndOfFile()
                group.wait()
                process.waitUntilExit()
                continuation.resume(returning: Output(status: process.terminationStatus, stdout: outData, stderr: String(decoding: errData, as: UTF8.self)))
            }
        }
    }

    /// Starts a long-running child process (a local web server) with output appended to a log file.
    static func spawn(_ executable: String, _ arguments: [String], log: URL) throws -> Process {
        try FileManager.default.createDirectory(at: log.deletingLastPathComponent(), withIntermediateDirectories: true)
        if !FileManager.default.fileExists(atPath: log.path) {
            FileManager.default.createFile(atPath: log.path, contents: nil)
        }
        let handle = try FileHandle(forWritingTo: log)
        handle.seekToEndOfFile()
        let process = Process()
        process.executableURL = URL(fileURLWithPath: executable)
        process.arguments = arguments
        process.environment = environment
        process.standardInput = FileHandle.nullDevice
        process.standardOutput = handle
        process.standardError = handle
        try process.run()
        return process
    }

    static func readTrimmed(_ path: String) -> String? {
        guard let text = try? String(contentsOfFile: path, encoding: .utf8) else { return nil }
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? nil : trimmed
    }

    /// True when anything answers HTTP at `url`, whatever the status code.
    static func answers(_ url: URL) async -> Bool {
        var request = URLRequest(url: url)
        request.timeoutInterval = 1.5
        return (try? await URLSession.shared.data(for: request)) != nil
    }

    static var appSupport: URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        return base.appendingPathComponent("Tackroom", isDirectory: true)
    }

    static var logs: URL {
        URL(fileURLWithPath: home).appendingPathComponent("Library/Logs/Tackroom", isDirectory: true)
    }
}
