import XCTest
@testable import WorkbenchKit

final class ExternalAgentAccessTests: XCTestCase {
    private let endpoint = "https://agentdock.example.com/mcp"
    private let localEndpoint = "http://127.0.0.1:8765/mcp"
    private let promptTemplate = "Connect %@. %@ Then call agentdock_context once."
    private let oauthInstruction = "Use OAuth discovery."

    func testEndpointValidationAndNormalization() {
        XCTAssertEqual(
            ExternalAgentAccess.normalizedEndpoint("  http://127.0.0.1:8765/mcp/  "),
            localEndpoint
        )
        XCTAssertEqual(ExternalAgentAccess.normalizedEndpoint(endpoint), endpoint)

        for invalid in [
            "",
            "ftp://agentdock.example.com/mcp",
            "https://user:password@agentdock.example.com/mcp",
            "https://agentdock.example.com/mcp?token=secret",
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
            authMode: .oauth,
            agentSetupPromptTemplate: promptTemplate,
            authenticationInstruction: oauthInstruction
        )
        XCTAssertTrue(text.contains("codex mcp add agentdock --url \"\(endpoint)\""))
        XCTAssertTrue(text.contains("codex mcp login agentdock"))
        XCTAssertTrue(text.contains("[mcp_servers.agentdock]"))
        XCTAssertTrue(text.contains("url = \"\(endpoint)\""))
        XCTAssertFalse(text.contains("auth = \"oauth\""))
        XCTAssertFalse(text.localizedCaseInsensitiveContains("Bearer "))
        XCTAssertFalse(text.localizedCaseInsensitiveContains("Authorization"))
    }

    func testClaudeCodeConfigurationUsesHTTPAndOAuthFlow() throws {
        let text = try ExternalAgentAccess.configuration(
            for: .claudeCode,
            endpoint: endpoint,
            authMode: .oauth,
            agentSetupPromptTemplate: promptTemplate,
            authenticationInstruction: oauthInstruction
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
            authMode: .oauth,
            agentSetupPromptTemplate: promptTemplate,
            authenticationInstruction: oauthInstruction
        )
        let server = try mcpServer(from: text)
        XCTAssertEqual(server["type"] as? String, "http")
        XCTAssertEqual(server["url"] as? String, endpoint)
        XCTAssertNil(server["headers"])
    }

    func testBearerConfigurationsReferenceEnvironmentWithoutEmbeddingCredential() throws {
        let codex = try ExternalAgentAccess.configuration(
            for: .codex,
            endpoint: localEndpoint,
            authMode: .bearer,
            agentSetupPromptTemplate: promptTemplate,
            authenticationInstruction: "Use the bearer environment variable."
        )
        XCTAssertTrue(codex.contains("bearer_token_env_var = \"AGENTDOCK_MCP_BEARER\""))
        XCTAssertFalse(codex.contains("codex mcp login"))

        let claude = try ExternalAgentAccess.configuration(
            for: .claudeCode,
            endpoint: localEndpoint,
            authMode: .bearer,
            agentSetupPromptTemplate: promptTemplate,
            authenticationInstruction: "Use the bearer environment variable."
        )
        let server = try mcpServer(from: claude)
        let headers = try XCTUnwrap(server["headers"] as? [String: String])
        XCTAssertEqual(headers["Authorization"], "Bearer ${AGENTDOCK_MCP_BEARER}")
        XCTAssertFalse(claude.contains("actual-secret"))
    }

    func testAnonymousConfigurationOmitsLoginAndBearerReference() throws {
        let text = try ExternalAgentAccess.configuration(
            for: .codex,
            endpoint: localEndpoint,
            authMode: .none,
            agentSetupPromptTemplate: promptTemplate,
            authenticationInstruction: "No authentication is required."
        )
        XCTAssertFalse(text.contains("codex mcp login"))
        XCTAssertFalse(text.contains("bearer_token_env_var"))
    }

    func testAgentSetupPromptSubstitutesEndpointAndAuthenticationInstruction() throws {
        let text = try ExternalAgentAccess.configuration(
            for: .agentSetupPrompt,
            endpoint: endpoint,
            authMode: .oauth,
            agentSetupPromptTemplate: promptTemplate,
            authenticationInstruction: oauthInstruction
        )
        XCTAssertEqual(
            text,
            "Connect \(endpoint). Use OAuth discovery. Then call agentdock_context once."
        )
    }

    func testRuntimeConfigurationUsesCoreAuthenticationPrecedence() throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("AgentDockExternalAgentAuthTests-\(UUID().uuidString)", isDirectory: true)
        defer { try? FileManager.default.removeItem(at: root) }
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        let environmentURL = root.appendingPathComponent("agentdock.env")
        let cases: [(name: String, oauthEnabled: Bool, authToken: String, expected: ExternalAgentAuthMode)] = [
            ("oauth", true, "token", .oauth),
            ("bearer", false, "token", .bearer),
            ("none", false, "", .none),
        ]

        for testCase in cases {
            let environment = """
            AGENTDOCK_HOST='127.0.0.1'
            AGENTDOCK_PORT='8765'
            AGENTDOCK_AUTH_TOKEN='\(testCase.authToken)'
            AGENTDOCK_OAUTH_ENABLED='\(testCase.oauthEnabled ? "true" : "false")'
            """
            try Data(environment.utf8).write(to: environmentURL, options: .atomic)
            let configuration = try XCTUnwrap(ServiceConfiguration.load(from: environmentURL), testCase.name)
            XCTAssertEqual(configuration.oauthEnabled, testCase.oauthEnabled, testCase.name)
            XCTAssertEqual(configuration.authRequired, testCase.expected != .none, testCase.name)
            XCTAssertEqual(
                ExternalAgentAccess.authMode(
                    oauthEnabled: configuration.oauthEnabled,
                    authRequired: configuration.authRequired
                ),
                testCase.expected,
                testCase.name
            )
        }
    }

    private func mcpServer(from text: String) throws -> [String: Any] {
        let data = try XCTUnwrap(text.data(using: .utf8))
        let root = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let servers = try XCTUnwrap(root["mcpServers"] as? [String: Any])
        return try XCTUnwrap(servers["agentdock"] as? [String: Any])
    }
}
