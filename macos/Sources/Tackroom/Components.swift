import SwiftUI

/// Lays children out in rows and wraps to a new row when the width runs out.
struct FlowLayout: Layout {
    var spacing: CGFloat = 6

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let limit = proposal.width ?? .infinity
        var x: CGFloat = 0, y: CGFloat = 0, row: CGFloat = 0, widest: CGFloat = 0
        for view in subviews {
            let size = view.sizeThatFits(.unspecified)
            if x > 0, x + size.width > limit {
                x = 0
                y += row + spacing
                row = 0
            }
            x += size.width + spacing
            row = max(row, size.height)
            widest = max(widest, x - spacing)
        }
        return CGSize(width: widest, height: y + row)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX, y = bounds.minY, row: CGFloat = 0
        for view in subviews {
            let size = view.sizeThatFits(.unspecified)
            if x > bounds.minX, x + size.width > bounds.maxX {
                x = bounds.minX
                y += row + spacing
                row = 0
            }
            view.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(size))
            x += size.width + spacing
            row = max(row, size.height)
        }
    }
}

/// What tackroom's inventory says about one agent for one skill or MCP server.
enum CellState: String {
    case ok, drift, missing, conflict, stale, off, disabled, unsupported, absent, error, unknown
    /// No entry at all: tackroom does not inspect agents that are turned off.
    case agentOff

    init(_ raw: String?) {
        guard let raw else { self = .agentOff; return }
        self = CellState(rawValue: raw) ?? .unknown
    }

    var symbol: String {
        switch self {
        case .ok: "checkmark.circle.fill"
        case .drift: "exclamationmark.triangle.fill"
        case .missing: "arrow.down.circle"
        case .conflict: "xmark.octagon.fill"
        case .stale: "clock.arrow.circlepath"
        case .off, .agentOff: "minus.circle"
        case .disabled: "pause.circle"
        case .unsupported: "nosign"
        case .absent: "circle.dashed"
        case .error: "exclamationmark.octagon.fill"
        case .unknown: "questionmark.circle"
        }
    }

    var color: Color {
        switch self {
        case .ok: .green
        case .drift, .missing, .stale: .orange
        case .conflict, .error: .red
        case .off, .disabled, .unsupported, .absent, .agentOff, .unknown: .secondary
        }
    }

    var meaning: String {
        switch self {
        case .ok: "in sync"
        case .drift: "differs from tackroom"
        case .missing: "not written yet"
        case .conflict: "a native copy is in the way"
        case .stale: "left over from an old sync"
        case .off: "not sent to this agent"
        case .disabled: "server is disabled"
        case .unsupported: "this agent cannot use it"
        case .absent: "agent not installed"
        case .error: "agent config unreadable"
        case .unknown: "state unknown"
        case .agentOff: "agent is turned off in tackroom"
        }
    }
}

/// A state symbol with the agent and meaning as a tooltip.
struct StateGlyph: View {
    let agent: String
    let state: CellState

    var body: some View {
        Image(systemName: state.symbol)
            .foregroundStyle(state.color)
            .frame(width: 16)
            .help("\(agent): \(state.meaning)")
            .accessibilityLabel("\(agent), \(state.meaning)")
    }
}

/// The result of the last config save, CLI run or error, with the follow-up actions.
struct ConfigBanner: View {
    @Bindable var model: AppModel

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let error = model.configError {
                Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
            }
            if let saved = model.lastSave {
                GroupBox {
                    VStack(alignment: .leading, spacing: 6) {
                        HStack {
                            Label("Saved to tackroom.yaml. Agents change when you apply a sync.", systemImage: "checkmark.circle.fill")
                                .foregroundStyle(.green)
                            Spacer()
                            Button("Preview sync…") {
                                model.selection = .sync
                                Task { await model.previewSync() }
                            }
                            Button("Dismiss") { model.lastSave = nil }
                        }
                        if !saved.diff.isEmpty {
                            DiffText(diff: saved.diff)
                        }
                        if saved.maskedEnvLines > 0 {
                            Text("Env values are hidden.").font(.caption).foregroundStyle(.secondary)
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(4)
                }
            }
            if let result = model.actionResult {
                GroupBox {
                    VStack(alignment: .leading, spacing: 6) {
                        HStack {
                            Label(result.failed ? "Failed" : "Done", systemImage: result.failed ? "xmark.circle.fill" : "checkmark.circle.fill")
                                .foregroundStyle(result.failed ? .red : .green)
                            Text(result.title).font(.caption.monospaced()).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                            Spacer()
                            Button("Dismiss") { model.actionResult = nil }
                        }
                        ScrollView {
                            Text(result.text).font(.caption.monospaced()).textSelection(.enabled)
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                        .frame(maxHeight: 110)
                    }
                    .padding(4)
                }
            }
        }
    }
}

/// A unified diff with added and removed lines colored.
struct DiffText: View {
    let diff: String

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                ForEach(Array(diff.components(separatedBy: "\n").enumerated()), id: \.offset) { _, line in
                    Text(line.isEmpty ? " " : line)
                        .font(.caption.monospaced())
                        .foregroundStyle(color(line))
                        .textSelection(.enabled)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .frame(maxHeight: 130)
    }

    private func color(_ line: String) -> Color {
        if line.hasPrefix("+++") || line.hasPrefix("---") { return .secondary }
        if line.hasPrefix("+") { return .green }
        if line.hasPrefix("-") { return .red }
        return .secondary
    }
}
