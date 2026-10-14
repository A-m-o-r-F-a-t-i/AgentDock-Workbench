package contract

const (
	StructuredResponseMinBytes = 4 << 10
	StructuredResponseMaxBytes = 64 << 10
)

// InputObject 构造内建工具的根输入对象。结构已知的工具默认拒绝未知字段；
// 真正动态的字段应在对应 property 上显式声明 additionalProperties。
func InputObject(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = append([]string(nil), required...)
	}
	return schema
}

// OutputObject 保持结果根对象可扩展。输出字段用于帮助客户端理解稳定结果，
// 但运行时允许能力在不破坏旧客户端的前提下增加新的领域字段。
func OutputObject(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": true,
	}
	if len(required) > 0 {
		schema["required"] = append([]string(nil), required...)
	}
	return schema
}

func String(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func Integer(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func BoundedInteger(description string, minimum, maximum int) map[string]any {
	return map[string]any{
		"type":        "integer",
		"description": description,
		"minimum":     minimum,
		"maximum":     maximum,
	}
}

func Boolean(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func StringArray(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       map[string]any{"type": "string"},
	}
}

func ObjectArray(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items": map[string]any{
			"type":                 "object",
			"additionalProperties": true,
		},
	}
}

func OpenObject(description string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          description,
		"additionalProperties": true,
	}
}

// StructuredPagingInput adds compact shared controls. Detailed continuation
// semantics are returned only when pagination is actually required.
func StructuredPagingInput(properties map[string]any) {
	properties["cursor"] = map[string]any{
		"type": "string", "maxLength": 2048,
	}
	properties["max_response_bytes"] = map[string]any{
		"type":    "integer",
		"minimum": StructuredResponseMinBytes,
		"maximum": StructuredResponseMaxBytes,
	}
}

// StructuredPagingOutput documents sparse paging metadata. These fields are
// absent when the complete structured result already fits the response budget.
func StructuredPagingOutput(properties map[string]any) {
	properties["next_cursor"] = String("Opaque cursor for the next retained structured result page.")
	properties["structured_output"] = OpenObject("Structured result budget, retained source, page coverage and continuation status.")
}
