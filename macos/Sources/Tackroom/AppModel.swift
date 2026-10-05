import AppKit
import Observation
import ServiceManagement
import UserNotifications
import WebKit

enum Pane: String, Hashable, Identifiable {
    case overview, sync, skills, foreign, config, sessions, inspector

    var id: String { rawValue }

    var title: String {
        switch self {
        case .overview: "Overview"
        case .sync: "Sync"
        case .skills: "Skill usage"
        case .foreign: "Foreign items"
        case .config: "Config"
        case .sessions: "Sessions"
        case .inspector: "HarnessKit"
        }
    }

    var symbol: String {
        switch self {
        case .overview: "gauge.with.dots.needle.33percent"
        case .sync: "arrow.triangle.2.circlepath"
        case .skills: "chart.bar.xaxis"
        case .foreign: "questionmark.folder"
        case .config: "slider.horizontal.3"
        case .sessions: "text.bubble"
        case .inspector: "wrench.and.screwdriver"
        }
    }
}

@MainActor
@Observable
final class AppModel {
    static let shared = AppModel()

    var selection: Pane? = .overview

    // tackroom view, started or attached by the app.
    private(set) var client: TackroomClient?
    var serverError: String?
    var status: StatusResponse?
    var statusError: String?
    var lastRefresh: Date?
    var inventory: Inventory?
    var inventoryError: String?

    // AgentsView daemon.
    private(set) var agentsView: AgentsViewDaemon?
    var todayCost: String?
    var usageDays = 30
    var skillRows: [SkillRow] = []
    var untrackedSkills: [(name: String, calls: Int)] = []
    var usageError: String?
    var usageLoading = false

    // HarnessKit (optional).
    let hkInstalled = HarnessKit.installed
    var hkEndpoint: WebEndpoint?
    var hkError: String?
    var foreignItems: [ForeignItem] = []
    var foreignError: String?
    var foreignLoading = false

    // Sync preview and apply.
    var preview: SyncPreview?
    var confirmed: Set<String> = []
    var syncError: String?
    var syncBusy = false
    var lastApplied: Date?

    var launchAtLogin = false
    var loginError: String?

    @ObservationIgnored private var children: [Process] = []
    @ObservationIgnored private var webViews: [Pane: WKWebView] = [:]
    @ObservationIgnored private var refreshTask: Task<Void, Never>?
    @ObservationIgnored private var lastSynced: [String: Bool] = [:]

    /// Notifications and login items need a real app bundle; `swift run` has none.
    var isBundled: Bool { Bundle.main.bundleIdentifier != nil }

    var needsSyncCount: Int {
        status?.reports.filter { $0.state == .drifted || $0.state == .unreadable }.count ?? 0
    }

    func start() async {
        if isBundled {
            launchAtLogin = SMAppService.mainApp.status == .enabled
            // Not awaited inline: the prompt waits for the user and must not hold up startup.
            Task { _ = try? await UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound]) }
        }
        await connectTackroom()
        refreshTask = Task { [weak self] in
            while !Task.isCancelled {
                await self?.refresh()
                try? await Task.sleep(nanoseconds: 60_000_000_000)
            }
        }
        await connectHarnessKit()
    }

    /// Finds AgentsView and finds or starts `tackroom view`. `--check` calls this without the UI.
    func connectTackroom() async {
        agentsView = AgentsViewDaemon.discover()
        do {
            let (client, process) = try await TackroomClient.connect()
            self.client = client
            if let process { children.append(process) }
        } catch {
            serverError = error.localizedDescription
        }
    }

    func connectHarnessKit() async {
        guard hkInstalled else { return }
        do {
            let (endpoint, process) = try await HarnessKit.connect()
            hkEndpoint = endpoint
            if let process { children.append(process) }
        } catch {
            hkError = error.localizedDescription
        }
    }

    func shutdown() {
        refreshTask?.cancel()
        for child in children where child.isRunning { child.terminate() }
    }

    func refresh() async {
        async let cost: Void = loadCost()
        await loadStatus()
        await cost
    }

    private func loadStatus() async {
        guard let client else { return }
        do {
            let next: StatusResponse = try await client.get("api/status", decoder: Decoders.tackroomStatus)
            notifyNewDrift(next)
            status = next
            statusError = nil
            lastRefresh = Date()
        } catch {
            statusError = error.localizedDescription
        }
    }

    private func loadCost() async {
        guard let agentsview = Tools.find("agentsview"),
              let output = try? await Tools.run(agentsview, ["usage", "statusline"]), output.status == 0 else {
            todayCost = nil
            return
        }
        todayCost = output.text
    }

    // MARK: Drift notifications

    /// Posts one notification per agent that was synced at the last poll and is not now.
    private func notifyNewDrift(_ next: StatusResponse) {
        defer { lastSynced = Dictionary(next.reports.map { ($0.name, $0.state == .synced) }, uniquingKeysWith: { a, _ in a }) }
        guard isBundled, !lastSynced.isEmpty else { return }
        for report in next.reports where lastSynced[report.name] == true && (report.state == .drifted || report.state == .unreadable) {
            let content = UNMutableNotificationContent()
            content.title = "\(report.name) drifted from tackroom"
            content.body = report.issueSummary
            let request = UNNotificationRequest(identifier: "drift-\(report.name)-\(Int(Date().timeIntervalSince1970))", content: content, trigger: nil)
            UNUserNotificationCenter.current().add(request)
        }
    }

    // MARK: Launch at login

    func setLaunchAtLogin(_ enabled: Bool) {
        guard isBundled else { return }
        do {
            if enabled { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
            loginError = nil
        } catch {
            loginError = error.localizedDescription
        }
        launchAtLogin = SMAppService.mainApp.status == .enabled
    }

    // MARK: Sync

    func previewSync() async {
        guard let client else { return }
        syncBusy = true
        defer { syncBusy = false }
        do {
            preview = try await client.post("api/sync/preview", body: Data("{}".utf8), decoder: JSONDecoder())
            confirmed = []
            syncError = nil
        } catch {
            preview = nil
            syncError = error.localizedDescription
        }
    }

    var allDestructiveConfirmed: Bool {
        Set(preview?.plan.destructive ?? []).isSubset(of: confirmed)
    }

    func applySync() async {
        guard let client, let preview, allDestructiveConfirmed else { return }
        struct Applied: Decodable { let applied: Bool }
        syncBusy = true
        defer { syncBusy = false }
        do {
            let request = SyncApplyRequest(expected_revision: preview.revision, plan_digest: preview.digest,
                                           confirmed_destructive: preview.plan.destructive ?? [])
            let _: Applied = try await client.post("api/sync/apply", body: try JSONEncoder().encode(request), decoder: JSONDecoder())
            self.preview = nil
            confirmed = []
            syncError = nil
            lastApplied = Date()
            await loadStatus()
        } catch {
            syncError = error.localizedDescription
        }
    }

    func discardPreview() {
        preview = nil
        confirmed = []
    }

    // MARK: Inventory, skill usage, foreign items

    func loadInventory() async {
        guard let client else { return }
        do {
            inventory = try await client.get("api/inventory", decoder: JSONDecoder())
            inventoryError = nil
        } catch {
            inventoryError = error.localizedDescription
        }
    }

    func loadSkillUsage() async {
        guard let agentsView else {
            usageError = "AgentsView is not running. Start it with `agentsview daemon start`."
            return
        }
        usageLoading = true
        defer { usageLoading = false }
        await loadInventory()
        guard let inventory else {
            usageError = inventoryError
            return
        }
        do {
            let since = Calendar.current.date(byAdding: .day, value: -usageDays, to: Date()) ?? Date()
            let analytics = try await agentsView.skillAnalytics(since: since)

            // AgentsView keeps namespaced names ("plugin:skill") apart; tackroom names are bare.
            struct Merged { var calls = 0, sessions = 0, last: Date?, agents: [String: Int] = [:] }
            var merged: [String: Merged] = [:]
            for usage in analytics.bySkill {
                let name = usage.skillName.split(separator: ":").last.map(String.init) ?? usage.skillName
                var entry = merged[name, default: Merged()]
                entry.calls += usage.callCount
                entry.sessions += usage.sessionCount
                if let last = usage.lastUsedAt, last > (entry.last ?? .distantPast) { entry.last = last }
                for agent in usage.agentBreakdown ?? [] { entry.agents[agent.agent, default: 0] += agent.count }
                merged[name] = entry
            }

            let canonical = Set(inventory.skills.map(\.name))
            skillRows = inventory.skills.map { skill in
                let usage = merged[skill.name] ?? Merged()
                let agents = usage.agents.sorted { $0.value > $1.value }.map { "\($0.key) \($0.value)" }.joined(separator: " · ")
                return SkillRow(name: skill.name, tokens: skill.tokens ?? 0, calls: usage.calls, sessions: usage.sessions,
                                lastUsed: usage.last, agents: agents)
            }
            untrackedSkills = merged.filter { !canonical.contains($0.key) }
                .map { (name: $0.key, calls: $0.value.calls) }
                .sorted { $0.calls > $1.calls }
            usageError = nil
        } catch {
            usageError = error.localizedDescription
        }
    }

    func loadForeign() async {
        foreignLoading = true
        defer { foreignLoading = false }
        await loadInventory()
        var items = (inventory?.unmanaged ?? []).map {
            ForeignItem(kind: $0.kind, name: $0.name, agents: $0.agents, detail: $0.detail, source: "tackroom", hint: $0.hint)
        }
        foreignError = inventoryError
        if hkInstalled {
            do {
                let list = try await HarnessKit.list()
                let managedMCP = Set(inventory?.mcp.map(\.name) ?? [])
                let hookCommands = (inventory?.hooks ?? []).compactMap(\.command).flatMap { [$0, NSString(string: $0).expandingTildeInPath] }
                for row in list.rows {
                    switch row.kind {
                    case "skill":
                        continue // tackroom's own scan of the skill roots is the more precise source
                    case "mcp" where managedMCP.contains(row.name):
                        continue
                    case "hook":
                        // hk names hooks "Event:matcher:command".
                        let parts = row.name.split(separator: ":", maxSplits: 2).map(String.init)
                        let command = parts.last ?? row.name
                        if hookCommands.contains(where: { command.contains($0) }) { continue }
                        items.append(ForeignItem(kind: "hook", name: command, agents: row.agents,
                                                 detail: parts.count == 3 ? "\(parts[0]) hook" : "hook", source: "HarnessKit", hint: ""))
                    default:
                        items.append(ForeignItem(kind: row.kind, name: row.name, agents: row.agents,
                                                 detail: row.pack.map { "from \($0)" } ?? "", source: "HarnessKit", hint: ""))
                    }
                }
            } catch {
                foreignError = error.localizedDescription
            }
        }
        foreignItems = items
    }

    // MARK: Embedded web UIs

    func endpoint(for pane: Pane) -> WebEndpoint? {
        switch pane {
        case .config: client?.endpoint
        case .sessions: agentsView?.endpoint
        case .inspector: hkEndpoint
        default: nil
        }
    }

    func webView(for pane: Pane) -> WKWebView? {
        if let existing = webViews[pane] { return existing }
        guard let endpoint = endpoint(for: pane) else { return nil }
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .default()
        if pane == .sessions, let script = agentsView?.webSignInScript {
            configuration.userContentController.addUserScript(WKUserScript(source: script, injectionTime: .atDocumentStart, forMainFrameOnly: true))
        }
        let view = WKWebView(frame: .zero, configuration: configuration)
        view.load(URLRequest(url: endpoint.start))
        webViews[pane] = view
        return view
    }

    func reloadWeb(_ pane: Pane) {
        guard let view = webViews[pane], let endpoint = endpoint(for: pane) else { return }
        view.load(URLRequest(url: endpoint.start))
    }

    func startAgentsView() async {
        guard let agentsview = Tools.find("agentsview") else { return }
        _ = try? await Tools.run(agentsview, ["daemon", "start"])
        agentsView = AgentsViewDaemon.discover()
        webViews[.sessions] = nil
        await loadCost()
    }
}
