using System.Globalization;
using System.Text.Json;

namespace AgentDock.ControlPanel;

internal enum ExternalAgentClientKind
{
    Codex,
    ClaudeCode,
    GenericMcp,
    AgentSetupPrompt,
}

internal static class ExternalAgentAccess
{
    internal const string ServerName = "agentdock";

    internal static bool TryNormalizeEndpoint(string? value, out string normalizedEndpoint)
    {
        normalizedEndpoint = "";
        if (string.IsNullOrWhiteSpace(value) ||
            !Uri.TryCreate(value.Trim(), UriKind.Absolute, out var uri) ||
            (uri.Scheme != Uri.UriSchemeHttp && uri.Scheme != Uri.UriSchemeHttps) ||
            string.IsNullOrWhiteSpace(uri.Host) ||
            !string.IsNullOrEmpty(uri.UserInfo) ||
            !string.IsNullOrEmpty(uri.Query) ||
            !string.IsNullOrEmpty(uri.Fragment))
        {
            return false;
        }

        var path = uri.AbsolutePath.TrimEnd('/');
        if (!path.EndsWith("/mcp", StringComparison.OrdinalIgnoreCase))
        {
            return false;
        }

        normalizedEndpoint = uri.GetLeftPart(UriPartial.Path).TrimEnd('/');
        return normalizedEndpoint.Length > 0;
    }

    internal static string BuildConfiguration(
        ExternalAgentClientKind client,
        string endpoint,
        string agentSetupPromptTemplate)
    {
        if (!TryNormalizeEndpoint(endpoint, out var normalizedEndpoint))
        {
            throw new ArgumentException("A valid AgentDock MCP endpoint is required.", nameof(endpoint));
        }

        return client switch
        {
            ExternalAgentClientKind.Codex => BuildCodexConfiguration(normalizedEndpoint),
            ExternalAgentClientKind.ClaudeCode => BuildClaudeCodeConfiguration(normalizedEndpoint),
            ExternalAgentClientKind.GenericMcp => BuildGenericMcpConfiguration(normalizedEndpoint),
            ExternalAgentClientKind.AgentSetupPrompt => BuildAgentSetupPrompt(normalizedEndpoint, agentSetupPromptTemplate),
            _ => throw new ArgumentOutOfRangeException(nameof(client), client, "Unsupported external agent client."),
        };
    }

    private static string BuildCodexConfiguration(string endpoint)
    {
        var quotedEndpoint = QuoteCommandArgument(endpoint);
        var tomlEndpoint = endpoint.Replace("\\", "\\\\", StringComparison.Ordinal)
            .Replace("\"", "\\\"", StringComparison.Ordinal);
        return $$"""
            # Codex CLI (user configuration)
            codex mcp add {{ServerName}} --url {{quotedEndpoint}}
            codex mcp login {{ServerName}}

            # Equivalent ~/.codex/config.toml entry
            [mcp_servers.{{ServerName}}]
            url = "{{tomlEndpoint}}"
            auth = "oauth"
            """;
    }

    private static string BuildClaudeCodeConfiguration(string endpoint)
    {
        return $$"""
            claude mcp add --scope user --transport http {{ServerName}} {{QuoteCommandArgument(endpoint)}}

            # Start Claude Code, run /mcp, select "{{ServerName}}", then choose Authenticate.
            """;
    }

    private static string BuildGenericMcpConfiguration(string endpoint)
    {
        var jsonEndpoint = JsonSerializer.Serialize(endpoint);
        return $$"""
            {
              "mcpServers": {
                "{{ServerName}}": {
                  "type": "http",
                  "url": {{jsonEndpoint}}
                }
              }
            }
            """;
    }

    private static string BuildAgentSetupPrompt(string endpoint, string template)
    {
        if (string.IsNullOrWhiteSpace(template))
        {
            throw new ArgumentException("The agent setup prompt template is required.", nameof(template));
        }

        return string.Format(CultureInfo.CurrentCulture, template, endpoint);
    }

    private static string QuoteCommandArgument(string value)
    {
        return $"\"{value.Replace("\"", "\\\"", StringComparison.Ordinal)}\"";
    }
}
