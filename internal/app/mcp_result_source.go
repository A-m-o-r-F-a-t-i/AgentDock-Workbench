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
	parent, ok := r.mcpResultOrigin(ctx, callID, binding)
	if !ok {
		return ""
	}
	return parent.CallID
}

func (r *Runtime) mcpResultOrigin(ctx context.Context, callID string, binding activity.Binding) (activity.ExecutionCall, bool) {
	if callID == "" {
		return activity.ExecutionCall{}, false
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	parent, err := r.activity.Call(lookupCtx, callID)
	if err != nil || parent.ToolName != toolmcp.ToolCall || !activity.CallTerminal(parent.Status) {
		return activity.ExecutionCall{}, false
	}
	if parent.SourceOwnerKey != binding.SourceOwnerKey || parent.ConversationID != binding.ConversationID ||
		parent.TaskID != binding.TaskID || parent.ThreadID != binding.ThreadID || parent.WorkspaceID != binding.WorkspaceID {
		return activity.ExecutionCall{}, false
	}
	return parent, true
}

// A continuation projects the authenticated origin's immutable source facts.
// It does not store its host page as a replacement source or re-capture the body.
func (r *Runtime) mcpResultSourceCapture(binding activity.Binding, callID, reference string) map[string]any {
	unavailable := map[string]any{"state": "not_stored", "complete": false, "reason": "The complete MCP response has no retained activity source."}
	if callID == "" || reference == "" {
		return unavailable
	}
	origin, ok := r.mcpResultOrigin(context.Background(), callID, binding)
	if !ok {
		return unavailable
	}
	source := origin.OutputSource
	if activity.SourceReference(callID, source) != reference {
		source = nil
		for _, previous := range origin.OutputSourceVersions {
			if activity.SourceReference(callID, previous) == reference {
				source = previous
				break
			}
		}
	}
	if source == nil {
		return unavailable
	}
	capture := map[string]any{"state": source.State, "complete": source.State == "complete", "retained_bytes": source.Bytes, "source_ref": reference}
	if source.Reason != "" {
		capture["reason"] = source.Reason
	}
	return capture
}

func (r *Runtime) retainMCPRawResult(ctx context.Context, callID, toolName string, raw map[string]any, arguments map[string]any) map[string]any {
	capture := map[string]any{"state": "not_stored", "complete": false}
	binding := activity.FromContext(ctx)
	if callID == "" || binding.CallID == "" || binding.CallID != callID || raw == nil {
		capture["reason"] = "The complete MCP response could not be bound to its originating activity."
		return capture
	}
	captureCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	payload := r.activity.CapturePayload(captureCtx, raw, "complete", r.executionRedactor(arguments))
	if payload.Ref == "" {
		capture["reason"] = payload.Reason
		return capture
	}
	if _, err := r.activity.Append(captureCtx, activity.Event{Binding: binding, Kind: "call.payload", ToolName: toolName + ".raw", OutputSource: payload}); err != nil {
		capture["storage_error"] = "The complete MCP response could not be journaled. Verify the original operation before retrying."
		return capture
	}
	capture["state"], capture["complete"] = payload.State, payload.State == "complete"
	capture["retained_bytes"] = payload.Bytes
	capture["source_ref"] = activity.SourceReference(binding.CallID, payload)
	return capture
}
