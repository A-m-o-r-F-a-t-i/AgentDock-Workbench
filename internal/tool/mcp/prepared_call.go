package mcp

import (
	"context"
	"errors"
	"strings"

	"github.com/uvwt/agentdock/internal/capabilityrouting"
	mcpclient "github.com/uvwt/agentdock/internal/mcp/client"
)

type PreparedCall struct {
	server string
	client *mcpclient.PreparedMCPCall
}

type PreparedCallSummary = mcpclient.PreparedCallSummary

func (p *PreparedCall) Summary() PreparedCallSummary {
	if p == nil || p.client == nil {
		return PreparedCallSummary{}
	}
	return p.client.Summary()
}

type preparedCallContextKey struct{}

type validatedCall struct {
	qualifiedName string
	serverName    string
	arguments     map[string]any
	pluginName    string
}

// WithPreparedCall binds the private catalog snapshot created before approval
// to the eventual fixed handler invocation.
func WithPreparedCall(ctx context.Context, prepared *PreparedCall) context.Context {
	if prepared == nil {
		return ctx
	}
	return context.WithValue(ctx, preparedCallContextKey{}, prepared)
}

func preparedCallFromContext(ctx context.Context) *PreparedCall {
	prepared, _ := ctx.Value(preparedCallContextKey{}).(*PreparedCall)
	return prepared
}

func (s *Service) validateCallRequest(request CallRequest) (validatedCall, error) {
	qualifiedName := strings.TrimSpace(request.Name)
	serverName, _, ok := strings.Cut(qualifiedName, ":")
	serverName = strings.TrimSpace(serverName)
	if !ok || serverName == "" {
		return validatedCall{}, toolErrorDetails("MCP_TOOL_NAME_INVALID", "MCP tool name must use <server>:<tool>", "validation", map[string]any{"tool": request.Name})
	}
	if err := s.ensureAvailable(serverName); err != nil {
		return validatedCall{}, err
	}
	pluginName := s.pluginName(serverName)
	if capabilityrouting.RequiresDesktopGUIIntent(pluginName, qualifiedName) && !capabilityrouting.ValidDesktopGUIIntent(request.InteractionIntent, request.Reason) {
		return validatedCall{}, toolErrorDetails(
			"MCP_EXPLICIT_INTENT_REQUIRED",
			"Computer Use calls require an explicit desktop GUI intent and a concrete user-task reason",
			"validation",
			map[string]any{
				"tool": qualifiedName, "plugin": pluginName,
				"required": map[string]any{"interaction_intent": capabilityrouting.DesktopGUIIntent, "reason": "specific user-task reason"},
			},
		)
	}
	arguments := request.Arguments
	if arguments == nil {
		arguments = map[string]any{}
	}
	return validatedCall{qualifiedName: qualifiedName, serverName: serverName, arguments: arguments, pluginName: pluginName}, nil
}

func (s *Service) PrepareCall(ctx context.Context, request CallRequest) (*PreparedCall, error) {
	validated, err := s.validateCallRequest(request)
	if err != nil {
		return nil, err
	}
	prepared, err := s.mcpClients.PrepareCall(ctx, validated.qualifiedName, validated.arguments)
	if err != nil {
		return nil, dynamicMCPToolError(err)
	}
	return &PreparedCall{server: validated.serverName, client: prepared}, nil
}

func (s *Service) ValidatePreparedCall(ctx context.Context, prepared *PreparedCall) error {
	if prepared == nil || prepared.client == nil {
		return toolErrorDetails("MCP_PREPARED_CALL_REQUIRED", "prepared MCP call is required", "validation", map[string]any{"executed": false})
	}
	if err := s.ensureAvailable(prepared.server); err != nil {
		return err
	}
	return dynamicMCPToolError(s.mcpClients.ValidatePreparedCall(ctx, prepared.client))
}

func preparedCallMatches(prepared *PreparedCall, validated validatedCall) bool {
	return prepared != nil && prepared.client != nil && prepared.server == validated.serverName && prepared.client.Matches(validated.qualifiedName, validated.arguments)
}

func preparedCallMismatch(validated validatedCall) error {
	return toolErrorDetails(
		"MCP_PREPARED_CALL_MISMATCH",
		"The dispatched MCP request does not match the fixed prepared call",
		"validation",
		map[string]any{"tool": validated.qualifiedName, "executed": false},
	)
}

func errorReportsNotExecuted(err error) bool {
	var toolErr *ToolError
	if errors.As(err, &toolErr) {
		value, exists := toolErr.Details["executed"]
		return exists && value == false
	}
	var mcpErr *mcpclient.Error
	if errors.As(err, &mcpErr) {
		value, exists := mcpErr.Details["executed"]
		return exists && value == false
	}
	return false
}

func (p *PreparedCall) OutputPolicy() (string, string) {
	if p == nil || p.client == nil {
		return "", ""
	}
	return p.client.OutputPolicy()
}
