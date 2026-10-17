package app

import (
	"context"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
)

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
