import CryptoKit
import Foundation

/// `Tackroom --e2e`: drives the same model and client code the window uses against a throwaway
/// `tackroom view` that edits a copy of the canonical config. It previews a sync but never applies
/// one, and it checksums the real config before and after to prove it was not touched.
@MainActor
enum HeadlessE2E {
    private static let secret = "e2e-secret-value-7c1f"
    private static let newServer = "e2e-new"

    @MainActor
    private final class Checks {
        var failures = 0

        func check(_ label: String, _ passed: Bool, _ detail: @autoclosure () -> String = "") {
            let text = detail()
            if passed {
                print("ok   \(label)\(text.isEmpty ? "" : ": " + text)")
            } else {
                failures += 1
                print("FAIL \(label)\(text.isEmpty ? "" : ": " + text)")
            }
        }
    }

    static func run() async -> Int32 {
        setvbuf(stdout, nil, _IOLBF, 0)
        let checks = Checks()

        let realPath = realConfigPath()
        guard let before = sha256(ofFile: realPath) else {
            print("FAIL real config: cannot read \(realPath)")
            return 1
        }
        print("real config \(realPath)")
        print("       sha256 before: \(before)")

        let temp = FileManager.default.temporaryDirectory.appendingPathComponent("tackroom-e2e-\(UUID().uuidString)")
        let tempConfig = temp.appendingPathComponent("tackroom.yaml")
        let model = AppModel()
        do {
            try FileManager.default.createDirectory(at: temp, withIntermediateDirectories: true)
            try FileManager.default.copyItem(atPath: realPath, toPath: tempConfig.path)
            checks.check("temp copy", sha256(ofFile: tempConfig.path) == before, temp.path)
            await scenario(model, temp: temp, tempConfig: tempConfig, checks)
        } catch {
            checks.check("setup", false, error.localizedDescription)
        }

        // Stop the throwaway server, delete its directory, and prove the real file is untouched.
        let base = model.client?.base
        model.shutdown()
        if let base {
            var stopped = false
            for _ in 0..<20 where !stopped {
                try? await Task.sleep(nanoseconds: 150_000_000)
                stopped = !(await Tools.answers(base))
            }
            checks.check("temp server stopped", stopped, base.absoluteString)
        }
        try? FileManager.default.removeItem(at: temp)
        checks.check("temp dir removed", !FileManager.default.fileExists(atPath: temp.path))

        let after = sha256(ofFile: realPath)
        print("       sha256 after:  \(after ?? "unreadable")")
        checks.check("real config unchanged", after == before, after == before ? "checksums match" : "CHECKSUMS DIFFER")
        return checks.failures == 0 ? 0 : 1
    }

    // MARK: Scenario

    private static func scenario(_ model: AppModel, temp: URL, tempConfig: URL, _ c: Checks) async {
        guard let port = Tools.freePort() else {
            c.check("free port", false)
            return
        }
        let server = TackroomServer(port: port, tokenFile: temp.appendingPathComponent("token").path,
                                    config: tempConfig.path, log: temp.appendingPathComponent("view.log"))
        await model.connectTackroom(server)
        guard let client = model.client else {
            c.check("temp tackroom view", false, model.serverError ?? "did not start")
            return
        }
        c.check("temp tackroom view", client.base.port == port && port != TackroomClient.port, client.base.absoluteString)

        await model.loadMCP()
        guard var state = model.configState, let first = state.servers.first else {
            c.check("config state", false, model.configError ?? "no MCP servers in the config to edit")
            return
        }
        c.check("state edits the temp copy", same(state.sharedPath, tempConfig.path), state.sharedPath)
        c.check("revision is the file's sha256", state.revision == sha256(ofFile: tempConfig.path))

        func reread() async -> ConfigState? {
            await model.loadConfigState()
            return model.configState
        }
        func fileText() -> String { (try? String(contentsOf: tempConfig, encoding: .utf8)) ?? "" }
        func find(_ name: String, in state: ConfigState) -> ConfigState.Server? { state.servers.first { $0.name == name } }
        func unrelatedSame(_ a: ConfigState, _ b: ConfigState, server: String? = nil, agent: String? = nil) -> Bool {
            a.servers.filter { $0.name != server } == b.servers.filter { $0.name != server }
                && a.agents.filter { $0.name != agent } == b.agents.filter { $0.name != agent }
        }
        func edited(_ s: ConfigState.Server, enabled: Bool? = nil, agents: [String]? = nil, envKeys: [String]? = nil) -> ConfigState.Server {
            .init(name: s.name, enabled: enabled ?? s.enabled, command: s.command, args: s.args, envKeys: envKeys ?? s.envKeys, agents: agents ?? s.agents)
        }

        // 1. Toggle an MCP server's enabled flag.
        var saved = await model.setMCPEnabled(first.name, !first.enabled)
        c.check("toggle \(first.name) enabled saves", saved, model.configError ?? "")
        if let after = await reread() {
            c.check("toggle changes only that flag", find(first.name, in: after) == edited(first, enabled: !first.enabled) && unrelatedSame(state, after, server: first.name))
            let diff = model.lastSave?.diff ?? ""
            c.check("save returns a diff", diff.contains("enabled: \(first.enabled)") && diff.contains("enabled: \(!first.enabled)"), "\(diff.split(separator: "\n").count) lines")
            state = after
        }

        // 2. Change its agents, keeping the spelling the file uses for the rest.
        let current = find(first.name, in: state) ?? first
        let writtenBefore = state.writtenAgents[first.name] ?? []
        if current.agents.count >= 2, let drop = current.agents.last(where: { $0 != "claude" }) {
            saved = await model.setMCPTarget(current, agent: drop, on: false)
            c.check("remove \(drop) from \(first.name)", saved, model.configError ?? "")
            if let after = await reread() {
                let expected = current.agents.filter { $0 != drop }
                c.check("agents lose only \(drop)", find(first.name, in: after) == edited(current, agents: expected) && unrelatedSame(state, after, server: first.name))
                let writtenAfter = after.writtenAgents[first.name] ?? []
                c.check("file keeps the other spellings", writtenAfter == writtenBefore.filter { AgentNames.canonical($0) != drop },
                        writtenAfter.joined(separator: ", "))
                state = after
            }
            saved = await model.setMCPTarget(find(first.name, in: state) ?? current, agent: drop, on: true)
            c.check("add \(drop) back", saved, model.configError ?? "")
            if let after = await reread() {
                c.check("agents are whole again", Set(find(first.name, in: after)?.agents ?? []) == Set(current.agents) && unrelatedSame(state, after, server: first.name))
                state = after
            }
        } else {
            print("skip agents: \(first.name) lists fewer than two agents")
        }
        // The last agent cannot be removed: an empty list would mean "every agent".
        if let only = state.servers.first(where: { $0.agents.count == 1 }) {
            let refused = await model.setMCPTarget(only, agent: only.agents[0], on: false)
            c.check("last agent of \(only.name) cannot be removed", !refused, model.configError ?? "")
        }

        // 3. Add an env key, then replace its value. Servers with and without an env block take different paths.
        let withEnv = state.servers.first { !$0.envKeys.isEmpty }
        let withoutEnv = state.servers.first { $0.envKeys.isEmpty }
        for target in [withEnv, withoutEnv].compactMap({ $0 }) {
            let label = target.envKeys.isEmpty ? "\(target.name) (no env block)" : "\(target.name) (has env)"
            let textBefore = fileText()
            saved = await model.editMCP(target, command: target.command, args: target.args, env: ("E2E_KEY", secret))
            c.check("add env key to \(label)", saved, model.configError ?? "")
            guard let after = await reread() else { continue }
            c.check("env key added, nothing else touched", find(target.name, in: after) == edited(target, envKeys: (target.envKeys + ["E2E_KEY"]).sorted()) && unrelatedSame(state, after, server: target.name))
            let text = fileText()
            let beforeLines = textBefore.components(separatedBy: "\n")
            var rest = text.components(separatedBy: "\n")[...]
            var kept = 0
            for line in beforeLines {
                if let index = rest.firstIndex(of: line) {
                    rest = rest[(index + 1)...]
                    kept += 1
                }
            }
            c.check("file keeps every existing line, values included", kept == beforeLines.count, "\(kept) of \(beforeLines.count)")
            c.check("env value is in the file", text.contains(secret))
            c.check("save diff hides the value", !(model.lastSave?.diff.contains(secret) ?? true), "\(model.lastSave?.maskedEnvLines ?? 0) env lines masked")
            state = after

            saved = await model.editMCP(find(target.name, in: state) ?? target, command: target.command, args: target.args, env: ("E2E_KEY", secret + "-2"))
            c.check("replace env value in \(label)", saved, model.configError ?? "")
            if let replaced = await reread() {
                let text = fileText()
                c.check("value replaced, key not duplicated", text.contains(secret + "-2") && find(target.name, in: replaced)?.envKeys == find(target.name, in: state)?.envKeys)
                state = replaced
            }
        }

        // 4. Add a new server.
        let capable = Array(model.mcpCapableAgents.prefix(2))
        saved = await model.addMCP(name: newServer, command: "echo", args: ["hello", "world"], env: nil, agents: capable)
        c.check("add \(newServer)", saved, model.configError ?? "")
        if let after = await reread() {
            let expected = ConfigState.Server(name: newServer, enabled: true, command: "echo", args: ["hello", "world"], envKeys: [], agents: capable)
            c.check("new server is as typed, others untouched", find(newServer, in: after) == expected && after.servers.filter { $0.name != newServer } == state.servers && after.agents == state.agents)
            state = after
        }
        let duplicate = await model.addMCP(name: newServer, command: "echo", args: [], env: nil, agents: capable)
        c.check("duplicate name is refused", !duplicate, model.configError ?? "")
        do {
            _ = try await client.patchConfig(expectedRevision: state.revision,
                                             operations: [ConfigEdit.addMCP(name: "e2e-bad", command: "echo", args: [], env: nil, agents: ["no-such-agent"])])
            c.check("unknown agent is refused", false, "the server accepted it")
        } catch let error as ServerError {
            c.check("unknown agent is refused", error.status == 400 && error.message.contains("no-such-agent"), error.message)
        } catch {
            c.check("unknown agent is refused", false, error.localizedDescription)
        }

        // 5. Toggle an agent.
        if let agent = state.agents.last(where: \.enabled) {
            saved = await model.setAgentEnabled(agent.name, false)
            await model.loadInventory()
            if let after = await reread() {
                c.check("turn \(agent.name) off", after.agents.first { $0.name == agent.name }?.enabled == false && unrelatedSame(state, after, agent: agent.name)
                            && model.inventory?.agents.first { $0.name == agent.name }?.enabled == false, model.configError ?? "")
                state = after
            }
            _ = await model.setAgentEnabled(agent.name, true)
            if let after = await reread() {
                c.check("turn \(agent.name) back on", after.agents.first { $0.name == agent.name }?.enabled == true && unrelatedSame(state, after, agent: agent.name))
                state = after
            }
        }

        // 6. A stale revision is a readable 409. Edit behind the model's back so it holds an old revision.
        let stale = state.revision
        do {
            _ = try await client.patchConfig(expectedRevision: stale, operations: [ConfigEdit.mcpEnabled(newServer, false)])
            let refused = await model.setMCPEnabled(newServer, true)
            c.check("stale save is refused by the model", !refused)
            c.check("model explains it in plain words", model.configError?.hasPrefix("The config changed since this page loaded") == true, model.configError ?? "")
            c.check("model reloaded the new revision", model.configState?.revision != stale && model.configState?.revision == sha256(ofFile: tempConfig.path))
            do {
                _ = try await client.patchConfig(expectedRevision: stale, operations: [ConfigEdit.mcpEnabled(newServer, true)])
                c.check("server answers 409", false, "the stale save went through")
            } catch let error as ServerError {
                c.check("server answers 409 stale_revision", error.status == 409 && error.isStaleRevision && !error.message.isEmpty, error.message)
            }
        } catch {
            c.check("stale revision setup", false, error.localizedDescription)
        }

        // 7. Preview a sync against the temp config: read-only, never applied.
        await model.previewSync()
        c.check("sync preview returns a plan", model.preview != nil && model.syncError == nil,
                model.syncError ?? "digest \(model.preview?.digest.prefix(12) ?? ""), \(model.preview?.plan.summary?.count ?? 0) lines")
        c.check("plan is for the edited revision", model.preview?.revision == sha256(ofFile: tempConfig.path))
        for line in model.preview?.plan.summary ?? [] { print("       \(line.prefix(200))") }

        // 8. Import an MCP server that only an agent has, into the temp config.
        await importScenario(model, tempConfig: tempConfig, c)
    }

    private static func importScenario(_ model: AppModel, tempConfig: URL, _ c: Checks) async {
        await model.loadMCP()
        guard !model.unmanagedMCP.isEmpty else {
            print("skip import: no MCP server found only in agent configs")
            return
        }
        var attempts: [String] = []
        for row in model.unmanagedMCP {
            guard let plan = MCPImportPlan.make(name: row.name, foundIn: row.agents, configured: model.mcpCapableAgents, config: model.configState?.sharedPath) else { continue }
            // Never import against anything but the temp copy.
            guard let config = plan.config, same(config, tempConfig.path) else {
                c.check("import targets the temp copy", false, plan.config ?? "no --config")
                return
            }
            await model.importMCP(plan)
            let result = model.actionResult
            if result?.failed == false, let state = model.configState, let imported = state.servers.first(where: { $0.name == row.name }) {
                c.check("import \(row.name) into the temp config", true, "agents \(imported.agents.joined(separator: ", ")), env keys \(imported.envKeys.count)")
                c.check("import targets only the agents that have it", Set(imported.agents) == Set(plan.targets))
                return
            }
            attempts.append("\(row.name): \(result?.text.prefix(120) ?? "no output")")
        }
        c.check("import into the temp config", false, "no unmanaged server could be imported (\(attempts.joined(separator: "; ")))")
    }

    // MARK: Helpers

    private static func realConfigPath() -> String {
        if let home = ProcessInfo.processInfo.environment["TACKROOM_HOME"], !home.isEmpty {
            return NSString(string: home).expandingTildeInPath + "/tackroom.yaml"
        }
        return Tools.home + "/.agents/tackroom.yaml"
    }

    private static func sha256(ofFile path: String) -> String? {
        guard let data = FileManager.default.contents(atPath: path) else { return nil }
        return SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }

    /// Paths compare equal across /var and /private/var.
    private static func same(_ a: String, _ b: String) -> Bool {
        URL(fileURLWithPath: a).resolvingSymlinksInPath().path == URL(fileURLWithPath: b).resolvingSymlinksInPath().path
    }
}
