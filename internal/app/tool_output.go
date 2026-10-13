package app

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/textutil"
)

type outputTextSlot struct {
	name string
	body string
	set  func(string)
}

// Only declared ordinary text is budgeted. Unknown structured output is kept
// contract-valid and reported as exempt, never cut as serialized JSON.
func ordinaryOutputSlots(name string, args map[string]any, result Result) ([]outputTextSlot, []string) {
	var slots []outputTextSlot
	add := func(object map[string]any, key, label string) {
		if text, ok := object[key].(string); ok {
			slots = append(slots, outputTextSlot{name: label, body: text, set: func(value string) { object[key] = value }})
		}
	}
	switch name {
	case "exec_command", "session_observe", "session_act":
		add(result, "stdout", "stdout")
		add(result, "stderr", "stderr")
	case "read_file":
		path := stringArg(args, "path")
		if strings.HasPrefix(path, "skill://") || strings.HasPrefix(path, "activity://") || strings.EqualFold(filepath.Base(strings.ReplaceAll(path, "\\", "/")), "AGENTS.md") {
			return nil, nil
		}
		add(result, "content", "content")
	case "file_edit":
		add(result, "diff_preview", "diff_preview")
	case "mcp_tool_call":
		remote, ok := result["result"].(map[string]any)
		if !ok {
			return nil, []string{"result: unknown structured representation"}
		}
		// Arbitrary schemas may constrain strings with const/pattern/minLength.
		if remote["structuredContent"] != nil {
			return nil, []string{"result.content and result.structuredContent: no declared pageable structured contract"}
		}
		remote = maps.Clone(remote)
		result["result"] = remote
		blocks, ok := remote["content"].([]any)
		if !ok {
			if typed, yes := remote["content"].([]map[string]any); yes {
				blocks = make([]any, len(typed))
				for i, item := range typed {
					blocks[i] = item
				}
			} else {
				return nil, nil
			}
		}
		blocks = append([]any(nil), blocks...)
		remote["content"] = blocks
		var exempt []string
		for index, block := range blocks {
			object, ok := block.(map[string]any)
			if !ok || object["type"] != "text" {
				continue
			}
			text, ok := object["text"].(string)
			if !ok {
				continue
			}
			label := fmt.Sprintf("result.content[%d].text", index)
			if json.Valid([]byte(text)) {
				exempt = append(exempt, label+": serialized JSON without a pageable contract")
				continue
			}
			object = maps.Clone(object)
			blocks[index] = object
			add(object, "text", label)
		}
		return slots, exempt
	}
	return slots, nil
}

func (r *Runtime) applyToolOutputPolicy(p *preparedExecution, result Result) Result {
	policy := p.outputPolicy
	if !policy.Enabled || policy.MaxChars < 1 || p.state.binding.ParentCallID != "" || result == nil {
		return result
	}
	slots, exempt := ordinaryOutputSlots(p.spec.Name, p.args, result)
	if len(slots) == 0 && len(exempt) == 0 {
		return result
	}
	remaining := policy.MaxChars
	// Audit records mask all caller-supplied environment values. Applying that
	// rule to authorized output would hide ordinary compiler and path settings.
	previewRedactor := r.executionRedactor(nil)
	storageRedactor := r.executionRedactor(p.args)
	source := make(map[string]any, len(slots))
	truncated := false
	for _, slot := range slots {
		clean := previewRedactor.Text(slot.body, math.MaxInt)
		preview, count, more := textutil.ScalarPrefix(clean, remaining)
		remaining -= count
		truncated = truncated || more
		source[slot.name] = clean
		slot.set(preview)
	}
	if !truncated {
		if len(exempt) > 0 {
			result["output_policy"] = map[string]any{"truncated": false, "unapplied_fields": exempt}
		}
		return result
	}
	description := map[string]any{"truncated": true}
	if len(exempt) > 0 {
		description["unapplied_fields"] = exempt
	}
	result["output_policy"] = description
	state, reason := "complete", ""
	for _, key := range []string{"truncated", "stdout_truncated", "stderr_truncated", "diff_truncated", "partial"} {
		if result[key] == true {
			state, reason = "partial", "The source tool returned only part of its output; uncollected content is not present in this snapshot."
		}
	}
	if result["status"] == "running" {
		state, reason = "partial", "Snapshot of a running command; subsequent output belongs to the original command session."
	}
	if state == "partial" {
		description["source_truncated"] = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	payload := r.activity.CapturePayload(ctx, source, state, storageRedactor)
	if payload.Ref != "" && reason != "" {
		payload.Reason = reason
	}
	if payload.Ref != "" {
		_, err := r.activity.Append(ctx, activity.Event{Binding: p.state.binding, Kind: "call.payload", ToolName: p.spec.Name, OutputSource: payload})
		if err != nil {
			description["storage_state"] = "not_stored"
			description["storage_reason"] = "The source reference could not be journaled; no continuation was issued."
			return result
		}
	}
	if payload.State != "" && payload.State != "complete" {
		description["storage_state"] = payload.State
	}
	if payload.Reason != "" {
		description["storage_reason"] = payload.Reason
	}
	if payload.Ref != "" {
		description["saved_bytes"] = payload.Bytes
		if p.state.binding.SourceOwnerKey != "" {
			description["continue_read"] = map[string]any{"tool": "read_file", "arguments": map[string]any{"path": "activity://call/" + p.state.binding.CallID + "/source", "offset": 0, "limit_chars": policy.MaxChars}, "offset_unit": "utf8_byte", "format": "json_text_fields", "text": "Read retained ordinary fields from byte zero, then follow next_offset. This never re-executes the source tool."}
		} else {
			description["continuation_unavailable"] = "No authenticated source identity; inspect the saved source in the local activity center."
		}
	}
	return result
}
