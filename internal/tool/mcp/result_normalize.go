package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"sort"
	"strings"
)

const (
	resultPageBudget      = 6 << 10
	resultMetadataReserve = 1536
)

type resultPage struct {
	Data  map[string]any
	Mode  string
	Bytes int
}

type resultPath []string

type resultPagePosition struct {
	PathIndex int
	ItemIndex int
}

func normalizeDynamicMCPResult(raw map[string]any) (map[string]any, []string, bool) {
	result := cloneResultMap(raw)
	blocks := resultContentBlocks(result["content"])
	structured, hasStructured := result["structuredContent"]
	parsedText := false
	if (!hasStructured || structured == nil) && len(blocks) == 1 {
		block := resultMap(blocks[0])
		if block["type"] == "text" {
			if text, ok := block["text"].(string); ok {
				var parsed any
				decoder := json.NewDecoder(strings.NewReader(text))
				decoder.UseNumber()
				if decoder.Decode(&parsed) == nil && decoder.Decode(new(any)) == io.EOF {
					structured, hasStructured, parsedText = parsed, true, true
					result["structuredContent"] = parsed
				}
			}
		}
	}
	kinds := map[string]bool{}
	kept := make([]any, 0, len(blocks))
	for _, block := range blocks {
		object := resultMap(block)
		kind, _ := object["type"].(string)
		if kind == "" {
			kind = "unknown"
		}
		if kind == "text" && hasStructured {
			text, _ := object["text"].(string)
			if jsonTextEquivalent(text, structured) {
				continue
			}
		}
		kinds[kind] = true
		kept = append(kept, cloneResultValue(block))
	}
	if hasStructured {
		kinds["structured"] = true
	}
	if result["content"] != nil || len(kept) > 0 {
		result["content"] = kept
	}
	orderedKinds := make([]string, 0, len(kinds))
	for kind := range kinds {
		orderedKinds = append(orderedKinds, kind)
	}
	sort.Strings(orderedKinds)
	return result, orderedKinds, parsedText
}

func jsonTextEquivalent(text string, structured any) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	var parsed any
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	if decoder.Decode(&parsed) != nil || decoder.Decode(new(any)) != io.EOF {
		return false
	}
	left, err := canonicalResultJSON(parsed)
	if err != nil {
		return false
	}
	right, err := canonicalResultJSON(structured)
	return err == nil && bytes.Equal(left, right)
}

func canonicalResultJSON(value any) ([]byte, error) {
	return json.Marshal(cloneResultValue(value))
}

func resultContentBlocks(value any) []any {
	switch blocks := value.(type) {
	case []any:
		return blocks
	case []map[string]any:
		out := make([]any, len(blocks))
		for index := range blocks {
			out[index] = blocks[index]
		}
		return out
	default:
		return nil
	}
}

func resultMap(value any) map[string]any {
	switch object := value.(type) {
	case map[string]any:
		return object
	case Result:
		return map[string]any(object)
	default:
		return nil
	}
}

func cloneResultMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = cloneResultValue(item)
	}
	return out
}

func cloneResultValue(value any) any {
	switch item := value.(type) {
	case map[string]any:
		return cloneResultMap(item)
	case Result:
		return cloneResultMap(map[string]any(item))
	case []any:
		out := make([]any, len(item))
		for index := range item {
			out[index] = cloneResultValue(item[index])
		}
		return out
	case []map[string]any:
		out := make([]any, len(item))
		for index := range item {
			out[index] = cloneResultMap(item[index])
		}
		return out
	case json.Number:
		return item
	default:
		return item
	}
}

func buildResultPages(normalized map[string]any) ([]resultPage, error) {
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	if len(encoded)+resultMetadataReserve <= resultPageBudget {
		return []resultPage{{Data: cloneResultMap(normalized), Mode: "full", Bytes: len(encoded)}}, nil
	}
	paths := discoverResultArrayPaths(normalized)
	if len(paths) > 0 {
		if pages, ok := buildSemanticResultPages(normalized, paths); ok {
			return pages, nil
		}
	}
	return buildFragmentResultPages(encoded), nil
}

func discoverResultArrayPaths(root map[string]any) []resultPath {
	paths := []resultPath{}
	var walk func(any, resultPath)
	walk = func(value any, path resultPath) {
		switch item := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(item))
			for key := range item {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				walk(item[key], append(append(resultPath{}, path...), key))
			}
		case []any:
			if len(item) > 0 {
				paths = append(paths, append(resultPath{}, path...))
			}
		}
	}
	walk(root, nil)
	return paths
}

func buildSemanticResultPages(full map[string]any, paths []resultPath) ([]resultPage, bool) {
	base := cloneResultMap(full)
	for _, path := range paths {
		if !setResultPath(base, path, []any{}) {
			return nil, false
		}
	}
	baseBytes, err := json.Marshal(base)
	if err != nil || len(baseBytes)+resultMetadataReserve > resultPageBudget {
		return nil, false
	}
	pages := []resultPage{}
	position := resultPagePosition{}
	for position.PathIndex < len(paths) {
		page := cloneResultMap(base)
		returned := 0
		for position.PathIndex < len(paths) {
			path := paths[position.PathIndex]
			items := resultPathItems(full, path)
			if position.ItemIndex >= len(items) {
				position.PathIndex++
				position.ItemIndex = 0
				continue
			}
			current := append([]any{}, resultPathItems(page, path)...)
			setResultPath(page, path, append(current, cloneResultValue(items[position.ItemIndex])))
			encoded, marshalErr := json.Marshal(page)
			if marshalErr == nil && len(encoded)+resultMetadataReserve <= resultPageBudget {
				returned++
				position.ItemIndex++
				continue
			}
			setResultPath(page, path, current)
			if returned == 0 {
				return nil, false
			}
			break
		}
		encoded, err := json.Marshal(page)
		if err != nil {
			return nil, false
		}
		pages = append(pages, resultPage{Data: page, Mode: "semantic_records", Bytes: len(encoded)})
	}
	return pages, len(pages) > 0
}

func buildFragmentResultPages(encoded []byte) []resultPage {
	// JSON escaping can expand a fragment; measure its encoded envelope, not just its source bytes.
	limit := resultPageBudget - resultMetadataReserve
	pages := []resultPage{}
	for len(encoded) > 0 {
		low, high, fit := 1, min(len(encoded), limit), 0
		for low <= high {
			mid := low + (high-low)/2
			end := mid
			for end > 0 && end < len(encoded) && encoded[end]&0xC0 == 0x80 { end-- }
			candidate := map[string]any{"format": "application/json; host-fragment=v1", "fragment_index": len(pages), "fragment_count": len(encoded), "json_fragment": string(encoded[:end])}
			data, _ := json.Marshal(candidate)
			if len(data) <= limit { fit = max(fit, end); low = mid+1 } else { high = mid-1 }
		}
		if fit == 0 { fit = min(len(encoded), 4) }
		data := map[string]any{"format": "application/json; host-fragment=v1", "fragment_index": len(pages), "fragment_count": 0, "json_fragment": string(encoded[:fit])}
		pages = append(pages, resultPage{Data: data, Mode: "json_fragment"})
		encoded = encoded[fit:]
	}
	for index := range pages {
		pages[index].Data["fragment_count"] = len(pages)
		data, _ := json.Marshal(pages[index].Data)
		pages[index].Bytes = len(data)
	}
	return pages
}

func splitUTF8Bytes(value []byte, limit int) [][]byte {
	if len(value) == 0 {
		return [][]byte{{}}
	}
	chunks := [][]byte{}
	for len(value) > 0 {
		end := min(len(value), limit)
		for end > 0 && end < len(value) && value[end]&0xC0 == 0x80 {
			end--
		}
		if end == 0 {
			end = min(len(value), limit)
		}
		chunks = append(chunks, append([]byte{}, value[:end]...))
		value = value[end:]
	}
	return chunks
}

func resultPathItems(root map[string]any, path resultPath) []any {
	value, ok := resultPathValue(root, path)
	if !ok {
		return nil
	}
	items, _ := value.([]any)
	return items
}

func resultPathValue(root map[string]any, path resultPath) (any, bool) {
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

func setResultPath(root map[string]any, path resultPath, value any) bool {
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

func resultValuesEqual(left, right any) bool {
	return reflect.DeepEqual(cloneResultValue(left), cloneResultValue(right))
}
