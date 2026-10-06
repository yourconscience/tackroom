import Foundation
import Testing
@testable import Tackroom

// Shapes copied from a live `agentsview session search --json` (v0.42.0).
private let pageJSON = """
{"matches":[
  {"session_id":"m4~11111111-1111","project":"p","agent":"codex","location":"tool_input","role":"assistant","tool_name":"exec",
   "ordinal":3,"timestamp":"2026-10-05T17:54:57.534Z",
   "snippet":"cat ~/.agents/<mark>skills/tech-search/SKILL.md</mark> | head","ordinal_range":[1,5]},
  {"session_id":"22222222-2222","project":"p","agent":"claude","location":"tool_input","tool_name":"Read",
   "ordinal":9,"timestamp":"2026-10-05T21:36:02.5Z",
   "snippet":"x"}
 ],"next_cursor":500}
"""

private func match(_ session: String, _ agent: String, _ snippet: String, _ time: String? = nil) -> SearchMatch {
    SearchMatch(sessionID: session, agent: agent, timestamp: time.flatMap(Decoders.timestamp), snippet: snippet)
}

private func usage(_ name: String, calls: Int, last: String? = nil, agents: [(String, Int)] = []) -> SkillUsage {
    let json = """
    {"skill_name":"\(name)","call_count":\(calls),"session_count":1,
     "agent_breakdown":[\(agents.map { "{\"agent\":\"\($0.0)\",\"count\":\($0.1)}" }.joined(separator: ","))]
     \(last.map { ",\"last_used_at\":\"\($0)\"" } ?? "")}
    """
    return try! Decoders.snakeCase.decode(SkillUsage.self, from: Data(json.utf8))
}

// MARK: Search parsing

@Test func parsesSearchPage() throws {
    let page = try SkillSearch.parsePage(Data(pageJSON.utf8))
    #expect(page.matches.count == 2)
    #expect(page.matches[0].sessionID == "m4~11111111-1111")
    #expect(page.matches[0].agent == "codex")
    #expect(page.matches[0].timestamp != nil)
    #expect(page.matches[1].timestamp != nil) // one fractional digit: "02.5Z"
    #expect(page.nextCursor == 500)
}

@Test func parsesEmptyPageAndZeroCursor() throws {
    #expect(try SkillSearch.parsePage(Data(#"{"matches":[],"next_cursor":0}"#.utf8)) == SearchPage(matches: [], nextCursor: 0))
    #expect(try SkillSearch.parsePage(Data(#"{"matches":null,"next_cursor":null}"#.utf8)).matches.isEmpty)
}

@Test func stopsPagingWhenTheCursorDoesNotAdvance() {
    let some = [match("a", "codex", "x")]
    #expect(SkillSearch.advance(SearchPage(matches: some, nextCursor: 500), from: nil) == 500)
    #expect(SkillSearch.advance(SearchPage(matches: some, nextCursor: 1000), from: 500) == 1000)
    #expect(SkillSearch.advance(SearchPage(matches: some, nextCursor: nil), from: 500) == nil)
    // The last page of a real search answers next_cursor 0, which would restart from page one.
    #expect(SkillSearch.advance(SearchPage(matches: some, nextCursor: 0), from: 500) == nil)
    #expect(SkillSearch.advance(SearchPage(matches: some, nextCursor: 500), from: 500) == nil)
    #expect(SkillSearch.advance(SearchPage(matches: [], nextCursor: 1500), from: 1000) == nil)
}

@Test func buildsTheSearchCommand() {
    #expect(SkillSearch.arguments(days: 30, machine: nil, cursor: nil) ==
        ["session", "search", #"skills/[a-z0-9-]+/SKILL\.md"#, "--regex", "--in", "tool_input", "--since", "30d", "--limit", "500", "--json"])
    let paged = SkillSearch.arguments(days: 7, machine: "m4", cursor: 500)
    #expect(paged.suffix(4) == ["--cursor", "500", "--machine", "m4"])
    #expect(paged.contains("7d"))
}

@Test func stripsMarkTags() {
    #expect(SkillSearch.skillNames(inSnippet: "cat ~/.agents/<mark>skills/tech-search/SKILL.md</mark> | head") == ["tech-search"])
    #expect(SkillSearch.skillNames(inSnippet: "<mark>skills/a1/SKILL.md</mark>") == ["a1"])
}

@Test func findsSeveralPathsInOneSnippet() {
    let snippet = "diff skills/decide/SKILL.md <mark>skills/tern/SKILL.md</mark> skills/decide/SKILL.md skills/Upper/SKILL.md skills/x/SKILL.txt"
    #expect(SkillSearch.skillNames(inSnippet: snippet) == ["decide", "tern"]) // distinct, in order; no uppercase, no other file
}

@Test func ignoresPlaceholdersAndHiddenFolders() {
    #expect(SkillSearch.skillNames(inSnippet: "skills/<name>/SKILL.md and skills/.system/SKILL.md and skills/-x/SKILL.md").isEmpty)
}

@Test func countsDistinctSessionsPerSkillAndAgent() {
    let matches = [
        match("s1", "codex", "skills/tech-search/SKILL.md", "2026-10-01T10:00:00Z"),
        match("s1", "codex", "skills/tech-search/SKILL.md skills/decide/SKILL.md", "2026-10-02T10:00:00Z"), // same session again
        match("s2", "codex", "skills/tech-search/SKILL.md", "2026-10-03T10:00:00Z"),
        match("s3", "pi", "skills/tech-search/SKILL.md", "2026-10-04T10:00:00Z"),
        match("m4~s1", "codex", "skills/tech-search/SKILL.md", "2026-09-30T10:00:00Z"), // other machine, same bare id
    ]
    let loads = SkillSearch.loads(from: matches)
    #expect(loads.first { $0.skill == "tech-search" && $0.agent == "codex" }?.sessions == 3)
    #expect(loads.first { $0.skill == "tech-search" && $0.agent == "pi" }?.sessions == 1)
    #expect(loads.first { $0.skill == "decide" && $0.agent == "codex" }?.sessions == 1)
    #expect(loads.first { $0.skill == "tech-search" && $0.agent == "codex" }?.last == Decoders.timestamp("2026-10-03T10:00:00Z"))
}

// MARK: Merge

private let skills: [(name: String, tokens: Int)] = [("tech-search", 70), ("decide", 40), ("idle", 10), ("docx", 20)]

@Test func mergesNamespacedInvocationsIntoTheBareName() {
    let merged = SkillUsageMerger.merge(skills: skills, invoked: [
        usage("docx", calls: 2, agents: [("claude", 2)]),
        usage("anthropic-skills:docx", calls: 3, agents: [("claude", 1), ("hermes", 2)]),
    ], loads: [])
    let docx = merged.rows.first { $0.name == "docx" }
    #expect(docx?.invoked == 5)
    #expect(docx?.agents == "claude 3 · hermes 2")
    #expect(merged.untracked.isEmpty)
}

@Test func skillIsUnusedOnlyWhenBothSignalsAreZero() {
    let merged = SkillUsageMerger.merge(skills: skills,
        invoked: [usage("tech-search", calls: 4)],
        loads: [SkillLoad(skill: "decide", agent: "claude", sessions: 1, last: nil)])
    let byName = Dictionary(uniqueKeysWithValues: merged.rows.map { ($0.name, $0) })
    #expect(byName["tech-search"]?.isUnused == false) // invoked only
    #expect(byName["decide"]?.isUnused == false)      // loaded only
    #expect(byName["idle"]?.isUnused == true)         // neither
    #expect(byName["docx"]?.isUnused == true)
    #expect(merged.rows.filter(\.isUnused).map(\.name) == ["idle", "docx"])
}

@Test func lastUsedIsTheLatestAcrossBothSignals() {
    let invokedLater = SkillUsageMerger.merge(skills: skills,
        invoked: [usage("tech-search", calls: 1, last: "2026-10-05T12:00:00Z")],
        loads: [SkillLoad(skill: "tech-search", agent: "codex", sessions: 1, last: Decoders.timestamp("2026-10-01T12:00:00Z"))])
    #expect(invokedLater.rows[0].lastUsed == Decoders.timestamp("2026-10-05T12:00:00Z"))

    let loadedLater = SkillUsageMerger.merge(skills: skills,
        invoked: [usage("tech-search", calls: 1, last: "2026-10-01T12:00:00Z")],
        loads: [SkillLoad(skill: "tech-search", agent: "codex", sessions: 1, last: Decoders.timestamp("2026-10-05T23:16:29.459Z"))])
    #expect(loadedLater.rows[0].lastUsed == Decoders.timestamp("2026-10-05T23:16:29.459Z"))

    let neither = SkillUsageMerger.merge(skills: skills, invoked: [], loads: [])
    #expect(neither.rows.allSatisfy { $0.lastUsed == nil })
}

@Test func byAgentAddsInvocationsAndLoadsPerAgent() {
    let merged = SkillUsageMerger.merge(skills: skills,
        invoked: [usage("tech-search", calls: 5, agents: [("claude", 2), ("hermes", 3)])],
        loads: [
            SkillLoad(skill: "tech-search", agent: "claude", sessions: 1, last: nil),
            SkillLoad(skill: "tech-search", agent: "codex", sessions: 6, last: nil),
            SkillLoad(skill: "tech-search", agent: "pi", sessions: 3, last: nil),
        ])
    let row = merged.rows[0]
    #expect(row.invoked == 5)
    #expect(row.loaded == 10)
    // claude 2 + 1, hermes 3, codex 6, pi 3: biggest first, ties by name
    #expect(row.agents == "codex 6 · claude 3 · hermes 3 · pi 3")
}

@Test func namesInEitherSignalButNotInTackroomAreUntracked() {
    let merged = SkillUsageMerger.merge(skills: skills,
        invoked: [usage("dotagents", calls: 18), usage("plugin:tg", calls: 14)],
        loads: [SkillLoad(skill: "stray", agent: "pi", sessions: 2, last: nil), SkillLoad(skill: "tg", agent: "omp", sessions: 1, last: nil)])
    #expect(merged.untracked == [
        UntrackedSkill(name: "dotagents", invoked: 18, loaded: 0),
        UntrackedSkill(name: "tg", invoked: 14, loaded: 1),
        UntrackedSkill(name: "stray", invoked: 0, loaded: 2),
    ])
}

@Test func subtitleSaysWhatIsCountedWhereAndWhen() {
    #expect(SkillUsageMerger.scope(days: 30, machine: nil, machines: ["m1.local", "m4"]) ==
        "Invocations and SKILL.md loads from AgentsView on m1.local and m4, last 30 days. Loads include editing the skill itself.")
    #expect(SkillUsageMerger.scope(days: 7, machine: "m4", machines: ["m1.local", "m4"]).contains("on m4, last 7 days"))
    #expect(SkillUsageMerger.scope(days: 90, machine: nil, machines: []).contains("on all machines"))
    #expect(SkillUsageMerger.scope(days: 90, machine: nil, machines: ["a", "b", "c"]).contains("on a, b and c"))
}

@Test func parsesTimestampsWithAnyFractionalDigits() {
    for text in ["2026-10-05T21:36:02.5Z", "2026-10-05T12:02:21.572Z", "2026-10-01T10:10:47Z", "2026-10-01T10:10:47+00:00"] {
        #expect(Decoders.timestamp(text) != nil, "\(text)")
    }
    #expect(Decoders.timestamp("yesterday") == nil)
}
