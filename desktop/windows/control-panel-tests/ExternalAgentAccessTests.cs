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

        const string promptTemplate = "Connect {0}; call agentdock_context once.";
        var codex = ExternalAgentAccess.BuildConfiguration(ExternalAgentClientKind.Codex, publicEndpoint, promptTemplate);
        check(codex.Contains("codex mcp add agentdock --url \"https://agentdock.example.com/mcp\"", StringComparison.Ordinal),
            "Codex MCP add command");
        check(codex.Contains("codex mcp login agentdock", StringComparison.Ordinal) &&
              codex.Contains("[mcp_servers.agentdock]", StringComparison.Ordinal) &&
              codex.Contains("auth = \"oauth\"", StringComparison.Ordinal),
            "Codex OAuth configuration");

        var claude = ExternalAgentAccess.BuildConfiguration(ExternalAgentClientKind.ClaudeCode, publicEndpoint, promptTemplate);
        check(claude.Contains("claude mcp add --scope user --transport http agentdock \"https://agentdock.example.com/mcp\"", StringComparison.Ordinal),
            "Claude Code HTTP MCP command");
        check(claude.Contains("/mcp", StringComparison.Ordinal) && claude.Contains("Authenticate", StringComparison.Ordinal),
            "Claude Code OAuth guidance");

        var generic = ExternalAgentAccess.BuildConfiguration(ExternalAgentClientKind.GenericMcp, publicEndpoint, promptTemplate);
        using (var document = JsonDocument.Parse(generic))
        {
            var agentdock = document.RootElement.GetProperty("mcpServers").GetProperty("agentdock");
            check(agentdock.GetProperty("type").GetString() == "http", "generic MCP transport");
            check(agentdock.GetProperty("url").GetString() == publicEndpoint, "generic MCP endpoint");
        }

        var prompt = ExternalAgentAccess.BuildConfiguration(ExternalAgentClientKind.AgentSetupPrompt, publicEndpoint, promptTemplate);
        check(prompt == "Connect https://agentdock.example.com/mcp; call agentdock_context once.",
            "agent setup prompt substitution");
        check(!codex.Contains("Bearer ", StringComparison.OrdinalIgnoreCase) &&
              !claude.Contains("Bearer ", StringComparison.OrdinalIgnoreCase) &&
              !generic.Contains("Authorization", StringComparison.OrdinalIgnoreCase),
            "generated configurations do not embed credentials");
    }
}
