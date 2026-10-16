package app

import (
	"maps"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

// Parts contain JSON values, never slices of serialized JSON. A string larger
// than a page uses explicit UTF-8 byte ranges so reconstruction is lossless.
type semanticOutputPart struct {
	Path        string  `json:"path"`
	Value       any     `json:"value"`
	Text        *string `json:"text,omitempty"`
	ByteStart   int     `json:"byte_start,omitempty"`
	ByteEnd     int     `json:"byte_end,omitempty"`
	StringBytes int     `json:"string_bytes,omitempty"`
}

func semanticKeyRank(key string) int {
	order := []string{
		"runtime", "workspace", "workspace_id", "workdir", "workspace_root", "conversation_state",
		"operation", "capture", "permission", "host_tools", "context_revision", "schema_revision",
		"resolution_status", "match_kind", "selected", "next_call",
		"name", "tool_name", "qualified_name", "type", "required", "additionalProperties", "input_schema", "inputSchema",
		"instruction_files", "instructions",
		"builtin_tool_bundles", "plugins", "dynamic_mcp", "tools", "skills", "common_skills",
		"tasks", "rules", "properties",
	}
	for index, item := range order {
		if item == key {
			return index
		}
	}
	return len(order)
}

func semanticKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := semanticKeyRank(keys[i]), semanticKeyRank(keys[j])
		if left == right {
			return keys[i] < keys[j]
		}
		return left < right
	})
	return keys
}

func semanticPointer(parent, key string) string {
	return parent + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

func requiredOutputParts(full Result, budget int) []semanticOutputPart {
	parts := []semanticOutputPart{}
	var visit func(string, any)
	visit = func(path string, value any) {
		part := semanticOutputPart{Path: path, Value: value}
		chars, err := toolcontract.JSONChars(part)
		if err == nil && chars <= budget {
			parts = append(parts, part)
			return
		}
		switch item := value.(type) {
		case map[string]any:
			// Preserve container type before its children, including numeric object keys.
			parts = append(parts, semanticOutputPart{Path: path, Value: map[string]any{}})
			for _, key := range semanticKeys(item) {
				visit(semanticPointer(path, key), item[key])
			}
		case []any:
			parts = append(parts, semanticOutputPart{Path: path, Value: []any{}})
			for index, entry := range item {
				visit(semanticPointer(path, strconv.Itoa(index)), entry)
			}
		case string:
			for offset := 0; offset < len(item); {
				// The part's path and offsets are included in the same JSON budget.
				room := budget - 160
				pathChars, _ := toolcontract.JSONChars(path)
				room -= pathChars
				text := toolcontract.JSONTextPrefix(item[offset:], max(1, room))
				if len(text) < len(item)-offset {
					if boundary := strings.LastIndex(text, "\n\n"); boundary >= len(text)/2 {
						text = text[:boundary+2]
					} else if boundary := strings.LastIndexByte(text, '\n'); boundary >= len(text)/2 {
						text = text[:boundary+1]
					}
				}
				if text == "" {
					_, width := utf8.DecodeRuneInString(item[offset:])
					text = item[offset : offset+width]
				}
				end := offset + len(text)
				parts = append(parts, semanticOutputPart{Path: path, Text: &text, ByteStart: offset, ByteEnd: end, StringBytes: len(item)})
				offset = end
			}
		default:
			parts = append(parts, part)
		}
	}
	for _, key := range semanticKeys(full) {
		visit(semanticPointer("", key), full[key])
	}
	return parts
}

// The fixed raw snapshot is decoded once per page; no tool, filesystem scan or
// plugin preparation is repeated while selecting a continuation page.
func requiredPageHeader(full Result, first bool) Result {
	page := Result{"semantic_format": "json_pointer_parts"}
	for _, key := range []string{"context_revision", "schema_revision", "operation", "capture", "bootstrap_complete"} {
		if value, exists := full[key]; exists {
			page[key] = value
		}
	}
	if diagnostics := modelObject(full["context_diagnostics"]); diagnostics != nil {
		page["context_diagnostics"] = map[string]any{"complete": false, "components_complete": diagnostics["components_complete"], "binding_status": diagnostics["binding_status"]}
	}
	if first && full["host_tools"] != nil {
		for _, key := range contextBootstrapHeaderKeys {
			if value, exists := full[key]; exists {
				page[key] = value
			}
		}
		if instructions := modelObject(full["instruction_files"]); instructions != nil && instructions["revision"] != nil {
			page["instruction_files"] = map[string]any{"complete": instructions["complete"], "revision": instructions["revision"]}
		}
	}
	return page
}

func buildRequiredOutputPage(full Result, start, budget int) (Result, int, int, int) {
	body := maps.Clone(full)
	for _, key := range []string{"context_revision", "schema_revision", "operation", "capture", "host_tools", "bootstrap_complete"} {
		delete(body, key)
	}
	if _, bootstrap := full["host_tools"]; bootstrap {
		for _, key := range contextBootstrapHeaderKeys {
			delete(body, key)
		}
	}
	headerChars, _ := toolcontract.JSONChars(requiredPageHeader(full, false))
	// Reserve section-name accounting and array punctuation before filling a page.
	budget -= 700
	parts := requiredOutputParts(body, max(256, budget-headerChars-128))
	groups := [][]semanticOutputPart{}
	for index := 0; index < len(parts); {
		group := []semanticOutputPart{}
		page := requiredPageHeader(full, len(groups) == 0)
		for index < len(parts) {
			trial := append(append([]semanticOutputPart{}, group...), parts[index])
			page["sections"] = trial
			chars, _ := toolcontract.JSONChars(page)
			if chars > budget {
				if len(group) == 0 && len(groups) > 0 {
					// The source remains available even for an indivisible control object
					// or path longer than a response. Do not advance past that object.
					page["sections"] = []semanticOutputPart{}
					page["required_unit_unavailable"] = parts[index].Path
					return page, start + 1, start + 2, len(parts)
				}
				break
			}
			group = trial
			index++
		}
		groups = append(groups, group)
	}
	if len(groups) == 0 {
		groups = append(groups, []semanticOutputPart{})
	}
	start = min(max(0, start), len(groups)-1)
	page := requiredPageHeader(full, start == 0)
	page["sections"] = groups[start]
	if diagnostics := modelObject(page["context_diagnostics"]); diagnostics != nil {
		_, bootstrap := full["host_tools"]
		diagnostics["complete"] = modelObject(full["context_diagnostics"])["complete"] == true && (bootstrap || start+1 == len(groups))
	}
	received := map[string]bool{}
	for key := range requiredPageHeader(full, true) {
		if key != "semantic_format" {
			received[key] = true
		}
	}
	for _, group := range groups[:start+1] {
		for _, part := range group {
			key := strings.SplitN(strings.TrimPrefix(part.Path, "/"), "/", 2)[0]
			received[strings.ReplaceAll(strings.ReplaceAll(key, "~1", "/"), "~0", "~")] = true
		}
	}
	sections := make([]string, 0, len(received))
	for key := range received {
		sections = append(sections, key)
	}
	sort.Strings(sections)
	page["received_sections"] = sections
	return page, start + 1, len(groups), len(parts)
}
