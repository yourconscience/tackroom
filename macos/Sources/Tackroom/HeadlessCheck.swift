import Foundation

/// `Tackroom --check`: runs the window's data paths without UI and prints what
/// each view would show. Read-only: it previews a sync but never applies one.
typealias Report = (_ label: String, _ error: String?, _ detail: @autoclosure () -> String) -> Void

@MainActor
enum HeadlessCheck {
    static func run() async -> Int32 {
        let model = AppModel.shared
        var failures = 0
        let report: Report = { label, error, detail in
            if let error {
                failures += 1
                print("FAIL \(label): \(error)")
            } else {
                print("ok   \(label): \(detail())")
            }
        }

        await model.connectTackroom()
        await model.connectHarnessKit()
        report("tackroom view", model.serverError, model.client?.base.absoluteString ?? "")
        report("AgentsView", model.agentsView == nil ? "daemon not found" : nil, model.agentsView?.base.absoluteString ?? "")
        if model.hkInstalled {
            report("HarnessKit", model.hkError, model.hkEndpoint?.base.absoluteString ?? "")
        }

        await model.refresh()
        report("status", model.statusError ?? (model.status == nil ? "no status" : nil),
               "\(model.status?.reports.count ?? 0) agents, \(model.needsSyncCount) need sync")
        for agent in model.status?.reports ?? [] {
            print("       \(agent.name): \(agent.state) \(agent.issueSummary)")
        }
        print("       cost: \(model.todayCost ?? "unavailable")")

        await model.previewSync()
        report("sync preview", model.syncError, "digest \(model.preview?.digest.prefix(12) ?? ""), \(model.preview?.plan.destructive?.count ?? 0) destructive")
        for line in model.preview?.plan.summary ?? [] { print("       \(line)") }

        await checkSkills(model, report: report)
        await checkMCP(model, report: report)

        await model.loadForeign()
        let byKind = Dictionary(grouping: model.foreignItems, by: \.kind).map { "\($0.value.count) \($0.key)" }.sorted()
        report("foreign items", model.foreignError, byKind.joined(separator: ", "))

        model.shutdown()
        return failures == 0 ? 0 : 1
    }

    /// Skills that had SKILL.md reads but no explicit invocations when the usage view was built.
    /// If the view lists one as unused while the search shows reads for it, the merge is wrong.
    private static let loadedOnly = ["ai-engineering-radar", "decide", "redesign-skill", "tern"]

    private static func checkSkills(_ model: AppModel, report: Report) async {
        model.usageDays = 30
        model.usageMachine = nil
        await model.loadSkillUsage()
        let rows = model.skillRows
        let unused = rows.filter(\.isUnused).map(\.name)
        report("skill usage", model.usageError,
               "\(rows.count) skills, \(unused.count) unused in \(model.usageDays) days (\(model.usageScope)), \(model.untrackedSkills.count) called but not in tackroom")
        print("       machines: \(model.machines.isEmpty ? "unknown" : model.machines.joined(separator: ", "))")
        print("       unused (no invocations, no SKILL.md loads): \(unused.isEmpty ? "none" : unused.joined(separator: ", "))")
        if let row = rows.first(where: { $0.name == "tech-search" }) {
            let last = row.lastUsed.map { $0.formatted(date: .abbreviated, time: .shortened) } ?? "never"
            print("       tech-search: invoked \(row.invoked), loaded \(row.loaded), last used \(last), by agent \(row.agents)")
        }

        // Compare against the raw search, run on its own.
        do {
            let loads = SkillSearch.loads(from: try await SkillSearch.fetch(days: model.usageDays, machine: nil))
            for name in loadedOnly {
                let reads = loads.filter { $0.skill == name }
                let detail = reads.isEmpty ? "no SKILL.md reads in the window" : "SKILL.md reads in " + reads.map { "\($0.agent) \($0.sessions)" }.joined(separator: ", ")
                if unused.contains(name), !reads.isEmpty {
                    report("\(name) is not unused", "reported unused, but \(detail)", "")
                } else if let row = rows.first(where: { $0.name == name }) {
                    report("\(name) is not unused", nil, "invoked \(row.invoked), loaded \(row.loaded); \(detail)")
                } else {
                    print("       \(name): not a tackroom skill right now, skipped")
                }
            }
        } catch {
            report("SKILL.md search", error.localizedDescription, "")
        }

        // The Skills view: inventory plus per-agent states.
        let skills = model.inventory?.skills ?? []
        let agents = model.inventory?.agents ?? []
        report("skills inventory", model.inventoryError ?? (skills.isEmpty ? "no skills" : nil),
               "\(skills.count) skills (\(skills.filter(\.isExternal).count) external), agents: " + agents.map { "\($0.name)\($0.enabled ? "" : " (off)")" }.joined(separator: ", "))
    }

    private static func checkMCP(_ model: AppModel, report: Report) async {
        await model.loadMCP()
        guard let state = model.configState else {
            report("config state", model.configError ?? "no state", "")
            return
        }
        report("config state", model.configError, "revision \(state.revision.prefix(12)), shared file \(NSString(string: state.sharedPath).abbreviatingWithTildeInPath)")
        let capable = Set(model.mcpCapableAgents.map(AgentNames.canonical))
        for server in state.servers {
            let written = state.writtenAgents[server.name]
            // The typed list drops agents without MCP support, so compare on the capable ones.
            let spelled = written.map { Set($0.map(AgentNames.canonical).filter(capable.contains)) }
            let mismatch = spelled.flatMap { $0 == Set(server.agents.map(AgentNames.canonical)) ? nil : "file lists \($0.sorted()), config says \(server.agents.sorted())" }
            report("mcp \(server.name)", mismatch,
                   "\(server.enabled ? "enabled" : "disabled"), \(([server.command] + server.args).joined(separator: " ")), env keys \(server.envKeys.count), agents as written: \(written?.joined(separator: ", ") ?? "all")")
        }
        report("mcp not managed", model.unmanagedMCPError, model.unmanagedMCP.isEmpty ? "none" : model.unmanagedMCP.map(\.name).joined(separator: ", "))
        for row in model.unmanagedMCP {
            let plan = MCPImportPlan.make(name: row.name, foundIn: row.agents, configured: model.mcpCapableAgents, config: state.sharedPath)
            print("       \(row.name): \(plan.map { "import with `\($0.command)` (not run)" } ?? "no agent can import it")")
        }
    }
}
