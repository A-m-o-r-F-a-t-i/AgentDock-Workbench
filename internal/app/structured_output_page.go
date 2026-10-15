package app

import (
	"bytes"
	"encoding/json"
	"maps"

	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

const (
	defaultStructuredResponseBytes = 24 << 10
	structuredMetadataReserveBytes = 1 << 10
)

type structuredOutputPath []string

type structuredPageState struct {
	PathIndex int
	ItemIndex int
}

func structuredOutputPaths(name string) []structuredOutputPath {
	switch name {
	case "search_text":
		return []structuredOutputPath{{"matches"}}
	case "list_dir":
		return []structuredOutputPath{{"entries"}, {"skipped_paths"}}
	case "mcp_tool_search":
		return []structuredOutputPath{{"tools"}, {"catalogs"}, {"builtin_tools"}}
	case "mcp_tool_list":
		return []structuredOutputPath{{"tools"}, {"mcp_catalog", "tools"}}
	case "mcp_tool_inspect":
		return []structuredOutputPath{{"tools"}, {"errors"}, {"catalogs"}, {"builtin_tools"}}
	case "workspace_context":
		return []structuredOutputPath{{"warnings"}, {"instructions"}, {"workspace_skills"}}
	case "agentdock_context":
		return []structuredOutputPath{
			{"rules"}, {"warnings"}, {"instruction_files", "files"},
			{"skills"}, {"common_skills", "items"},
			{"plugins"}, {"dynamic_mcp"}, {"acp", "profiles"}, {"workflow_templates"},
			{"recall", "items"}, {"tasks", "items"},
		}
	default:
		return nil
	}
}

func structuredOutputBudget(name string, args map[string]any) int {
	fallback := defaultStructuredResponseBytes
	value := integerArgument(args["max_response_bytes"])
	if value == 0 {
		value = fallback
	}
	if value < toolcontract.StructuredResponseMinBytes {
		value = toolcontract.StructuredResponseMinBytes
	}
	if value > toolcontract.StructuredResponseMaxBytes {
		value = toolcontract.StructuredResponseMaxBytes
	}
	return value
}

func structuredExecutionEnvelopeReserve(name string) int {
	if name == "agentdock_context" {
		return 4 << 10
	}
	return 1 << 10
}

func integerArgument(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int8:
		return int(typed)
	case int16:
		return int(typed)
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case uint:
		return int(typed)
	case uint8:
		return int(typed)
	case uint16:
		return int(typed)
	case uint32:
		return int(typed)
	case uint64:
		if typed <= uint64(^uint(0)>>1) {
			return int(typed)
		}
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	}
	return 0
}

func decodeStructuredResult(data []byte) (Result, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var result Result
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

func cloneStructuredResult(result Result) (Result, []byte, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, nil, err
	}
	cloned, err := decodeStructuredResult(encoded)
	return cloned, encoded, err
}

func buildStructuredPage(full Result, paths []structuredOutputPath, start structuredPageState, budget, hardBudget int) (Result, structuredPageState, int, int, map[string]any) {
	page, _, err := cloneStructuredResult(full)
	if err != nil {
		return full, start, 0, 0, nil
	}
	for _, path := range paths {
		setStructuredPath(page, path, []any{})
	}
	position := normalizeStructuredPosition(full, paths, start)
	returned := 0
	minimumBytes := 0
	var oversized map[string]any
	for position.PathIndex < len(paths) {
		path := paths[position.PathIndex]
		items := structuredPathItems(full, path)
		if position.ItemIndex >= len(items) {
			position.PathIndex++
			position.ItemIndex = 0
			continue
		}
		current := append([]any{}, structuredPathItems(page, path)...)
		trialItems := append(current, items[position.ItemIndex])
		setStructuredPath(page, path, trialItems)
		encodedChars, marshalErr := toolcontract.JSONChars(page)
		if marshalErr == nil && encodedChars+structuredMetadataReserveBytes <= budget {
			returned++
			position.ItemIndex++
			continue
		}
		setStructuredPath(page, path, current)
		if returned > 0 {
			break
		}
		// Search snippets may shrink, but match identity and path/line stay atomic.
		item := modelObject(items[position.ItemIndex])
		if item != nil && structuredPathName(path) == "matches" {
			item = maps.Clone(item)
			for _, key := range []string{"line", "text", "match", "context_before", "context_after"} {
				if text, ok := item[key].(string); ok {
					item[key] = toolcontract.JSONTextPrefix(text, 384)
				}
			}
			item["preview_shortened"] = true
			setStructuredPath(page, path, append(current, item))
			if chars, err := toolcontract.JSONChars(page); err == nil && chars+structuredMetadataReserveBytes <= budget {
				returned++
				position.ItemIndex++
				continue
			}
			setStructuredPath(page, path, current)
		}
		oversized = map[string]any{
			"collection": structuredPathName(path), "index": position.ItemIndex,
			"serialized_chars": max(0, encodedChars),
			"reason":           "The item exceeds the fixed semantic page size; read the retained activity source. This cursor has not skipped it.",
		}
		break
	}
	position = normalizeStructuredPosition(full, paths, position)
	return page, position, returned, minimumBytes, oversized
}

func normalizeStructuredPosition(full Result, paths []structuredOutputPath, position structuredPageState) structuredPageState {
	if position.PathIndex < 0 {
		position.PathIndex = 0
	}
	if position.ItemIndex < 0 {
		position.ItemIndex = 0
	}
	for position.PathIndex < len(paths) {
		if position.ItemIndex < len(structuredPathItems(full, paths[position.PathIndex])) {
			break
		}
		position.PathIndex++
		position.ItemIndex = 0
	}
	return position
}

func structuredPageHasMore(full Result, paths []structuredOutputPath, position structuredPageState) bool {
	position = normalizeStructuredPosition(full, paths, position)
	return position.PathIndex < len(paths)
}

func structuredPathItems(root map[string]any, path structuredOutputPath) []any {
	value, ok := structuredPathValue(root, path)
	if !ok {
		return nil
	}
	items, _ := value.([]any)
	return items
}

func structuredPathValue(root map[string]any, path structuredOutputPath) (any, bool) {
	var current any = root
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[key]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func setStructuredPath(root map[string]any, path structuredOutputPath, value any) bool {
	if len(path) == 0 {
		return false
	}
	current := root
	for _, key := range path[:len(path)-1] {
		next, ok := current[key].(map[string]any)
		if !ok {
			return false
		}
		current = next
	}
	if _, exists := current[path[len(path)-1]]; !exists {
		return false
	}
	current[path[len(path)-1]] = value
	return true
}

func structuredPathName(path structuredOutputPath) string {
	if len(path) == 0 {
		return ""
	}
	name := path[0]
	for _, part := range path[1:] {
		name += "." + part
	}
	return name
}

func countRemainingStructuredItems(full Result, paths []structuredOutputPath, next structuredPageState) int {
	count := 0
	for index := next.PathIndex; index < len(paths); index++ {
		length := len(structuredPathItems(full, paths[index]))
		if index == next.PathIndex {
			length -= next.ItemIndex
		}
		count += max(0, length)
	}
	return count
}
