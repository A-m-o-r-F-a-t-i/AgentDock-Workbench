package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/externalagent"
)

func TestRunExternalAgentBuildsLocalCodexConfiguration(t *testing.T) {
	t.Setenv("AGENTDOCK_HOST", "0.0.0.0")
	t.Setenv("AGENTDOCK_PORT", "9876")
	t.Setenv("AGENTDOCK_OAUTH_ENABLED", "true")

	var stdout bytes.Buffer
	if err := run(context.Background(), []string{"external-agent", "--client", "codex", "--format", "json"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var access externalagent.Access
	if err := json.Unmarshal(stdout.Bytes(), &access); err != nil {
		t.Fatalf("external-agent returned invalid JSON: %v\n%s", err, stdout.String())
	}
	if access.Endpoint != "http://127.0.0.1:9876/mcp" || access.AuthMode != externalagent.AuthOAuth {
		t.Fatalf("unexpected access descriptor: %#v", access)
	}
	if !strings.Contains(access.Configuration, "codex mcp login agentdock") || access.CredentialsIncluded {
		t.Fatalf("unexpected Codex configuration: %#v", access)
	}
}

func TestRunExternalAgentBuildsPublicClaudeCodeConfiguration(t *testing.T) {
	t.Setenv("AGENTDOCK_SERVER_URL", "https://agentdock.example.com")
	t.Setenv("AGENTDOCK_OAUTH_ENABLED", "true")

	var stdout bytes.Buffer
	if err := run(context.Background(), []string{
		"external-agent", "--client", "claude-code", "--source", "public",
	}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if !strings.Contains(output, "claude mcp add --scope user --transport http agentdock \"https://agentdock.example.com/mcp\"") ||
		!strings.Contains(output, "Authenticate") {
		t.Fatalf("unexpected Claude Code configuration:\n%s", output)
	}
}

func TestRunExternalAgentAcceptsExplicitEndpointAndAuth(t *testing.T) {
	t.Setenv("AGENTDOCK_PORT", "invalid-but-ignored")

	var stdout bytes.Buffer
	if err := run(context.Background(), []string{
		"external-agent",
		"--client", "generic-mcp",
		"--endpoint", "https://custom.example.com/gateway/mcp/",
		"--auth", "none",
		"--format", "json",
	}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var access externalagent.Access
	if err := json.Unmarshal(stdout.Bytes(), &access); err != nil {
		t.Fatal(err)
	}
	if access.Endpoint != "https://custom.example.com/gateway/mcp" || access.AuthMode != externalagent.AuthNone {
		t.Fatalf("unexpected explicit endpoint result: %#v", access)
	}
}

func TestRunExternalAgentRejectsInvalidSourceAndEndpoint(t *testing.T) {
	for _, args := range [][]string{
		{"external-agent", "--source", "unknown"},
		{"external-agent", "--endpoint", "https://agentdock.example.com/healthz"},
		{"external-agent", "--format", "yaml"},
	} {
		if err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatalf("run(%q) unexpectedly succeeded", args)
		}
	}
}
