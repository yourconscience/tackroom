import Foundation
import Testing
@testable import Tackroom

// Shapes copied from a live `tackroom view` /api/status: PascalCase keys, null lists.
private let statusJSON = """
{"repo":{"Path":"/h/.agents","ExpectedTarget":"/h/.agents","ActualTarget":"/h/myagents","State":"synced"},
 "revision":"abc",
 "reports":[
  {"Name":"claude","Error":"","Detected":true,"Synced":false,"RootState":"synced",
   "Missing":null,"MissingHook":["memory-session-end","memory-session-start","memory-stop","pr-triage-stop"],
   "DriftedMCP":null,"DriftedHook":null},
  {"Name":"codex","Error":"","Detected":true,"Synced":false,"RootState":"synced",
   "DriftedMCP":["linkedin"],"DriftedHook":["memory-session-end","memory-session-start"]},
  {"Name":"hermes","Error":"","Detected":true,"Synced":true},
  {"Name":"droid","Error":"","Detected":false,"Synced":false},
  {"Name":"pi","Error":"read config: bad toml","Detected":true,"Synced":false}
 ]}
"""

@Test func decodesPascalCaseStatus() throws {
    let status = try Decoders.tackroomStatus.decode(StatusResponse.self, from: Data(statusJSON.utf8))
    #expect(status.repo.state == "synced")
    #expect(status.reports.map(\.name) == ["claude", "codex", "hermes", "droid", "pi"])
    #expect(status.reports[0].missingHook?.count == 4)
}

@Test func summarizesDriftPerAgent() throws {
    let reports = try Decoders.tackroomStatus.decode(StatusResponse.self, from: Data(statusJSON.utf8)).reports
    #expect(reports[0].state == .drifted)
    #expect(reports[0].issueSummary == "4 hooks missing")
    #expect(reports[1].issueSummary == "MCP linkedin · 2 hooks drifted")
    #expect(reports[2].state == .synced)
    #expect(reports[2].issueSummary == "")
    #expect(reports[3].state == .notInstalled)
    #expect(reports[4].state == .unreadable)
    #expect(reports[4].issueSummary == "config unreadable")
}

@Test func decodesAgentsViewSkillAnalytics() throws {
    let json = """
    {"total_skill_calls":3,"distinct_skills":2,"by_skill":[
      {"skill_name":"tech-search","call_count":2,"session_count":2,
       "agent_breakdown":[{"agent":"claude","count":2}],"last_used_at":"2026-10-05T12:02:21.572Z","pct":66.7},
      {"skill_name":"anthropic-skills:docx","call_count":1,"session_count":1,"last_used_at":"2026-10-01T10:10:47Z"}]}
    """
    let analytics = try Decoders.snakeCase.decode(SkillAnalytics.self, from: Data(json.utf8))
    #expect(analytics.bySkill.count == 2)
    #expect(analytics.bySkill[0].agentBreakdown?.first?.count == 2)
    #expect(analytics.bySkill[0].lastUsedAt != nil)
    #expect(analytics.bySkill[1].lastUsedAt != nil)
}
