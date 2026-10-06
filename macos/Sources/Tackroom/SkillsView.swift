import AppKit
import SwiftUI

/// A skill from the inventory with its usage numbers, as one table row.
struct SkillItem: Identifiable {
    let skill: InventorySkill
    let usage: SkillRow?

    var id: String { skill.name }
    var name: String { skill.name }
    var origin: String { skill.origin ?? "local" }
    var tokens: Int { skill.tokens ?? 0 }
    var invoked: Int { usage?.invoked ?? 0 }
    var loaded: Int { usage?.loaded ?? 0 }
}

/// First bytes of a SKILL.md for the detail pane.
enum SkillPreview {
    static let limit = 20_000

    static func read(directory: String) -> (text: String, truncated: Bool)? {
        guard let handle = FileHandle(forReadingAtPath: directory + "/SKILL.md") else { return nil }
        defer { try? handle.close() }
        guard let data = try? handle.read(upToCount: limit + 1) else { return nil }
        return (String(decoding: data.prefix(limit), as: UTF8.self), data.count > limit)
    }
}

// MARK: Skills

struct SkillsView: View {
    @Bindable var model: AppModel
    @State private var search = ""
    @State private var selection: SkillItem.ID?
    @State private var sortOrder = [KeyPathComparator(\SkillItem.name)]

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            PageHeader(title: "Skills", subtitle: subtitle) {
                if model.usageLoading { ProgressView().controlSize(.small) }
                Button("Refresh") { Task { await reload() } }
            }
            agentToggles
            ConfigBanner(model: model)
            if let error = model.inventoryError ?? model.serverError {
                Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
            }
            if model.inventory == nil, model.inventoryError == nil, model.serverError == nil {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                VSplitView {
                    table.frame(minHeight: 160, maxHeight: .infinity)
                    detail.frame(minHeight: 180, maxHeight: .infinity)
                }
                .frame(maxHeight: .infinity)
                legend
            }
        }
        .padding(20)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .searchable(text: $search, prompt: "Filter skills")
        .task { await reload() }
    }

    private func reload() async {
        model.configError = nil
        await model.loadConfigState()
        await model.loadSkillUsage()
    }

    private var agents: [InventoryAgent] { model.inventory?.agents ?? [] }

    private var items: [SkillItem] {
        let usage = Dictionary(model.skillRows.map { ($0.name, $0) }, uniquingKeysWith: { first, _ in first })
        let needle = search.trimmingCharacters(in: .whitespaces).lowercased()
        return (model.inventory?.skills ?? [])
            .filter { needle.isEmpty || $0.name.lowercased().contains(needle) || ($0.description ?? "").lowercased().contains(needle) }
            .map { SkillItem(skill: $0, usage: usage[$0.name]) }
            .sorted(using: sortOrder)
    }

    private var subtitle: String {
        let skills = model.inventory?.skills ?? []
        guard !skills.isEmpty else { return "Skills in the tackroom repo, where each agent has them, and how much they are used." }
        let external = skills.filter(\.isExternal).count
        let source = external == 0 ? "" : ", \(external) from external repos"
        let counts = model.usageError == nil ? "Counts: last \(model.usageDays) days, \(model.usageMachine ?? "all machines")." : "Usage counts are unavailable."
        return "\(skills.count) skills\(source). \(counts)"
    }

    // MARK: Agent switches

    private var agentToggles: some View {
        VStack(alignment: .leading, spacing: 4) {
            FlowLayout(spacing: 14) {
                Text("Agents").font(.callout.weight(.medium))
                ForEach(agents) { agent in
                    Toggle(agent.name, isOn: Binding(get: { agent.enabled }, set: { on in Task { await model.setAgentEnabled(agent.name, on) } }))
                        .toggleStyle(.checkbox)
                        .disabled(model.configBusy || model.configState == nil)
                }
            }
            Text("Turn a whole agent on or off. A disabled agent is skipped by sync.").font(.caption).foregroundStyle(.secondary)
        }
    }

    // MARK: List

    private var table: some View {
        Table(items, selection: $selection, sortOrder: $sortOrder) {
            TableColumn("Skill", value: \.name)
            TableColumn("Origin", value: \.origin) { Text($0.origin).foregroundStyle(.secondary) }
                .width(min: 70, ideal: 130)
            TableColumn("Tokens", value: \.tokens) { Text("\($0.tokens)").monospacedDigit() }
                .width(min: 50, ideal: 56)
            TableColumn("Agents") { item in
                HStack(spacing: 2) {
                    ForEach(agents) { agent in
                        StateGlyph(agent: agent.name, state: agent.state(in: item.skill.states))
                    }
                }
            }
            .width(min: CGFloat(agents.count) * 18 + 8, ideal: CGFloat(agents.count) * 18 + 16)
            TableColumn("Invoked", value: \.invoked) { Text(count($0.usage?.invoked)).monospacedDigit() }
                .width(min: 56, ideal: 62)
            TableColumn("Loaded", value: \.loaded) { Text(count($0.usage?.loaded)).monospacedDigit() }
                .width(min: 56, ideal: 62)
        }
        .overlay {
            if items.isEmpty {
                Text(search.isEmpty ? "tackroom has no skills yet." : "No skill matches.").foregroundStyle(.secondary)
            }
        }
    }

    private func count(_ value: Int?) -> String { value.map(String.init) ?? "-" }

    private var legend: some View {
        Text("Agent columns, left to right: \(agents.map(\.name).joined(separator: ", ")). Hover a symbol for its state.")
            .font(.caption)
            .foregroundStyle(.secondary)
    }

    // MARK: Detail

    @ViewBuilder private var detail: some View {
        if let item = items.first(where: { $0.id == selection }) {
            SkillDetail(model: model, item: item, agents: agents)
                .id(item.id)
        } else {
            ContentUnavailableView("Select a skill", systemImage: "puzzlepiece.extension", description: Text("See its description, where each agent has it, and its SKILL.md."))
        }
    }
}

struct SkillDetail: View {
    @Bindable var model: AppModel
    let item: SkillItem
    let agents: [InventoryAgent]
    @State private var preview: (text: String, truncated: Bool)?
    @State private var previewMissing = false
    @State private var confirmUpdate = false

    private var directory: String? { item.skill.path }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 10) {
                HStack(alignment: .firstTextBaseline) {
                    Text(item.name).font(.title3.weight(.semibold)).textSelection(.enabled)
                    Text(item.origin).font(.caption).foregroundStyle(.secondary)
                    Spacer()
                }
                if let description = item.skill.description, !description.isEmpty {
                    Text(description).textSelection(.enabled)
                }
                if let directory {
                    Text(NSString(string: directory).abbreviatingWithTildeInPath + " · about \(item.tokens) tokens")
                        .font(.callout.monospaced())
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                }
                Text(usageLine).font(.callout)
                FlowLayout(spacing: 10) {
                    ForEach(agents) { agent in
                        let state = agent.state(in: item.skill.states)
                        HStack(spacing: 4) {
                            StateGlyph(agent: agent.name, state: state)
                            Text("\(agent.name): \(state.meaning)").font(.caption)
                        }
                    }
                }
                Text("Read-only. tackroom cannot send one skill to one agent yet; use the agent switches above.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                buttons
                previewBox
            }
            .padding(12)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .task(id: directory) { await loadPreview() }
    }

    private var usageLine: String {
        guard let usage = item.usage else { return "Usage: not available." }
        var line = "Last \(model.usageDays) days: \(usage.invoked) invoked, \(usage.loaded) loaded"
        if !usage.agents.isEmpty { line += " (\(usage.agents))" }
        if let last = usage.lastUsed { line += ". Last used \(last.formatted(.relative(presentation: .named)))." }
        return line
    }

    private var buttons: some View {
        HStack {
            if let directory {
                Button("Reveal in Finder") {
                    NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: directory + "/SKILL.md")])
                }
                Button("Open SKILL.md") { NSWorkspace.shared.open(URL(fileURLWithPath: directory + "/SKILL.md")) }
            }
            if let source = item.skill.sourceName {
                Button("Update from \(source)…") { confirmUpdate = true }
                    .disabled(model.actionBusy)
                    .confirmationDialog("Update every skill from \(source)?", isPresented: $confirmUpdate) {
                        Button("Update") { Task { await model.updateSkillSource(source) } }
                        Button("Cancel", role: .cancel) {}
                    } message: {
                        Text("Runs `tackroom skill update \(source)`. It fetches the latest commit and rewrites the lock file.")
                    }
            }
            if model.actionBusy { ProgressView().controlSize(.small) }
        }
    }

    @ViewBuilder private var previewBox: some View {
        if let preview {
            GroupBox("SKILL.md") {
                ScrollView {
                    Text(preview.text + (preview.truncated ? "\n\n[Showing the first \(SkillPreview.limit / 1000) KB]" : ""))
                        .font(.caption.monospaced())
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(6)
                }
                .frame(height: 220)
            }
        } else if previewMissing {
            Text("No SKILL.md found at this path.").font(.callout).foregroundStyle(.secondary)
        }
    }

    private func loadPreview() async {
        preview = nil
        previewMissing = false
        guard let directory else { return }
        let loaded = await Task.detached { SkillPreview.read(directory: directory) }.value
        preview = loaded
        previewMissing = loaded == nil
    }
}
