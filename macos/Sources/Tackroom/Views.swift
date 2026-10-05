import AppKit
import SwiftUI
import WebKit

// MARK: Shared pieces

struct StatePill: View {
    let state: AgentReport.State

    var body: some View {
        Text(label)
            .font(.caption.weight(.medium))
            .padding(.horizontal, 7)
            .padding(.vertical, 2)
            .foregroundStyle(color)
            .background(color.opacity(0.14), in: Capsule())
    }

    private var label: String {
        switch state {
        case .synced: "synced"
        case .drifted: "drifted"
        case .notInstalled: "not installed"
        case .unreadable: "unreadable"
        }
    }

    private var color: Color {
        switch state {
        case .synced: .green
        case .drifted: .orange
        case .notInstalled: .secondary
        case .unreadable: .red
        }
    }
}

@MainActor private func headline(_ model: AppModel) -> String {
    if model.status == nil { return model.serverError == nil ? "Connecting to tackroom…" : "tackroom unavailable" }
    switch model.needsSyncCount {
    case 0: return "All agents synced"
    case 1: return "1 agent needs sync"
    default: return "\(model.needsSyncCount) agents need sync"
    }
}

@MainActor private func repoLine(_ model: AppModel) -> String {
    guard let repo = model.status?.repo else { return "" }
    return "\(NSString(string: repo.path).abbreviatingWithTildeInPath) · \(repo.state)"
}

// MARK: Menu bar

struct MenuBarLabel: View {
    var model: AppModel
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        HStack(spacing: 3) {
            Image(nsImage: Mark.menuBarImage)
            if model.serverError != nil || model.statusError != nil {
                Text("!")
            } else if model.needsSyncCount > 0 {
                Text("\(model.needsSyncCount)")
            }
        }
        // The label lives as long as the app, so it hosts the reopen handler.
        .onReceive(NotificationCenter.default.publisher(for: .showMainWindow)) { _ in
            openWindow(id: "main")
            NSApp.activate()
        }
    }
}

struct MenuContent: View {
    var model: AppModel
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            VStack(alignment: .leading, spacing: 2) {
                Text(headline(model)).font(.headline)
                Text(repoLine(model)).font(.caption.monospaced()).foregroundStyle(.secondary)
            }
            .padding(.horizontal, 8)
            .padding(.bottom, 4)

            if let error = model.serverError ?? model.statusError {
                Text(error).font(.callout).foregroundStyle(.red).padding(.horizontal, 8)
            }

            ForEach(model.status?.reports ?? []) { report in
                HStack(spacing: 8) {
                    Text(report.name).frame(width: 64, alignment: .leading)
                    StatePill(state: report.state)
                    Spacer(minLength: 4)
                    Text(report.state == .notInstalled ? "" : report.issueSummary)
                        .font(.caption.monospaced())
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
                .padding(.horizontal, 8)
                .padding(.vertical, 2)
            }

            Divider().padding(.vertical, 2)
            if let cost = model.todayCost {
                HStack {
                    Text("AI spend (AgentsView)")
                    Spacer()
                    Text(cost).monospacedDigit()
                }
                .font(.callout)
                .foregroundStyle(.secondary)
                .padding(.horizontal, 8)
                Divider().padding(.vertical, 2)
            }

            MenuRow(title: "Open Tackroom", shortcut: "⌘O") { open(.overview) }
                .keyboardShortcut("o")
            MenuRow(title: "Preview sync…", shortcut: "⌘S") {
                open(.sync)
                Task { await model.previewSync() }
            }
            .keyboardShortcut("s")
            MenuRow(title: "Refresh", shortcut: "⌘R") { Task { await model.refresh() } }
                .keyboardShortcut("r")
            if model.isBundled {
                Toggle("Launch at login", isOn: Binding(get: { model.launchAtLogin }, set: { model.setLaunchAtLogin($0) }))
                    .toggleStyle(.checkbox)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 3)
            }
            MenuRow(title: "Quit Tackroom", shortcut: "⌘Q") { NSApp.terminate(nil) }
                .keyboardShortcut("q")
        }
        .padding(8)
        .frame(width: 360)
    }

    private func open(_ pane: Pane) {
        model.selection = pane
        openWindow(id: "main")
        NSApp.activate()
    }
}

/// A full-width, menu-like button with hover highlight.
struct MenuRow: View {
    let title: String
    let shortcut: String
    let action: () -> Void
    @State private var hovered = false

    var body: some View {
        Button(action: action) {
            HStack {
                Text(title)
                Spacer()
                Text(shortcut).foregroundStyle(.tertiary)
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 4)
            .contentShape(Rectangle())
            .background(hovered ? Color.accentColor.opacity(0.18) : .clear, in: RoundedRectangle(cornerRadius: 5))
        }
        .buttonStyle(.plain)
        .onHover { hovered = $0 }
    }
}

// MARK: Window

struct MainView: View {
    @Bindable var model: AppModel

    var body: some View {
        NavigationSplitView {
            List(selection: $model.selection) {
                Section("Status") {
                    row(.overview)
                    row(.sync)
                }
                Section("Inventory") {
                    row(.skills)
                    row(.foreign)
                }
                Section("Web") {
                    row(.config)
                    row(.sessions)
                    if model.hkInstalled { row(.inspector) }
                }
            }
            .navigationSplitViewColumnWidth(min: 180, ideal: 200)
        } detail: {
            switch model.selection ?? .overview {
            case .overview: OverviewView(model: model)
            case .sync: SyncView(model: model)
            case .skills: SkillsView(model: model)
            case .foreign: ForeignView(model: model)
            case .config: WebDetail(model: model, pane: .config)
            case .sessions: WebDetail(model: model, pane: .sessions)
            case .inspector: InspectorView(model: model)
            }
        }
        .frame(minWidth: 900, minHeight: 560)
        .onAppear { NSApp.setActivationPolicy(.regular) }
        .onDisappear { NSApp.setActivationPolicy(.accessory) }
    }

    private func row(_ pane: Pane) -> some View {
        Label(pane.title, systemImage: pane.symbol).tag(pane)
    }
}

struct PageHeader<Trailing: View>: View {
    let title: String
    let subtitle: String
    @ViewBuilder var trailing: Trailing

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            VStack(alignment: .leading, spacing: 4) {
                Text(title).font(.title2.weight(.semibold))
                Text(subtitle).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 16)
            trailing
        }
    }
}

// MARK: Overview

struct OverviewView: View {
    var model: AppModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                PageHeader(title: headline(model), subtitle: subtitle) {
                    Button("Refresh") { Task { await model.refresh() } }
                    Button("Preview sync…") {
                        model.selection = .sync
                        Task { await model.previewSync() }
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(model.client == nil)
                }
                if let error = model.serverError ?? model.statusError {
                    Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
                }
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 250), spacing: 12, alignment: .top)], spacing: 12) {
                    ForEach(model.status?.reports ?? []) { AgentCard(report: $0) }
                }
                GroupBox("Local servers") {
                    Grid(alignment: .leading, horizontalSpacing: 16, verticalSpacing: 8) {
                        GridRow {
                            Text("tackroom view").bold()
                            Text(model.client?.base.absoluteString ?? model.serverError ?? "starting…").textSelection(.enabled)
                        }
                        GridRow {
                            Text("AgentsView").bold()
                            if let daemon = model.agentsView {
                                Text(daemon.base.absoluteString).textSelection(.enabled)
                            } else {
                                HStack {
                                    Text("not running")
                                    Button("Start") { Task { await model.startAgentsView() } }
                                }
                            }
                        }
                        GridRow {
                            Text("HarnessKit").bold()
                            Text(model.hkEndpoint?.base.absoluteString ?? model.hkError ?? (model.hkInstalled ? "starting…" : "not installed"))
                                .textSelection(.enabled)
                        }
                    }
                    .font(.callout.monospaced())
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(6)
                }
            }
            .padding(20)
        }
    }

    private var subtitle: String {
        var parts = [repoLine(model)]
        if let refreshed = model.lastRefresh {
            parts.append("checked \(refreshed.formatted(date: .omitted, time: .shortened)), every 60 s")
        }
        return parts.filter { !$0.isEmpty }.joined(separator: " · ")
    }
}

struct AgentCard: View {
    let report: AgentReport

    var body: some View {
        GroupBox {
            VStack(alignment: .leading, spacing: 6) {
                HStack {
                    Text(report.name).font(.headline)
                    Spacer()
                    StatePill(state: report.state)
                }
                switch report.state {
                case .synced:
                    Text("Matches tackroom.yaml").foregroundStyle(.secondary)
                case .notInstalled:
                    Text("Binary not on PATH").foregroundStyle(.secondary)
                case .unreadable:
                    Text(report.error ?? "").foregroundStyle(.red).font(.caption)
                case .drifted:
                    let items = report.issueItems
                    ForEach(items.prefix(6), id: \.self) { Text($0).font(.caption.monospaced()) }
                    if items.count > 6 { Text("+\(items.count - 6) more").font(.caption).foregroundStyle(.secondary) }
                    if items.isEmpty { Text(report.issueSummary).font(.caption) }
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(4)
        }
    }
}

// MARK: Sync

struct SyncView: View {
    @Bindable var model: AppModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                PageHeader(title: "Sync",
                           subtitle: "Preview what tackroom sync changes in each agent, then apply it. Each destructive change needs its own check.") {
                    if model.syncBusy { ProgressView().controlSize(.small) }
                    Button(model.preview == nil ? "Preview sync" : "Preview again") { Task { await model.previewSync() } }
                        .disabled(model.syncBusy || model.client == nil)
                }
                if let error = model.syncError {
                    Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
                }
                if let preview = model.preview {
                    GroupBox("Plan") {
                        VStack(alignment: .leading, spacing: 4) {
                            ForEach(preview.plan.summary ?? [], id: \.self) { line in
                                Text(line).font(.callout.monospaced()).textSelection(.enabled)
                            }
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(6)
                    }
                    let destructive = preview.plan.destructive ?? []
                    if !destructive.isEmpty {
                        GroupBox("Destructive changes") {
                            VStack(alignment: .leading, spacing: 6) {
                                ForEach(destructive, id: \.self) { item in
                                    Toggle(item, isOn: Binding(
                                        get: { model.confirmed.contains(item) },
                                        set: { if $0 { model.confirmed.insert(item) } else { model.confirmed.remove(item) } }))
                                }
                            }
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(6)
                        }
                    }
                    HStack {
                        Button("Apply sync") { Task { await model.applySync() } }
                            .buttonStyle(.borderedProminent)
                            .disabled(model.syncBusy || !model.allDestructiveConfirmed)
                        Button("Discard preview") { model.discardPreview() }
                        Text("plan \(preview.digest.prefix(12))").font(.caption.monospaced()).foregroundStyle(.tertiary)
                    }
                } else if let applied = model.lastApplied {
                    Label("Sync applied at \(applied.formatted(date: .omitted, time: .standard))", systemImage: "checkmark.circle.fill")
                        .foregroundStyle(.green)
                }
            }
            .padding(20)
        }
    }
}

// MARK: Skill usage

struct SkillsView: View {
    @Bindable var model: AppModel
    @State private var sortOrder = [KeyPathComparator(\SkillRow.calls, order: .reverse)]

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            PageHeader(title: "Skill usage", subtitle: subtitle) {
                if model.usageLoading { ProgressView().controlSize(.small) }
                Picker("Window", selection: $model.usageDays) {
                    Text("7 days").tag(7)
                    Text("30 days").tag(30)
                    Text("90 days").tag(90)
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(width: 220)
            }
            if let error = model.usageError {
                Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
            }
            Table(model.skillRows.sorted(using: sortOrder), sortOrder: $sortOrder) {
                TableColumn("Skill", value: \.name)
                TableColumn("Calls", value: \.calls) { row in
                    Text("\(row.calls)").monospacedDigit().foregroundStyle(row.calls == 0 ? .orange : .primary)
                }
                .width(min: 50, ideal: 60)
                TableColumn("Sessions", value: \.sessions) { Text("\($0.sessions)").monospacedDigit() }
                    .width(min: 60, ideal: 70)
                TableColumn("Last used", value: \.lastUsedSortKey) { row in
                    Text(row.lastUsed.map { $0.formatted(.relative(presentation: .named)) } ?? "not in window")
                        .foregroundStyle(row.lastUsed == nil ? .secondary : .primary)
                }
                TableColumn("By agent") { Text($0.agents).foregroundStyle(.secondary) }
                TableColumn("Tokens", value: \.tokens) { Text("\($0.tokens)").monospacedDigit() }
                    .width(min: 50, ideal: 60)
            }
            if !model.untrackedSkills.isEmpty {
                DisclosureGroup("Called but not in tackroom (\(model.untrackedSkills.count))") {
                    Text(model.untrackedSkills.map { "\($0.name) \($0.calls)" }.joined(separator: " · "))
                        .font(.caption.monospaced())
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
        }
        .padding(20)
        .task(id: model.usageDays) { await model.loadSkillUsage() }
    }

    private var subtitle: String {
        let unused = model.skillRows.filter { $0.calls == 0 }
        guard !model.skillRows.isEmpty else { return "Calls per tackroom skill, from AgentsView session history." }
        let tokens = unused.reduce(0) { $0 + $1.tokens }
        return "\(unused.count) of \(model.skillRows.count) skills had no calls in the last \(model.usageDays) days. Their descriptions cost about \(tokens) tokens in every agent."
    }
}

// MARK: Foreign items

struct ForeignView: View {
    var model: AppModel
    @State private var kind = "all"
    @State private var selection = Set<ForeignItem.ID>()

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            PageHeader(title: "Foreign items", subtitle: subtitle) {
                if model.foreignLoading { ProgressView().controlSize(.small) }
                Picker("Kind", selection: $kind) {
                    Text("All").tag("all")
                    ForEach(kinds, id: \.self) { Text($0).tag($0) }
                }
                .frame(width: 160)
                Button("Refresh") { Task { await model.loadForeign() } }
            }
            if let error = model.foreignError {
                Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
            }
            if !model.hkInstalled {
                Text("Install HarnessKit (hk) to also list MCP servers, hooks and plugins that tackroom does not manage.")
                    .foregroundStyle(.secondary)
            }
            Table(filtered, selection: $selection) {
                TableColumn("Kind", value: \.kind).width(min: 50, ideal: 60)
                TableColumn("Name", value: \.name)
                TableColumn("Agents") { Text($0.agents.joined(separator: ", ")) }
                TableColumn("Found by", value: \.source).width(min: 70, ideal: 90)
                TableColumn("Detail", value: \.detail)
            }
            .contextMenu(forSelectionType: ForeignItem.ID.self) { ids in
                let hints = model.foreignItems.filter { ids.contains($0.id) && !$0.hint.isEmpty }.map(\.hint)
                if !hints.isEmpty {
                    Button(hints.count == 1 ? "Copy promote command" : "Copy \(hints.count) promote commands") {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(hints.joined(separator: "\n"), forType: .string)
                    }
                }
            }
        }
        .padding(20)
        .task { await model.loadForeign() }
    }

    private var kinds: [String] { Array(Set(model.foreignItems.map(\.kind))).sorted() }

    private var filtered: [ForeignItem] {
        kind == "all" ? model.foreignItems : model.foreignItems.filter { $0.kind == kind }
    }

    private var subtitle: String {
        let counts = Dictionary(grouping: model.foreignItems, by: \.kind).map { "\($0.value.count) \($0.key)" }.sorted()
        let summary = counts.isEmpty ? "" : counts.joined(separator: " · ") + ". "
        return summary + "Items in agent folders that tackroom does not manage. Right-click a skill to copy its `tackroom skill promote` command."
    }
}

// MARK: Embedded web UIs

struct WebPane: NSViewRepresentable {
    let webView: WKWebView
    func makeNSView(context: Context) -> WKWebView { webView }
    func updateNSView(_ nsView: WKWebView, context: Context) {}
}

struct WebDetail: View {
    var model: AppModel
    let pane: Pane

    var body: some View {
        Group {
            if let webView = model.webView(for: pane) {
                WebPane(webView: webView)
            } else {
                ContentUnavailableView(unavailableTitle, systemImage: pane.symbol, description: Text(unavailableReason))
            }
        }
        .toolbar {
            Button { model.reloadWeb(pane) } label: { Label("Reload", systemImage: "arrow.clockwise") }
            if let endpoint = model.endpoint(for: pane) {
                Button { NSWorkspace.shared.open(endpoint.start) } label: { Label("Open in browser", systemImage: "safari") }
            }
        }
    }

    private var unavailableTitle: String {
        switch pane {
        case .config: "tackroom view is not running"
        case .sessions: "AgentsView is not running"
        default: "HarnessKit is not running"
        }
    }

    private var unavailableReason: String {
        switch pane {
        case .config: model.serverError ?? "Starting…"
        case .sessions: "Start it with `agentsview daemon start`, or use Start on the Overview."
        default: model.hkError ?? "Starting `hk serve`…"
        }
    }
}

struct InspectorView: View {
    var model: AppModel

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 10) {
                Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                Text("HarnessKit writes agent files directly. Changes made here bypass tackroom, so preview a sync afterwards.")
                Spacer()
                Button("Preview sync…") {
                    model.selection = .sync
                    Task { await model.previewSync() }
                }
            }
            .padding(10)
            .background(Color.orange.opacity(0.12))
            WebDetail(model: model, pane: .inspector)
        }
    }
}
