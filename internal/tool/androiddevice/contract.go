package androiddevice

import (
	"github.com/uvwt/agentdock/internal/tool/command"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
	"maps"
	"sort"
)

func InputSchema(tool string) (map[string]any, bool) {
	if !IsTool(tool) {
		return nil, false
	}
	all := map[string]any{}
	toolcontract.ActivityProperties(all)
	for _, field := range []string{"package", "component", "namespace", "key", "operation", "mode", "permission", "path", "capture_dir", "text"} {
		all[field] = map[string]any{"type": "string", "minLength": 1, "maxLength": 4096}
	}
	all["value"] = map[string]any{"type": "string", "maxLength": 4096}
	all["namespace"] = map[string]any{"type": "string", "enum": []string{"system", "secure", "global"}}
	all["mode"] = map[string]any{"type": "string", "enum": []string{"allow", "ignore", "deny", "default", "foreground"}}
	all["execution_mode"] = map[string]any{"type": "string", "enum": []string{"auto", "sync", "async"}}
	for _, field := range []string{"x", "y", "x2", "y2"} {
		all[field] = toolcontract.BoundedInteger("Primary-display coordinate; obtain display geometry first.", 0, 32767)
	}
	all["duration_ms"] = toolcontract.BoundedInteger("Swipe duration in milliseconds; default 300.", 1, 5000)
	all["keycode"] = toolcontract.BoundedInteger("Android keycode integer.", 1, 1000)
	all["lines"] = toolcontract.BoundedInteger("Maximum recent logcat lines; default 200.", 1, 2000)
	all["timeout_ms"] = toolcontract.BoundedInteger("Remote deadline in milliseconds; default 30000.", 1, 120000)
	all["yield_time_ms"] = toolcontract.BoundedInteger("Foreground wait before returning the original session.", 0, 30000)
	all["max_output_bytes"] = toolcontract.BoundedInteger("Bounded ordinary text output; images stay in capture files.", 1024, 1048576)
	actions := []string{}
	for action, def := range Definitions {
		if def.Read == (tool == ReadTool) {
			actions = append(actions, action)
		}
	}
	sort.Strings(actions)
	properties := map[string]any{}
	variants := []any{}
	for _, action := range actions {
		def := Definitions[action]
		fields := map[string]any{"action": map[string]any{"type": "string", "const": action}}
		for _, key := range append(append([]string{}, common[1:]...), def.Fields...) {
			fields[key] = all[key]
			properties[key] = all[key]
		}
		variant := toolcontract.InputObject(fields, append([]string{"action"}, def.Required...)...)
		variants = append(variants, variant)
	}
	properties["action"] = map[string]any{"type": "string", "enum": actions}
	schema := toolcontract.InputObject(properties, "action")
	schema["oneOf"] = variants
	return schema, true
}
func OutputSchema(tool string) (map[string]any, bool) {
	if !IsTool(tool) {
		return nil, false
	}
	base, _ := command.OutputSchema(command.ToolExecCommand)
	base = maps.Clone(base)
	props := maps.Clone(base["properties"].(map[string]any))
	props["action"] = toolcontract.String("Validated Android operation.")
	props["backend"] = map[string]any{"type": "string", "const": "android_shizuku"}
	props["device_status"] = map[string]any{"type": "object", "additionalProperties": true}
	props["artifact_path"] = toolcontract.String("Generated capture path in the explicitly selected Android shared directory.")
	props["artifact_type"] = toolcontract.String("Capture media type.")
	props["artifact_state"] = map[string]any{"type": "string", "enum": []string{"ready", "pending", "unconfirmed"}}
	props["verification"] = toolcontract.String("What the command checks on successful completion; pending sessions remain unverified.")
	base["properties"] = props
	return base, true
}
