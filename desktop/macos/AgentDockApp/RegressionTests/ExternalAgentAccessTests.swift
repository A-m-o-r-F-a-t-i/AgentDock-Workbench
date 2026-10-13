import XCTest
@testable import WorkbenchKit

final class ExternalAgentAccessTests: XCTestCase {
    private let endpoint = "https://agentdock.example.com/mcp"
    private let promptTemplate = "Connect %@; call agentdock_context once."

    func testEndpointValidationAndNormalization() {
        XCTAssertEqual(
            ExternalAgentAccess.normalizedEndpoint("  http://127.0.0.1:8765/mcp/  "),
            "http://127.0.0.1:8765/mcp"
        )
        XCTAssertEqual(ExternalAgentAccess.normalizedEndpoint(endpoint), endpoint)

        for invalid in [
            "",
            "ftp://agentdock.example.com/mcp",
            "https://user:pass@agentdock.example.com/mcp",
            "https://agentdock.example.com/mcp?token=value",
            "https://agentdock.example.com/mcp#fragment",
            "https://agentdock.example.com/healthz",
            "https://agentdock.example.com/not-mcp",
        ] {
            XCTAssertNil(ExternalAgentAccess.normalizedEndpoint(invalid), invalid)
        }
    }

    func testCodexConfigurationUsesOAuthWithoutEmbeddingCredentials() throws {
        let text = try ExternalAgentAccess.configuration(
            for: .codex,
            endpoint: endpoint,
            agentSetupPromptTemplate: promptTemplate
        )
        XCTAssertTrue(text.contains("codex mcp add agentdock --url \"\(endpoint)\""))
        XCTAssertTrue(text.contains("codex mcp login agentdock"))
        XCTAssertTrue(text.contains("[mcp_servers.agentdock]"))
        XCTAssertTrue(text.contains("auth = \"oauth\""))
        XCTAssertFalse(text.localizedCaseInsensitiveContains("Bearer "))
        XCTAssertFalse(text.localizedCaseInsensitiveContains("Authorization"))
    }

    func testClaudeCodeConfigurationUsesHTTPAndOAuthFlow() throws {
        let text = try ExternalAgentAccess.configuration(
            for: .claudeCode,
            endpoint: endpoint,
            agentSetupPromptTemplate: promptTemplate
        )
        XCTAssertTrue(text.contains("claude mcp add --scope user --transport http agentdock \"\(endpoint)\""))
        XCTAssertTrue(text.contains("/mcp"))
        XCTAssertTrue(text.contains("Authenticate"))
        XCTAssertFalse(text.localizedCaseInsensitiveContains("Bearer "))
    }

    func testGenericMCPConfigurationIsValidJSON() throws {
        let text = try ExternalAgentAccess.configuration(
            for: .genericMCP,
            endpoint: endpoint,
            agentSetupPromptTemplate: promptTemplate
        )
        let data = try XCTUnwrap(text.data(using: .utf8))
        let root = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let servers = try XCTUnwrap(root["mcpServers"] as? [String: Any])
        let agentdock = try XCTUnwrap(servers["agentdock"] as? [String: String])
        XCTAssertEqual(agentdock["type"], "http")
        XCTAssertEqual(agentdock["url"], endpoint)
        XCTAssertFalse(text.localizedCaseInsensitiveContains("Authorization"))
    }

    func testAgentSetupPromptSubstitutesEndpoint() throws {
        let text = try ExternalAgentAccess.configuration(
            for: .agentSetupPrompt,
            endpoint: endpoint,
            agentSetupPromptTemplate: promptTemplate
        )
        XCTAssertEqual(text, "Connect \(endpoint); call agentdock_context once.")
    }
}
