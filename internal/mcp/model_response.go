package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"unicode/utf8"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/buildinfo"
	"github.com/uvwt/agentdock/internal/config"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

// Measure the actual SDK CallToolResult wire JSON, including base64 attachment
// data. Oversized native content is replaced by a retained-source receipt.
func modelEnvelopeChars(envelope map[string]any) (int, error) {
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return 0, err
	}
	var result mcpsdk.CallToolResult
	if err := json.Unmarshal(encoded, &result); err != nil {
		return 0, err
	}
	encoded, err = json.Marshal(&result)
	return utf8.RuneCount(encoded), err
}

func singleBodyEnvelope(name string, envelope map[string]any) map[string]any {
	out := maps.Clone(envelope)
	content := []any{map[string]any{"type": "text", "text": toolResultSummary(name, envelope)}}
	for _, block := range envelopeBlocks(envelope["content"]) {
		if asMap(block)["type"] != "text" {
			content = append(content, block)
		}
	}
	out["content"] = content
	return out
}

// This is the last shared SDK/Bridge boundary, after trusted additions and SDK
// metadata. A large result remains a successful operation with a source receipt.
func (s *Server) boundModelResponse(ctx context.Context, name string, arguments map[string]any, pending *app.ToolResponse, envelope map[string]any) (map[string]any, error) {
	out := normalizeResponseStages(name, arguments, envelope)
	nativeBytes := nativeAttachmentBytes(out)
	if responseTextMode(ctx, name) != responseTextOnly {
		out = singleBodyEnvelope(name, out)
	}
	if modern, _ := ctx.Value(contextWireProtocolKey{}).(bool); modern {
		out = maps.Clone(out)
		meta := maps.Clone(asMap(out["_meta"]))
		if meta == nil {
			meta = map[string]any{}
		}
		meta[mcpsdk.MetaKeyServerInfo] = &mcpsdk.Implementation{Name: config.ServerName, Version: buildinfo.Version}
		out["_meta"] = meta
		out["resultType"] = "complete"
	}
	chars, err := modelWireChars(ctx, name, out)
	if err != nil {
		return nil, err
	}
	if chars > toolcontract.ModelVisibleTotalChars {
		source, storeErr := s.runtime.RetainResponseSource(pending, responseRetentionValue(name, envelope))
		if storeErr != nil {
			return nil, fmt.Errorf("retain completed tool output before projection: %w", storeErr)
		}
		out = compactModelResponse(name, arguments, out, source)
	}
	for range 8 {
		payload := maps.Clone(asMap(out["structuredContent"]))
		if payload == nil {
			payload = map[string]any{}
		}
		projection := maps.Clone(asMap(payload["projection"]))
		if len(projection) == 0 {
			projection = map[string]any{"mode": "full", "complete": true}
		}
		payload["model_projection"] = map[string]any{
			"chars": chars, "budget_chars": toolcontract.ModelVisibleTotalChars,
			"class":                   modelOutputClass(name, arguments, payload),
			"native_attachment_bytes": nativeBytes,
			"mode":                    projection["mode"], "complete": projection["complete"],
		}
		out["structuredContent"] = payload
		out, err = measureContextResponse(ctx, name, out)
		if err != nil {
			return nil, err
		}
		next, countErr := modelWireChars(ctx, name, out)
		if countErr != nil {
			return nil, countErr
		}
		if next > toolcontract.ModelVisibleTotalChars {
			source, storeErr := s.runtime.RetainResponseSource(pending, responseRetentionValue(name, envelope))
			if storeErr != nil {
				return nil, storeErr
			}
			out = compactModelResponse(name, arguments, out, source)
			if remaining, _ := modelWireChars(ctx, name, out); remaining > toolcontract.ModelVisibleTotalChars {
				return nil, fmt.Errorf("MODEL_RESPONSE_BUDGET_EXCEEDED: retained source=%s; inspect the completed operation before any retry", source)
			}
			chars = next
			continue
		}
		if next == chars {
			return modelWireEnvelope(ctx, name, out)
		}
		chars = next
	}
	return nil, fmt.Errorf("model response character count did not converge")
}

func compactModelResponse(name string, args map[string]any, original map[string]any, source string) map[string]any {
	full := asMap(original["structuredContent"])
	class := modelOutputClass(name, args, full)
	payload := map[string]any{}
	for _, key := range []string{"operation", "capture", "agentdock_guidance", "call_id", "request_id", "conversation_id", "task_id", "thread_id", "workspace_id", "session_id", "status", "command_ok", "exit_code", "timed_out", "cancelled", "signal", "action", "changed", "files_changed", "verified", "error", "code", "category", "retryable", "executed", "route_code", "required_host_tool", "retry_dynamic_search", "no_change", "next_observe_after_ms"} {
		if value, exists := full[key]; exists {
			if text, ok := value.(string); ok {
				value = toolcontract.JSONTextPrefix(text, 512)
			}
			payload[key] = value
		}
	}
	if payload["operation"] == nil {
		payload["operation"] = map[string]any{"state": "returned", "ok": original["isError"] != true}
	}
	payload["projection"] = map[string]any{"mode": "source_receipt", "complete": false}
	payload["truncated"] = true
	payload["truncation_stage"] = "projection"
	payload["source_ref"] = source
	payload["delivery"] = map[string]any{
		"class": class, "sequence_complete": class != toolcontract.OutputRequiredFull,
		"continuation_required":  class == toolcontract.OutputRequiredFull && source != "",
		"continuation_available": source != "",
	}
	if source != "" {
		payload["continue_read"] = map[string]any{"tool": "read_file", "arguments": map[string]any{"path": source, "offset": 0, "limit_chars": 8000}}
	}
	if name == "agentdock_context" || name == "workspace_context" {
		payload["context_ready"] = false
		payload["context_revision"] = full["context_revision"]
		payload["context_diagnostics"] = map[string]any{"complete": false, "components_complete": asMap(full["context_diagnostics"])["components_complete"], "binding_status": asMap(full["context_diagnostics"])["binding_status"]}
	}
	// Preserve the error tail for commands even when decorations force a smaller
	// emergency projection. File cursors are never advanced by this fallback.
	for _, key := range []string{"stderr", "stdout"} {
		if text, _ := full[key].(string); text != "" {
			payload[key] = toolcontract.JSONTextSuffix(text, 1500)
		}
	}
	if omitted := omittedContentDescriptors(original); len(omitted) > 0 {
		payload["content_blocks_omitted"] = omitted
	}
	out := maps.Clone(original)
	out["structuredContent"] = payload
	return compactContentEnvelope(name, out)
}

func compactContentEnvelope(name string, envelope map[string]any) map[string]any {
	out := maps.Clone(envelope)
	content := []any{map[string]any{"type": "text", "text": toolResultSummary(name, envelope)}}
	for _, block := range envelopeBlocks(envelope["content"]) {
		if asMap(block)["type"] == "resource_link" {
			content = append(content, block)
		}
	}
	out["content"] = content
	return out
}

func responseRetentionValue(name string, envelope map[string]any) any {
	for _, block := range envelopeBlocks(envelope["content"]) {
		switch asMap(block)["type"] {
		case "image", "audio", "resource":
			return envelope
		}
	}
	if name == "mcp_tool_call" {
		payload := asMap(envelope["structuredContent"])
		result := asMap(payload["result"])
		if structured, exists := result["structuredContent"]; exists && structured != nil {
			content := canonicalDynamicMCPContent(result["content"], structured)
			if len(content) == 0 {
				return structured
			}
			retained := map[string]any{"structuredContent": structured, "content": content}
			if value, exists := result["isError"]; exists {
				retained["isError"] = value
			}
			return retained
		}
	}
	return envelope["structuredContent"]
}

func projectDynamicMCPResult(payload map[string]any) map[string]any {
	result := asMap(payload["result"])
	structured, exists := result["structuredContent"]
	if !exists || structured == nil {
		return payload
	}
	projected := map[string]any{"structuredContent": structured}
	if value, exists := result["isError"]; exists {
		projected["isError"] = value
	}
	if content := canonicalDynamicMCPContent(result["content"], structured); len(content) > 0 {
		projected["content"] = content
	}
	out := maps.Clone(payload)
	out["result"] = projected
	return out
}

func canonicalDynamicMCPContent(value, structured any) []any {
	blocks := envelopeBlocks(value)
	if len(blocks) == 0 {
		return nil
	}
	kept := make([]any, 0, len(blocks))
	for _, block := range blocks {
		object := asMap(block)
		if object["type"] == "text" {
			text, _ := object["text"].(string)
			if redundantDynamicMCPText(text, structured) {
				continue
			}
		}
		kept = append(kept, block)
	}
	return kept
}

func redundantDynamicMCPText(text string, structured any) bool {
	if jsonTextMatches(text, structured) {
		return true
	}
	var receipt map[string]any
	if json.Unmarshal([]byte(text), &receipt) != nil {
		return false
	}
	marker, _ := receipt["structuredContent"].(bool)
	if !marker {
		return false
	}
	for key := range receipt {
		switch key {
		case "ok", "structuredContent", "transportOk", "outcome", "requestStatus", "schema", "resultId", "receiptId", "total", "nextOffset", "coverageComplete", "error":
		default:
			return false
		}
	}
	return true
}

func omittedContentDescriptors(envelope map[string]any) []map[string]any {
	items := []map[string]any{}
	for _, block := range envelopeBlocks(envelope["content"]) {
		value := asMap(block)
		kind, _ := value["type"].(string)
		switch kind {
		case "image", "audio":
			data, _ := value["data"].(string)
			items = append(items, map[string]any{
				"type": kind, "mime_type": value["mimeType"],
				"encoded_chars": len(data), "decoded_bytes": decodedAttachmentBytes(data),
			})
		case "resource":
			resource := asMap(value["resource"])
			text, _ := resource["text"].(string)
			blob, _ := resource["blob"].(string)
			items = append(items, map[string]any{
				"type": kind, "uri": resource["uri"], "mime_type": resource["mimeType"],
				"text_chars": len([]rune(text)), "blob_encoded_chars": len(blob),
			})
		}
	}
	return items
}

func modelOutputClass(name string, args, payload map[string]any) toolcontract.OutputClass {
	if value, ok := asMap(payload["delivery"])["class"].(toolcontract.OutputClass); ok {
		return value
	}
	if value, ok := asMap(payload["delivery"])["class"].(string); ok {
		switch toolcontract.OutputClass(value) {
		case toolcontract.OutputRequiredFull, toolcontract.OutputRecoverablePreview, toolcontract.OutputCompactReceipt:
			return toolcontract.OutputClass(value)
		}
	}
	return toolcontract.ResolveOutputClass(name, args, payload)
}

func nativeAttachmentBytes(envelope map[string]any) int64 {
	total := int64(0)
	for _, block := range envelopeBlocks(envelope["content"]) {
		value := asMap(block)
		if value["type"] == "image" || value["type"] == "audio" {
			if data, ok := value["data"].(string); ok {
				total += int64(decodedAttachmentBytes(data))
			}
		}
	}
	return total
}

func decodedAttachmentBytes(data string) int {
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return 0
	}
	return len(decoded)
}

func normalizeResponseStages(name string, args, envelope map[string]any) map[string]any {
	out := maps.Clone(envelope)
	payload := maps.Clone(asMap(out["structuredContent"]))
	if payload == nil {
		payload = map[string]any{}
	}
	if name == "mcp_tool_call" {
		payload = projectDynamicMCPResult(payload)
	}
	if payload["operation"] == nil {
		state, ok := "completed", out["isError"] != true
		if !ok {
			state = "failed"
		}
		operation := map[string]any{"state": state, "ok": ok}
		if code, exists := payload["code"]; exists {
			operation["error_code"] = code
		}
		payload["operation"] = operation
	}
	if payload["capture"] == nil {
		payload["capture"] = map[string]any{"state": "complete", "complete": true, "retained_with": "response"}
	}
	if payload["projection"] == nil {
		payload["projection"] = map[string]any{"mode": "full", "complete": true}
	}
	if payload["delivery"] == nil {
		payload["delivery"] = map[string]any{"class": toolcontract.ResolveOutputClass(name, args, payload), "sequence_complete": true, "continuation_required": false, "continuation_available": false}
	}
	out["structuredContent"] = payload
	return out
}
