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

internal enum ExternalAgentAuthMode
{
    OAuth,
    Bearer,
    None,
}

internal static class ExternalAgentAccess
{
    internal const string ServerName = "agentdock";
    internal const string BearerEnvironmentVariable = "AGENTDOCK_MCP_BEARER";

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

    internal static ExternalAgentAuthMode ParseAuthMode(string? value)
    {
        return value?.Trim().ToLowerInvariant() switch
        {
            "oauth" => ExternalAgentAuthMode.OAuth,
            "none" => ExternalAgentAuthMode.None,
            _ => ExternalAgentAuthMode.Bearer,
        };
    }

    internal static string BuildConfiguration(
        ExternalAgentClientKind client,
        string endpoint,
        ExternalAgentAuthMode authMode,
        string agentSetupPromptTemplate,
        string authenticationInstruction)
    {
        if (!TryNormalizeEndpoint(endpoint, out var normalizedEndpoint))
        {
            throw new ArgumentException("A valid AgentDock MCP endpoint is required.", nameof(endpoint));
        }

        return client switch
        {
            ExternalAgentClientKind.Codex => BuildCodexConfiguration(normalizedEndpoint, authMode),
            ExternalAgentClientKind.ClaudeCode => BuildClaudeCodeConfiguration(normalizedEndpoint, authMode),
            ExternalAgentClientKind.GenericMcp => BuildGenericMcpConfiguration(normalizedEndpoint, authMode),
            ExternalAgentClientKind.AgentSetupPrompt => BuildAgentSetupPrompt(
                normalizedEndpoint,
                agentSetupPromptTemplate,
                authenticationInstruction),
            _ => throw new ArgumentOutOfRangeException(nameof(client), client, "Unsupported external agent client."),
        };
    }

    private static string BuildCodexConfiguration(string endpoint, ExternalAgentAuthMode authMode)
    {
        var tomlEndpoint = endpoint.Replace("\\", "\\\\", StringComparison.Ordinal)
            .Replace("\"", "\\\"", StringComparison.Ordinal);
        var lines = new List<string>
        {
            "# Codex CLI (user configuration)",
            $"codex mcp add {ServerName} --url {QuoteCommandArgument(endpoint)}",
        };
        if (authMode == ExternalAgentAuthMode.OAuth)
        {
            lines.Add($"codex mcp login {ServerName}");
        }

        lines.AddRange([
            "",
            "# Equivalent ~/.codex/config.toml entry",
            $"[mcp_servers.{ServerName}]",
            $"url = \"{tomlEndpoint}\"",
        ]);
        if (authMode == ExternalAgentAuthMode.Bearer)
        {
            lines.Add($"bearer_token_env_var = \"{BearerEnvironmentVariable}\"");
        }
        return string.Join(Environment.NewLine, lines);
    }

    private static string BuildClaudeCodeConfiguration(string endpoint, ExternalAgentAuthMode authMode)
    {
        if (authMode == ExternalAgentAuthMode.Bearer)
        {
            return SerializeMcpConfiguration(endpoint, includeBearerHeader: true);
        }

        var command = $"claude mcp add --scope user --transport http {ServerName} {QuoteCommandArgument(endpoint)}";
        if (authMode == ExternalAgentAuthMode.OAuth)
        {
            return command + Environment.NewLine + Environment.NewLine +
                $"# Start Claude Code, run /mcp, select \"{ServerName}\", then choose Authenticate.";
        }
        return command;
    }

    private static string BuildGenericMcpConfiguration(string endpoint, ExternalAgentAuthMode authMode)
    {
        return SerializeMcpConfiguration(endpoint, authMode == ExternalAgentAuthMode.Bearer);
    }

    private static string SerializeMcpConfiguration(string endpoint, bool includeBearerHeader)
    {
        var server = new Dictionary<string, object>
        {
            ["type"] = "http",
            ["url"] = endpoint,
        };
        if (includeBearerHeader)
        {
            server["headers"] = new Dictionary<string, string>
            {
                ["Authorization"] = $"Bearer ${{{BearerEnvironmentVariable}}}",
            };
        }
        var configuration = new Dictionary<string, object>
        {
            ["mcpServers"] = new Dictionary<string, object>
            {
                [ServerName] = server,
            },
        };
        return JsonSerializer.Serialize(configuration, new JsonSerializerOptions { WriteIndented = true });
    }

    private static string BuildAgentSetupPrompt(
        string endpoint,
        string template,
        string authenticationInstruction)
    {
        if (string.IsNullOrWhiteSpace(template) ||
            string.IsNullOrWhiteSpace(authenticationInstruction) ||
            !template.Contains("{0}", StringComparison.Ordinal) ||
            !template.Contains("{1}", StringComparison.Ordinal))
        {
            throw new ArgumentException("The agent setup prompt template and authentication instruction are required.", nameof(template));
        }

        return string.Format(CultureInfo.CurrentCulture, template, endpoint, authenticationInstruction);
    }

    private static string QuoteCommandArgument(string value)
    {
        return $"\"{value.Replace("\"", "\\\"", StringComparison.Ordinal)}\"";
    }
}
