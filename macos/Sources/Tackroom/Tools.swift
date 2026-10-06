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

            // Wait with a termination handler. `waitUntilExit` can miss the exit of a fast command
            // and block forever (seen with `agentsview session search`). Both pipes are drained
            // concurrently so a chatty command cannot block on a full pipe.
            let finished = DispatchGroup()
            var outData = Data()
            var errData = Data()
            finished.enter()
            process.terminationHandler = { _ in finished.leave() }
            do {
                try process.run()
            } catch {
                finished.leave()
                continuation.resume(throwing: error)
                return
            }
            finished.enter()
            DispatchQueue.global().async {
                outData = out.fileHandleForReading.readDataToEndOfFile()
                finished.leave()
            }
            finished.enter()
            DispatchQueue.global().async {
                errData = err.fileHandleForReading.readDataToEndOfFile()
                finished.leave()
            }
            finished.notify(queue: .global()) {
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

    /// A TCP port on 127.0.0.1 that nothing listens on right now.
    static func freePort() -> Int? {
        let fd = socket(AF_INET, SOCK_STREAM, 0)
        guard fd >= 0 else { return nil }
        defer { close(fd) }
        var address = sockaddr_in()
        address.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        address.sin_family = sa_family_t(AF_INET)
        address.sin_addr.s_addr = inet_addr("127.0.0.1")
        let size = socklen_t(MemoryLayout<sockaddr_in>.size)
        let bound = withUnsafePointer(to: &address) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { bind(fd, $0, size) } }
        var length = size
        let named = withUnsafeMutablePointer(to: &address) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { getsockname(fd, $0, &length) } }
        guard bound == 0, named == 0 else { return nil }
        return Int(UInt16(bigEndian: address.sin_port))
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
