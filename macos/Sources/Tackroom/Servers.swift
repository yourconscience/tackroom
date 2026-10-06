import Foundation

/// A local web UI the app embeds: where it lives and the URL that signs a web view in.
struct WebEndpoint: Equatable {
    let base: URL
    let start: URL
}

struct ServerError: LocalizedError {
    let message: String
    var status: Int?
    /// The API's error code, such as "stale_revision".
    var code: String?
    var errorDescription: String? { message }

    var isStaleRevision: Bool { code == "stale_revision" }
}

/// Where a `tackroom view` server lives and how to start it.
struct TackroomServer {
    let port: Int
    let tokenFile: String
    let config: String?
    let log: URL

    /// The app's own server: stable port and token, default config.
    static var app: TackroomServer {
        TackroomServer(port: TackroomClient.port, tokenFile: Tools.appSupport.appendingPathComponent("view.token").path,
                       config: nil, log: Tools.logs.appendingPathComponent("tackroom-view.log"))
    }
}

/// Client for the app's own `tackroom view` server. It signs in with the
/// startup token like a browser would, so GETs carry the session cookie and
/// mutations also carry the CSRF header and an Origin the server accepts.
final class TackroomClient {
    static let port = 8791

    let base: URL
    let token: String
    private let session: URLSession
    private var signedIn = false

    init(base: URL, token: String) {
        self.base = base
        self.token = token
        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = 60
        session = URLSession(configuration: configuration)
    }

    var endpoint: WebEndpoint { WebEndpoint(base: base, start: startURL) }

    private var startURL: URL {
        var components = URLComponents(url: base, resolvingAgainstBaseURL: false)!
        components.path = "/"
        components.queryItems = [URLQueryItem(name: "token", value: token)]
        return components.url!
    }

    func signIn() async throws {
        let (_, response) = try await session.data(from: startURL)
        guard let http = response as? HTTPURLResponse, http.statusCode == 200 else {
            throw ServerError(message: "tackroom view rejected the startup token")
        }
        signedIn = true
    }

    func get<T: Decodable>(_ path: String, decoder: JSONDecoder) async throws -> T {
        try await send(path, method: "GET", body: nil, decoder: decoder)
    }

    func post<T: Decodable>(_ path: String, body: Data, decoder: JSONDecoder) async throws -> T {
        try await send(path, method: "POST", body: body, decoder: decoder)
    }

    /// PATCH /api/config: applies edit operations to the shared layer. The server refuses
    /// the save with 409 `stale_revision` when the file changed since `expectedRevision`.
    func patchConfig(expectedRevision: String, operations: [ConfigOperation]) async throws -> ConfigSave {
        struct Reply: Decodable {
            let revision: String
            let diff: String
        }
        let body = ConfigEdit.requestBody(expectedRevision: expectedRevision, operations: operations)
        let reply: Reply = try await send("api/config", method: "PATCH", body: body, decoder: JSONDecoder())
        // The server's diff keeps every line of the file, env values included.
        let redacted = ConfigText.redactedDiff(reply.diff)
        return ConfigSave(revision: reply.revision, diff: redacted.text, maskedEnvLines: redacted.masked)
    }

    private func send<T: Decodable>(_ path: String, method: String, body: Data?, decoder: JSONDecoder, retry: Bool = true) async throws -> T {
        if !signedIn { try await signIn() }
        var request = URLRequest(url: base.appendingPathComponent(path))
        request.httpMethod = method
        if let body {
            request.httpBody = body
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.setValue("http://\(base.host!):\(base.port!)", forHTTPHeaderField: "Origin")
            if let csrf = session.configuration.httpCookieStorage?.cookies(for: base)?.first(where: { $0.name == "tackroom_csrf" }) {
                request.setValue(csrf.value, forHTTPHeaderField: "X-Tackroom-CSRF")
            }
        }
        let (data, response) = try await session.data(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        // A restarted server keeps the token but mints a new CSRF secret: sign in again once.
        if (status == 401 || status == 403) && retry {
            signedIn = false
            return try await send(path, method: method, body: body, decoder: decoder, retry: false)
        }
        guard (200..<300).contains(status) else { throw Self.apiError(data, status: status) }
        return try decoder.decode(T.self, from: data)
    }

    private static func apiError(_ data: Data, status: Int) -> ServerError {
        let object = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
        let error = object?["error"] as? [String: Any]
        let message = error?["message"] as? String ?? object?["message"] as? String ?? object?["error"] as? String
        return ServerError(message: message ?? "tackroom view returned HTTP \(status)", status: status, code: error?["code"] as? String)
    }

    /// Attaches to a `tackroom view` on 127.0.0.1, or starts one. By default that is the
    /// app's own server on :8791. The token file keeps the token stable, so a server left
    /// running by an earlier app session is reused instead of fighting over the port.
    static func connect(_ server: TackroomServer = .app) async throws -> (TackroomClient, Process?) {
        guard let tackroom = Tools.find("tackroom") else {
            throw ServerError(message: "tackroom not found. Install it with `brew install yourconscience/tap/tackroom`.")
        }
        try FileManager.default.createDirectory(at: URL(fileURLWithPath: server.tokenFile).deletingLastPathComponent(), withIntermediateDirectories: true)
        let base = URL(string: "http://127.0.0.1:\(server.port)")!

        if let token = Tools.readTrimmed(server.tokenFile) {
            let client = TackroomClient(base: base, token: token)
            if (try? await client.signIn()) != nil { return (client, nil) }
        }

        var arguments = ["view", "--no-open", "--addr", "127.0.0.1:\(server.port)", "--token-file", server.tokenFile]
        if let config = server.config { arguments += ["--config", config] }
        let process = try Tools.spawn(tackroom, arguments, log: server.log)
        for _ in 0..<50 {
            try await Task.sleep(nanoseconds: 200_000_000)
            if !process.isRunning {
                throw ServerError(message: "tackroom view exited (status \(process.terminationStatus)); see \(NSString(string: server.log.path).abbreviatingWithTildeInPath)")
            }
            if let token = Tools.readTrimmed(server.tokenFile) {
                let client = TackroomClient(base: base, token: token)
                if (try? await client.signIn()) != nil { return (client, process) }
            }
        }
        process.terminate()
        throw ServerError(message: "tackroom view did not start within 10 s")
    }
}

/// The running AgentsView daemon, found through the file it writes on start.
struct AgentsViewDaemon {
    let base: URL
    let token: String?

    static func discover() -> AgentsViewDaemon? {
        let dir = URL(fileURLWithPath: Tools.home).appendingPathComponent(".agentsview")
        guard let names = try? FileManager.default.contentsOfDirectory(atPath: dir.path) else { return nil }
        for name in names where name.hasPrefix("daemon.") && name.hasSuffix(".json") {
            guard let data = try? Data(contentsOf: dir.appendingPathComponent(name)),
                  let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                  let pid = object["pid"] as? Int, kill(pid_t(pid), 0) == 0,
                  let address = object["address"] as? String,
                  let base = URL(string: "http://\(address)") else { continue }
            return AgentsViewDaemon(base: base, token: authToken(configDir: dir))
        }
        return nil
    }

    // Same sources the AgentsView web UI tells people to copy the token from.
    private static func authToken(configDir: URL) -> String? {
        if let env = ProcessInfo.processInfo.environment["AGENTSVIEW_AUTH_TOKEN"], !env.isEmpty { return env }
        guard let text = try? String(contentsOf: configDir.appendingPathComponent("config.toml"), encoding: .utf8) else { return nil }
        for line in text.split(separator: "\n") {
            let parts = line.split(separator: "=", maxSplits: 1).map { $0.trimmingCharacters(in: .whitespaces) }
            if parts.count == 2, parts[0] == "auth_token" {
                return parts[1].trimmingCharacters(in: CharacterSet(charactersIn: "\""))
            }
        }
        return nil
    }

    var endpoint: WebEndpoint { WebEndpoint(base: base, start: base) }

    /// Document-start script that hands the web UI its token, so it skips the paste prompt.
    var webSignInScript: String? {
        let origin = "\(base.scheme!)://\(base.host!)\(base.port.map { ":\($0)" } ?? "")"
        guard let token, let data = try? JSONEncoder().encode([origin, token]), let values = String(data: data, encoding: .utf8) else { return nil }
        return """
        (function (v) {
          if (location.origin !== v[0]) return;
          try { localStorage.setItem("agentsview-auth-token", v[1]); } catch (e) {}
        })(\(values));
        """
    }

    /// Explicit skill calls only; skills that agents load by reading SKILL.md are not counted here.
    func skillAnalytics(since: Date, machine: String?) async throws -> SkillAnalytics {
        var query = [URLQueryItem(name: "from", value: Self.day.string(from: since))]
        if let machine { query.append(URLQueryItem(name: "machine", value: machine)) }
        let data = try await get("api/v1/analytics/skills", query: query)
        return try Decoders.snakeCase.decode(SkillAnalytics.self, from: data)
    }

    /// The machines AgentsView merges sessions from, such as ["m1.local", "m4"].
    func machines() async throws -> [String] {
        struct Reply: Decodable { let machines: [String]? }
        let data = try await get("api/v1/machines", query: [])
        return try JSONDecoder().decode(Reply.self, from: data).machines ?? []
    }

    private func get(_ path: String, query: [URLQueryItem]) async throws -> Data {
        var components = URLComponents(url: base.appendingPathComponent(path), resolvingAgainstBaseURL: false)!
        components.queryItems = query.isEmpty ? nil : query
        var request = URLRequest(url: components.url!)
        if let token { request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        let (data, response) = try await URLSession.shared.data(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard status == 200 else { throw ServerError(message: "AgentsView returned HTTP \(status) for /\(path)", status: status) }
        return data
    }

    private static let day: DateFormatter = {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter
    }()
}

/// HarnessKit's web UI, attached or started on its default port.
enum HarnessKit {
    static let port = 7070

    static var installed: Bool { Tools.find("hk") != nil }

    static func connect() async throws -> (WebEndpoint, Process?) {
        guard let hk = Tools.find("hk") else { throw ServerError(message: "hk not found") }
        let base = URL(string: "http://127.0.0.1:\(port)")!
        var process: Process?
        if !(await Tools.answers(base)) {
            process = try Tools.spawn(hk, ["serve", "--port", "\(port)"], log: Tools.logs.appendingPathComponent("hk-serve.log"))
            var up = false
            for _ in 0..<50 {
                try await Task.sleep(nanoseconds: 200_000_000)
                if await Tools.answers(base) { up = true; break }
                if process?.isRunning == false { break }
            }
            guard up else {
                process?.terminate()
                throw ServerError(message: "hk serve did not start; see ~/Library/Logs/Tackroom/hk-serve.log")
            }
        }
        // hk keeps a persistent token here unless it runs with --no-token.
        var start = base
        if let token = Tools.readTrimmed("\(Tools.home)/.harnesskit/web-token") {
            var components = URLComponents(url: base, resolvingAgainstBaseURL: false)!
            components.path = "/"
            components.queryItems = [URLQueryItem(name: "token", value: token)]
            start = components.url!
        }
        return (WebEndpoint(base: base, start: start), process)
    }

    static func list() async throws -> HKList {
        guard let hk = Tools.find("hk") else { throw ServerError(message: "hk not found") }
        let output = try await Tools.run(hk, ["list", "--json"])
        guard output.status == 0 else { throw ServerError(message: "hk list failed: \(output.stderr)") }
        return try JSONDecoder().decode(HKList.self, from: output.stdout)
    }
}
