package mcp

import (
	"context"
	"encoding/json"
	"errors"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/buildinfo"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/requesttrace"
	"maps"
	"reflect"
)

const ContextResponseCapability = "agentdock/context-response-v1"
const ContextSummaryMaxBytes = 1024
const transportMetadataKey = "agentdock/transport"

type contextResponseKey struct{}
type contextWireProtocolKey struct{}

// The SDK has already validated per-request protocol metadata before calling
// a registered tool. Materialize its deterministic final decorations before
// measuring; the SDK will preserve these identical values on return.
func contextWireProtocol(ctx context.Context, meta map[string]any) context.Context {
	version, _ := meta[mcpsdk.MetaKeyProtocolVersion].(string)
	return context.WithValue(ctx, contextWireProtocolKey{}, version >= "2026-07-28")
}

// WithStructuredContext asserts that the adapter delivers structuredContent
// intact. Unknown clients continue receiving complete compatibility JSON text.
func WithStructuredContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextResponseKey{}, true)
}
func summaryContext(ctx context.Context) bool {
	value, _ := ctx.Value(contextResponseKey{}).(bool)
	return value
}
func contextResponseOptions(ctx context.Context, meta map[string]any) (context.Context, error) {
	raw, exists := meta[ContextResponseCapability]
	if !exists {
		return ctx, nil
	}
	options, ok := raw.(map[string]any)
	if !ok || len(options) != 2 || options["structured"] != true {
		return ctx, errors.New("context response negotiation requires structured=true and text=full or summary")
	}
	switch options["text"] {
	case "summary":
		return WithStructuredContext(ctx), nil
	case "full":
		return context.WithValue(ctx, contextResponseKey{}, false), nil
	default:
		return ctx, errors.New("invalid context compatibility text mode")
	}
}
func responseEnvelope(ctx context.Context, name string, result any, err error) map[string]any {
	if name != "agentdock_context" || err != nil || !summaryContext(ctx) {
		return toolEnvelope(name, result, err)
	}
	payload := asMap(result)
	count := func(key string) int {
		value := reflect.ValueOf(payload[key])
		if value.IsValid() && value.Kind() == reflect.Slice {
			return value.Len()
		}
		return 0
	}
	summary, _ := json.Marshal(map[string]any{
		"message": "Read structuredContent for complete rules, workspace, capabilities, task state and warnings. This text is a compatibility summary only.",
		"skills":  count("skills"), "plugins": count("plugins"), "dynamic_mcp": count("dynamic_mcp"),
	})
	return map[string]any{"isError": false, "structuredContent": result, "content": []map[string]any{{"type": "text", "text": string(summary)}}}
}

// Decorate only the adapter-owned top level, never third-party nested metadata.
func responseTrace(ctx context.Context, envelope map[string]any) map[string]any {
	trace := requesttrace.Read(ctx)
	if trace.RequestID == "" {
		return envelope
	}
	output := maps.Clone(envelope)
	meta := maps.Clone(asMap(output["_meta"]))
	if meta == nil {
		meta = map[string]any{}
	}
	meta[transportMetadataKey] = trace
	output["_meta"] = meta
	return output
}

// Measure final SDK CallToolResult JSON, excluding JSON-RPC/HTTP framing.
// Fixed-point iteration includes the counter's own decimal digits.
func measureContextResponse(ctx context.Context, name string, envelope map[string]any) (map[string]any, error) {
	if name != "agentdock_context" {
		return envelope, nil
	}
	output := maps.Clone(envelope)
	meta := maps.Clone(asMap(output["_meta"]))
	if meta == nil {
		meta = map[string]any{}
	}
	if modern, _ := ctx.Value(contextWireProtocolKey{}).(bool); modern {
		output["resultType"] = "complete"
		meta[mcpsdk.MetaKeyServerInfo] = &mcpsdk.Implementation{Name: config.ServerName, Version: buildinfo.Version}
	}
	structured, err := json.Marshal(output["structuredContent"])
	if err != nil {
		return nil, err
	}
	textBytes := 0
	for _, block := range envelopeBlocks(output["content"]) {
		text, _ := asMap(block)["text"].(string)
		textBytes += len(text)
	}
	mode := "full"
	if summaryContext(ctx) && output["isError"] != true {
		mode = "summary"
	}
	diagnostics := map[string]any{
		"structured_bytes": len(structured), "text_content_bytes": textBytes,
		"response_bytes": 0, "compat_text_mode": mode, "response_truncated": false,
		"response_bytes_scope": "CallToolResult JSON; excludes JSON-RPC and HTTP",
	}
	meta[ContextResponseCapability] = diagnostics
	output["_meta"] = meta
	for range 8 {
		encoded, err := json.Marshal(output)
		if err != nil {
			return nil, err
		}
		var result mcpsdk.CallToolResult
		if err = json.Unmarshal(encoded, &result); err != nil {
			return nil, err
		}
		encoded, err = json.Marshal(&result)
		if err != nil {
			return nil, err
		}
		if diagnostics["response_bytes"] == len(encoded) {
			return output, nil
		}
		diagnostics["response_bytes"] = len(encoded)
	}
	return nil, errors.New("context response size did not converge")
}
