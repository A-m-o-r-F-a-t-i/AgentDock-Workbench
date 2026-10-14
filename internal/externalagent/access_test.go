package externalagent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeEndpoint(t *testing.T) {
	t.Parallel()

	valid := map[string]string{
		"  http://127.0.0.1:8765/mcp/  ":            "http://127.0.0.1:8765/mcp",
		"HTTPS://agentdock.example.com/gateway/mcp": "https://agentdock.example.com/gateway/mcp",
		"http://[::1]:8765/mcp":                     "http://[::1]:8765/mcp",
	}
	for input, expected := range valid {
		actual, err := NormalizeEndpoint(input)
		if err != nil {
			t.Fatalf("NormalizeEndpoint(%q): %v", input, err)
		}
		if actual != expected {
			t.Fatalf("NormalizeEndpoint(%q) = %q, want %q", input, actual, expected)
		}
	}

	for _, input := range []string{
		"",
		"ftp://agentdock.example.com/mcp",
		"https://user:password@agentdock.example.com/mcp",
		"https://agentdock.example.com/mcp?token=secret",
		"https://agentdock.example.com/mcp#fragment",
		"https://agentdock.example.com/healthz",
	} {
		if _, err := NormalizeEndpoint(input); err == nil {
			t.Fatalf("NormalizeEndpoint(%q) unexpectedly succeeded", input)
		}
	}
}

func TestEndpointDerivation(t *testing.T) {
	t.Parallel()

	local, err := LocalEndpoint("0.0.0.0", 8765)
	if err != nil {
		t.Fatal(err)
	}
	if local != "http://127.0.0.1:8765/mcp" {
		t.Fatalf("unexpected local endpoint: %s", local)
	}
	ipv6, err := LocalEndpoint("::1", 8765)
	if err != nil {
		t.Fatal(err)
	}
	if ipv6 != "http://[::1]:8765/mcp" {
		t.Fatalf("unexpected IPv6 endpoint: %s", ipv6)
	}
	public, err := PublicEndpoint("https://agentdock.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if public != "https://agentdock.example.com/mcp" {
		t.Fatalf("unexpected public endpoint: %s", public)
	}
}

func TestBuildCodexOAuthConfigurationUsesCurrentContract(t *testing.T) {
	t.Parallel()

	access, err := Build(ClientCodex, "https://agentdock.example.com/mcp", AuthOAuth)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"codex mcp add agentdock --url \"https://agentdock.example.com/mcp\"",
		"codex mcp login agentdock",
		"[mcp_servers.agentdock]",
		"url = \"https://agentdock.example.com/mcp\"",
	} {
		if !strings.Contains(access.Configuration, expected) {
			t.Fatalf("Codex configuration missing %q:\n%s", expected, access.Configuration)
		}
	}
	if strings.Contains(access.Configuration, "auth = \"oauth\"") {
		t.Fatalf("Codex configuration contains unsupported auth field:\n%s", access.Configuration)
	}
	if access.CredentialsIncluded {
		t.Fatal("generated configuration must not contain credentials")
	}
}

func TestBuildBearerConfigurationsUseEnvironmentReference(t *testing.T) {
	t.Parallel()

	codex, err := Build(ClientCodex, "http://127.0.0.1:8765/mcp", AuthBearer)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(codex.Configuration, "bearer_token_env_var = \""+BearerEnvironmentVariable+"\"") {
		t.Fatalf("Codex bearer configuration is missing environment reference:\n%s", codex.Configuration)
	}
	if strings.Contains(codex.Configuration, "codex mcp login") {
		t.Fatalf("bearer configuration must not request OAuth login:\n%s", codex.Configuration)
	}

	claude, err := Build(ClientClaudeCode, "http://127.0.0.1:8765/mcp", AuthBearer)
	if err != nil {
		t.Fatal(err)
	}
	var claudeConfig struct {
		MCPServers map[string]struct {
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(claude.Configuration), &claudeConfig); err != nil {
		t.Fatalf("Claude Code bearer configuration is invalid JSON: %v\n%s", err, claude.Configuration)
	}
	if claudeConfig.MCPServers[ServerName].Headers["Authorization"] != "Bearer ${"+BearerEnvironmentVariable+"}" {
		t.Fatalf("Claude Code bearer configuration is missing environment reference:\n%s", claude.Configuration)
	}
	if claude.CredentialsIncluded {
		t.Fatal("generated Claude Code configuration must not contain credentials")
	}
}

func TestBuildGenericMCPConfigurationIsValidJSON(t *testing.T) {
	t.Parallel()

	access, err := Build(ClientGenericMCP, "https://agentdock.example.com/mcp", AuthOAuth)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCPServers map[string]struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(access.Configuration), &config); err != nil {
		t.Fatalf("generic configuration is invalid JSON: %v\n%s", err, access.Configuration)
	}
	server := config.MCPServers[ServerName]
	if server.Type != "http" || server.URL != access.Endpoint {
		t.Fatalf("unexpected generic MCP configuration: %#v", server)
	}
}

func TestDescribeDoesNotExposeCredentials(t *testing.T) {
	t.Parallel()

	status := Describe(
		"http://127.0.0.1:8765/mcp",
		"https://agentdock.example.com/mcp",
		AuthOAuth,
	)
	if status.SchemaVersion != SchemaVersion || status.Protocol != Protocol || status.ConfigurationCommand != "agentdock external-agent" {
		t.Fatalf("unexpected status: %#v", status)
	}
	if status.CredentialsIncluded || len(status.SupportedClients) != 4 {
		t.Fatalf("unsafe or incomplete status: %#v", status)
	}
}
