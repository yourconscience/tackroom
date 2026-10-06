import Foundation
import Testing
@testable import Tackroom

/// Writes a small shell script that stands in for an MCP server.
private func fakeServer(_ body: String) throws -> (path: String, cleanup: () -> Void) {
    let dir = FileManager.default.temporaryDirectory.appendingPathComponent("probe-\(UUID().uuidString)")
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    let file = dir.appendingPathComponent("server.sh")
    try ("#!/bin/sh\n" + body).write(to: file, atomically: true, encoding: .utf8)
    try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: file.path)
    return (file.path, { try? FileManager.default.removeItem(at: dir) })
}

private let hello = #"{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"fake","version":"1.2.3"}}}"#

private func answering(tools: String) -> String {
    """
    while IFS= read -r line; do
      case "$line" in
        *'"method":"initialize"'*) echo '\(hello)' ;;
        *'"method":"tools/list"'*) \(tools) ;;
      esac
    done
    """
}

@Test func probeReadsTheServerNameVersionAndToolCount() async throws {
    let server = try fakeServer(answering(tools: #"echo '{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"a"},{"name":"b"},{"name":"c"}]}}'"#))
    defer { server.cleanup() }
    let outcome = await MCPProbe.run(command: server.path, args: [], timeout: 5)
    #expect(outcome == .ok(server: "fake", version: "1.2.3", tools: 3))
    #expect(outcome.text == "fake 1.2.3: 3 tools")
}

@Test func probeFollowsToolPages() async throws {
    let tools = #"""
    case "$line" in
          *'"cursor":"p2"'*) echo '{"jsonrpc":"2.0","id":3,"result":{"tools":[{"name":"c"}]}}' ;;
          *) echo '{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"a"},{"name":"b"}],"nextCursor":"p2"}}' ;;
        esac
    """#
    let server = try fakeServer(answering(tools: tools))
    defer { server.cleanup() }
    #expect(await MCPProbe.run(command: server.path, args: [], timeout: 5) == .ok(server: "fake", version: "1.2.3", tools: 3))
}

@Test func probeSkipsLogLinesAndNotifications() async throws {
    let body = """
    echo 'starting up...'
    echo '{"jsonrpc":"2.0","method":"notifications/message","params":{}}'
    \(answering(tools: #"echo '{"jsonrpc":"2.0","id":2,"result":{"tools":[]}}'"#))
    """
    let server = try fakeServer(body)
    defer { server.cleanup() }
    let outcome = await MCPProbe.run(command: server.path, args: [], timeout: 5)
    #expect(outcome == .ok(server: "fake", version: "1.2.3", tools: 0))
    #expect(outcome.text == "fake 1.2.3: 0 tools")
}

@Test func probeReportsAnErrorReply() async throws {
    let server = try fakeServer(#"read -r line; echo '{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"nope"}}'; sleep 5"#)
    defer { server.cleanup() }
    #expect(await MCPProbe.run(command: server.path, args: [], timeout: 5) == .failed("initialize failed: nope"))
}

@Test func probeGivesUpAfterTheTimeout() async throws {
    let server = try fakeServer("exec sleep 30")
    defer { server.cleanup() }
    #expect(await MCPProbe.run(command: server.path, args: [], timeout: 1) == .failed("No answer within 1 s."))
}

@Test func probeSaysWhyAServerStoppedEarly() async throws {
    let server = try fakeServer("echo 'boom: missing TOKEN' >&2; exit 3")
    defer { server.cleanup() }
    #expect(await MCPProbe.run(command: server.path, args: [], timeout: 5) == .failed("The server stopped before it answered: boom: missing TOKEN"))
}

@Test func probeReportsAMissingCommand() async {
    #expect(await MCPProbe.run(command: "/nonexistent/mcp-server", args: [], timeout: 1) == .failed("/nonexistent/mcp-server was not found."))
    #expect(await MCPProbe.run(command: "definitely-not-a-real-command-4821", args: [], timeout: 1).text.hasSuffix("was not found."))
}

@Test func probeBuildsPagedToolRequests() {
    #expect(MCPProbe.toolsRequest(id: 2, cursor: nil) == #"{"jsonrpc":"2.0","id":2,"method":"tools/list"}"#)
    #expect(MCPProbe.toolsRequest(id: 3, cursor: "p2") == #"{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{"cursor":"p2"}}"#)
}
