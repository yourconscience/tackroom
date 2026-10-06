import Foundation

/// Config edits and the CLIs the Skills and MCP panes run. The app never writes agent files:
/// edits go to tackroom.yaml through `tackroom view`, and agents change only when the user applies a sync.
extension AppModel {
    func loadConfigState() async {
        guard let client else { return }
        do {
            configState = try await client.get("api/state", decoder: JSONDecoder())
        } catch {
            configError = error.localizedDescription
        }
    }

    /// Sends the edits as one PATCH against the revision the screen was built from, so a file
    /// that changed underneath is refused instead of overwritten. Returns whether it saved.
    @discardableResult
    func saveConfig(_ operations: [ConfigOperation]) async -> Bool {
        guard let client, let revision = configState?.revision else {
            configError = "tackroom view is not connected."
            return false
        }
        configBusy = true
        defer { configBusy = false }
        do {
            lastSave = try await client.patchConfig(expectedRevision: revision, operations: operations)
            configError = nil
            discardPreview() // the old plan belongs to the old revision
            await reloadAfterConfigChange()
            return true
        } catch let error as ServerError where error.isStaleRevision {
            lastSave = nil
            await reloadAfterConfigChange()
            configError = "The config changed since this page loaded. It has been reloaded. Make the change again."
        } catch {
            lastSave = nil
            configError = error.localizedDescription
        }
        return false
    }

    private func reloadAfterConfigChange() async {
        await loadConfigState()
        await loadInventory()
        await loadStatus()
    }

    // MARK: Agents

    @discardableResult
    func setAgentEnabled(_ name: String, _ enabled: Bool) async -> Bool {
        await saveConfig([ConfigEdit.agentEnabled(name, enabled)])
    }

    // MARK: MCP servers

    func loadMCP() async {
        mcpLoading = true
        defer { mcpLoading = false }
        await loadConfigState()
        await loadInventory()
        unmanagedMCP = []
        unmanagedMCPError = nil
        guard hkInstalled else { return }
        do {
            let managed = Set((configState?.servers.map(\.name) ?? []) + (inventory?.mcp.map(\.name) ?? []))
            unmanagedMCP = try await HarnessKit.list().rows.filter { $0.kind == "mcp" && !managed.contains($0.name) }
        } catch {
            unmanagedMCPError = error.localizedDescription
        }
    }

    /// Agents that can take MCP servers, in config order.
    var mcpCapableAgents: [String] {
        (inventory?.agents ?? []).filter(\.supportsMcp).map(\.name)
    }

    @discardableResult
    func setMCPEnabled(_ server: String, _ enabled: Bool) async -> Bool {
        guard ConfigEdit.canAddress(server) else { return fail("\(server) has a / in its name and cannot be edited here.") }
        return await saveConfig([ConfigEdit.mcpEnabled(server, enabled)])
    }

    /// Adds or removes one agent from a server's list, keeping the spelling the file already uses.
    @discardableResult
    func setMCPTarget(_ server: ConfigState.Server, agent: String, on: Bool) async -> Bool {
        guard ConfigEdit.canAddress(server.name) else { return fail("\(server.name) has a / in its name and cannot be edited here.") }
        let written = configState?.writtenAgents[server.name] ?? []
        // The typed list is the fallback if the file's own spelling could not be read.
        let base = written.isEmpty ? server.agents : written
        guard let agents = AgentNames.toggled(written: base, capable: mcpCapableAgents, agent: agent, on: on) else {
            return fail("\(server.name) needs at least one agent. Disable the server instead.")
        }
        return await saveConfig([ConfigEdit.mcpAgents(server.name, agents)])
    }

    /// Saves only what changed: command, arguments, and one env key to add or replace.
    @discardableResult
    func editMCP(_ server: ConfigState.Server, command: String, args: [String], env: (key: String, value: String)?) async -> Bool {
        guard ConfigEdit.canAddress(server.name) else { return fail("\(server.name) has a / in its name and cannot be edited here.") }
        let command = command.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !command.isEmpty else { return fail("The command cannot be empty.") }
        var operations: [ConfigOperation] = []
        if command != server.command { operations.append(ConfigEdit.mcpCommand(server.name, command)) }
        if args != server.args { operations.append(ConfigEdit.mcpArgs(server.name, args)) }
        if let env {
            guard ConfigEdit.isValidEnvKey(env.key) else { return fail("An env key cannot be empty or contain spaces or /.") }
            operations.append(ConfigEdit.mcpEnv(server.name, key: env.key, value: env.value, hasEnv: !server.envKeys.isEmpty))
        }
        guard !operations.isEmpty else { return fail("Nothing to save.") }
        return await saveConfig(operations)
    }

    @discardableResult
    func addMCP(name: String, command: String, args: [String], env: (key: String, value: String)?, agents: [String]) async -> Bool {
        let name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        let command = command.trimmingCharacters(in: .whitespacesAndNewlines)
        guard ConfigEdit.isValidNewName(name) else { return fail("A name can use letters, digits, dot, dash and underscore.") }
        guard !(configState?.servers ?? []).contains(where: { $0.name == name }) else { return fail("\(name) already exists.") }
        guard !command.isEmpty else { return fail("The command cannot be empty.") }
        guard !agents.isEmpty else { return fail("Pick at least one agent.") }
        if let env, !ConfigEdit.isValidEnvKey(env.key) { return fail("An env key cannot be empty or contain spaces or /.") }
        return await saveConfig([ConfigEdit.addMCP(name: name, command: command, args: args, env: env, agents: agents)])
    }

    /// Starts the server's command for a few seconds and asks it for its tools.
    func testMCP(_ server: ConfigState.Server) async {
        probing.insert(server.name)
        defer { probing.remove(server.name) }
        probes[server.name] = await MCPProbe.run(command: server.command, args: server.args)
    }

    private func fail(_ message: String) -> Bool {
        configError = message
        return false
    }

    /// Runs `tackroom mcp import` for a server found only in agent configs. The user confirmed it.
    func importMCP(_ plan: MCPImportPlan) async {
        await runTackroom(plan.arguments, title: plan.command)
        await loadMCP()
        await loadForeign()
    }

    // MARK: External skills

    /// `tackroom skill update` takes an external repo's name and updates every skill from it.
    func updateSkillSource(_ source: String) async {
        await runTackroom(["skill", "update", source], title: "tackroom skill update \(source)")
        await loadInventory()
        await loadStatus()
    }

    private func runTackroom(_ arguments: [String], title: String) async {
        guard let tackroom = Tools.find("tackroom") else {
            actionResult = ActionResult(title: title, text: "tackroom not found.", failed: true)
            return
        }
        actionBusy = true
        defer { actionBusy = false }
        do {
            let output = try await Tools.run(tackroom, arguments)
            let text = [output.text, output.stderr.trimmingCharacters(in: .whitespacesAndNewlines)].filter { !$0.isEmpty }.joined(separator: "\n")
            actionResult = ActionResult(title: title, text: text.isEmpty ? "Done." : text, failed: output.status != 0)
        } catch {
            actionResult = ActionResult(title: title, text: error.localizedDescription, failed: true)
        }
    }
}
