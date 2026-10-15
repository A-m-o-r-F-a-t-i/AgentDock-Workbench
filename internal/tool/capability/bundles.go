package capability

import (
	"fmt"
	"sort"
	"strings"

	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

const (
	ToolLoad                 = "capability_load"
	ActionList               = "list"
	ActionLoad               = "load"
	BundleSystemManagement   = "system-management"
	BundleArtifactPublishing = "artifact-publishing"
)

type Bundle struct {
	Name        string
	Description string
	Tools       []string
}

type Request struct {
	Action string `json:"action"`
	Name   string `json:"name,omitempty"`
}

var builtinBundles = []Bundle{
	{
		Name:        BundleSystemManagement,
		Description: "Configure dynamic MCP servers, plugins, and installed Skill packages.",
		Tools:       []string{"mcp_manage", "plugin_manage", "skill_package"},
	},
	{
		Name:        BundleArtifactPublishing,
		Description: "Publish a local file or directory as a signed AgentDock artifact.",
		Tools:       []string{"file_publish"},
	},
}

func Bundles() []Bundle {
	items := make([]Bundle, len(builtinBundles))
	for index, bundle := range builtinBundles {
		items[index] = bundle
		items[index].Tools = append([]string(nil), bundle.Tools...)
	}
	return items
}

func ByName(name string) (Bundle, bool) {
	name = strings.TrimSpace(name)
	for _, bundle := range builtinBundles {
		if bundle.Name == name {
			bundle.Tools = append([]string(nil), bundle.Tools...)
			return bundle, true
		}
	}
	return Bundle{}, false
}

func ForTool(name string) (Bundle, bool) {
	for _, bundle := range builtinBundles {
		for _, tool := range bundle.Tools {
			if tool == name {
				bundle.Tools = append([]string(nil), bundle.Tools...)
				return bundle, true
			}
		}
	}
	return Bundle{}, false
}

func Validate(request Request) error {
	request.Action = strings.TrimSpace(request.Action)
	request.Name = strings.TrimSpace(request.Name)
	switch request.Action {
	case ActionList:
		if request.Name != "" {
			return fmt.Errorf("name is only valid for action=load")
		}
		return nil
	case ActionLoad:
		if _, ok := ByName(request.Name); !ok {
			return fmt.Errorf("unknown built-in tool bundle %q", request.Name)
		}
		return nil
	default:
		return fmt.Errorf("unsupported capability_load action %q", request.Action)
	}
}

func InputSchema(name string) (map[string]any, bool) {
	if name != ToolLoad {
		return nil, false
	}
	bundleNames := make([]string, 0, len(builtinBundles))
	for _, bundle := range builtinBundles {
		bundleNames = append(bundleNames, bundle.Name)
	}
	sort.Strings(bundleNames)
	properties := map[string]any{
		"action": map[string]any{
			"type": "string", "enum": []string{ActionList, ActionLoad},
			"description": "List built-in bundles or load one for the current authenticated conversation.",
		},
		"name": map[string]any{
			"type": "string", "enum": bundleNames,
			"description": "Exact bundle name; required only for action=load.",
		},
	}
	schema := toolcontract.InputObject(properties, "action")
	schema["allOf"] = []any{map[string]any{
		"if": map[string]any{
			"properties": map[string]any{"action": map[string]any{"const": ActionLoad}},
			"required":   []string{"action"},
		},
		"then": map[string]any{"required": []string{"name"}},
	}}
	return schema, true
}

func OutputSchema(name string) (map[string]any, bool) {
	if name != ToolLoad {
		return nil, false
	}
	bundleItem := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"name", "description", "loaded", "tool_count"},
		"properties": map[string]any{
			"name":        map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"loaded":      map[string]any{"type": "boolean"},
			"tool_count":  map[string]any{"type": "integer", "minimum": 0},
			"tools": map[string]any{
				"type": "array", "items": map[string]any{"type": "string"},
			},
		},
	}
	return toolcontract.OutputObject(map[string]any{
		"action":            map[string]any{"type": "string", "enum": []string{ActionList, ActionLoad}},
		"scoped":            map[string]any{"type": "boolean"},
		"changed":           map[string]any{"type": "boolean"},
		"directory_changed": map[string]any{"type": "boolean"},
		"bundles": map[string]any{
			"type": "array", "maxItems": len(builtinBundles), "items": bundleItem,
		},
		"loaded":                map[string]any{"type": "boolean"},
		"bundle":                map[string]any{"type": "string"},
		"tool_catalog_revision": map[string]any{"type": "string"},
		"host_refresh_required": map[string]any{"type": "boolean"},
		"execution_performed":   map[string]any{"type": "boolean"},
		"required_action":       toolcontract.OpenObject("Host directory refresh required before the original business call."),
		"loaded_bundle":         map[string]any{"type": "string"},
		"revealed_tools":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"next_required_action":  map[string]any{"type": "string"},
	}, "action", "scoped", "changed", "directory_changed", "bundles"), true
}
