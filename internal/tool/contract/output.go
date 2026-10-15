package contract

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	ModelVisibleTotalChars   = 12000
	ModelVisibleControlChars = 1500
	ModelVisiblePayloadChars = ModelVisibleTotalChars - ModelVisibleControlChars
	CompatSummaryMaxChars    = 512
)

type OutputClass string

const (
	OutputRequiredFull       OutputClass = "required_full"
	OutputRecoverablePreview OutputClass = "recoverable_preview"
	OutputCompactReceipt     OutputClass = "compact_receipt"
)

// Classification changes presentation only; it never grants execution permissions.
func ClassifyOutput(name string, args map[string]any) OutputClass {
	switch name {
	case "agentdock_context", "workspace_context", "mcp_tool_inspect":
		return OutputRequiredFull
	case "read_file":
		path, _ := args["path"].(string)
		if strings.HasPrefix(path, "skill://") || strings.EqualFold(filepath.Base(strings.ReplaceAll(path, "\\", "/")), "AGENTS.md") {
			return OutputRequiredFull
		}
	case "task_manage":
		action, _ := args["action"].(string)
		if action == "get" || action == "resume" || action == "thread_get" || action == "thread_resume" {
			return OutputRequiredFull
		}
		return OutputCompactReceipt
	case "file_edit", "workspace_manage", "plugin_load", "plugin_manage", "skill_package", "mcp_manage", "insertion_ack", "session_act":
		return OutputCompactReceipt
	}
	return OutputRecoverablePreview
}

// JSONChars measures the actual escaped representation, not UTF-8 storage bytes.
func JSONChars(value any) (int, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	return utf8.RuneCount(encoded), nil
}

// JSONTextPrefix returns a character-aligned prefix whose escaped JSON string
// content fits budget. The surrounding quotes are reserved by the caller.
func JSONTextPrefix(text string, budget int) string {
	end, used := 0, 0
	for index, value := range text {
		cost := jsonRuneChars(value)
		if used+cost > budget {
			break
		}
		used += cost
		_, size := utf8.DecodeRuneInString(text[index:])
		end = index + size
	}
	return text[:end]
}

func JSONTextSuffix(text string, budget int) string {
	start, used := len(text), 0
	for start > 0 {
		value, size := utf8.DecodeLastRuneInString(text[:start])
		cost := jsonRuneChars(value)
		if used+cost > budget {
			break
		}
		used += cost
		start -= size
	}
	return text[start:]
}

func jsonRuneChars(value rune) int {
	switch value {
	case '\\', '"', '\b', '\f', '\n', '\r', '\t':
		return 2
	case '<', '>', '&', '\u2028', '\u2029':
		return 6
	default:
		if value < 0x20 {
			return 6
		}
		return 1
	}
}
