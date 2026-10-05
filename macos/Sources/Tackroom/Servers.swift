import Foundation

/// A local web UI the app embeds: where it lives and the URL that signs a web view in.
struct WebEndpoint: Equatable {
    let base: URL
    let start: URL
}

struct ServerError: LocalizedError {
    let message: String
    var errorDescription: String? { message }
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
        guard (200..<300).contains(status) else {
            throw ServerError(message: Self.apiMessage(data) ?? "tackroom view returned HTTP \(status)")
        }
        return try decoder.decode(T.self, from: data)
    }

    private static func apiMessage(_ data: Data) -> String? {
        guard let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return nil }
        if let error = object["error"] as? [String: Any], let message = error["message"] as? String { return message }
        return object["message"] as? String ?? object["error"] as? String
    }

    /// Attaches to the app's `tackroom view` on 127.0.0.1:8791, or starts it.
    /// The token file keeps the token stable, so a server left running by an
    /// earlier app session is reused instead of fighting over the port.
    static func connect() async throws -> (TackroomClient, Process?) {
        guard let tackroom = Tools.find("tackroom") else {
            throw ServerError(message: "tackroom not found. Install it with `brew install yourconscience/tap/tackroom`.")
        }
        try FileManager.default.createDirectory(at: Tools.appSupport, withIntermediateDirectories: true)
        let tokenFile = Tools.appSupport.appendingPathComponent("view.token").path
        let base = URL(string: "http://127.0.0.1:\(port)")!

        if let token = Tools.readTrimmed(tokenFile) {
            let client = TackroomClient(base: base, token: token)
            if (try? await client.signIn()) != nil { return (client, nil) }
        }

        let process = try Tools.spawn(tackroom, ["view", "--no-open", "--addr", "127.0.0.1:\(port)", "--token-file", tokenFile],
                                      log: Tools.logs.appendingPathComponent("tackroom-view.log"))
        for _ in 0..<50 {
            try await Task.sleep(nanoseconds: 200_000_000)
            if !process.isRunning {
                throw ServerError(message: "tackroom view exited (status \(process.terminationStatus)); see ~/Library/Logs/Tackroom/tackroom-view.log")
            }
            if let token = Tools.readTrimmed(tokenFile) {
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

    func skillAnalytics(since: Date) async throws -> SkillAnalytics {
        var components = URLComponents(url: base.appendingPathComponent("api/v1/analytics/skills"), resolvingAgainstBaseURL: false)!
        components.queryItems = [URLQueryItem(name: "from", value: Self.day.string(from: since))]
        var request = URLRequest(url: components.url!)
        if let token { request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        let (data, response) = try await URLSession.shared.data(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard status == 200 else { throw ServerError(message: "AgentsView returned HTTP \(status) for skill analytics") }
        return try Decoders.snakeCase.decode(SkillAnalytics.self, from: data)
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
