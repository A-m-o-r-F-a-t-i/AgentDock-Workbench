package app

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"strings"
	"unicode/utf8"

	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

type outputTextSlot struct {
	name string
	body string
	set  func(string)
}

// File content has its own byte-exact pager. Unknown MCP objects are projected
// at the envelope boundary, preserving the original validated object in storage.
func ordinaryOutputSlots(name string, args map[string]any, result Result) ([]outputTextSlot, []string) {
	slots := []outputTextSlot{}
	add := func(object map[string]any, key, label string) {
		if text, ok := object[key].(string); ok {
			slots = append(slots, outputTextSlot{name: label, body: text, set: func(value string) { object[key] = value }})
		}
	}
	switch name {
	case "exec_command", "session_observe", "session_act":
		if result["command_ok"] == false {
			add(result, "stderr", "stderr")
			add(result, "stdout", "stdout")
		} else {
			add(result, "stdout", "stdout")
			add(result, "stderr", "stderr")
		}
	case "apply_patch":
		add(result, "diff", "diff")
	case "mcp_tool_call":
		remote := modelObject(result["result"])
		if remote == nil || remote["structuredContent"] != nil {
			return nil, nil
		}
		remote = maps.Clone(remote)
		result["result"] = remote
		blocks, ok := remote["content"].([]any)
		if !ok {
			for _, block := range typedContentMaps(remote["content"]) {
				blocks = append(blocks, block)
			}
		}
		blocks = append([]any(nil), blocks...)
		remote["content"] = blocks
		for index, block := range blocks {
			object := modelObject(block)
			text, ok := object["text"].(string)
			if !ok || object["type"] != "text" || json.Valid([]byte(text)) {
				continue
			}
			object = maps.Clone(object)
			blocks[index] = object
			add(object, "text", fmt.Sprintf("result.content[%d].text", index))
		}
	}
	return slots, nil
}

func typedContentMaps(value any) []map[string]any {
	items, _ := value.([]map[string]any)
	return items
}

func headTailPreview(text string, budget int) (string, int) {
	chars, _ := toolcontract.JSONChars(text)
	if chars-2 <= budget {
		return text, len(text)
	}
	// Include marker cost before dividing the remaining body budget. The tail
	// gets three quarters, so command failures retain their final diagnostics.
	body := max(0, budget-96)
	head := toolcontract.JSONTextPrefix(text, body/4)
	tail := toolcontract.JSONTextSuffix(text[len(head):], body-body/4)
	visible := len(head) + len(tail)
	if body == 0 {
		return "", 0
	}
	return head + fmt.Sprintf("\n[%d UTF-8 bytes omitted; retained source available]\n", len(text)-visible) + tail, visible
}

func (r *Runtime) applyToolOutputPolicy(p *preparedExecution, result Result) Result {
	if p.state.binding.ParentCallID != "" || result == nil {
		return result
	}
	slots, _ := ordinaryOutputSlots(p.spec.Name, p.args, result)
	if len(slots) == 0 {
		return result
	}
	for _, slot := range slots {
		slot.set("")
	}
	control, _ := toolcontract.JSONChars(result)
	budget := min(toolcontract.ModelVisiblePayloadChars, max(0, toolcontract.ModelVisibleTotalChars-control-1800))
	if p.outputPolicy.Enabled && p.outputPolicy.MaxChars > 0 {
		budget = min(budget, p.outputPolicy.MaxChars)
	}
	if toolcontract.ResolveOutputClass(p.spec.Name, p.args, result) == toolcontract.OutputCompactReceipt {
		budget = min(budget, 1500)
	}
	redactor := r.executionRedactor(nil)
	remaining, omitted, visibleChars := budget, 0, 0
	truncated := false
	for index, slot := range slots {
		clean := strings.ToValidUTF8(redactor.Text(slot.body, math.MaxInt), "\ufffd")
		share := remaining
		if index+1 < len(slots) && slots[index+1].body != "" {
			share = remaining * 3 / 4
		}
		preview, bytes := headTailPreview(clean, share)
		cost, _ := toolcontract.JSONChars(preview)
		remaining -= max(0, cost-2)
		omitted += len(clean) - bytes
		visibleChars += utf8.RuneCountInString(preview)
		truncated = truncated || bytes < len(clean)
		slot.set(preview)
	}
	if !truncated {
		return result
	}
	capture := modelObject(result["capture"])
	description := map[string]any{"truncated": true, "budget_chars": budget, "visible_chars": visibleChars, "omitted_bytes": omitted}
	if source := stringArg(capture, "source_ref"); source != "" {
		description["source_ref"] = source
		description["continue_read"] = map[string]any{"tool": "read_file", "arguments": map[string]any{"path": source, "offset": 0, "limit_chars": 8000}, "offset_unit": "utf8_byte"}
	} else {
		description["continuation_unavailable"] = "The original result could not be retained; inspect the activity record before retrying an operation."
	}
	if p.spec.Name == "apply_patch" {
		result["diff_truncated"] = true
	}
	result["output_policy"] = description
	result["projection"] = map[string]any{"mode": "head_tail", "complete": false, "visible_chars": visibleChars, "omitted_bytes": omitted}
	return result
}
