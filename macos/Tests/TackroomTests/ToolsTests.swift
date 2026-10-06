import Foundation
import Testing
@testable import Tackroom

// A fast command used to leave `Tools.run` waiting forever, so run many at once.
@Test func runCapturesOutputAndStatusUnderLoad() async throws {
    let statuses = try await withThrowingTaskGroup(of: (Int, Int32, String, String).self) { group in
        for i in 0..<300 {
            group.addTask {
                let output = try await Tools.run("/bin/sh", ["-c", "echo out\(i); echo err\(i) >&2; exit \(i % 3)"])
                return (i, output.status, output.text, output.stderr.trimmingCharacters(in: .whitespacesAndNewlines))
            }
        }
        var results: [(Int, Int32, String, String)] = []
        for try await result in group { results.append(result) }
        return results
    }
    #expect(statuses.count == 300)
    for (i, status, out, err) in statuses {
        #expect(status == Int32(i % 3))
        #expect(out == "out\(i)")
        #expect(err == "err\(i)")
    }
}

@Test func runReportsAMissingExecutable() async {
    await #expect(throws: (any Error).self) { try await Tools.run("/nonexistent/tool", []) }
}
