using System.Text.Json;
using AgentDock.ControlPanel;

internal static class ExternalAgentAccessTests
{
    internal static void Run(Action<bool, string> check)
    {
        check(
            ExternalAgentAccess.TryNormalizeEndpoint("  http://127.0.0.1:8765/mcp/  ", out var localEndpoint) &&
            localEndpoint == "http://127.0.0.1:8765/mcp",
            "local MCP endpoint normalization");
        check(
            ExternalAgentAccess.TryNormalizeEndpoint("https://agentdock.example.com/mcp", out var publicEndpoint) &&
            publicEndpoint == "https://agentdock.example.com/mcp",
            "public MCP endpoint normalization");

        foreach (var invalid in new[]
                 {
                     "",
                     "ftp://agentdock.example.com/mcp",
                     "https://user:pass@agentdock.example.com/mcp",
                     "https://agentdock.example.com/mcp?token=value",
                     "https://agentdock.example.com/mcp#fragment",
                     "https://agentdock.example.com/healthz",
                 })
        {
            check(!ExternalAgentAccess.TryNormalizeEndpoint(invalid, out _), "reject unsafe or non-MCP endpoint: " + invalid);
        }

        const string promptTemplate = "Connect {0}. {1} Then call agentdock_context once.";
        const string oauthInstruction = "Use OAuth discovery.";
        var codex = ExternalAgentAccess.BuildConfiguration(
            ExternalAgentClientKind.Codex,
            publicEndpoint,
            ExternalAgentAuthMode.OAuth,
            promptTemplate,
            oauthInstruction);
        check(codex.Contains("codex mcp add agentdock --url \"https://agentdock.example.com/mcp\"", StringComparison.Ordinal),
            "Codex MCP add command");
        check(codex.Contains("codex mcp login agentdock", StringComparison.Ordinal) &&
              codex.Contains("[mcp_servers.agentdock]", StringComparison.Ordinal) &&
              codex.Contains("url = \"https://agentdock.example.com/mcp\"", StringComparison.Ordinal) &&
              !codex.Contains("auth = \"oauth\"", StringComparison.Ordinal),
            "Codex OAuth configuration");

        var claude = ExternalAgentAccess.BuildConfiguration(
            ExternalAgentClientKind.ClaudeCode,
            publicEndpoint,
            ExternalAgentAuthMode.OAuth,
            promptTemplate,
            oauthInstruction);
        check(claude.Contains("claude mcp add --scope user --transport http agentdock \"https://agentdock.example.com/mcp\"", StringComparison.Ordinal),
            "Claude Code HTTP MCP command");
        check(claude.Contains("/mcp", StringComparison.Ordinal) && claude.Contains("Authenticate", StringComparison.Ordinal),
            "Claude Code OAuth guidance");

        var generic = ExternalAgentAccess.BuildConfiguration(
            ExternalAgentClientKind.GenericMcp,
            publicEndpoint,
            ExternalAgentAuthMode.OAuth,
            promptTemplate,
            oauthInstruction);
        using (var document = JsonDocument.Parse(generic))
        {
            var agentdock = document.RootElement.GetProperty("mcpServers").GetProperty("agentdock");
            check(agentdock.GetProperty("type").GetString() == "http", "generic MCP transport");
            check(agentdock.GetProperty("url").GetString() == publicEndpoint, "generic MCP endpoint");
            check(!agentdock.TryGetProperty("headers", out _), "OAuth generic MCP omits bearer headers");
        }

        var bearerCodex = ExternalAgentAccess.BuildConfiguration(
            ExternalAgentClientKind.Codex,
            localEndpoint,
            ExternalAgentAuthMode.Bearer,
            promptTemplate,
            "Use the bearer environment variable.");
        check(bearerCodex.Contains("bearer_token_env_var = \"AGENTDOCK_MCP_BEARER\"", StringComparison.Ordinal) &&
              !bearerCodex.Contains("codex mcp login", StringComparison.Ordinal),
            "Codex bearer configuration");
        var bearerClaude = ExternalAgentAccess.BuildConfiguration(
            ExternalAgentClientKind.ClaudeCode,
            localEndpoint,
            ExternalAgentAuthMode.Bearer,
            promptTemplate,
            "Use the bearer environment variable.");
        using (var document = JsonDocument.Parse(bearerClaude))
        {
            var authorization = document.RootElement.GetProperty("mcpServers").GetProperty("agentdock")
                .GetProperty("headers").GetProperty("Authorization").GetString();
            check(authorization == "Bearer ${AGENTDOCK_MCP_BEARER}", "Claude Code bearer environment reference");
        }

        var anonymous = ExternalAgentAccess.BuildConfiguration(
            ExternalAgentClientKind.Codex,
            localEndpoint,
            ExternalAgentAuthMode.None,
            promptTemplate,
            "No authentication is required.");
        check(!anonymous.Contains("codex mcp login", StringComparison.Ordinal) &&
              !anonymous.Contains("bearer_token_env_var", StringComparison.Ordinal),
            "anonymous Codex configuration");

        var prompt = ExternalAgentAccess.BuildConfiguration(
            ExternalAgentClientKind.AgentSetupPrompt,
            publicEndpoint,
            ExternalAgentAuthMode.OAuth,
            promptTemplate,
            oauthInstruction);
        check(prompt == "Connect https://agentdock.example.com/mcp. Use OAuth discovery. Then call agentdock_context once.",
            "agent setup prompt substitution");
        check(ExternalAgentAccess.ParseAuthMode("oauth") == ExternalAgentAuthMode.OAuth &&
              ExternalAgentAccess.ParseAuthMode("none") == ExternalAgentAuthMode.None &&
              ExternalAgentAccess.ParseAuthMode("bearer") == ExternalAgentAuthMode.Bearer,
            "runtime auth mode parsing");
        check(!codex.Contains("Bearer ", StringComparison.OrdinalIgnoreCase) &&
              !claude.Contains("Bearer ", StringComparison.OrdinalIgnoreCase) &&
              !generic.Contains("Authorization", StringComparison.OrdinalIgnoreCase) &&
              !bearerClaude.Contains("actual-secret", StringComparison.Ordinal),
            "generated configurations do not embed credentials");
    }
}
