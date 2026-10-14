import Foundation

enum ExternalAgentClientKind: Int, CaseIterable {
    case codex
    case claudeCode
    case genericMCP
    case agentSetupPrompt
}

enum ExternalAgentEndpointSource: Int, CaseIterable {
    case local
    case publicAccess
}

enum ExternalAgentAuthMode: String, Equatable {
    case oauth
    case bearer
    case none
}

enum ExternalAgentAccessError: Error, Equatable {
    case invalidEndpoint
    case invalidPromptTemplate
}

enum ExternalAgentAccess {
    static let serverName = "agentdock"
    static let bearerEnvironmentVariable = "AGENTDOCK_MCP_BEARER"

    static func authMode(oauthEnabled: Bool, authRequired: Bool) -> ExternalAgentAuthMode {
        if oauthEnabled { return .oauth }
        return authRequired ? .bearer : .none
    }

    static func normalizedEndpoint(_ value: String?) -> String? {
        guard let value else { return nil }
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty,
              var components = URLComponents(string: trimmed),
              let scheme = components.scheme?.lowercased(),
              scheme == "http" || scheme == "https",
              let host = components.host,
              !host.isEmpty,
              components.user == nil,
              components.password == nil,
              components.query == nil,
              components.fragment == nil
        else {
            return nil
        }

        let path = components.percentEncodedPath.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        let lowercasePath = path.lowercased()
        guard lowercasePath == "mcp" || lowercasePath.hasSuffix("/mcp") else { return nil }

        components.scheme = scheme
        components.percentEncodedPath = "/" + path
        guard let endpoint = components.url?.absoluteString else { return nil }
        return endpoint.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
    }

    static func configuration(
        for client: ExternalAgentClientKind,
        endpoint: String,
        authMode: ExternalAgentAuthMode,
        agentSetupPromptTemplate: String,
        authenticationInstruction: String
    ) throws -> String {
        guard let endpoint = normalizedEndpoint(endpoint) else {
            throw ExternalAgentAccessError.invalidEndpoint
        }

        switch client {
        case .codex:
            return codexConfiguration(endpoint: endpoint, authMode: authMode)
        case .claudeCode:
            return try claudeCodeConfiguration(endpoint: endpoint, authMode: authMode)
        case .genericMCP:
            return try mcpJSONConfiguration(endpoint: endpoint, authMode: authMode)
        case .agentSetupPrompt:
            guard agentSetupPromptTemplate.components(separatedBy: "%@").count >= 3,
                  !authenticationInstruction.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
                throw ExternalAgentAccessError.invalidPromptTemplate
            }
            return String(
                format: agentSetupPromptTemplate,
                locale: Locale.current,
                arguments: [endpoint, authenticationInstruction]
            )
        }
    }

    private static func codexConfiguration(endpoint: String, authMode: ExternalAgentAuthMode) -> String {
        var lines = [
            "# Codex CLI (user configuration)",
            "codex mcp add \(serverName) --url \"\(endpoint)\"",
        ]
        if authMode == .oauth {
            lines.append("codex mcp login \(serverName)")
        }
        lines.append(contentsOf: [
            "",
            "# Equivalent ~/.codex/config.toml entry",
            "[mcp_servers.\(serverName)]",
            "url = \"\(endpoint)\"",
        ])
        if authMode == .bearer {
            lines.append("bearer_token_env_var = \"\(bearerEnvironmentVariable)\"")
        }
        return lines.joined(separator: "\n")
    }

    private static func claudeCodeConfiguration(
        endpoint: String,
        authMode: ExternalAgentAuthMode
    ) throws -> String {
        if authMode == .bearer {
            return try mcpJSONConfiguration(endpoint: endpoint, authMode: authMode)
        }
        let command = "claude mcp add --scope user --transport http \(serverName) \"\(endpoint)\""
        if authMode == .oauth {
            return command + "\n\n# Start Claude Code, run /mcp, select \"\(serverName)\", then choose Authenticate."
        }
        return command
    }

    private static func mcpJSONConfiguration(
        endpoint: String,
        authMode: ExternalAgentAuthMode
    ) throws -> String {
        var server: [String: Any] = [
            "type": "http",
            "url": endpoint,
        ]
        if authMode == .bearer {
            server["headers"] = [
                "Authorization": "Bearer ${\(bearerEnvironmentVariable)}",
            ]
        }
        let object: [String: Any] = [
            "mcpServers": [
                serverName: server,
            ],
        ]
        let data = try JSONSerialization.data(withJSONObject: object, options: [.prettyPrinted, .sortedKeys])
        guard let text = String(data: data, encoding: .utf8) else {
            throw ExternalAgentAccessError.invalidEndpoint
        }
        return text
    }
}
