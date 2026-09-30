package mcp

import (
	"errors"
	"fmt"
	"testing"

	mcpclient "github.com/uvwt/agentdock/internal/mcp/client"
)

func TestAuditDynamicMCPErrorPreservesExistingToolError(t *testing.T) {
	original := toolErrorDetails("PLUGIN_STATE_INVALID", "read MCP plugin availability", "runtime", map[string]any{"server": "example", "next_action": "inspect"})
	for _, err := range []error{original, fmt.Errorf("inspect selector: %w", original)} {
		actual := dynamicMCPToolError(err)
		if actual != original {
			t.Fatalf("existing structured error lost code/details: %#v", actual)
		}
	}
	if dynamicMCPToolError(nil) != nil {
		t.Fatal("nil error became failure")
	}
}

func TestAuditDynamicMCPAuthDenialRemainsNonRetryable(t *testing.T) {
	original := &mcpclient.Error{Code: "MCP_AUTH_REQUIRED", Message: "missing scoped credential", Details: map[string]any{"server": "private"}}
	err := dynamicMCPToolError(original)
	var typed *ToolError
	if !errors.As(err, &typed) || typed.Code != original.Code || typed.Retryable || typed.Details["server"] != "private" || !errors.Is(err, original) {
		t.Fatalf("credential denial was weakened: %#v", err)
	}
}
