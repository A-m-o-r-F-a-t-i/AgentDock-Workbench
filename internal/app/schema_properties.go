package app

import "strings"

// schemaPropertyReferences keeps one field definition at the visible schema
// root. Fields widened by a result union keep their branch-local constraints.
func schemaPropertyReferences(properties map[string]any, localFields ...string) map[string]any {
	local := make(map[string]bool, len(localFields))
	for _, name := range localFields {
		local[name] = true
	}
	result := make(map[string]any, len(properties))
	for name, property := range properties {
		if local[name] {
			result[name] = property
			continue
		}
		// Escape JSON Pointer tokens in order, including non-identifier field names.
		token := strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
		result[name] = map[string]any{"$ref": "#/properties/" + token}
	}
	return result
}
