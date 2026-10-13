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

enum ExternalAgentAccessError: Error, Equatable {
    case invalidEndpoint
    case invalidPromptTemplate
}

enum ExternalAgentAccess {
    static let serverName = "agentdock"

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
        agentSetupPromptTemplate: String
    ) throws -> String {
        guard let endpoint = normalizedEndpoint(endpoint) else {
            throw ExternalAgentAccessError.invalidEndpoint
        }

        switch client {
        case .codex:
            return """
            # Codex CLI (user configuration)
            codex mcp add \(serverName) --url "\(endpoint)"
            codex mcp login \(serverName)

            # Equivalent ~/.codex/config.toml entry
            [mcp_servers.\(serverName)]
            url = "\(endpoint)"
            auth = "oauth"
            """
        case .claudeCode:
            return """
            claude mcp add --scope user --transport http \(serverName) "\(endpoint)"

            # Start Claude Code, run /mcp, select "\(serverName)", then choose Authenticate.
            """
        case .genericMCP:
            let object: [String: Any] = [
                "mcpServers": [
                    serverName: [
                        "type": "http",
                        "url": endpoint,
                    ],
                ],
            ]
            let data = try JSONSerialization.data(withJSONObject: object, options: [.prettyPrinted, .sortedKeys])
            guard let text = String(data: data, encoding: .utf8) else {
                throw ExternalAgentAccessError.invalidEndpoint
            }
            return text
        case .agentSetupPrompt:
            guard agentSetupPromptTemplate.contains("%@") else {
                throw ExternalAgentAccessError.invalidPromptTemplate
            }
            return String(format: agentSetupPromptTemplate, locale: Locale.current, arguments: [endpoint])
        }
    }
}
