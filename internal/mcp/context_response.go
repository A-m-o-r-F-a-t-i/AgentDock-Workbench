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
const StructuredResultsCapability = "agentdock/structured-results-v1"
const ContextSummaryMaxBytes = 1024
const ToolSummaryMaxBytes = 512
const transportMetadataKey = "agentdock/transport"

const (
	responseTextFull    = "full"
	responseTextSummary = "summary"
)

type contextResponseKey struct{}
type structuredResultsKey struct{}
type contextWireProtocolKey struct{}

// The SDK has already validated per-request protocol metadata before calling
// a registered tool. Materialize its deterministic final decorations before
// measuring; the SDK will preserve these identical values on return.
func contextWireProtocol(ctx context.Context, meta map[string]any) context.Context {
	version, _ := meta[mcpsdk.MetaKeyProtocolVersion].(string)
	return context.WithValue(ctx, contextWireProtocolKey{}, version >= "2026-07-28")
}

// WithStructuredContext asserts that the adapter delivers structuredContent
// intact for bootstrap context. Unknown clients retain complete compatibility JSON.
func WithStructuredContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextResponseKey{}, responseTextSummary)
}

// WithStructuredResults selects compact compatibility text for ordinary tools.
// Complete business data remains in structuredContent.
func WithStructuredResults(ctx context.Context) context.Context {
	return context.WithValue(ctx, structuredResultsKey{}, responseTextSummary)
}

func summaryContext(ctx context.Context) bool {
	return responseTextMode(ctx, "agentdock_context") == responseTextSummary
}

func responseTextMode(ctx context.Context, name string) string {
	if name == "agentdock_context" {
		if mode, _ := ctx.Value(contextResponseKey{}).(string); mode != "" {
			return mode
		}
	}
	if mode, _ := ctx.Value(structuredResultsKey{}).(string); mode != "" {
		return mode
	}
	if name != "agentdock_context" {
		if modern, _ := ctx.Value(contextWireProtocolKey{}).(bool); modern {
			return responseTextSummary
		}
	}
	return responseTextFull
}

func responseModeOption(raw any, capability string) (string, error) {
	options, ok := raw.(map[string]any)
	if !ok || len(options) != 2 || options["structured"] != true {
		return "", errors.New(capability + " negotiation requires structured=true and text=full or summary")
	}
	mode, _ := options["text"].(string)
	if mode != responseTextFull && mode != responseTextSummary {
		return "", errors.New("invalid " + capability + " text mode")
	}
	return mode, nil
}

func contextResponseOptions(ctx context.Context, meta map[string]any) (context.Context, error) {
	if raw, exists := meta[ContextResponseCapability]; exists {
		mode, err := responseModeOption(raw, ContextResponseCapability)
		if err != nil {
			return ctx, err
		}
		ctx = context.WithValue(ctx, contextResponseKey{}, mode)
	}
	if raw, exists := meta[StructuredResultsCapability]; exists {
		mode, err := responseModeOption(raw, StructuredResultsCapability)
		if err != nil {
			return ctx, err
		}
		ctx = context.WithValue(ctx, structuredResultsKey{}, mode)
	}
	return ctx, nil
}

func responseEnvelope(ctx context.Context, name string, result any, err error) map[string]any {
	result = projectContextReference(ctx, name, result, err)
	envelope := toolEnvelope(name, result, err)
	if responseTextMode(ctx, name) != responseTextSummary {
		return envelope
	}
	if name != "agentdock_context" || err != nil {
		return compactToolEnvelopeText(name, envelope)
	}
	payload := asMap(result)
	if payload["delivery"] == "reference" {
		summary, _ := json.Marshal(map[string]any{
			"message":          "Context revision is unchanged. Reuse the complete context already held by the caller; this response is a compact reference.",
			"context_revision": payload["context_revision"],
		})
		return map[string]any{"isError": false, "structuredContent": result, "content": []map[string]any{{"type": "text", "text": string(summary)}}}
	}
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
