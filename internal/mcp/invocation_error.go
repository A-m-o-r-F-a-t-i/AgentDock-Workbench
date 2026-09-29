package mcp

import (
	"context"
	"errors"
	"fmt"
	"github.com/uvwt/agentdock/internal/requesttrace"
	"maps"
)

// InvocationError describes adapter failure independently of business outcome.
// It never grants permission to replay an already dispatched tool.
type InvocationError struct {
	Stage             string `json:"stage"`
	RequestID         string `json:"request_id,omitempty"`
	CallID            string `json:"backend_call_id,omitempty"`
	HandlerDispatched bool   `json:"handler_dispatched"`
	Retryable         bool   `json:"retryable"`
	Message           string `json:"message"`
	cause             error
}

func (e *InvocationError) Error() string {
	return fmt.Sprintf("%s (stage=%s request_id=%s backend_call_id=%s)", e.Message, e.Stage, e.RequestID, e.CallID)
}
func (e *InvocationError) Unwrap() error { return e.cause }
func invocationError(ctx context.Context, stage, message string, cause error) *InvocationError {
	requesttrace.Stage(ctx, stage)
	trace := requesttrace.Read(ctx)
	return &InvocationError{Stage: stage, RequestID: trace.RequestID, CallID: trace.CallID,
		HandlerDispatched: trace.HandlerDispatched, Retryable: false, Message: message, cause: cause}
}
func invocationFailure(envelope map[string]any, failure *InvocationError) (map[string]any, error) {
	output := maps.Clone(envelope)
	if output == nil {
		output = map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": failure.Error()}}}
	}
	meta := maps.Clone(asMap(output["_meta"]))
	if meta == nil {
		meta = map[string]any{}
	}
	meta["agentdock/invocation-error-v1"] = failure
	meta[transportMetadataKey] = requesttrace.Snapshot{RequestID: failure.RequestID, CallID: failure.CallID, Stage: failure.Stage, HandlerDispatched: failure.HandlerDispatched}
	output["_meta"] = meta
	if diagnostics, ok := meta[ContextResponseCapability]; ok {
		ctx := context.Background()
		if asMap(diagnostics)["compat_text_mode"] == "summary" {
			ctx = WithStructuredContext(ctx)
		}
		measured, err := measureContextResponse(ctx, "agentdock_context", output)
		if err == nil {
			output = measured
		} else {
			delete(meta, ContextResponseCapability)
		}
	}
	return output, failure
}
func guardedProjection(project func(map[string]any) (map[string]any, error), input map[string]any) (result map[string]any, err error) {
	defer func() {
		if recover() != nil {
			result = nil
			err = errors.New("projection callback panicked")
		}
	}()
	return project(input)
}
func guardedCommit(commit func(context.Context, map[string]any) error, ctx context.Context, value map[string]any) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("context commit callback panicked")
		}
	}()
	return commit(ctx, value)
}
