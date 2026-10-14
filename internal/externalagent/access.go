package externalagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

const (
	SchemaVersion = 1
	ServerName    = "agentdock"
	Protocol      = "mcp-streamable-http"
	EndpointPath  = "/mcp"

	BearerEnvironmentVariable = "AGENTDOCK_MCP_BEARER"
)

type Client string

const (
	ClientCodex       Client = "codex"
	ClientClaudeCode  Client = "claude-code"
	ClientGenericMCP  Client = "generic-mcp"
	ClientAgentPrompt Client = "agent-prompt"
)

type AuthMode string

const (
	AuthOAuth  AuthMode = "oauth"
	AuthBearer AuthMode = "bearer"
	AuthNone   AuthMode = "none"
)

type Access struct {
	SchemaVersion       int      `json:"schema_version"`
	ServerName          string   `json:"server_name"`
	Protocol            string   `json:"protocol"`
	Client              Client   `json:"client"`
	Endpoint            string   `json:"endpoint"`
	AuthMode            AuthMode `json:"auth_mode"`
	Configuration       string   `json:"configuration"`
	CredentialsIncluded bool     `json:"credentials_included"`
}

type Status struct {
	SchemaVersion        int      `json:"schema_version"`
	ServerName           string   `json:"server_name"`
	Protocol             string   `json:"protocol"`
	EndpointPath         string   `json:"endpoint_path"`
	LocalEndpoint        string   `json:"local_endpoint,omitempty"`
	PublicEndpoint       string   `json:"public_endpoint,omitempty"`
	AuthMode             AuthMode `json:"auth_mode"`
	SupportedClients     []Client `json:"supported_clients"`
	ConfigurationCommand string   `json:"configuration_command"`
	CredentialsIncluded  bool     `json:"credentials_included"`
}

func SupportedClients() []Client {
	return []Client{ClientCodex, ClientClaudeCode, ClientGenericMCP, ClientAgentPrompt}
}

func ParseClient(raw string) (Client, error) {
	switch Client(strings.ToLower(strings.TrimSpace(raw))) {
	case ClientCodex:
		return ClientCodex, nil
	case ClientClaudeCode, "claude", "claude_code":
		return ClientClaudeCode, nil
	case ClientGenericMCP, "generic", "mcp":
		return ClientGenericMCP, nil
	case ClientAgentPrompt, "prompt", "agent-setup-prompt":
		return ClientAgentPrompt, nil
	default:
		return "", fmt.Errorf("unsupported external agent client %q; expected codex, claude-code, generic-mcp, or agent-prompt", raw)
	}
}

func ParseAuthMode(raw string) (AuthMode, error) {
	switch AuthMode(strings.ToLower(strings.TrimSpace(raw))) {
	case AuthOAuth:
		return AuthOAuth, nil
	case AuthBearer:
		return AuthBearer, nil
	case AuthNone:
		return AuthNone, nil
	default:
		return "", fmt.Errorf("unsupported external agent auth mode %q; expected oauth, bearer, or none", raw)
	}
}

func AuthModeForRuntime(oauthEnabled, authRequired bool) AuthMode {
	if oauthEnabled {
		return AuthOAuth
	}
	if authRequired {
		return AuthBearer
	}
	return AuthNone
}

func NormalizeEndpoint(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("AgentDock MCP endpoint is required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Hostname() == "" {
		return "", fmt.Errorf("invalid AgentDock MCP endpoint %q", raw)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("AgentDock MCP endpoint must use http or https")
	}
	if parsed.Opaque != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("AgentDock MCP endpoint cannot contain user information, a query, or a fragment")
	}

	path := strings.TrimRight(parsed.Path, "/")
	if !strings.EqualFold(path, EndpointPath) && !strings.HasSuffix(strings.ToLower(path), EndpointPath) {
		return "", errors.New("AgentDock MCP endpoint path must end with /mcp")
	}
	parsed.Path = path
	parsed.RawPath = ""
	return parsed.String(), nil
}

func LocalEndpoint(host string, port int) (string, error) {
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("AgentDock port must be between 1 and 65535: %d", port)
	}
	host = strings.TrimSpace(strings.Trim(strings.TrimSpace(host), "[]"))
	switch strings.ToLower(host) {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return NormalizeEndpoint("http://" + net.JoinHostPort(host, strconv.Itoa(port)) + EndpointPath)
}

func PublicEndpoint(origin string) (string, error) {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return "", errors.New("AgentDock public origin is not configured")
	}
	if endpoint, err := NormalizeEndpoint(origin); err == nil {
		return endpoint, nil
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Hostname() == "" {
		return "", fmt.Errorf("invalid AgentDock public origin %q", origin)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("AgentDock public origin cannot contain user information, a query, or a fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("AgentDock public origin must not contain a path")
	}
	parsed.Path = EndpointPath
	parsed.RawPath = ""
	return NormalizeEndpoint(parsed.String())
}

func Build(client Client, endpoint string, authMode AuthMode) (Access, error) {
	parsedClient, err := ParseClient(string(client))
	if err != nil {
		return Access{}, err
	}
	parsedAuth, err := ParseAuthMode(string(authMode))
	if err != nil {
		return Access{}, err
	}
	normalizedEndpoint, err := NormalizeEndpoint(endpoint)
	if err != nil {
		return Access{}, err
	}
	configuration, err := buildConfiguration(parsedClient, normalizedEndpoint, parsedAuth)
	if err != nil {
		return Access{}, err
	}
	return Access{
		SchemaVersion:       SchemaVersion,
		ServerName:          ServerName,
		Protocol:            Protocol,
		Client:              parsedClient,
		Endpoint:            normalizedEndpoint,
		AuthMode:            parsedAuth,
		Configuration:       configuration,
		CredentialsIncluded: false,
	}, nil
}

func Describe(localEndpoint, publicEndpoint string, authMode AuthMode) Status {
	status := Status{
		SchemaVersion:        SchemaVersion,
		ServerName:           ServerName,
		Protocol:             Protocol,
		EndpointPath:         EndpointPath,
		AuthMode:             authMode,
		SupportedClients:     SupportedClients(),
		ConfigurationCommand: "agentdock external-agent",
		CredentialsIncluded:  false,
	}
	if normalized, err := NormalizeEndpoint(localEndpoint); err == nil {
		status.LocalEndpoint = normalized
	}
	if normalized, err := NormalizeEndpoint(publicEndpoint); err == nil {
		status.PublicEndpoint = normalized
	}
	return status
}

func buildConfiguration(client Client, endpoint string, authMode AuthMode) (string, error) {
	switch client {
	case ClientCodex:
		return buildCodexConfiguration(endpoint, authMode), nil
	case ClientClaudeCode:
		return buildClaudeCodeConfiguration(endpoint, authMode)
	case ClientGenericMCP:
		return buildGenericMCPConfiguration(endpoint, authMode)
	case ClientAgentPrompt:
		return buildAgentPrompt(endpoint, authMode), nil
	default:
		return "", fmt.Errorf("unsupported external agent client %q", client)
	}
}

func buildCodexConfiguration(endpoint string, authMode AuthMode) string {
	var output strings.Builder
	fmt.Fprintf(&output, "# Codex CLI (user configuration)\ncodex mcp add %s --url %s\n", ServerName, strconv.Quote(endpoint))
	if authMode == AuthOAuth {
		fmt.Fprintf(&output, "codex mcp login %s\n", ServerName)
	}
	output.WriteString("\n# Equivalent ~/.codex/config.toml entry\n")
	fmt.Fprintf(&output, "[mcp_servers.%s]\nurl = %s\n", ServerName, strconv.Quote(endpoint))
	if authMode == AuthBearer {
		fmt.Fprintf(&output, "bearer_token_env_var = %s\n", strconv.Quote(BearerEnvironmentVariable))
	}
	return strings.TrimSpace(output.String())
}

func buildClaudeCodeConfiguration(endpoint string, authMode AuthMode) (string, error) {
	if authMode == AuthBearer {
		entry := map[string]any{
			"mcpServers": map[string]any{
				ServerName: map[string]any{
					"type": "http",
					"url":  endpoint,
					"headers": map[string]string{
						"Authorization": "Bearer ${" + BearerEnvironmentVariable + "}",
					},
				},
			},
		}
		data, err := json.MarshalIndent(entry, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	var output strings.Builder
	fmt.Fprintf(&output, "claude mcp add --scope user --transport http %s %s", ServerName, strconv.Quote(endpoint))
	if authMode == AuthOAuth {
		fmt.Fprintf(&output, "\n\n# Start Claude Code, run /mcp, select %q, then choose Authenticate.", ServerName)
	}
	return output.String(), nil
}

func buildGenericMCPConfiguration(endpoint string, authMode AuthMode) (string, error) {
	server := map[string]any{"type": "http", "url": endpoint}
	if authMode == AuthBearer {
		server["headers"] = map[string]string{
			"Authorization": "Bearer ${" + BearerEnvironmentVariable + "}",
		}
	}
	configuration := map[string]any{"mcpServers": map[string]any{ServerName: server}}
	data, err := json.MarshalIndent(configuration, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func buildAgentPrompt(endpoint string, authMode AuthMode) string {
	authInstruction := "No authentication is required for this loopback endpoint."
	switch authMode {
	case AuthOAuth:
		authInstruction = "Use the server's OAuth discovery flow and complete interactive authorization when requested."
	case AuthBearer:
		authInstruction = "Read the existing AgentDock bearer credential from a secret store or environment variable; never embed it in prompts or source files."
	}
	return fmt.Sprintf(
		"Connect the MCP Streamable HTTP server named %q at %s. %s Call agentdock_context once before the first device operation, then reuse the returned context until it becomes invalid.",
		ServerName,
		endpoint,
		authInstruction,
	)
}
