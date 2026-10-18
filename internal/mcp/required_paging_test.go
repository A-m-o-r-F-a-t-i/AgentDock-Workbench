package mcp

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

// Existing transport assertions inspect the complete contract after consuming
// its real required pages. Each wire response is checked before reconstruction.
func requiredPayload(t *testing.T, first map[string]any, arguments map[string]any, call func(string, map[string]any) map[string]any) map[string]any {
	t.Helper()
	assembled := map[string]any{}
	page := first
	var source strings.Builder
	readingSource := false
	snapshot := ""
	for count := 0; count < 512; count++ {
		page = normalizedEnvelope(t, page)
		encoded, err := json.Marshal(page)
		if err != nil || utf8.RuneCount(encoded) > toolcontract.ModelVisibleTotalChars {
			t.Fatalf("required page exceeds wire budget: chars=%d budget=%d err=%v", utf8.RuneCount(encoded), toolcontract.ModelVisibleTotalChars, err)
		}
		if page["isError"] == true {
			t.Fatalf("required continuation failed: %.700s", encoded)
		}
		body := asMap(page["structuredContent"])
		if readingSource {
			text, _ := body["content"].(string)
			source.WriteString(text)
			if body["has_more"] != true {
				var recovered map[string]any
				if err := json.Unmarshal([]byte(source.String()), &recovered); err != nil {
					t.Fatal(err)
				}
				return recovered
			}
		} else {
			for key, value := range body {
				if key == "sections" || key == "semantic_format" {
					continue
				}
				_, exists := assembled[key]
				if !exists || key == "delivery" || key == "structured_output" || key == "context_ready" || key == "has_more" || key == "next_cursor" {
					assembled[key] = value
				}
			}
			current, _ := asMap(body["structured_output"])["snapshot_id"].(string)
			if snapshot != "" && current != "" && current != snapshot {
				t.Fatal("required snapshot changed during paging")
			}
			if current != "" {
				snapshot = current
			}
			if parts, ok := body["sections"].([]any); ok {
				for _, item := range parts {
					applyRequiredPart(t, assembled, asMap(item))
				}
			}
			if asMap(body["delivery"])["continuation_required"] != true && body["has_more"] != true {
				return assembled
			}
			if cursor, _ := body["next_cursor"].(string); cursor != "" {
				page = call("agentdock_context", map[string]any{"cursor": cursor})
				continue
			}
		}
		continuation := asMap(body["continue_read"])
		if len(continuation) == 0 {
			continuation = asMap(asMap(body["structured_output"])["continue_read"])
		}
		if continuation["tool"] != "read_file" {
			t.Fatalf("required result has no stable continuation: %#v", body["delivery"])
		}
		readingSource = true
		page = call("read_file", asMap(continuation["arguments"]))
	}
	t.Fatal("required sequence did not finish")
	return nil
}

func requiredSDKPayload(t *testing.T, session *sdk.ClientSession, first *sdk.CallToolResult, arguments map[string]any, meta sdk.Meta) map[string]any {
	t.Helper()
	return requiredPayload(t, normalizedEnvelope(t, first), arguments, func(name string, args map[string]any) map[string]any {
		result, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: args, Meta: meta})
		if err != nil {
			t.Fatal(err)
		}
		if name == "agentdock_context" {
			assertContextMeasurements(t, result)
		}
		return normalizedEnvelope(t, result)
	})
}

func applyRequiredPart(t *testing.T, root map[string]any, part map[string]any) {
	t.Helper()
	path, _ := part["path"].(string)
	if !strings.HasPrefix(path, "/") {
		t.Fatalf("invalid semantic path %q", path)
	}
	keys := strings.Split(path[1:], "/")
	for index := range keys {
		keys[index] = strings.ReplaceAll(strings.ReplaceAll(keys[index], "~1", "/"), "~0", "~")
	}
	value := part["value"]
	if text, ok := part["text"].(string); ok {
		node := any(root)
		for _, key := range keys {
			switch object := node.(type) {
			case map[string]any:
				node = object[key]
			case []any:
				index, err := strconv.Atoi(key)
				if err != nil || index < 0 || index >= len(object) {
					t.Fatal("invalid semantic array cursor")
				}
				node = object[index]
			default:
				node = nil
			}
		}
		previous, _ := node.(string)
		start, _ := part["byte_start"].(float64)
		if int(start) != len(previous) {
			t.Fatalf("non-contiguous string %s: got=%d expected=%v", path, len(previous), start)
		}
		value = previous + text
	}
	var assign func(any, []string) any
	assign = func(node any, rest []string) any {
		if len(rest) == 0 {
			return value
		}
		switch object := node.(type) {
		case nil:
			return assign(map[string]any{}, rest)
		case map[string]any:
			object[rest[0]] = assign(object[rest[0]], rest[1:])
			return object
		case []any:
			index, err := strconv.Atoi(rest[0])
			if err != nil || index < 0 || index > 1000000 {
				t.Fatal("invalid semantic array index")
			}
			for len(object) <= index {
				object = append(object, nil)
			}
			object[index] = assign(object[index], rest[1:])
			return object
		default:
			t.Fatal("semantic child conflicts with its declared container")
			return nil
		}
	}
	assign(root, keys)
}
