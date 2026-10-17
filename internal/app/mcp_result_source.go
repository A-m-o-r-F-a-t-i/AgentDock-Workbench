package app

import (
	"context"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
)

// mcpResultActivityParent folds a host projection page under the original
// MCP call only after the retained activity proves the same execution scope.
func (r *Runtime) mcpResultActivityParent(ctx context.Context, name string, arguments map[string]any, binding activity.Binding) string {
	if name != toolmcp.ToolResultContinue {
		return ""
	}
	callID := strings.TrimSpace(stringArg(arguments, "call_id"))
	if callID == "" {
		return ""
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	parent, err := r.activity.Call(lookupCtx, callID)
	if err != nil || parent.ToolName != toolmcp.ToolCall || !activity.CallTerminal(parent.Status) {
		return ""
	}
	if parent.SourceOwnerKey != binding.SourceOwnerKey || parent.ConversationID != binding.ConversationID ||
		parent.TaskID != binding.TaskID || parent.ThreadID != binding.ThreadID || parent.WorkspaceID != binding.WorkspaceID {
		return ""
	}
	return parent.CallID
}

func (r *Runtime) retainMCPRawResult(ctx context.Context, callID, toolName string, raw map[string]any, arguments map[string]any) string {
	binding := activity.FromContext(ctx)
	if callID == "" || binding.CallID == "" || binding.CallID != callID || len(raw) == 0 {
		return ""
	}
	captureCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	payload := r.activity.CapturePayload(captureCtx, raw, "complete", r.executionRedactor(arguments))
	if payload.Ref == "" {
		return ""
	}
	if _, err := r.activity.Append(captureCtx, activity.Event{Binding: binding, Kind: "call.payload", ToolName: toolName + ".raw", OutputSource: payload}); err != nil {
		return ""
	}
	return activity.SourceReference(binding.CallID, payload)
}
