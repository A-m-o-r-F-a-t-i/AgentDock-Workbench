package mcp

import (
	"context"
	"encoding/json"
	"errors"

	rpc "github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/requesttrace"
)

// Protocol errors retain their original code and message, with bounded
// correlation facts in data. Tool arguments never supply tracing metadata.
func (s *Server) traceProtocol(next sdk.MethodHandler) sdk.MethodHandler {
	return func(ctx context.Context, method string, request sdk.Request) (sdk.Result, error) {
		var err error
		ctx, err = requesttrace.Ensure(ctx, "")
		if err != nil {
			return nil, err
		}
		result, err := next(ctx, method, request)
		if err == nil {
			return result, nil
		}
		trace := requesttrace.Read(ctx)
		data := map[string]any{"request_id": trace.RequestID, "backend_call_id": trace.CallID, "stage": trace.Stage, "handler_dispatched": trace.HandlerDispatched, "retryable": false}
		var failure *InvocationError
		if errors.As(err, &failure) {
			data["stage"] = failure.Stage
		}
		code, message := int64(rpc.CodeInternalError), "MCP adapter failed; inspect the original request before retrying"
		var protocolError *rpc.Error
		if errors.As(err, &protocolError) {
			code, message = protocolError.Code, protocolError.Message
			if len(protocolError.Data) > 0 {
				data["original_data"] = protocolError.Data
			}
		}
		encoded, encodeErr := json.Marshal(data)
		if encodeErr != nil {
			return result, err
		}
		return result, &rpc.Error{Code: code, Message: message, Data: encoded}
	}
}
