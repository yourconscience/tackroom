import Foundation

extension AppModel {
    /// What the usage numbers count, for the page subtitle.
    var usageScope: String {
        SkillUsageMerger.scope(days: usageDays, machine: usageMachine, machines: machines)
    }

    /// Joins the canonical skills with both usage signals for the chosen window and machine.
    /// If either signal is unavailable the rows are cleared: a skill is only "unused" when both say zero.
    func loadSkillUsage() async {
        usageGeneration += 1
        let generation = usageGeneration
        usageLoading = true
        defer { if generation == usageGeneration { usageLoading = false } }

        await loadInventory()
        guard let inventory else {
            clearUsage(inventoryError)
            return
        }
        guard let agentsView else {
            clearUsage("AgentsView is not running. Start it with `agentsview daemon start`.")
            return
        }
        let days = usageDays
        let machine = usageMachine
        do {
            if machines.isEmpty { machines = (try? await agentsView.machines()) ?? [] }
            let since = Calendar.current.date(byAdding: .day, value: -days, to: Date()) ?? Date()
            async let analytics = agentsView.skillAnalytics(since: since, machine: machine)
            async let matches = SkillSearch.fetch(days: days, machine: machine)
            let (invoked, found) = try await (analytics, matches)
            // A newer window or machine was picked while this one loaded.
            guard generation == usageGeneration else { return }
            let merged = SkillUsageMerger.merge(skills: inventory.skills.map { ($0.name, $0.tokens ?? 0) },
                                                invoked: invoked.bySkill, loads: SkillSearch.loads(from: found))
            skillRows = merged.rows
            untrackedSkills = merged.untracked
            usageError = nil
        } catch {
            guard generation == usageGeneration else { return }
            clearUsage(error.localizedDescription)
        }
    }

    private func clearUsage(_ message: String?) {
        skillRows = []
        untrackedSkills = []
        usageError = message
    }
}
