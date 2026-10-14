package app

// newToolContract keeps the capability-owned validation contract intact while
// projecting a smaller model-facing contract. The public contract may omit
// inherited execution overrides, but Runtime validation always uses the full
// schemas supplied by the capability owner.
func newToolContract(name string, inputSchema, outputSchema map[string]any) ToolContract {
	return completeToolContract(name, ToolContract{
		ValidationInputSchema:  inputSchema,
		ValidationOutputSchema: outputSchema,
	})
}

func completeToolContract(name string, contract ToolContract) ToolContract {
	if contract.PublicInputSchema == nil {
		contract.PublicInputSchema = publicToolInputSchema(name, contract.ValidationInputSchema)
	}
	if contract.PublicOutputSchema == nil {
		contract.PublicOutputSchema = cloneJSONSchemaMap(contract.ValidationOutputSchema)
	}
	return contract
}

// publishedToolOutputSchema intentionally omits outputSchema from the public
// MCP directory. AgentDock structuredContent combines the capability result
// with a server-owned execution envelope and a pending-approval alternative;
// publishing only one layer would advertise a false result contract. The full
// executable contract remains available as ToolDefinition.ValidationOutputSchema.
func publishedToolOutputSchema(_ map[string]any) map[string]any { return nil }

var executionOverrideTools = map[string]bool{
	"exec_command":        true,
	"file_edit":           true,
	"mcp_tool_call":       true,
	"plugin_manage":       true,
	"android_device_read": true,
	"android_device_act":  true,
	"browser_session":     true,
	"browser_act":         true,
	"browser_snapshot":    true,
}

var inheritedExecutionFields = []string{"task_id", "thread_id", "step_id", "activity_label"}

var publicToolHiddenFields = map[string][]string{
	"android_device_read": {"workspace_id", "retry_of_call_id"},
	"android_device_act":  {"workspace_id", "retry_of_call_id"},
	"mcp_tool_call":       {"workspace_id"},
	"plugin_manage":       {"workspace_id", "retry_of_call_id"},
	"browser_session":     {"workspace_id"},
	"browser_act":         {"workspace_id"},
	"browser_snapshot":    {"workspace_id"},
}

func publicToolInputSchema(name string, validation map[string]any) map[string]any {
	public := cloneJSONSchemaMap(validation)
	if len(public) == 0 {
		return public
	}
	properties, _ := public["properties"].(map[string]any)
	if executionOverrideTools[name] {
		hidden := append([]string(nil), inheritedExecutionFields...)
		hidden = append(hidden, publicToolHiddenFields[name]...)
		for _, field := range hidden {
			delete(properties, field)
		}
		public["required"] = withoutSchemaFields(public["required"], hidden)
		compactPublicExecutionDescriptions(properties)
	}
	compactPublicToolDescriptions(name, properties)
	if name == "android_device_read" || name == "android_device_act" {
		// The validation contract retains one strict branch per action. Publishing
		// those branches repeats the same common fields many times, so the model
		// receives the union plus the action enum and Runtime enforces the exact
		// action-specific required/allowed fields.
		delete(public, "oneOf")
		if action, ok := properties["action"].(map[string]any); ok {
			action["description"] = "Android action; Runtime enforces action-specific fields."
		}
	}
	return public
}

func compactPublicExecutionDescriptions(properties map[string]any) {
	setSchemaDescription(properties, "retry_of_call_id", "Previous call_id for an explicit retry; omit for normal calls.")
	setSchemaDescription(properties, "workspace_id", "Registered workspace override; omit to inherit the current workspace.")
	setSchemaDescription(properties, "target_kind", "Workspace target kind for this call.")
	setSchemaDescription(properties, "external_path", "Absolute one-call path when target_kind=external.")
	setSchemaDescription(properties, "backend", "Execution backend. Android backends require a ready paired provider and never fall back.")
	setSchemaDescription(properties, "execution_mode", "auto waits briefly then returns a session; sync waits for exit; async returns immediately.")
	setSchemaDescription(properties, "skill", "Installed Skill name or exact reference; explicit workdir takes priority.")
	setSchemaDescription(properties, "skill_env", "Optional isolated Skill environment.")
	setSchemaDescription(properties, "skill_ref", "Exact Skill reference from AgentDock context; uses its resolved root and managed environment.")
	setSchemaDescription(properties, "env", "Command environment overrides; reserved runtime values remain protected.")
}

func compactPublicToolDescriptions(name string, properties map[string]any) {
	switch name {
	case "agentdock_context":
		setSchemaDescription(properties, "workdir", "Existing workspace directory; omit to inherit the conversation workspace. Complete context coordinates binding; running sessions stay unchanged.")
		setSchemaDescription(properties, "known_context_revision", "Revision of complete context held by the caller; matching, revalidated context may return a compact reference. Omit after context loss.")
	case "file_edit":
		setSchemaDescription(properties, "path", "Source file or directory path for the selected action.")
		setSchemaDescription(properties, "new_path", "Destination path for move.")
		setSchemaDescription(properties, "patch", "Structured envelope: *** Begin Patch; repeat *** Add File: <path>, *** Delete File: <path>, or *** Update File: <path>; use @@ [<optional context line>] and optional *** End of File; finish with *** End Patch.")
		setSchemaDescription(properties, "workdir", "Working directory for patch paths.")
	case "mcp_tool_search":
		setSchemaDescription(properties, "query", "Dynamic MCP capability query. Call known host tools directly.")
		setSchemaDescription(properties, "server", "Server name from agentdock_context.")
		setSchemaDescription(properties, "limit", "Maximum matches; default 10.")
	case "mcp_tool_list":
		setSchemaDescription(properties, "server", "Enabled server; lists all visible tool names and descriptions.")
	case "mcp_tool_inspect":
		setSchemaDescription(properties, "name", "Dynamic MCP name: server:tool.")
		setSchemaDescription(properties, "names", "server:tool or server:* selectors; may mix services.")
	case "mcp_manage":
		setSchemaDescription(properties, "header_env", "HTTP header name to host environment variable name.")
		setSchemaDescription(properties, "env_from_env", "Child environment variable name to host environment variable name.")
		setSchemaDescription(properties, "patch", "Editable host override fields for update.")
		setSchemaDescription(properties, "scope", "Override scope: runtime or persistent.")
		setSchemaDescription(properties, "expected_revision", "Current server revision for compare-and-swap.")
		setSchemaDescription(properties, "timeout_ms", "Request timeout in milliseconds.")
		// The patch exposes the same management fields. Reuse their public
		// descriptions without changing the capability-owned validation schema.
		if patch, ok := properties["patch"].(map[string]any); ok {
			patchProperties, _ := patch["properties"].(map[string]any)
			for field := range patchProperties {
				if property, ok := properties[field].(map[string]any); ok {
					if description, ok := property["description"].(string); ok {
						setSchemaDescription(patchProperties, field, description)
					}
				}
			}
		}
	case "task_manage":
		setSchemaDescription(properties, "action", "Task action. create/resume/set_current changes continuation; ordinary tools inherit it.")
		setSchemaDescription(properties, "next_action", "Concrete next action saved by a checkpoint.")
		setSchemaDescription(properties, "completion_conditions", "Completion conditions required for create.")
		setSchemaDescription(properties, "completed_step_ids", "Step ids completed atomically by a checkpoint.")
		setSchemaDescription(properties, "status", "Action-specific task, step, or review status.")
		setSchemaDescription(properties, "steps", "Concrete task steps.")
	}
}

func setSchemaDescription(properties map[string]any, name, description string) {
	property, ok := properties[name].(map[string]any)
	if ok {
		property["description"] = description
	}
}

func withoutSchemaFields(value any, removed []string) any {
	items, ok := value.([]string)
	if !ok {
		return value
	}
	blocked := make(map[string]struct{}, len(removed))
	for _, name := range removed {
		blocked[name] = struct{}{}
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if _, drop := blocked[item]; !drop {
			result = append(result, item)
		}
	}
	return result
}

func cloneJSONSchemaMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = cloneJSONSchemaValue(value)
	}
	return result
}

func cloneJSONSchemaValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneJSONSchemaMap(typed)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = cloneJSONSchemaValue(item)
		}
		return result
	case []map[string]any:
		result := make([]map[string]any, len(typed))
		for index, item := range typed {
			result[index] = cloneJSONSchemaMap(item)
		}
		return result
	case []string:
		result := make([]string, len(typed))
		copy(result, typed)
		return result
	case []int:
		result := make([]int, len(typed))
		copy(result, typed)
		return result
	case []bool:
		result := make([]bool, len(typed))
		copy(result, typed)
		return result
	default:
		return value
	}
}
