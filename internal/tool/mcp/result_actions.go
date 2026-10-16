package mcp

import (
	"context"
	"strings"

	"github.com/uvwt/agentdock/internal/activity"
)

func (s *Service) ContinueResult(ctx context.Context, request ResultContinueRequest) (Result, error) {
	callID, cursor := strings.TrimSpace(request.CallID), strings.TrimSpace(request.Cursor)
	if callID == "" || cursor == "" {
		return nil, toolErrorDetails("MCP_RESULT_CURSOR_REQUIRED", "call_id and cursor are required", "validation", map[string]any{"call_id": callID})
	}
	return s.results.continueResult(activity.FromContext(ctx), callID, cursor)
}

func (s *Service) RawResultPage(ctx context.Context, request ResultRawRequest) (Result, error) {
	request.CallID = strings.TrimSpace(request.CallID)
	if request.CallID == "" {
		return nil, toolErrorDetails("MCP_RESULT_CALL_ID_REQUIRED", "call_id is required", "validation", nil)
	}
	return s.results.rawResult(activity.FromContext(ctx), request)
}
