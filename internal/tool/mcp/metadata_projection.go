package mcp

import "strings"

func projectToolMetadata(input map[string]any) map[string]any {
	projected, keep := projectMetadataValue(input)
	if !keep {
		return nil
	}
	result, _ := projected.(map[string]any)
	return result
}

func projectMetadataValue(value any) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
			switch normalized {
			case "icon", "icons", "resourceuri", "ui":
				continue
			}
			projected, keep := projectMetadataValue(item)
			if keep {
				result[key] = projected
			}
		}
		return result, len(result) > 0
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			projected, keep := projectMetadataValue(item)
			if keep {
				result = append(result, projected)
			}
		}
		return result, len(result) > 0
	case string:
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(typed)), "data:image/") {
			return nil, false
		}
		return typed, true
	case nil:
		return nil, false
	default:
		return typed, true
	}
}
