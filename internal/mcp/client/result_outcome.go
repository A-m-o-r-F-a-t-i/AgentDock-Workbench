package client

import (
	"encoding/json"
	"io"
	"maps"
	"strings"
)

// ResultFailure preserves the supported MCP failure shapes at both the client
// validation boundary and the runtime outcome boundary. Success output schemas
// must not replace an explicit business failure with a schema mismatch.
func ResultFailure(result map[string]any) (map[string]any, bool) {
	structured, _ := result["structuredContent"].(map[string]any)
	if result["structuredContent"] == nil {
		structured = singleTextResultObject(result["content"])
	}
	details := maps.Clone(structured)
	if details == nil {
		details = map[string]any{}
	}
	if failure, _ := structured["error"].(map[string]any); failure != nil {
		for _, key := range []string{"code", "message", "effect_state", "effectState", "execution_performed", "executed", "retry_safe", "required_action", "requiredAction", "outcome", "counts", "saved"} {
			if details[key] == nil {
				details[key] = failure[key]
			}
		}
	}
	if details["code"] == nil {
		details["code"] = result["code"]
	}
	if details["effect_state"] == nil {
		details["effect_state"] = details["effectState"]
	}
	if details["required_action"] == nil {
		details["required_action"] = details["requiredAction"]
	}
	partial := strings.EqualFold(resultString(details["outcome"]), "partial")
	if partial {
		details["operation_status"] = "partial"
		details["retry_safe"] = false
		if partialResultChanged(details) {
			details["effect_state"] = "partial"
			details["execution_performed"] = true
		}
	}
	code, _ := details["code"].(string)
	failed := result["operation_status"] == "failed" || result["operation_status"] == "partial" || result["isError"] == true || result["is_error"] == true || result["ok"] == false || result["browser_ok"] == false ||
		details["operation_status"] == "failed" || details["operation_status"] == "partial" || details["ok"] == false && code != ""
	if failed && strings.EqualFold(resultString(details["effect_state"]), "changed") {
		details["effect_state"] = "partial"
	}
	return details, failed
}

func resultString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func partialResultChanged(details map[string]any) bool {
	switch strings.ToLower(resultString(details["effect_state"])) {
	case "changed", "partial", "complete":
		return true
	}
	if details["saved"] == true {
		return true
	}
	counts, _ := details["counts"].(map[string]any)
	return positiveResultCount(counts["applied"])
}

func positiveResultCount(value any) bool {
	switch typed := value.(type) {
	case int:
		return typed > 0
	case int32:
		return typed > 0
	case int64:
		return typed > 0
	case float64:
		return typed > 0
	case json.Number:
		parsed, err := typed.Int64()
		return err == nil && parsed > 0
	default:
		return false
	}
}

// Legacy servers may return their business envelope as one JSON text block.
// Recognize only one complete object, without promoting text to successful
// structured output or accepting a trailing JSON value.
func singleTextResultObject(value any) map[string]any {
	var block map[string]any
	switch blocks := value.(type) {
	case []any:
		if len(blocks) == 1 {
			block, _ = blocks[0].(map[string]any)
		}
	case []map[string]any:
		if len(blocks) == 1 {
			block = blocks[0]
		}
	}
	if block["type"] != "text" {
		return nil
	}
	text, _ := block["text"].(string)
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var result map[string]any
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil
	}
	return result
}
