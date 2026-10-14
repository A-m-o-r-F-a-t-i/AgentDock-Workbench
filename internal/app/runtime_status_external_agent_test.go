package app

import (
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/externalagent"
)

func TestRuntimeStatusDescribesExternalAgentAccess(t *testing.T) {
	t.Parallel()

	runtime := &Runtime{cfg: config.Config{
		Host:           "0.0.0.0",
		Port:           8765,
		OAuthEnabled:   true,
		OAuthServerURL: "https://agentdock.example.com",
	}}
	result := runtime.RuntimeStatus()

	access, ok := result["external_agent_access"].(externalagent.Status)
	if !ok {
		t.Fatalf("external_agent_access has unexpected type %T", result["external_agent_access"])
	}
	if access.SchemaVersion != externalagent.SchemaVersion || access.Protocol != externalagent.Protocol {
		t.Fatalf("unexpected external agent contract: %#v", access)
	}
	if access.LocalEndpoint != "http://127.0.0.1:8765/mcp" {
		t.Fatalf("local endpoint = %q", access.LocalEndpoint)
	}
	if access.PublicEndpoint != "https://agentdock.example.com/mcp" {
		t.Fatalf("public endpoint = %q", access.PublicEndpoint)
	}
	if access.AuthMode != externalagent.AuthOAuth {
		t.Fatalf("auth mode = %q", access.AuthMode)
	}
	if access.CredentialsIncluded {
		t.Fatal("runtime status must not expose credentials")
	}
	if len(access.SupportedClients) != 4 {
		t.Fatalf("supported clients = %#v", access.SupportedClients)
	}
}

func TestRuntimeStatusDescribesBearerAndAnonymousModes(t *testing.T) {
	t.Parallel()

	bearer := (&Runtime{cfg: config.Config{Host: "127.0.0.1", Port: 8765, AuthToken: "configured"}}).RuntimeStatus()
	bearerAccess := bearer["external_agent_access"].(externalagent.Status)
	if bearerAccess.AuthMode != externalagent.AuthBearer {
		t.Fatalf("bearer auth mode = %q", bearerAccess.AuthMode)
	}

	anonymous := (&Runtime{cfg: config.Config{Host: "127.0.0.1", Port: 8765}}).RuntimeStatus()
	anonymousAccess := anonymous["external_agent_access"].(externalagent.Status)
	if anonymousAccess.AuthMode != externalagent.AuthNone {
		t.Fatalf("anonymous auth mode = %q", anonymousAccess.AuthMode)
	}
}
