import AppKit
import SwiftUI

// MARK: MCP servers

struct MCPView: View {
    @Bindable var model: AppModel
    @State private var search = ""
    @State private var editing: ConfigState.Server?
    @State private var adding = false
    @State private var importPlan: MCPImportPlan?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            PageHeader(title: "MCP servers", subtitle: subtitle) {
                if model.mcpLoading || model.configBusy { ProgressView().controlSize(.small) }
                Button("Add server…") { adding = true }.disabled(model.configState == nil)
                Button("Refresh") { Task { await model.loadMCP() } }
            }
            ConfigBanner(model: model)
            if let error = model.inventoryError ?? model.serverError {
                Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
            }
            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    if managed.isEmpty, model.configState != nil {
                        Text(search.isEmpty ? "tackroom manages no MCP servers yet." : "No managed server matches.")
                            .foregroundStyle(.secondary)
                    }
                    ForEach(managed) { server in
                        MCPCard(model: model, server: server, entry: model.inventory?.mcp.first { $0.name == server.name }) { editing = server }
                    }
                    if !managed.isEmpty {
                        Text("A server cannot be deleted from here. Turn it off instead, or run `tackroom mcp remove <name>`. Test starts a server's command without the env values from the config, which are masked, so a server that needs them can fail.")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    unmanagedSection
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .frame(maxHeight: .infinity)
        }
        .padding(20)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .searchable(text: $search, prompt: "Filter servers")
        .task { await reload() }
        .sheet(item: $editing) { server in MCPForm(model: model, server: server) }
        .sheet(isPresented: $adding) { MCPForm(model: model, server: nil) }
        .confirmationDialog("Import into tackroom?", isPresented: Binding(get: { importPlan != nil }, set: { if !$0 { importPlan = nil } }),
                            presenting: importPlan) { plan in
            Button("Import \(plan.name)") { Task { await model.importMCP(plan) } }
            Button("Cancel", role: .cancel) {}
        } message: { plan in
            Text("Runs `\(plan.command)`. It adds the server to tackroom.yaml for \(plan.targets.joined(separator: ", ")). Env values are not copied: they become ${KEY} references.")
        }
    }

    private func reload() async {
        model.configError = nil
        await model.loadMCP()
    }

    private var managed: [ConfigState.Server] {
        let needle = search.trimmingCharacters(in: .whitespaces).lowercased()
        return (model.configState?.servers ?? []).filter {
            needle.isEmpty || $0.name.lowercased().contains(needle) || $0.command.lowercased().contains(needle)
        }
    }

    private var subtitle: String {
        let servers = model.configState?.servers ?? []
        guard !servers.isEmpty else { return "MCP servers tackroom writes into each agent's config." }
        let on = servers.filter(\.enabled).count
        return "\(servers.count) managed, \(on) enabled. Click an agent to send a server to it or stop. Changes go to tackroom.yaml; agents change when you apply a sync."
    }

    // MARK: Not managed

    @ViewBuilder private var unmanagedSection: some View {
        let needle = search.trimmingCharacters(in: .whitespaces).lowercased()
        let rows = model.unmanagedMCP.filter { needle.isEmpty || $0.name.lowercased().contains(needle) }
        if !rows.isEmpty || model.unmanagedMCPError != nil {
            Divider()
            Text("Not managed by tackroom").font(.headline)
            if let error = model.unmanagedMCPError {
                Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
            }
            ForEach(rows, id: \.name) { row in
                unmanagedRow(row)
            }
            Text("Found by HarnessKit in agent configs. Importing copies a server into tackroom.yaml; it does not change any agent until you apply a sync.")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }

    private func unmanagedRow(_ row: HKRow) -> some View {
        let plan = MCPImportPlan.make(name: row.name, foundIn: row.agents, configured: model.mcpCapableAgents,
                                      config: model.configState?.sharedPath)
        return GroupBox {
            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(row.name).font(.headline)
                    Text("in \(row.agents.joined(separator: ", "))").font(.caption).foregroundStyle(.secondary)
                    if plan == nil {
                        Text("None of these agents can take MCP servers in tackroom.").font(.caption).foregroundStyle(.secondary)
                    }
                }
                Spacer()
                if let plan {
                    Button("Copy command") {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(plan.command, forType: .string)
                    }
                    Button("Import into tackroom…") { importPlan = plan }
                        .disabled(model.actionBusy)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(4)
        }
    }
}

// MARK: Server card

struct MCPCard: View {
    @Bindable var model: AppModel
    let server: ConfigState.Server
    let entry: InventoryEntry?
    let edit: () -> Void

    var body: some View {
        GroupBox {
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Toggle(isOn: Binding(get: { server.enabled }, set: { on in Task { await model.setMCPEnabled(server.name, on) } })) {
                        Text(server.name).font(.headline)
                    }
                    .toggleStyle(.switch)
                    .disabled(model.configBusy)
                    Spacer()
                    Button("Edit…", action: edit).disabled(model.configBusy)
                }
                Text(([server.command] + server.args).joined(separator: " "))
                    .font(.callout.monospaced())
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                    .textSelection(.enabled)
                if !server.envKeys.isEmpty {
                    Text("env: \(server.envKeys.joined(separator: ", ")) (values hidden)").font(.caption).foregroundStyle(.secondary)
                }
                FlowLayout(spacing: 6) {
                    ForEach(model.inventory?.agents.filter(\.supportsMcp) ?? []) { agent in
                        chip(agent)
                    }
                }
                probe
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(4)
        }
    }

    private var probe: some View {
        HStack(spacing: 8) {
            Button("Test") { Task { await model.testMCP(server) } }
                .disabled(model.probing.contains(server.name))
                .help("Starts the command for up to \(Int(MCPProbe.timeout)) s and asks the server for its tools. Env values from the config are masked, so a server that needs them may fail here.")
            if model.probing.contains(server.name) {
                ProgressView().controlSize(.small)
            } else if let outcome = model.probes[server.name] {
                switch outcome {
                case .ok: Label(outcome.text, systemImage: "checkmark.circle.fill").foregroundStyle(.green)
                case .failed: Label(outcome.text, systemImage: "xmark.circle.fill").foregroundStyle(.red)
                }
            }
        }
        .font(.callout)
    }

    /// An empty list means every agent, so every capable agent shows as a target.
    private func isTarget(_ agent: String) -> Bool {
        let targets = entry?.targets ?? server.agents
        let explicit = entry?.explicit ?? !server.agents.isEmpty
        return !explicit || targets.contains { AgentNames.canonical($0) == AgentNames.canonical(agent) }
    }

    private func chip(_ agent: InventoryAgent) -> some View {
        let target = isTarget(agent.name)
        let state = agent.state(in: entry?.states)
        return Button {
            Task { await model.setMCPTarget(server, agent: agent.name, on: !target) }
        } label: {
            HStack(spacing: 4) {
                Image(systemName: state.symbol).foregroundStyle(state.color)
                Text(agent.name).font(.callout)
            }
            .padding(.horizontal, 9)
            .padding(.vertical, 3)
            .background(target ? Color.accentColor.opacity(0.14) : Color.clear, in: Capsule())
            .overlay(Capsule().stroke(target ? Color.accentColor.opacity(0.5) : Color.secondary.opacity(0.35)))
            .opacity(target ? 1 : 0.7)
        }
        .buttonStyle(.plain)
        .disabled(model.configBusy)
        .help("\(agent.name): \(state.meaning). Click to \(target ? "stop sending" : "send") \(server.name) to \(agent.name).")
    }
}

// MARK: Add and edit form

/// One form for both: `server` is nil when adding.
struct MCPForm: View {
    @Bindable var model: AppModel
    let server: ConfigState.Server?
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var command = ""
    @State private var args = ""
    @State private var envKey = ""
    @State private var envValue = ""
    @State private var agents = Set<String>()

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(server.map { "Edit \($0.name)" } ?? "Add MCP server").font(.title3.weight(.semibold))
            Form {
                if server == nil { TextField("Name", text: $name) }
                TextField("Command", text: $command)
                VStack(alignment: .leading, spacing: 4) {
                    Text("Arguments, one per line")
                    TextEditor(text: $args)
                        .font(.body.monospaced())
                        .frame(height: 90)
                        .overlay(RoundedRectangle(cornerRadius: 4).stroke(Color.secondary.opacity(0.3)))
                }
                Section("Environment") {
                    if let server, !server.envKeys.isEmpty {
                        Text("Set: \(server.envKeys.joined(separator: ", ")). Values are hidden and stay as they are.")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    TextField("Key", text: $envKey)
                    SecureField("Value", text: $envValue)
                    Text("Adds the key or replaces its value. Leave both empty to keep the env as is.")
                        .font(.caption).foregroundStyle(.secondary)
                }
                if server == nil {
                    Section("Agents") {
                        ForEach(model.mcpCapableAgents, id: \.self) { agent in
                            Toggle(agent, isOn: Binding(get: { agents.contains(agent) }, set: { on in
                                if on { agents.insert(agent) } else { agents.remove(agent) }
                            }))
                        }
                    }
                }
            }
            .formStyle(.grouped)
            if let error = model.configError {
                Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button(server == nil ? "Add" : "Save") { Task { await save() } }
                    .keyboardShortcut(.defaultAction)
                    .disabled(model.configBusy)
            }
        }
        .padding(20)
        .frame(width: 480)
        .onAppear(perform: load)
    }

    private func load() {
        model.configError = nil
        if let server {
            command = server.command
            args = server.args.joined(separator: "\n")
        } else {
            agents = Set(model.mcpCapableAgents)
        }
    }

    private var argumentList: [String] {
        args.split(whereSeparator: \.isNewline).map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
    }

    private var env: (key: String, value: String)? {
        let key = envKey.trimmingCharacters(in: .whitespaces)
        return key.isEmpty && envValue.isEmpty ? nil : (key, envValue)
    }

    private func save() async {
        let saved: Bool
        if let server {
            saved = await model.editMCP(server, command: command, args: argumentList, env: env)
        } else {
            saved = await model.addMCP(name: name, command: command, args: argumentList, env: env,
                                       agents: model.mcpCapableAgents.filter(agents.contains))
        }
        if saved { dismiss() }
    }
}
