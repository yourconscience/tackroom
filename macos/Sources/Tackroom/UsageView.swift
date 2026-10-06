import SwiftUI

// MARK: Skill usage

struct UsageView: View {
    @Bindable var model: AppModel
    @State private var sortOrder = [KeyPathComparator(\SkillRow.uses, order: .reverse)]

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            PageHeader(title: "Skill usage", subtitle: model.usageScope) {
                if model.usageLoading { ProgressView().controlSize(.small) }
            }
            HStack {
                Picker("Machine", selection: $model.usageMachine) {
                    Text("All machines").tag(String?.none)
                    ForEach(model.machines, id: \.self) { Text($0).tag(String?.some($0)) }
                }
                .labelsHidden()
                .frame(width: 160)
                Picker("Window", selection: $model.usageDays) {
                    Text("7 days").tag(7)
                    Text("30 days").tag(30)
                    Text("90 days").tag(90)
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(width: 220)
                Spacer()
            }
            if let error = model.usageError {
                Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
            }
            if !model.skillRows.isEmpty {
                Text(summary).font(.callout)
            }
            Table(model.skillRows.sorted(using: sortOrder), sortOrder: $sortOrder) {
                TableColumn("Skill", value: \.name)
                TableColumn("Invoked", value: \.invoked) { row in
                    Text("\(row.invoked)").monospacedDigit().foregroundStyle(row.isUnused ? .orange : .primary)
                }
                .width(min: 56, ideal: 66)
                TableColumn("Loaded", value: \.loaded) { row in
                    Text("\(row.loaded)").monospacedDigit().foregroundStyle(row.isUnused ? .orange : .primary)
                }
                .width(min: 56, ideal: 66)
                TableColumn("Last used", value: \.lastUsedSortKey) { row in
                    Text(row.lastUsed.map { $0.formatted(.relative(presentation: .named)) } ?? "not in window")
                        .foregroundStyle(row.lastUsed == nil ? .secondary : .primary)
                }
                TableColumn("By agent") { Text($0.agents).foregroundStyle(.secondary) }
                TableColumn("Tokens", value: \.tokens) { Text("\($0.tokens)").monospacedDigit() }
                    .width(min: 50, ideal: 60)
            }
            .frame(minHeight: 160, maxHeight: .infinity)
            Text("Invoked counts explicit skill calls. Loaded counts sessions that read the skill's SKILL.md, which is how Codex, Pi and OMP use skills. AgentsView skips one-shot sessions, and the SKILL.md search also skips subagent sessions.")
                .font(.caption)
                .foregroundStyle(.secondary)
            if !model.untrackedSkills.isEmpty {
                DisclosureGroup("Called but not in tackroom (\(model.untrackedSkills.count))") {
                    ScrollView {
                        Text(model.untrackedSkills.map(describe).joined(separator: " · "))
                            .font(.caption.monospaced())
                            .foregroundStyle(.secondary)
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .frame(maxHeight: 100)
                }
            }
        }
        .padding(20)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .task(id: "\(model.usageDays)|\(model.usageMachine ?? "")") { await model.loadSkillUsage() }
    }

    private var summary: String {
        let unused = model.skillRows.filter(\.isUnused)
        let tokens = unused.reduce(0) { $0 + $1.tokens }
        return "\(unused.count) of \(model.skillRows.count) skills have no invocations and no loads. Their descriptions cost about \(tokens) tokens in every agent."
    }

    private func describe(_ skill: UntrackedSkill) -> String {
        var parts: [String] = []
        if skill.invoked > 0 { parts.append("\(skill.invoked) invoked") }
        if skill.loaded > 0 { parts.append("\(skill.loaded) loaded") }
        return "\(skill.name) (\(parts.joined(separator: ", ")))"
    }
}
