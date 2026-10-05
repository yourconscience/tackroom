import Foundation

/// `Tackroom --check`: runs the window's data paths without UI and prints what
/// each view would show. Read-only: it previews a sync but never applies one.
@MainActor
enum HeadlessCheck {
    static func run() async -> Int32 {
        let model = AppModel.shared
        var failures = 0
        func report(_ label: String, _ error: String?, _ detail: @autoclosure () -> String) {
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

        await model.loadSkillUsage()
        let unused = model.skillRows.filter { $0.calls == 0 }.map(\.name)
        report("skill usage", model.usageError, "\(model.skillRows.count) skills, \(unused.count) unused in \(model.usageDays) days, \(model.untrackedSkills.count) called but not in tackroom")
        if !unused.isEmpty { print("       unused: \(unused.joined(separator: ", "))") }

        await model.loadForeign()
        let byKind = Dictionary(grouping: model.foreignItems, by: \.kind).map { "\($0.value.count) \($0.key)" }.sorted()
        report("foreign items", model.foreignError, byKind.joined(separator: ", "))

        model.shutdown()
        return failures == 0 ? 0 : 1
    }
}
